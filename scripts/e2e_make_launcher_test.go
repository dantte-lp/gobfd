package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // Real Make and flock execute only isolated test-binary command fixtures.
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const e2eMakeState = "GOBFD_E2E_MAKE_FIXTURE_STATE"

type e2eMakeInvocation struct {
	Stage       string            `json:"stage"`
	Target      string            `json:"target"`
	Args        []string          `json:"args"`
	Directory   string            `json:"directory"`
	Environment map[string]string `json:"environment"`
}

type e2eMakeFixture struct {
	root     string
	state    string
	commands string
	self     string
}

func TestMain(m *testing.M) {
	if os.Getenv(e2eMakeState) != "" {
		switch filepath.Base(os.Args[0]) {
		case "podman", "go", "e2ectl":
			os.Exit(runE2EMakeFixture())
		}
	}
	if os.Getenv(benchmarkMakeProducerEnabled) == "1" && filepath.Base(os.Args[0]) == "producer" {
		os.Exit(runBenchmarkMakeProducer())
	}
	os.Exit(m.Run())
}

func TestE2EMakeAggregatesUsePinnedCompilerAndHostRunner(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"routing", "rfc", "overlay", "linux", "vendor"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			fixture := newE2EMakeFixture(t)
			output, err := fixture.command(t.Context(), target).CombinedOutput()
			if err != nil {
				t.Fatalf("aggregate failed with poisoned host Go: %v\n%s", err, output)
			}
			calls := fixture.invocations(t)
			if len(calls) != 3 || calls[0].Stage != "dev" || calls[1].Stage != "compiler" || calls[2].Stage != "runtime" {
				t.Fatalf("want dev/compiler/runtime once, got %+v", calls)
			}
			requireE2ECompilerContract(t, fixture, calls[1])
			runtime := calls[2]
			if !slices.Equal(runtime.Args, []string{target}) || runtime.Directory != fixture.root {
				t.Fatalf("runner arguments/cwd changed: %+v", runtime)
			}
			for name, want := range e2eMakeCallerEnvironment() {
				if runtime.Environment[name] != want {
					t.Errorf("runner environment %s=%q, want %q", name, runtime.Environment[name], want)
				}
			}
			artifact := filepath.Join(fixture.root, "reports", "e2e", target, "fixture", "environment.json")
			if data, err := os.ReadFile(artifact); err != nil || !bytes.Contains(data, []byte(target)) {
				t.Fatalf("selected runner artifact missing: %q, %v", data, err)
			}
			if info, err := os.Lstat(filepath.Join(fixture.root, "bin", "e2ectl")); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("persistent build artifact missing or replaced by symlink: %v", err)
			}
		})
	}
}

func TestE2EMakeCompilerFailureNeverRunsExistingBinary(t *testing.T) {
	t.Parallel()
	fixture := newE2EMakeFixture(t)
	if err := os.Mkdir(filepath.Join(fixture.root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyE2EFixtureBinary(fixture.self, filepath.Join(fixture.root, "bin", "e2ectl")); err != nil {
		t.Fatal(err)
	}
	command := fixture.command(t.Context(), "overlay")
	command.Env = append(command.Env, "GOBFD_E2E_MAKE_COMPILER_FAIL=1")
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("fixture compiler exit 29")) {
		t.Fatalf("compiler failure lost: %v\n%s", err, output)
	}
	calls := fixture.invocations(t)
	if len(calls) != 2 || calls[1].Stage != "compiler" {
		t.Fatalf("compiler failure executed stale runtime or wrong command: %+v", calls)
	}
}

func TestE2EMakeRuntimeFailureRemainsFailure(t *testing.T) {
	t.Parallel()
	fixture := newE2EMakeFixture(t)
	command := fixture.command(t.Context(), "vendor")
	command.Env = append(command.Env, "GOBFD_E2E_MAKE_RUNTIME_FAIL=vendor")
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("fixture runtime exit 23")) {
		t.Fatalf("runtime failure lost: %v\n%s", err, output)
	}
	calls := fixture.invocations(t)
	if len(calls) != 3 || calls[2].Stage != "runtime" || calls[2].Target != "vendor" {
		t.Fatalf("runtime was not the failure producer: %+v", calls)
	}
}

func TestE2EMakeSerializesWholeLifecycleAndUnlocksAfterFailure(t *testing.T) {
	t.Parallel()
	fixture := newE2EMakeFixture(t)
	sibling := filepath.Join(filepath.Dir(fixture.root), "unrelated")
	if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	first := fixture.command(ctx, "overlay")
	first.Env = append(first.Env, "GOBFD_E2E_MAKE_BLOCK=overlay", "GOBFD_E2E_MAKE_RUNTIME_FAIL=overlay")
	var firstOutput, secondOutput bytes.Buffer
	first.Stdout, first.Stderr = &firstOutput, &firstOutput
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Wait() }()
	runtimeReady := make(chan error, 1)
	go func() { runtimeReady <- waitE2EFixturePath(ctx, filepath.Join(fixture.state, "runtime-started")) }()
	select {
	case err := <-firstDone:
		t.Fatalf("first runner exited before runtime: %v\n%s", err, &firstOutput)
	case err := <-runtimeReady:
		if err != nil {
			cancel()
			firstErr := <-firstDone
			t.Fatalf("first runner never reached runtime: %v; shutdown result: %v\n%s", err, firstErr, &firstOutput)
		}
	}
	second := fixture.command(ctx, "linux")
	second.Stdout, second.Stderr = &secondOutput, &secondOutput
	if err := second.Start(); err != nil {
		cancel()
		firstErr := <-firstDone
		t.Fatalf("start second lifecycle: %v; first lifecycle shutdown: %v", err, firstErr)
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- second.Wait() }()
	select {
	case err := <-secondDone:
		cancel()
		firstErr := <-firstDone
		t.Fatalf("second lifecycle escaped held lock: %v; first lifecycle shutdown: %v\n%s", err, firstErr, &secondOutput)
	case <-time.After(200 * time.Millisecond):
	}
	if calls := fixture.invocations(t); len(calls) != 3 {
		t.Errorf("another dev recreation/compiler ran during active runtime: %+v", calls)
	}
	if err := os.WriteFile(filepath.Join(fixture.state, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-firstDone; err == nil {
		t.Errorf("first runtime failure lost: %s", &firstOutput)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("failure retained lock or broke next build: %v\n%s", err, &secondOutput)
	}
	calls := fixture.invocations(t)
	if len(calls) != 6 || calls[3].Stage != "dev" || calls[4].Stage != "compiler" || calls[5].Target != "linux" {
		t.Fatalf("serialized second lifecycle missing: %+v", calls)
	}
	if data, err := os.ReadFile(sibling); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated sibling changed: %q, %v", data, err)
	}
}

func TestE2EMakeRejectsLinkedOutputBeforeEngineCommands(t *testing.T) {
	t.Parallel()
	for _, linked := range []string{"bin", "bin/e2ectl"} {
		t.Run(linked, func(t *testing.T) {
			t.Parallel()
			fixture := newE2EMakeFixture(t)
			sibling := t.TempDir()
			if linked != "bin" {
				if err := os.Mkdir(filepath.Join(fixture.root, "bin"), 0o700); err != nil {
					t.Fatal(err)
				}
				sibling = filepath.Join(sibling, "unrelated")
				if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(sibling, filepath.Join(fixture.root, linked)); err != nil {
				t.Fatal(err)
			}
			output, err := fixture.command(t.Context(), "overlay").CombinedOutput()
			if err == nil || len(fixture.invocations(t)) != 0 {
				t.Fatalf("linked build output reached engine: %v\n%s", err, output)
			}
			if linked != "bin" {
				if data, err := os.ReadFile(sibling); err != nil || string(data) != "keep" {
					t.Fatalf("symlink target overwritten: %q, %v", data, err)
				}
			}
		})
	}
}

func TestE2EMakeRejectsMissingAmbiguousAndUnpinnedCompiler(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		"FROM example.invalid/compiler:latest AS dev\n",
		"FROM example.invalid/compiler:latest AS go-toolchain\n",
		"FROM docker.io/library/golang:1.27.3-trixie@sha256:bad AS go-toolchain\n",
		"FROM docker.io/library/golang:1.27.2-trixie@sha256:" + strings.Repeat("a", 64) + " AS go-toolchain\n",
	} {
		fixture := newE2EMakeFixture(t)
		path := filepath.Join(fixture.root, "deployments", "docker", "Containerfile.dev")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		help := fixture.command(t.Context(), "overlay")
		help.Args[2] = "e2e-help"
		if output, err := help.CombinedOutput(); err != nil || len(fixture.invocations(t)) != 0 {
			t.Fatalf("aggregate-only compiler validation broke unrelated help: %v\n%s", err, output)
		}
		output, err := fixture.command(t.Context(), "overlay").CombinedOutput()
		if err == nil || len(fixture.invocations(t)) != 0 {
			t.Fatalf("invalid compiler pin reached engine: %v\n%s", err, output)
		}
	}
	fixture := newE2EMakeFixture(t)
	path := filepath.Join(fixture.root, "deployments", "docker", "Containerfile.dev")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(path, append(content, content...), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	output, err := fixture.command(t.Context(), "overlay").CombinedOutput()
	if err == nil || len(fixture.invocations(t)) != 0 {
		t.Fatalf("ambiguous compiler pin reached engine: %v\n%s", err, output)
	}
}

func TestE2EMakeDryRunCreatesNoBuildOrEngineState(t *testing.T) {
	t.Parallel()
	fixture := newE2EMakeFixture(t)
	command := fixture.command(t.Context(), "overlay")
	command.Args = append(command.Args, "-n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, output)
	}
	if len(fixture.invocations(t)) != 0 {
		t.Fatal("dry run executed an engine or compiler command")
	}
	for _, name := range []string{"bin", "reports"} {
		if _, err := os.Lstat(filepath.Join(fixture.root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("dry run created %s: %v", name, err)
		}
	}
}

func TestE2EMakeInvalidRoutingProjectStopsBeforeCommands(t *testing.T) {
	t.Parallel()
	fixture := newE2EMakeFixture(t)
	command := fixture.command(t.Context(), "routing")
	command.Args = append(command.Args, "INTEROP_PROJECT_NAME=bad/name")
	if output, err := command.CombinedOutput(); err == nil || len(fixture.invocations(t)) != 0 {
		t.Fatalf("invalid routing identity reached engine: %v\n%s", err, output)
	}
}

func newE2EMakeFixture(t *testing.T) e2eMakeFixture {
	t.Helper()
	parent := t.TempDir()
	fixture := e2eMakeFixture{
		root: filepath.Join(parent, "checkout with spaces"), state: filepath.Join(parent, "state"),
		commands: filepath.Join(parent, "commands"),
	}
	var err error
	fixture.self, err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		fixture.root, fixture.state, fixture.commands,
		filepath.Join(fixture.root, "deployments", "docker"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Makefile", "go.mod", "deployments/docker/Containerfile.dev"} {
		data, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"podman", "go"} {
		if err := os.Symlink(fixture.self, filepath.Join(fixture.commands, name)); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture e2eMakeFixture) command(ctx context.Context, target string) *exec.Cmd {
	command := exec.CommandContext(ctx, "make", "--no-print-directory", "e2e-"+target)
	command.Dir = fixture.root
	command.Env = append(os.Environ(),
		e2eMakeState+"="+fixture.state, "GOBFD_E2E_MAKE_SELF="+fixture.self,
		"PATH="+fixture.commands+string(os.PathListSeparator)+os.Getenv("PATH"),
		"MAKEFLAGS=", "MFLAGS=", "GORACE=atexit_sleep_ms=0",
		"GOBFD_E2E_MAKE_TARGET="+target,
	)
	for name, value := range e2eMakeCallerEnvironment() {
		command.Env = append(command.Env, name+"="+value)
	}
	return command
}

func e2eMakeCallerEnvironment() map[string]string {
	return map[string]string{
		"COMPOSE_PROJECT_NAME": "fixture-dev", "INTEROP_PROJECT_NAME": "fixture-base",
		"INTEROP_BGP_PROJECT_NAME": "fixture-bgp", "PODMAN_COMPOSE_PROVIDER": "/fixture/provider with spaces",
		"DOCKER_BUILDKIT": "0", "DOCKER_HOST": "unix:///fixture/docker.sock",
		"PODMAN_HOST": "unix:///fixture/podman.sock", "CONTAINER_HOST": "unix:///fixture/container.sock",
		"PODMAN_SOCKET_PATH": "/fixture/engine.sock", "E2E_LINUX_IMAGE": "fixture-linux-image",
		"E2E_OVERLAY_IMAGE": "fixture-overlay-image",
	}
}

func (fixture e2eMakeFixture) invocations(t *testing.T) []e2eMakeInvocation {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.state, "calls.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []e2eMakeInvocation
	for line := range bytes.SplitSeq(bytes.TrimSpace(data), []byte("\n")) {
		var call e2eMakeInvocation
		if err := json.Unmarshal(line, &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}

func requireE2ECompilerContract(t *testing.T, fixture e2eMakeFixture, call e2eMakeInvocation) {
	t.Helper()
	joined := strings.Join(call.Args, "\n")
	for _, required := range []string{
		"--rm", "CGO_ENABLED=0", "GOTOOLCHAIN=local", "-trimpath", "-mod=readonly", "./test/cmd/e2ectl",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("compiler lacks %q: %q", required, call.Args)
		}
	}
	for name, want := range map[string]string{
		"--cpus": "2", "--memory": "2g", "--memory-swap": "2g",
		"--pids-limit": "512", "--cap-drop": "ALL",
	} {
		if value := e2eMakeOption(call.Args, name); value != want {
			t.Errorf("compiler %s=%q, want %q", name, value, want)
		}
	}
	if entrypoint := e2eMakeOption(call.Args, "--entrypoint"); filepath.Base(entrypoint) != "go" {
		t.Errorf("compiler entrypoint is not fixed Go: %q", entrypoint)
	}
	var source, output bool
	for _, argument := range call.Args {
		parts := strings.Split(argument, ":")
		if len(parts) >= 3 && parts[0] == fixture.root {
			source = slices.Contains(strings.Split(parts[2], ","), "ro")
		}
		if len(parts) >= 3 && parts[0] == filepath.Join(fixture.root, "bin") {
			output = slices.Contains(strings.Split(parts[2], ","), "rw")
		}
		for _, forbidden := range []string{
			"podman.sock", "docker.sock", "--volumes-from", "--privileged", "--cap-add",
			"DOCKER_HOST=", "CONTAINER_HOST=", "PODMAN_HOST=", "GOGC=",
		} {
			if strings.Contains(argument, forbidden) {
				t.Errorf("compiler exposed runtime authority or GC override %q", argument)
			}
		}
	}
	if !source || !output || e2eMakeOption(call.Args, "--network") == "host" {
		t.Errorf("compiler mounts/network violate isolation: %q", call.Args)
	}
	containerfile := readContractFile(t, filepath.Join(fixture.root, "deployments", "docker", "Containerfile.dev"))
	for line := range strings.SplitSeq(containerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 4 && fields[0] == "FROM" && fields[3] == "go-toolchain" && !slices.Contains(call.Args, fields[1]) {
			t.Errorf("compiler did not use current source pin %q", fields[1])
		}
	}
}

func e2eMakeOption(args []string, name string) string {
	for index, argument := range args {
		if value, found := strings.CutPrefix(argument, name+"="); found {
			return value
		}
		if argument == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func runE2EMakeFixture() int {
	stage := filepath.Base(os.Args[0])
	switch {
	case stage == "go":
		stage = "host-go"
	case stage == "e2ectl":
		stage = "runtime"
	case len(os.Args) > 1 && os.Args[1] == "run":
		stage = "compiler"
	default:
		stage = "dev"
	}
	directory, err := os.Getwd()
	if err != nil {
		return 90
	}
	call := e2eMakeInvocation{
		Stage: stage, Target: os.Getenv("GOBFD_E2E_MAKE_TARGET"),
		Args: os.Args[1:], Directory: directory, Environment: make(map[string]string),
	}
	for name := range e2eMakeCallerEnvironment() {
		call.Environment[name] = os.Getenv(name)
	}
	data, err := json.Marshal(call)
	if err != nil {
		return 90
	}
	file, err := os.OpenFile(filepath.Join(os.Getenv(e2eMakeState), "calls.jsonl"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return 90
	}
	_, writeErr := file.Write(append(data, '\n'))
	if errors.Join(writeErr, file.Close()) != nil {
		return 90
	}
	switch stage {
	case "host-go":
		fmt.Fprintln(os.Stderr, "poisoned host Go was invoked")
		return 91
	case "dev":
		return 0
	case "compiler":
		if os.Getenv("GOBFD_E2E_MAKE_COMPILER_FAIL") != "" {
			fmt.Fprintln(os.Stderr, "fixture compiler exit 29")
			return 29
		}
		destination := filepath.Join(directory, "bin", "e2ectl")
		if err := copyE2EFixtureBinary(os.Getenv("GOBFD_E2E_MAKE_SELF"), destination); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 90
		}
		return 0
	case "runtime":
		return runE2EMakeRuntime(call)
	default:
		return 90
	}
}

func runE2EMakeRuntime(call e2eMakeInvocation) int {
	if len(call.Args) != 1 || call.Args[0] != call.Target {
		return 92
	}
	if os.Getenv("GOBFD_E2E_MAKE_BLOCK") == call.Target {
		if err := os.WriteFile(filepath.Join(os.Getenv(e2eMakeState), "runtime-started"), nil, 0o600); err != nil {
			return 90
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := waitE2EFixturePath(ctx, filepath.Join(os.Getenv(e2eMakeState), "release")); err != nil {
			return 93
		}
	}
	report := filepath.Join(call.Directory, "reports", "e2e", call.Target, "fixture")
	if err := os.MkdirAll(report, 0o700); err != nil {
		return 90
	}
	data := []byte(`{"target":"` + call.Target + `"}`)
	if err := os.WriteFile(filepath.Join(report, "environment.json"), data, 0o600); err != nil {
		return 90
	}
	if os.Getenv("GOBFD_E2E_MAKE_RUNTIME_FAIL") == call.Target {
		fmt.Fprintln(os.Stderr, "fixture runtime exit 23")
		return 23
	}
	return 0
}

func copyE2EFixtureBinary(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return errors.Join(err, input.Close())
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close(), input.Close())
}

func waitE2EFixturePath(ctx context.Context, path string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
