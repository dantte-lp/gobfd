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
	images   map[string]bool
}

func (runner *recordingRunner) Run(_ context.Context, command Command) (Result, error) {
	if runner.images == nil {
		runner.images = make(map[string]bool)
	}
	runner.commands = append(runner.commands, command)
	if command.Executable == executableContainerlab {
		return Result{Stdout: `{"version":"` + ContainerlabVersion + `"}`}, nil
	}
	if len(command.Arguments) >= 3 && command.Executable == "podman" &&
		command.Arguments[0] == "image" && command.Arguments[1] == "exists" {
		if runner.images[command.Arguments[2]] {
			return Result{}, nil
		}
		return Result{ExitCode: 1}, nil
	}
	if len(command.Arguments) >= 3 && command.Executable == "podman" &&
		command.Arguments[0] == "pull" && command.Arguments[2] == runner.failPull {
		return Result{ExitCode: 17, Stderr: "injected pull failure"}, nil
	}
	if len(command.Arguments) >= 3 && command.Executable == "podman" && command.Arguments[0] == "pull" {
		runner.images[command.Arguments[2]] = true
	}
	return Result{}, nil
}

func TestRunKeepsOwnedAndVendorPhasesInGo(t *testing.T) {
	t.Parallel()

	root := bootstrapTestRoot(t)
	options := DefaultOptions(root)
	options.Archives = VendorArchives{Arista: filepath.Join(root, "ceos.tar"), Cisco: filepath.Join(root, "xrd.tar")}
	for _, archive := range []string{options.Archives.Arista, options.Archives.Cisco} {
		if err := os.WriteFile(archive, []byte("fixture"), 0o600); err != nil {
			t.Fatalf("write vendor archive fixture: %v", err)
		}
	}
	options.Deploy = true
	options.DryRun = true
	options.Jobs = 1
	runner := &recordingRunner{}

	if err := Run(t.Context(), options, runner); err != nil {
		t.Fatalf("run bootstrap: %v", err)
	}

	var pulls, builds, vendorCalls int
	var buildImage, runImage string
	for _, command := range runner.commands {
		switch {
		case command.Executable == "podman" && slices.Contains(command.Arguments, "pull"):
			pulls++
		case command.Executable == "podman" && slices.Contains(command.Arguments, "build"):
			builds++
			if reference := argumentAfter(command.Arguments, "-t"); strings.HasPrefix(reference, gobfdImageRepo+":") {
				buildImage = reference
			}
		case command.Executable == "podman" && slices.Contains(command.Arguments, "run"):
			if argumentAfter(command.Arguments, "--name") == gobfdContainer {
				runImage = command.Arguments[len(command.Arguments)-2]
			}
		case command.Executable == "podman" &&
			(slices.Contains(command.Arguments, "tag") || slices.Contains(command.Arguments, "load")):
			vendorCalls++
		case command.Executable == "python" || command.Executable == "python3" || command.Executable == "uv":
			t.Errorf("owned phase invokes Python directly: %q", command.Arguments)
		}
	}
	if pulls != 5 {
		t.Errorf("public image pulls = %d, want 5", pulls)
	}
	if builds != 2 {
		t.Errorf("application image builds = %d, want 2", builds)
	}
	if buildImage != dryRunImage || runImage != dryRunImage {
		t.Errorf("dry-run build/run images = %q/%q, want %q", buildImage, runImage, dryRunImage)
	}
	if vendorCalls != 2 {
		t.Errorf("dry-run vendor image operations = %d, want 2", vendorCalls)
	}
	if len(runner.commands) == 0 {
		t.Fatal("bootstrap issued no commands")
	}
	if !slices.ContainsFunc(runner.commands, func(command Command) bool {
		return command.Executable == executableContainerlab && slices.Contains(command.Arguments, "deploy")
	}) {
		t.Fatal("native Containerlab deploy command is missing")
	}
}

func TestRunAggregatesPhaseFailureAndSkipsDeploy(t *testing.T) {
	t.Parallel()

	root := bootstrapTestRoot(t)
	options := DefaultOptions(root)
	options.Deploy = true
	options.DryRun = true
	options.Jobs = 1
	failingReference := "ghcr.io/nokia/srlinux:" + options.Tags.Nokia
	runner := &recordingRunner{failPull: failingReference}

	err := Run(t.Context(), options, runner)
	if !errors.Is(err, ErrBootstrapFailed) {
		t.Fatalf("bootstrap error = %v, want errors.Is(ErrBootstrapFailed)", err)
	}
	if !strings.Contains(err.Error(), "pull:nokia") {
		t.Fatalf("bootstrap error = %v, want pull:nokia context", err)
	}
	for _, command := range runner.commands {
		if command.Executable == executableContainerlab && slices.Contains(command.Arguments, "deploy") {
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
	topology := []byte("topology:\n  nodes:\n    frr:\n      kind: ext-container\n")
	if err := os.WriteFile(filepath.Join(topologyDir, "gobfd-vendors.clab.yml"), topology, 0o600); err != nil {
		t.Fatalf("write topology fixture: %v", err)
	}
	return root
}
