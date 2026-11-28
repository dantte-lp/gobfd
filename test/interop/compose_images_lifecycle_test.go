package interop_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProjectControlImageRecovery(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	scenarios := []string{
		"separate down", "stage image cleanup", "foreign alias replacement", "unknown ID recovery",
		"foreign unknown ID", "unsafe receipt", "duplicate receipt", "cleanup retry", "failed build publishes image",
		"receipt replaced during build", "second build publishes then fails",
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			runProjectControlImageScenario(t, root, scenario)
		})
	}
}

func runProjectControlImageScenario(t *testing.T, root, scenario string) {
	t.Helper()
	fakeBin, stateDir, runtimeDir := t.TempDir(), t.TempDir(), secureRuntimeDir(t)
	fakeEnv := installFakeCommand(t, fakeBin, "podman", "bgp-build")
	commandLog := filepath.Join(t.TempDir(), "commands.log")
	env := append(os.Environ(), fakeEnv, fakeRaceOptions,
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_STATE_DIR="+stateDir, "INTEROP_FAKE_COMMAND_LOG="+commandLog,
		"INTEROP_PROJECT_NAME=custom-bgp", "INTEROP_PROJECT_KIND=bgp",
		"GOBFD_BUILD_REVISION="+strings.Repeat("a", 40), "COMPOSE_COMPATIBILITY=",
		"XDG_RUNTIME_DIR="+runtimeDir,
	)
	run := func(action string, extra ...string) error {
		t.Helper()
		cmd := projectControlCommand(t.Context(), root, action)
		cmd.Env = append(append([]string(nil), env...), extra...)
		output, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Logf("%s: %v\n%s", action, runErr, output)
		}
		return runErr
	}
	receiptPath := filepath.Join(
		runtimeDir, "gobfd-interop-"+strconv.Itoa(os.Getuid())+".locks", "custom-bgp.images.json",
	)
	imagesPath := filepath.Join(stateDir, "images.json")
	if scenario == "receipt replaced during build" {
		verifyReplacedReceiptDuringBuild(t, run, receiptPath, commandLog, imagesPath)
		return
	}
	if scenario == "failed build publishes image" || scenario == "second build publishes then fails" {
		verifyFailedBuildImageCleanup(t, scenario, run, receiptPath, commandLog, imagesPath)
		return
	}
	upExtra := []string(nil)
	if scenario == "stage image cleanup" {
		upExtra = []string{"INTEROP_FAKE_EMIT_STAGE_IMAGE=true"}
	}
	if upErr := run("up", upExtra...); upErr != nil {
		t.Fatal(upErr)
	}
	images := readFakeComposeImages(t, imagesPath)
	wantBuilt := 1
	if scenario == "stage image cleanup" {
		wantBuilt = 2
	}
	if len(images) != wantBuilt {
		t.Fatalf("images = %d, want %d", len(images), wantBuilt)
	}
	alias, owned := firstTaggedComposeImage(images)
	mutateProjectControlScenario(t, scenario, alias, owned, images, imagesPath, receiptPath)
	if scenario == "cleanup retry" {
		if downErr := run("down", "INTEROP_FAKE_IMAGE_REMOVE_FAIL=true"); downErr == nil {
			t.Fatal("failed image removal accepted")
		}
		if _, statErr := os.Stat(receiptPath); statErr != nil {
			t.Fatalf("cleanup failure lost recovery receipt: %v", statErr)
		}
	}
	reject := scenario == "foreign unknown ID" || scenario == "unsafe receipt" || scenario == "duplicate receipt"
	if downErr := run("down"); (downErr != nil) != reject {
		t.Fatalf("down error=%v, want rejection=%t", downErr, reject)
	}
	verifyProjectControlDownResult(t, scenario, alias, reject, imagesPath, receiptPath, commandLog)
}

func firstTaggedComposeImage(images map[string]fakeComposeImage) (string, fakeComposeImage) {
	for alias, owned := range images {
		if strings.HasPrefix(alias, "localhost/") {
			return alias, owned
		}
	}
	for alias, owned := range images {
		return alias, owned
	}
	return "", fakeComposeImage{}
}

func verifyReplacedReceiptDuringBuild(
	t *testing.T,
	run func(string, ...string) error,
	receiptPath, commandLog, imagesPath string,
) {
	t.Helper()
	if err := run("up", "INTEROP_FAKE_REPLACE_RECEIPT="+receiptPath); err == nil {
		t.Fatal("in-build replacement accepted")
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("checkpoint overwrote replacement")
	}
	logData, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "podman image rm ") || strings.Contains(string(logData), " up ") {
		t.Fatal("replacement triggered launch or destructive cleanup")
	}
	if len(readFakeComposeImages(t, imagesPath)) != 1 {
		t.Fatal("replacement did not preserve built image for recovery")
	}
	if downErr := run("down"); downErr != nil {
		t.Fatal(downErr)
	}
	if len(readFakeComposeImages(t, imagesPath)) != 0 {
		t.Fatal("separate down failed to recover the planned image")
	}
}

func verifyFailedBuildImageCleanup(
	t *testing.T,
	scenario string,
	run func(string, ...string) error,
	receiptPath, commandLog, imagesPath string,
) {
	t.Helper()
	options := []string{"INTEROP_FAKE_BUILD_FAIL_AFTER_IMAGE=true"}
	if scenario == "second build publishes then fails" {
		options = []string{"INTEROP_FAKE_FAIL_SECOND_IMAGE=true", "INTEROP_FAKE_BUILD_CONFIG=" + baseBuildConfig}
	}
	if err := run("up", options...); err == nil {
		t.Fatal("failed build accepted")
	}
	if _, statErr := os.Stat(receiptPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed-build cleanup retained receipt: %v", statErr)
	}
	if images := readFakeComposeImages(t, imagesPath); len(images) != 0 {
		t.Fatalf("failed build leaked images: %v", images)
	}
	if scenario == "second build publishes then fails" {
		logData, err := os.ReadFile(commandLog)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(logData), baseBuildLimits) != 2 ||
			strings.Count(string(logData), "podman image rm ") != 2 {
			t.Fatalf("partial build did not clean exactly both built images:\n%s", logData)
		}
	}
}

func mutateProjectControlScenario(
	t *testing.T,
	scenario, alias string,
	owned fakeComposeImage,
	images map[string]fakeComposeImage,
	imagesPath, receiptPath string,
) {
	t.Helper()
	switch scenario {
	case "foreign alias replacement":
		images[owned.ID] = owned
		images[alias] = fakeComposeImage{
			ID:     "sha256:" + strings.Repeat("f", 64),
			Labels: map[string]string{"foreign": "true"},
		}
		writeComposeFixture(t, imagesPath, images)
	case "unknown ID recovery", "foreign unknown ID":
		var state map[string]json.RawMessage
		readComposeFixture(t, receiptPath, &state)
		var owner map[string]json.RawMessage
		if err := json.Unmarshal(state["x-gobfd-owned-images"], &owner); err != nil {
			t.Fatal(err)
		}
		owner["images"] = json.RawMessage(`{"frr-bgp":{"alias":` + strconv.Quote(alias) + `}}`)
		ownerJSON, err := json.Marshal(owner)
		if err != nil {
			t.Fatal(err)
		}
		state["x-gobfd-owned-images"] = ownerJSON
		state["services"] = json.RawMessage(
			`{"frr-bgp":{"image":` + strconv.Quote(alias) + `,"pull_policy":"never"}}`,
		)
		writeComposeFixture(t, receiptPath, state)
		if scenario == "foreign unknown ID" {
			owned.Labels["io.gobfd.run"] = "foreign"
			images[alias] = owned
			writeComposeFixture(t, imagesPath, images)
		}
	case "unsafe receipt":
		if err := os.Chmod(receiptPath, 0o644); err != nil {
			t.Fatal(err)
		}
	case "duplicate receipt":
		data, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Fatal(err)
		}
		data = append([]byte(`{"services":{},`), data[1:]...)
		if writeErr := os.WriteFile(receiptPath, data, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
}

func verifyProjectControlDownResult(
	t *testing.T,
	scenario, alias string,
	reject bool,
	imagesPath, receiptPath, commandLog string,
) {
	t.Helper()
	remaining := readFakeComposeImages(t, imagesPath)
	if reject {
		if len(remaining) != 1 {
			t.Fatal("untrusted receipt triggered image deletion")
		}
		if _, statErr := os.Stat(receiptPath); statErr != nil {
			t.Fatalf("untrusted receipt was removed: %v", statErr)
		}
		logData, err := os.ReadFile(commandLog)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(logData), "podman image rm ") ||
			strings.Contains(string(logData), "podman rm ") {
			t.Fatal("rejected owner triggered a destructive command")
		}
		return
	}
	if scenario == "foreign alias replacement" {
		if len(remaining) != 1 || remaining[alias].Labels["foreign"] != "true" {
			t.Fatal("cleanup did not preserve foreign alias replacement")
		}
	} else if len(remaining) != 0 {
		t.Fatalf("owned or stage images remain after down: %v", remaining)
	}
	if _, statErr := os.Stat(receiptPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("verified cleanup retained receipt: %v", statErr)
	}
}

func readFakeComposeImages(t *testing.T, path string) map[string]fakeComposeImage {
	t.Helper()
	var images map[string]fakeComposeImage
	readComposeFixture(t, path, &images)
	return images
}

func readComposeFixture(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func writeComposeFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
