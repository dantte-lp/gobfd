package e2erunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/routingartifacts"
)

func TestRoutingMergeCleansAfterCancellation(t *testing.T) {
	testRoutingMergeRuntime(t, "merge canceled")
}

func TestRoutingMergeOwnedRuntime(t *testing.T) {
	modes := []string{
		"merge healthy", "merge base retired", "merge active retired",
		"merge no output", "merge removal failure", "partial ID replacement", "pre-existing",
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) { testRoutingMergeRuntime(t, mode) })
	}
}

func TestRoutingInventoryRequiresOwnedContainers(t *testing.T) {
	for _, test := range []struct {
		name, container string
		state           []runtimeFixtureContainer
		reject          bool
	}{
		{name: "required absent", container: "gobfd-interop", reject: true},
		{name: "optional absent", container: "scapy-interop"},
		{
			name:      "optional foreign",
			container: "scapy-interop",
			reject:    true,
			state: []runtimeFixtureContainer{
				{
					ID:     strings.Repeat("a", 64),
					Name:   "scapy-interop",
					Labels: map[string]string{"com.docker.compose.project": "foreign"},
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, fixture := t.TempDir(), t.TempDir()
			bin := filepath.Dir(installFakeGo(t))
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if linkErr := os.Symlink(executable, filepath.Join(bin, "podman")); linkErr != nil {
				t.Fatal(linkErr)
			}
			for key, value := range map[string]string{
				"PATH": bin, runtimeFixtureEnv: fixture,
				"GOBFD_RUNTIME_MODE": "", "GORACE": "atexit_sleep_ms=0",
			} {
				t.Setenv(key, value)
			}
			writeRuntimeFixture(t, fixture, test.state)
			controller, err := interopproject.NewProject(root, "fixture", "base", io.Discard, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			r := runner{root: root, reportDir: root, stderr: io.Discard}
			err = r.recordRoutingContainers(t.Context(), controller, routingSuite{containers: []string{test.container}})
			if (err != nil) != test.reject {
				t.Fatalf("inventory error=%v reject=%t", err, test.reject)
			}
			_, statErr := os.Stat(filepath.Join(root, containersJSONName))
			if test.reject && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("lookup failure produced a successful empty inventory")
			}
		})
	}
}

func testRoutingMergeRuntime(t *testing.T, mode string) {
	t.Helper()
	root, fixture := setupRoutingMergeRoot(t)
	bin := filepath.Dir(installFakeGo(t))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if linkErr := os.Symlink(executable, filepath.Join(bin, "podman")); linkErr != nil {
		t.Fatal(linkErr)
	}
	for key, value := range map[string]string{
		"PATH": bin, runtimeFixtureEnv: fixture, "GOBFD_RUNTIME_MODE": mode,
		"GOBFD_RUNTIME_MERGE_REPORT": root, "GORACE": "atexit_sleep_ms=0",
	} {
		t.Setenv(key, value)
	}
	writeRuntimeFixture(t, fixture, []runtimeFixtureContainer{{
		ID:     strings.Repeat("f", 64),
		Name:   "unrelated",
		Labels: map[string]string{"foreign": "true"},
	}})
	ctx := t.Context()
	if mode == "merge canceled" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	r := runner{
		root: root, target: "routing", runID: "fixed-run", reportDir: root,
		devProject: "fixture", stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
	}
	err = r.mergeRoutingArtifacts(ctx)
	if (err != nil) != (mode != "merge healthy" && mode != "merge base retired") {
		t.Fatalf("merge error=%v for %s", err, mode)
	}
	verifyRoutingMergeOutcome(t, mode, root, fixture, err)
}

func setupRoutingMergeRoot(t *testing.T) (string, string) {
	t.Helper()
	root, fixture := t.TempDir(), t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, suite := range []string{baseSuiteName, bgpSuiteName} {
		if err := os.Mkdir(filepath.Join(root, suite), 0o700); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			containersJSONName: `[{"Id":"` + suite + `"}]`,
			"packets.pcapng":   "capture fixture",
		}
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(root, suite, name), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		image := strings.Repeat("a", 64)
		if suite == bgpSuiteName {
			image = strings.Repeat("b", 64)
		}
		if err := routingartifacts.WriteImageID(root, filepath.Join(suite, "tshark-image-id"), image); err != nil {
			t.Fatal(err)
		}
	}
	return root, fixture
}

func verifyRoutingMergeOutcome(t *testing.T, mode, root, fixture string, mergeErr error) {
	t.Helper()
	if mode == "merge canceled" {
		if !errors.Is(mergeErr, context.DeadlineExceeded) {
			t.Fatalf("lost cancellation: %v", mergeErr)
		}
		if _, statErr := os.Stat(filepath.Join(fixture, "merge-started")); statErr != nil {
			t.Fatalf("merge did not reach runtime: %v", statErr)
		}
	}
	if mode == "merge no output" && !errors.Is(mergeErr, errUnsafePcap) {
		t.Fatalf("missing output lost typed capture failure: %v", mergeErr)
	}
	if mode == "merge removal failure" {
		var exitErr *ExitError
		if !errors.As(mergeErr, &exitErr) || exitErr.Code != 24 {
			t.Fatalf("missing cleanup exit24: %v", mergeErr)
		}
	}
	if mode == "merge active retired" {
		trace, readErr := os.ReadFile(filepath.Join(fixture, "trace.jsonl"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(trace, []byte(`"create"`)) || bytes.Contains(trace, []byte(`"start"`)) {
			t.Fatal("missing active image reached runtime mutation")
		}
	}
	remaining := readRuntimeFixture(t, fixture)
	want := 1
	if mode == "merge removal failure" || mode == "partial ID replacement" || mode == "pre-existing" {
		want = 2
	}
	if len(remaining) != want || remaining[0].Labels["foreign"] != "true" {
		t.Fatalf("merge cleanup left unexpected containers: %v", remaining)
	}
	verifyMergedInventoryAndProvenance(t, mode, root)
}

func verifyMergedInventoryAndProvenance(t *testing.T, mode, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, containersJSONName))
	if err != nil {
		t.Fatal(err)
	}
	var merged struct {
		Suites map[string][]json.RawMessage `json:"suites"`
	}
	if unmarshalErr := json.Unmarshal(data, &merged); unmarshalErr != nil || len(merged.Suites) != 2 {
		t.Fatalf("merge runtime diagnostics overwrote suite inventories: %s %v", data, unmarshalErr)
	}
	if mode != "merge healthy" && mode != "merge base retired" {
		return
	}
	provData, provErr := os.ReadFile(filepath.Join(root, "merge-runtime/image-provenance.json"))
	if provErr != nil {
		t.Fatal(provErr)
	}
	var provenance map[string]string
	if unmarshalErr := json.Unmarshal(provData, &provenance); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if provenance["base_capture_image"] != strings.Repeat("a", 64) ||
		provenance["bgp_capture_image"] != strings.Repeat("b", 64) ||
		provenance["merge_image"] != strings.Repeat("b", 64) {
		t.Fatalf("incorrect image lifetime provenance: %v", provenance)
	}
}
