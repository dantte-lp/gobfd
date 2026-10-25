package clabbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // Test-only fixed GNU Make argv runs private fixtures and command fakes.
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The cold launcher has its own journal, distinct from the payload test-run.json.
// Its intent is exactly one lowercase hexadecimal nonce followed by a newline;
// output and container names derive from that nonce, never from stored paths.
const (
	launcherFakePrefix = "launcher:"
	launcherTestIntent = "controller-build.intent"
	launcherTestLock   = "controller-build.lock"
	launcherTestNonce  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	launcherTestID     = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	launcherFakeFile   = "launcher-fixture.json"
	launcherTestOwner  = "gobfd-vendors"
)

type launcherTestCall struct {
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
	Path       string   `json:"path,omitempty"`
}

type launcherTestContainer struct {
	ID        string   `json:"id"`
	Owner     string   `json:"owner"`
	RunID     string   `json:"run_id"`
	Arguments []string `json:"arguments"`
}

type launcherTestState struct {
	Calls             []launcherTestCall               `json:"calls"`
	Containers        map[string]launcherTestContainer `json:"containers"`
	CompileExit       int                              `json:"compile_exit"`
	ControllerExit    int                              `json:"controller_exit"`
	FailRemove        bool                             `json:"fail_remove"`
	CompilerStateExit int                              `json:"compiler_state_exit"`
	CompilerOOM       bool                             `json:"compiler_oom"`
	CompilerRunning   bool                             `json:"compiler_running"`
}

func newLauncherTestFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "checkout with spaces")
	if err := os.MkdirAll(runtimeDirectory(root), 0o700); err != nil {
		t.Fatal(err)
	}
	makefile, err := os.ReadFile("../../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"Makefile": makefile, "go.mod": []byte("module github.com/dantte-lp/gobfd\n"), "go.sum": nil,
	} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{
		"test/cmd/clabbootstrap", "test/internal/clabbootstrap", "test/internal/interopproject",
		"test/internal/podmanapi", "test/internal/vendorprofiles",
	} {
		path := filepath.Join(root, directory)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "fixture.go"), []byte("package fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "fake-bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"podman", "go"} {
		installFakeCommand(t, bin, name, launcherFakePrefix+root)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MAKEFLAGS", "")
	t.Setenv("MFLAGS", "")
	writeLauncherTestState(t, root, launcherTestState{Containers: make(map[string]launcherTestContainer)})
	return root
}

func runLauncherTestMake(t *testing.T, root string, arguments ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// #nosec G204 -- Only GNU Make runs with fixed argv inside this test's private checkout.
	argv := append([]string{"--no-print-directory", "--file=Makefile"}, arguments...)
	command := exec.CommandContext(ctx, "make", argv...)
	command.Dir = root
	command.WaitDelay = commandWaitDelay
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("private Make fixture timed out: %v\n%s", ctx.Err(), output)
	}
	if err == nil {
		return string(output), 0
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return string(output), exit.ExitCode()
	}
	t.Fatalf("start private Make fixture: %v", err)
	return "", -1
}

func readLauncherTestState(t *testing.T, root string) launcherTestState {
	t.Helper()
	state, err := loadLauncherFakeState(root)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func writeLauncherTestState(t *testing.T, root string, state launcherTestState) {
	t.Helper()
	if err := saveLauncherFakeState(root, state); err != nil {
		t.Fatal(err)
	}
}

func TestMakeClabTargetsUseIsolatedColdController(t *testing.T) {
	for _, tc := range []struct {
		target   string
		makeArgs []string
		want     []string
	}{
		{"interop-clab-bootstrap", []string{"ARGS=--verbose --dry-run"}, []string{"--verbose", "--dry-run"}},
		{"interop-clab", nil, []string{"--test"}},
		{"interop-clab-up", nil, []string{"--deploy"}},
		{"interop-clab-test", nil, []string{"--test-only"}},
		{"interop-clab-down", nil, []string{"--down"}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			root := newLauncherTestFixture(t)
			output, code := runLauncherTestMake(t, root, append([]string{tc.target}, tc.makeArgs...)...)
			if code != 0 {
				t.Fatalf("%s did not use the socket-free cold controller: exit=%d\n%s", tc.target, code, output)
			}
			state := readLauncherTestState(t, root)
			controllers := launcherControllerCalls(state)
			if len(controllers) != 1 || !slices.Equal(controllers[0].Arguments, tc.want) {
				t.Fatalf("%s controller calls = %+v; want exactly %v", tc.target, controllers, tc.want)
			}
			assertLauncherTestCompiler(t, state)
			assertLauncherTestClean(t, root, state)
		})
	}
}

func launcherControllerCalls(state launcherTestState) []launcherTestCall {
	var calls []launcherTestCall
	for _, call := range state.Calls {
		if call.Executable == "controller" {
			calls = append(calls, call)
		}
	}
	return calls
}

func assertLauncherTestCompiler(t *testing.T, state launcherTestState) {
	t.Helper()
	compiled := false
	for _, call := range state.Calls {
		if call.Executable == "go" || slices.Contains(call.Arguments, "compose") {
			t.Fatalf("cold launcher used host Go or the development compiler: %+v", call)
		}
		if call.Executable != "podman" || len(call.Arguments) == 0 ||
			(call.Arguments[0] != "create" && call.Arguments[0] != "run") {
			continue
		}
		args := call.Arguments
		if !slices.Contains(args, "docker.io/library/golang:1.27.3-trixie@sha256:"+
			"604106102d33b1084e37ca08adf98ac0813624ae8595bd0d7f3965ecebded09c") ||
			!slices.Contains(args, "CGO_ENABLED=0") || !slices.Contains(args, "build") ||
			!slices.Contains(args, "./test/cmd/clabbootstrap") {
			t.Fatalf("cold compiler lacks pinned native controller build: %v", args)
		}
		for _, argument := range args {
			if strings.Contains(argument, "podman.sock") || strings.Contains(argument, "docker.sock") ||
				strings.HasPrefix(argument, "CONTAINER_HOST=") || strings.HasPrefix(argument, "DOCKER_HOST=") ||
				strings.HasPrefix(argument, "PODMAN_HOST=") {
				t.Fatalf("cold compiler received engine access: %v", args)
			}
		}
		if launcherArgument(args, "--cpus") != "2" || launcherArgument(args, "--memory") != "2g" ||
			launcherArgument(args, "--memory-swap") != "2g" || launcherArgument(args, "--pids-limit") != "512" {
			t.Fatalf("cold compiler lacks explicit resource bounds: %v", args)
		}
		_, readOnly := launcherTestMount(args, "/src")
		if !readOnly || launcherArgument(args, "--network") == "host" ||
			!slices.Contains(args, "--read-only") || !strings.EqualFold(launcherArgument(args, "--cap-drop"), "all") {
			t.Fatalf("cold compiler lacks source/root/network isolation: %v", args)
		}
		compiled = true
	}
	if !compiled {
		t.Fatal("Make did not emit an isolated compiler container")
	}
}

func assertLauncherTestClean(t *testing.T, root string, state launcherTestState) {
	t.Helper()
	if len(state.Containers) != 0 {
		t.Fatalf("cold launcher retained containers: %+v", state.Containers)
	}
	entries, err := os.ReadDir(runtimeDirectory(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != launcherTestLock {
		t.Fatalf("cold launcher retained output/intent or lost its persistent lock: %v", entries)
	}
}

func TestMakeClabRejectsUnsuccessfulCompilerState(t *testing.T) {
	for _, phase := range []string{"nonzero exit", "OOM", "still running"} {
		t.Run(phase, func(t *testing.T) {
			root := newLauncherTestFixture(t)
			state := readLauncherTestState(t, root)
			switch phase {
			case "nonzero exit":
				state.CompilerStateExit = 42
			case "OOM":
				state.CompilerOOM = true
			case "still running":
				state.CompilerRunning = true
			}
			writeLauncherTestState(t, root, state)
			output, code := runLauncherTestMake(t, root, "interop-clab-down")
			state = readLauncherTestState(t, root)
			started := slices.ContainsFunc(state.Calls, func(call launcherTestCall) bool {
				return call.Executable == "podman" && len(call.Arguments) > 0 && call.Arguments[0] == "start"
			})
			if !started {
				t.Fatalf("fault did not reach the compiler runtime: exit=%d\n%s", code, output)
			}
			if code == 0 || len(launcherControllerCalls(state)) != 0 {
				t.Fatalf("native controller ran after unsuccessful compiler state %q: exit=%d\n%s", phase, code, output)
			}
			assertLauncherTestClean(t, root, state)
		})
	}
}

func TestMakeClabLauncherPreservesFailureAndCleansOwnedResources(t *testing.T) {
	for _, phase := range []string{"compiler", "controller"} {
		t.Run(phase, func(t *testing.T) {
			root := newLauncherTestFixture(t)
			state := readLauncherTestState(t, root)
			if phase == "compiler" {
				state.CompileExit = 37
			} else {
				state.ControllerExit = 37
			}
			writeLauncherTestState(t, root, state)
			output, code := runLauncherTestMake(t, root, "interop-clab-down")
			// GNU Make itself exits 2; its diagnostic preserves the recipe's exact child status.
			if code != 2 || !strings.Contains(output, "Error 37") {
				t.Fatalf("%s status was lost: make exit=%d\n%s", phase, code, output)
			}
			state = readLauncherTestState(t, root)
			if phase == "compiler" && len(launcherControllerCalls(state)) != 0 {
				t.Fatal("failed compilation executed a stale controller")
			}
			assertLauncherTestClean(t, root, state)
		})
	}
}

func TestMakeClabLauncherKeepsLockInodeAndUsesFreshOutput(t *testing.T) {
	root := newLauncherTestFixture(t)
	if output, code := runLauncherTestMake(t, root, "interop-clab-down"); code != 0 {
		t.Fatalf("first cold controller failed: %s", output)
	}
	path := filepath.Join(runtimeDirectory(root), launcherTestLock)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if output, code := runLauncherTestMake(t, root, "interop-clab-down"); code != 0 {
		t.Fatalf("second cold controller failed: %s", output)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("persistent bootstrap lock inode was replaced: %v", err)
	}
	state := readLauncherTestState(t, root)
	controllers := launcherControllerCalls(state)
	if len(controllers) != 2 || controllers[0].Path == controllers[1].Path {
		t.Fatalf("cold invocations reused an old output path: %+v", controllers)
	}
	assertLauncherTestClean(t, root, state)
}

func seedLauncherTestIntent(t *testing.T, root, owner, runID string) {
	t.Helper()
	directory := filepath.Join(runtimeDirectory(root), "controller-build-"+launcherTestNonce)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "source-marker"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDirectory(root), launcherTestIntent),
		[]byte(launcherTestNonce+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := readLauncherTestState(t, root)
	state.Containers["gobfd-clab-bootstrap-"+launcherTestNonce] = launcherTestContainer{
		ID: launcherTestID, Owner: owner, RunID: runID,
	}
	writeLauncherTestState(t, root, state)
}

func TestMakeClabLauncherRecoversPrecreateIntentAndRetries(t *testing.T) {
	root := newLauncherTestFixture(t)
	seedLauncherTestIntent(t, root, launcherTestOwner, launcherTestNonce)
	state := readLauncherTestState(t, root)
	state.FailRemove = true
	writeLauncherTestState(t, root, state)
	output, code := runLauncherTestMake(t, root, "interop-clab-down")
	if code == 0 {
		t.Fatalf("failed recovery unexpectedly succeeded: %s", output)
	}
	state = readLauncherTestState(t, root)
	removed := false
	for _, call := range state.Calls {
		if call.Executable == "go" || slices.Contains(call.Arguments, "compose") {
			t.Fatalf("recovery used a forbidden compiler path: %+v", call)
		}
		if len(call.Arguments) > 0 && call.Arguments[0] == "rm" {
			if call.Arguments[len(call.Arguments)-1] != launcherTestID {
				t.Fatalf("recovery removed by name instead of exact ID: %+v", call)
			}
			removed = true
		}
	}
	if !removed || len(state.Containers) != 1 || len(launcherControllerCalls(state)) != 0 {
		t.Fatal("failed precreate-gap recovery did not retain its recorded intent and container")
	}
	assertLauncherTestIntentRetained(t, root)
	state.FailRemove = false
	state.Calls = nil
	writeLauncherTestState(t, root, state)
	output, code = runLauncherTestMake(t, root, "interop-clab-down")
	if code != 0 {
		t.Fatalf("precreate-gap recovery retry failed: %s", output)
	}
	state = readLauncherTestState(t, root)
	if len(state.Calls) == 0 || len(launcherControllerCalls(state)) != 1 {
		t.Fatal("recovery retry did not execute the fresh controller")
	}
	recovered, created := -1, -1
	for index, call := range state.Calls {
		if len(call.Arguments) == 0 {
			continue
		}
		if call.Arguments[0] == "rm" && call.Arguments[len(call.Arguments)-1] == launcherTestID {
			recovered = index
		}
		if created == -1 && (call.Arguments[0] == "create" || call.Arguments[0] == "run") {
			created = index
		}
	}
	if recovered < 0 || created <= recovered {
		t.Fatal("new compiler creation preceded exact recovery of the previous intent")
	}
	assertLauncherTestClean(t, root, state)
}

func TestMakeClabLauncherRefusesForeignRecoveryBeforeMutation(t *testing.T) {
	for _, field := range []string{"owner", "run"} {
		t.Run(field, func(t *testing.T) {
			root := newLauncherTestFixture(t)
			owner, runID := launcherTestOwner, launcherTestNonce
			if field == "owner" {
				owner = "foreign"
			} else {
				runID = "foreign"
			}
			seedLauncherTestIntent(t, root, owner, runID)
			output, code := runLauncherTestMake(t, root, "interop-clab-down")
			if code == 0 {
				t.Fatalf("foreign %s was accepted: %s", field, output)
			}
			state := readLauncherTestState(t, root)
			inspected := false
			for _, call := range state.Calls {
				if call.Executable != "podman" || len(call.Arguments) == 0 {
					t.Fatalf("foreign recovery escaped the fixed Podman boundary: %+v", call)
				}
				switch call.Arguments[0] {
				case "inspect":
					reference := call.Arguments[len(call.Arguments)-1]
					inspected = reference == "gobfd-clab-bootstrap-"+launcherTestNonce || reference == launcherTestID
				case "rm", "create", "run", "start", "compose":
					t.Fatalf("foreign identity allowed mutation: %+v", call)
				}
			}
			if !inspected || len(state.Containers) != 1 {
				t.Fatal("foreign recovery failed before reaching identity validation")
			}
			assertLauncherTestIntentRetained(t, root)
		})
	}
}

func assertLauncherTestIntentRetained(t *testing.T, root string) {
	t.Helper()
	intent, err := os.ReadFile(filepath.Join(runtimeDirectory(root), launcherTestIntent))
	if err != nil || string(intent) != launcherTestNonce+"\n" {
		t.Fatalf("failed recovery changed intent: %v", err)
	}
	marker := filepath.Join(runtimeDirectory(root), "controller-build-"+launcherTestNonce, "source-marker")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("failed recovery lost private output: %v", err)
	}
}

func TestMakeClabLauncherDryPrintDoesNotExecute(t *testing.T) {
	root := newLauncherTestFixture(t)
	output, code := runLauncherTestMake(t, root, "-n", "interop-clab-bootstrap", "ARGS=--dry-run")
	if code != 0 {
		t.Fatalf("Make dry print failed: %s", output)
	}
	if state := readLauncherTestState(t, root); len(state.Calls) != 0 || len(state.Containers) != 0 {
		t.Fatalf("Make dry print executed a compiler/controller: %+v", state)
	}
	entries, err := os.ReadDir(runtimeDirectory(root))
	if err != nil || len(entries) != 0 {
		t.Fatalf("Make dry print created runtime resources: %v %v", entries, err)
	}
}

func runLauncherFakeCommand(root string) int {
	state, err := loadLauncherFakeState(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	name := filepath.Base(os.Args[0])
	state.Calls = append(state.Calls, launcherTestCall{Executable: name, Arguments: os.Args[1:]})
	code := 0
	switch name {
	case "go":
		fmt.Fprintln(os.Stderr, "host Go is forbidden in the cold launcher")
		code = 97
	case "podman":
		code, err = runLauncherFakePodman(root, &state, os.Args[1:])
	default:
		state.Calls[len(state.Calls)-1].Executable = "controller"
		state.Calls[len(state.Calls)-1].Path, err = os.Executable()
		if err != nil {
			break
		}
		code, err = runLauncherFakeController(root, state)
	}
	if saveErr := saveLauncherFakeState(root, state); saveErr != nil {
		err = errors.Join(err, saveErr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	return code
}

func loadLauncherFakeState(root string) (launcherTestState, error) {
	data, err := os.ReadFile(filepath.Join(root, launcherFakeFile))
	if err != nil {
		return launcherTestState{}, err
	}
	var state launcherTestState
	if err := json.Unmarshal(data, &state); err != nil {
		return launcherTestState{}, err
	}
	return state, nil
}

func saveLauncherFakeState(root string, state launcherTestState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, launcherFakeFile), data, 0o600)
}

func runLauncherFakePodman(root string, state *launcherTestState, args []string) (int, error) {
	if len(args) == 0 || slices.Contains(args, "compose") {
		return 97, errors.New("engine-enabled development compiler is forbidden")
	}
	reference := args[len(args)-1]
	name, container, present := launcherFindContainer(*state, reference)
	switch args[0] {
	case "container":
		if len(args) != 3 || args[1] != "exists" {
			return 125, fmt.Errorf("unexpected container query: %v", args)
		}
		if present {
			return 0, nil
		}
		return 1, nil
	case "inspect":
		if !present {
			return 1, nil
		}
		if format := launcherArgument(args, "--format"); strings.Contains(format, ".State.") {
			for _, field := range []string{"Status", "Running", "ExitCode", "OOMKilled"} {
				if !strings.Contains(format, ".State."+field) {
					return 125, fmt.Errorf("compiler state inspection omits %s", field)
				}
			}
			status := "exited"
			if state.CompilerRunning {
				status = "running"
			}
			_, err := fmt.Fprintf(os.Stdout, "%s|%t|%d|%t\n",
				status, state.CompilerRunning, state.CompilerStateExit, state.CompilerOOM)
			return 0, err
		}
		return 0, printLauncherFakeIdentity(args, container)
	case "create", "run":
		return createLauncherFakeContainer(root, state, args)
	case "start":
		if !present {
			return 1, nil
		}
		if state.CompileExit != 0 {
			return state.CompileExit, nil
		}
		return 0, writeLauncherFakeBinary(root, container.Arguments)
	case "rm":
		if !present || reference != container.ID {
			return 125, fmt.Errorf("removal lacks an existing exact immutable ID: %v", args)
		}
		if state.FailRemove {
			return 45, nil
		}
		delete(state.Containers, name)
		return 0, nil
	case "image":
		if len(args) == 3 && args[1] == "exists" {
			return 0, nil
		}
	}
	return 125, fmt.Errorf("unexpected cold-launcher Podman argv: %v", args)
}

func launcherFindContainer(state launcherTestState, reference string) (string, launcherTestContainer, bool) {
	for name, container := range state.Containers {
		if reference == name || reference == container.ID {
			return name, container, true
		}
	}
	return "", launcherTestContainer{}, false
}

func printLauncherFakeIdentity(args []string, container launcherTestContainer) error {
	format := launcherArgument(args, "--format")
	if !strings.Contains(format, "io.gobfd.interop-clab.owner") ||
		!strings.Contains(format, "io.gobfd.interop-clab.run") ||
		(!strings.Contains(format, ".Id") && !strings.Contains(format, ".ID")) {
		return fmt.Errorf("ownership inspection omitted ID/owner/run: %v", args)
	}
	var output string
	switch strings.Count(format, "|") {
	case 2:
		output = container.ID + "|" + container.Owner + "|" + container.RunID
	case 3:
		output = container.ID + "|gobfd-vendors|" + container.Owner + "|" + container.RunID
	default:
		return fmt.Errorf("ownership inspection must retain ID/owner/run together: %v", args)
	}
	_, err := fmt.Fprintln(os.Stdout, output)
	return err
}

func launcherArgument(arguments []string, flag string) string {
	for index, argument := range arguments {
		if argument == flag && index+1 < len(arguments) {
			return arguments[index+1]
		}
		if value, found := strings.CutPrefix(argument, flag+"="); found {
			return value
		}
	}
	return ""
}

func createLauncherFakeContainer(root string, state *launcherTestState, args []string) (int, error) {
	intentPath := filepath.Join(runtimeDirectory(root), launcherTestIntent)
	info, err := os.Lstat(intentPath)
	if err != nil || info.Mode() != 0o600 {
		return 125, fmt.Errorf("compiler intent is not a private regular file: %w", errors.Join(err, errLifecycleState))
	}
	stat, valid := info.Sys().(*syscall.Stat_t)
	if !valid || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 {
		return 125, errors.New("compiler intent is not owned or has multiple links")
	}
	intent, err := os.ReadFile(intentPath)
	if err != nil {
		return 125, fmt.Errorf("compiler create preceded durable intent: %w", err)
	}
	nonce := strings.TrimSuffix(string(intent), "\n")
	if len(nonce) != 32 || string(intent) != nonce+"\n" || strings.Trim(nonce, "0123456789abcdef") != "" {
		return 125, fmt.Errorf("invalid cold-launcher intent %q", intent)
	}
	name := launcherArgument(args, "--name")
	if name != "gobfd-clab-bootstrap-"+nonce {
		return 125, fmt.Errorf("compiler name is not derived from private intent: %s", name)
	}
	if _, exists := state.Containers[name]; exists {
		return 125, errors.New("compiler name is already occupied")
	}
	container := launcherTestContainer{ID: strings.Repeat("c", 64), Arguments: args}
	for index, argument := range args {
		if argument == "--label" && index+1 < len(args) {
			key, value, _ := strings.Cut(args[index+1], "=")
			switch key {
			case "io.gobfd.interop-clab.owner":
				container.Owner = value
			case "io.gobfd.interop-clab.run":
				container.RunID = value
			}
		}
	}
	if container.Owner != launcherTestOwner || container.RunID != nonce {
		return 125, errors.New("compiler owner/run labels differ from intent")
	}
	state.Containers[name] = container
	if cidfile := launcherArgument(args, "--cidfile"); cidfile != "" {
		if writeErr := os.WriteFile(cidfile, []byte(container.ID+"\n"), 0o600); writeErr != nil {
			return 125, writeErr
		}
	}
	if args[0] == "run" {
		if slices.Contains(args, "--rm") {
			defer delete(state.Containers, name)
		}
		if state.CompileExit != 0 {
			return state.CompileExit, nil
		}
		return 0, writeLauncherFakeBinary(root, args)
	}
	_, err = fmt.Fprintln(os.Stdout, container.ID)
	return 0, err
}

func writeLauncherFakeBinary(root string, args []string) error {
	output := launcherArgument(args, "-o")
	if !strings.HasPrefix(output, "/out/") {
		return fmt.Errorf("compiler output is not in private /out: %s", output)
	}
	outputRoot, _ := launcherTestMount(args, "/out")
	if outputRoot == "" || !strings.HasPrefix(outputRoot, runtimeDirectory(root)+string(filepath.Separator)) {
		return fmt.Errorf("compiler did not bind a private output directory: %s", outputRoot)
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	path := filepath.Join(outputRoot, strings.TrimPrefix(output, "/out/"))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return errors.Join(err, input.Close())
	}
	_, copyErr := io.Copy(file, input)
	return errors.Join(copyErr, input.Close(), file.Close())
}

func launcherTestMount(args []string, want string) (string, bool) {
	for index, argument := range args {
		if argument != "--mount" || index+1 == len(args) {
			continue
		}
		fields := strings.Split(args[index+1], ",")
		var source, destination string
		readOnly := false
		for _, field := range fields {
			if field == "ro" || field == "ro=true" || field == "readonly" || field == "readonly=true" {
				readOnly = true
			}
			key, value, _ := strings.Cut(field, "=")
			switch key {
			case "source", "src":
				source = value
			case "destination", "dst", "target":
				destination = value
			}
		}
		if destination == want {
			return source, readOnly
		}
	}
	return "", false
}

func runLauncherFakeController(root string, state launcherTestState) (int, error) {
	path, err := os.Executable()
	if err != nil {
		return 125, err
	}
	intent, err := os.ReadFile(filepath.Join(runtimeDirectory(root), launcherTestIntent))
	if err != nil {
		return 125, err
	}
	nonce := strings.TrimSuffix(string(intent), "\n")
	directory := filepath.Join(runtimeDirectory(root), "controller-build-"+nonce)
	if filepath.Dir(path) != directory || len(state.Containers) != 0 {
		return 125, errors.New("controller is stale, outside private output, or still shares a compiler")
	}
	return state.ControllerExit, nil
}
