package e2erunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const runtimeFixtureEnv = "GOBFD_E2ERUNNER_RUNTIME_FIXTURE"

type runtimeFixtureContainer struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type runtimeInspectResponse struct {
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels"`
}

func TestLinuxPreparationFailurePreservesForeignContainer(t *testing.T) {
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
		"PATH": bin, runtimeFixtureEnv: fixture, "GOBFD_RUNTIME_MODE": "build failure",
		"E2E_LINUX_IMAGE": "qualified-image", "GORACE": "atexit_sleep_ms=0",
	} {
		t.Setenv(key, value)
	}
	foreign := runtimeFixtureContainer{
		ID:     strings.Repeat("f", 64),
		Name:   "e2e-linux-fixed-run",
		Labels: map[string]string{"foreign": "true"},
	}
	writeRuntimeFixture(t, fixture, []runtimeFixtureContainer{foreign})
	r := runner{
		root: root, target: "linux", runID: "fixed-run", reportDir: root,
		reportRel: "report", devProject: "fixture", stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
	}
	err = r.runLinux(t.Context())
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 23 {
		t.Fatalf("preparation error=%v, want exit23", err)
	}
	remaining := readRuntimeFixture(t, fixture)
	if len(remaining) != 1 || remaining[0].ID != foreign.ID {
		t.Fatal("preparation failure removed a pre-existing foreign container")
	}
}

func TestRuntimeContainerOwnership(t *testing.T) {
	modes := []string{
		"healthy", "pre-existing", "failed create", "partial create",
		"partial ID replacement", "malformed ID", "wrong valid ID",
		"foreign create", "replacement", "remove failure", "canceled",
	}
	for _, kind := range []string{"linux", "overlay"} {
		for _, mode := range modes {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				testRuntimeContainerMode(t, kind, mode)
			})
		}
	}
}

func testRuntimeContainerMode(t *testing.T, kind, mode string) {
	t.Helper()
	root, fixture := t.TempDir(), t.TempDir()
	if chmodErr := os.Chmod(root, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
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
		"E2E_LINUX_IMAGE": "qualified-image", "E2E_OVERLAY_IMAGE": "qualified-image",
		"GORACE": "atexit_sleep_ms=0",
	} {
		t.Setenv(key, value)
	}
	writeRuntimeFixture(t, fixture, []runtimeFixtureContainer{{
		ID:     strings.Repeat("f", 64),
		Name:   "unrelated",
		Labels: map[string]string{"foreign": "true"},
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	var output io.Writer = &stdout
	if mode == "canceled" {
		output = runtimeCancelWriter{cancel: cancel}
	}
	r := runner{
		root: root, target: kind, runID: "fixed-run", reportDir: root,
		reportRel: "report", devProject: "fixture", stdout: output, stderr: &bytes.Buffer{},
	}
	if kind == "linux" {
		err = r.runLinux(ctx)
	} else {
		err = r.runOverlay(ctx)
	}
	wantError := mode != "healthy" && mode != "replacement"
	if (err != nil) != wantError {
		t.Fatalf("run error=%v, want error=%t", err, wantError)
	}
	if mode == "canceled" && !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	verifyRuntimeOwnershipResult(t, mode, root, fixture)
}

func verifyRuntimeOwnershipResult(t *testing.T, mode, root, fixture string) {
	t.Helper()
	remaining := readRuntimeFixture(t, fixture)
	foreign, owned := 0, 0
	for _, container := range remaining {
		if container.Labels["foreign"] == "true" {
			foreign++
		} else {
			owned++
		}
	}
	wantForeign := 1
	if mode == "pre-existing" || mode == "foreign create" ||
		mode == "replacement" || mode == "partial ID replacement" {
		wantForeign = 2
	}
	wantOwned := 0
	if mode == "remove failure" {
		wantOwned = 1
	}
	if foreign != wantForeign || owned != wantOwned {
		t.Fatalf("remaining foreign=%d owned=%d, want %d/%d", foreign, owned, wantForeign, wantOwned)
	}
	trace, err := os.ReadFile(filepath.Join(fixture, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range bytes.SplitSeq(bytes.TrimSpace(trace), []byte("\n")) {
		var args []string
		if unmarshalErr := json.Unmarshal(line, &args); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		if args[0] == "rm" || args[0] == "start" {
			if !imageIDPattern.MatchString(args[len(args)-1]) {
				t.Fatalf("runtime command targets mutable name: %v", args)
			}
			allowStart := mode == "healthy" || mode == "replacement" ||
				mode == "remove failure" || mode == "canceled"
			if args[0] == "start" && !allowStart {
				t.Fatalf("invalid create continued to start: %v", args)
			}
		}
	}
	if mode == "remove failure" {
		data, _, readErr := interopproject.ReadPrivateReceipt(filepath.Join(root, "runtime-container.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var receipt ownedRuntime
		if unmarshalErr := json.Unmarshal(data, &receipt); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		if !imageIDPattern.MatchString(receipt.ID) {
			t.Fatal("failed cleanup lost bound ID receipt")
		}
	}
}

type runtimeCancelWriter struct{ cancel context.CancelFunc }

func (writer runtimeCancelWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("runtime-start-marker")) {
		writer.cancel()
	}
	return len(data), nil
}

func TestDevImageIDRejectsAmbiguousService(t *testing.T) {
	for _, test := range []struct {
		name, containers, image string
		reject                  bool
	}{
		{"single", strings.Repeat("a", 64) + "\n", strings.Repeat("b", 64), false},
		{"multiple", strings.Repeat("a", 64) + "\n" + strings.Repeat("c", 64), strings.Repeat("b", 64), true},
		{"empty", "", strings.Repeat("b", 64), true},
		{"short container", "abc", strings.Repeat("b", 64), true},
		{"short image", strings.Repeat("a", 64), "abc", true},
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
				"PATH": bin, runtimeFixtureEnv: fixture, "GOBFD_RUNTIME_MODE": "",
				"GOBFD_RUNTIME_DEV_IDS": test.containers, "GOBFD_RUNTIME_DEV_IMAGE": test.image,
				"GORACE": "atexit_sleep_ms=0",
			} {
				t.Setenv(key, value)
			}
			writeRuntimeFixture(t, fixture, nil)
			r := runner{root: root, devProject: "fixture", stderr: &bytes.Buffer{}}
			image, err := r.devImageID(t.Context())
			if (err != nil) != test.reject || (!test.reject && image != test.image) {
				t.Fatalf("image=%q error=%v reject=%t", image, err, test.reject)
			}
			if test.name == "multiple" || test.name == "empty" || test.name == "short container" {
				data, readErr := os.ReadFile(filepath.Join(fixture, "trace.jsonl"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if bytes.Contains(data, []byte(`"inspect"`)) {
					t.Fatal("invalid service ID reached inspection")
				}
			}
		})
	}
}

func readRuntimeFixture(t *testing.T, directory string) []runtimeFixtureContainer {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state []runtimeFixtureContainer
	if unmarshalErr := json.Unmarshal(data, &state); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	return state
}

func writeRuntimeFixture(t *testing.T, directory string, state []runtimeFixtureContainer) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(filepath.Join(directory, "state.json"), data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
}

func runRuntimeFixture() int {
	arguments := os.Args[1:]
	if len(arguments) == 0 {
		return 125
	}
	directory := os.Getenv(runtimeFixtureEnv)
	data, err := os.ReadFile(filepath.Join(directory, "state.json"))
	if err != nil {
		return 125
	}
	var state []runtimeFixtureContainer
	if json.Unmarshal(data, &state) != nil {
		return 125
	}
	trace, err := os.OpenFile(filepath.Join(directory, "trace.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 125
	}
	if traceErr := errors.Join(json.NewEncoder(trace).Encode(arguments), trace.Close()); traceErr != nil {
		return 125
	}
	mode := os.Getenv("GOBFD_RUNTIME_MODE")
	last := arguments[len(arguments)-1]
	index := -1
	for i, container := range state {
		if container.ID == last || container.Name == last {
			index = i
			break
		}
	}
	save := func() int {
		marshaled, marshalErr := json.Marshal(state)
		if marshalErr != nil || os.WriteFile(filepath.Join(directory, "state.json"), marshaled, 0o600) != nil {
			return 125
		}
		return 0
	}
	switch arguments[0] {
	case "compose":
		if ids, present := os.LookupEnv("GOBFD_RUNTIME_DEV_IDS"); present && slices.Contains(arguments, "ps") {
			fmt.Fprint(os.Stdout, ids)
			return 0
		}
		if mode == "build failure" {
			return 23
		}
		return 0
	case "ps":
		for _, arg := range arguments {
			filter, ok := strings.CutPrefix(arg, "label=")
			if !ok {
				continue
			}
			key, value, ok := strings.Cut(filter, "=")
			if !ok {
				return 125
			}
			for _, container := range state {
				if container.Labels[key] == value {
					fmt.Fprintln(os.Stdout, container.ID)
				}
			}
			return 0
		}
		fmt.Fprintln(os.Stdout, "[]")
		return 0
	case "image":
		if len(arguments) == 3 && arguments[1] == "exists" && imageIDPattern.MatchString(last) {
			if mode == "merge base retired" && last == strings.Repeat("a", 64) {
				return 1
			}
			if mode == "merge active retired" && last == strings.Repeat("b", 64) {
				return 1
			}
			return 0
		}
		return 125
	case "run":
		labels := make(map[string]string)
		for i := 0; i+1 < len(arguments); i++ {
			if arguments[i] == "--label" {
				key, value, ok := strings.Cut(arguments[i+1], "=")
				if !ok {
					return 125
				}
				labels[key] = value
			}
		}
		state = append(state, runtimeFixtureContainer{
			ID:     strings.Repeat("d", 64),
			Name:   "legacy-merge",
			Labels: labels,
		})
		if code := save(); code != 0 {
			return code
		}
		return runMergeFixture(mode, directory)
	case "container":
		if len(arguments) == 3 && arguments[1] == "exists" {
			if index < 0 && mode == "pre-existing" && !imageIDPattern.MatchString(last) {
				state = append(state, runtimeFixtureContainer{
					ID:     fmt.Sprintf("%x", sha256.Sum256([]byte(last+"foreign"))),
					Name:   last,
					Labels: map[string]string{"foreign": "true"},
				})
				if code := save(); code != 0 {
					return code
				}
				return 0
			}
			if index < 0 {
				return 1
			}
			return 0
		}
	case "inspect":
		return runRuntimeInspectFixture(arguments, state, index)
	case "logs":
		if mode == "rfc log failure" {
			return 24
		}
		fmt.Fprintln(os.Stdout, "runtime log")
		return 0
	case "exec":
		if !strings.HasPrefix(mode, "rfc ") || len(arguments) < 4 || !imageIDPattern.MatchString(arguments[1]) {
			return 125
		}
		if arguments[2] == "cat" {
			if mode != "rfc empty capture" {
				fmt.Fprintln(os.Stdout, "capture fixture")
			}
			return 0
		}
		if arguments[2] == "tshark" {
			fmt.Fprintln(os.Stdout, "frame.time_relative,ip.src")
			return 0
		}
		return 125
	case "rm":
		if mode == "remove failure" || mode == "merge removal failure" {
			return 24
		}
		if index < 0 {
			return 1
		}
		state = append(state[:index], state[index+1:]...)
		return save()
	case "create":
		return runRuntimeCreateFixture(arguments, mode, &state, save)
	case "start":
		return runRuntimeStartFixture(mode, directory, &state, index, save)
	}
	return 125
}

func runRuntimeInspectFixture(arguments []string, state []runtimeFixtureContainer, index int) int {
	if slices.Contains(arguments, "{{.Image}}") {
		fmt.Fprintln(os.Stdout, os.Getenv("GOBFD_RUNTIME_DEV_IMAGE"))
		return 0
	}
	if index < 0 {
		return 1
	}
	if strings.Contains(strings.Join(arguments, " "), "{{.ID}}|") {
		fmt.Fprintf(os.Stdout, "%s|%s\n", state[index].ID, state[index].Labels["com.docker.compose.project"])
		return 0
	}
	if slices.Contains(arguments, "--format") {
		if strings.Contains(strings.Join(arguments, " "), ".Id") {
			return 125
		}
		response := runtimeInspectResponse{ID: state[index].ID, Labels: state[index].Labels}
		encoded, err := json.Marshal(response)
		if err != nil {
			return 125
		}
		if _, writeErr := os.Stdout.Write(append(encoded, '\n')); writeErr != nil {
			return 125
		}
		return 0
	}
	encoded, err := json.Marshal(state[index])
	if err != nil {
		return 125
	}
	if _, writeErr := os.Stdout.Write(append(encoded, '\n')); writeErr != nil {
		return 125
	}
	return 0
}

func runRuntimeCreateFixture(
	arguments []string,
	mode string,
	state *[]runtimeFixtureContainer,
	save func() int,
) int {
	if mode == "failed create" {
		return 23
	}
	if mode == "merge base retired" && slices.Contains(arguments, strings.Repeat("a", 64)) {
		return 23
	}
	name := ""
	labels := make(map[string]string)
	for i := 0; i+1 < len(arguments); i++ {
		if arguments[i] == "--name" {
			name = arguments[i+1]
		}
		if arguments[i] == "--label" {
			key, value, ok := strings.Cut(arguments[i+1], "=")
			if !ok {
				return 125
			}
			labels[key] = value
		}
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
	if mode == "foreign create" {
		labels["io.gobfd.run"] = "foreign"
		labels["foreign"] = "true"
	}
	*state = append(*state, runtimeFixtureContainer{ID: id, Name: name, Labels: labels})
	if code := save(); code != 0 {
		return code
	}
	if mode == "partial create" {
		return 23
	}
	if mode == "partial ID replacement" {
		(*state)[len(*state)-1].Name = name + "-original"
		*state = append(*state, runtimeFixtureContainer{
			ID:     fmt.Sprintf("%x", sha256.Sum256([]byte(name+"foreign"))),
			Name:   name,
			Labels: map[string]string{"foreign": "true"},
		})
		if code := save(); code != 0 {
			return code
		}
		fmt.Fprintln(os.Stdout, id)
		return 23
	}
	if mode == "wrong valid ID" {
		fmt.Fprintln(os.Stdout, strings.Repeat("e", 64))
		return 0
	}
	if mode == "malformed ID" {
		fmt.Fprintln(os.Stdout, "invalid-id")
		return 0
	}
	fmt.Fprintln(os.Stdout, id)
	return 0
}

func runRuntimeStartFixture(
	mode, directory string,
	state *[]runtimeFixtureContainer,
	index int,
	save func() int,
) int {
	if index < 0 {
		return 1
	}
	if strings.HasPrefix(mode, "merge ") {
		return runMergeFixture(mode, directory)
	}
	if mode == "replacement" {
		name := (*state)[index].Name
		(*state)[index].Name = name + "-original"
		*state = append(*state, runtimeFixtureContainer{
			ID:     fmt.Sprintf("%x", sha256.Sum256([]byte(name+"foreign"))),
			Name:   name,
			Labels: map[string]string{"foreign": "true"},
		})
		if code := save(); code != 0 {
			return code
		}
	}
	if mode == "canceled" {
		fmt.Fprintln(os.Stdout, "runtime-start-marker")
		time.Sleep(time.Minute)
		return 23
	}
	fmt.Fprintln(os.Stdout, `{"Action":"pass"}`)
	return 0
}

func runMergeFixture(mode, directory string) int {
	if err := os.WriteFile(filepath.Join(directory, "merge-started"), []byte("started"), 0o600); err != nil {
		return 125
	}
	if mode == "merge canceled" {
		time.Sleep(time.Minute)
		return 23
	}
	if mode != "merge no output" {
		reportPath := filepath.Join(os.Getenv("GOBFD_RUNTIME_MERGE_REPORT"), "packets.pcapng")
		if err := os.WriteFile(reportPath, []byte("merged capture fixture"), 0o600); err != nil {
			return 125
		}
	}
	return 0
}
