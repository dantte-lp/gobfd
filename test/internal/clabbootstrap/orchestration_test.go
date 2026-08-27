package clabbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type recordingRunner struct {
	commands []Command
	failPull string
}

func (runner *recordingRunner) Run(_ context.Context, command Command) (Result, error) {
	runner.commands = append(runner.commands, command)
	if len(command.Arguments) >= 3 && command.Executable == "podman" &&
		command.Arguments[0] == "image" && command.Arguments[1] == "exists" {
		return Result{ExitCode: 1}, nil
	}
	if len(command.Arguments) >= 3 && command.Executable == "podman" &&
		command.Arguments[0] == "pull" && command.Arguments[2] == runner.failPull {
		return Result{ExitCode: 17, Stderr: "injected pull failure"}, nil
	}
	return Result{}, nil
}

func TestRunKeepsOwnedPhasesInGoAndVendorGlueNarrow(t *testing.T) {
	t.Parallel()

	root := bootstrapTestRoot(t)
	options := DefaultOptions(root)
	options.Archives = VendorArchives{
		Arista: "/images/ceos.tar",
		Cisco:  "/images/xrd.tar",
	}
	options.Deploy = true
	options.Jobs = 1
	runner := &recordingRunner{}

	if err := Run(t.Context(), options, runner); err != nil {
		t.Fatalf("run bootstrap: %v", err)
	}

	var pulls, builds, vendorCalls int
	for _, command := range runner.commands {
		switch {
		case command.Executable == "podman" && slices.Contains(command.Arguments, "pull"):
			pulls++
		case command.Executable == "podman" && slices.Contains(command.Arguments, "build"):
			builds++
		case command.Executable == "uv":
			vendorCalls++
			if !slices.Contains(command.Arguments, "vendor_images.py") &&
				!strings.Contains(strings.Join(command.Arguments, "\x00"), "vendor_images.py") {
				t.Errorf("Python boundary does not invoke vendor_images.py: %q", command.Arguments)
			}
		case command.Executable == "python" || command.Executable == "python3":
			t.Errorf("owned phase invokes Python directly: %q", command.Arguments)
		}
	}
	if pulls != 7 {
		t.Errorf("public image pulls = %d, want 7", pulls)
	}
	if builds != 1 {
		t.Errorf("GoBFD image builds = %d, want 1", builds)
	}
	if vendorCalls != 3 {
		t.Errorf("vendor Python calls = %d, want 3", vendorCalls)
	}
	if len(runner.commands) == 0 {
		t.Fatal("bootstrap issued no commands")
	}
	last := runner.commands[len(runner.commands)-1]
	wantRunner := filepath.Join(root, "test", "interop-clab", "run.sh")
	if last.Executable != wantRunner || !slices.Equal(last.Arguments, []string{"--up-only"}) {
		t.Fatalf("last command = %q %q, want %q [--up-only]", last.Executable, last.Arguments, wantRunner)
	}
}

func TestRunAggregatesPhaseFailureAndSkipsDeploy(t *testing.T) {
	t.Parallel()

	root := bootstrapTestRoot(t)
	options := DefaultOptions(root)
	options.Deploy = true
	options.Jobs = 1
	failingReference := "quay.io/frrouting/frr:10.7.0@sha256:" +
		"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68"
	runner := &recordingRunner{failPull: failingReference}

	err := Run(t.Context(), options, runner)
	if !errors.Is(err, ErrBootstrapFailed) {
		t.Fatalf("bootstrap error = %v, want errors.Is(ErrBootstrapFailed)", err)
	}
	if !strings.Contains(err.Error(), "pull:frr") {
		t.Fatalf("bootstrap error = %v, want pull:frr context", err)
	}
	runScript := filepath.Join(root, "test", "interop-clab", "run.sh")
	for _, command := range runner.commands {
		if command.Executable == runScript {
			t.Fatalf("deploy ran after phase failure: %q", command.Arguments)
		}
	}
}

func bootstrapTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	topologyDir := filepath.Join(root, "test", "interop-clab")
	if err := os.MkdirAll(topologyDir, 0o700); err != nil {
		t.Fatalf("create topology directory: %v", err)
	}
	topology := []byte("topology:\n  nodes:\n    frr:\n      image: " +
		"quay.io/frrouting/frr:10.7.0@sha256:" +
		"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68\n")
	if err := os.WriteFile(filepath.Join(topologyDir, "gobfd-vendors.clab.yml"), topology, 0o600); err != nil {
		t.Fatalf("write topology fixture: %v", err)
	}
	return root
}
