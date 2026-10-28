package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type cleanupRetryRunner struct {
	images      ownedImageRunner
	containers  map[string]string
	failDestroy bool
}

func (runner *cleanupRetryRunner) Run(ctx context.Context, command Command) (Result, error) {
	if command.Executable == executableContainerlab {
		if runner.failDestroy {
			return Result{ExitCode: 42}, nil
		}
		return Result{}, nil
	}
	if command.Executable != executablePodman || len(command.Arguments) == 0 {
		return Result{}, nil
	}
	reference := command.Arguments[len(command.Arguments)-1]
	switch command.Arguments[0] {
	case "network":
		return Result{ExitCode: 1}, nil
	case "container":
		for name, id := range runner.containers {
			if name == reference || id == reference {
				return Result{}, nil
			}
		}
		return Result{ExitCode: 1}, nil
	case "inspect":
		id, present := runner.containers[reference]
		if !present {
			return Result{ExitCode: 1}, nil
		}
		return Result{Stdout: fmt.Sprintf("%s|%s|%s|%s", id, labName, labName, runner.images.runID)}, nil
	case "rm":
		for name, id := range runner.containers {
			if id == reference {
				delete(runner.containers, name)
				return Result{}, nil
			}
		}
		return Result{ExitCode: 1}, nil
	default:
		return runner.images.Run(ctx, command)
	}
}

func TestFailedTopologyDestroyPreservesDiscoveryContainersForRetry(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(root)
	runner := &cleanupRetryRunner{
		containers:  map[string]string{gobfdContainer: "gobfd-container-id", frrContainer: "frr-container-id"},
		failDestroy: true,
	}
	receipt, err := stageTestImages(t, options, runner)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Topology = runtimeTopologyPath(root)
	receipt.Network.Name = networkName(receipt.RunID)
	receipt.ExpectedContainers = []string{gobfdContainer, frrContainer}
	receipt.Containers = map[string]string{gobfdContainer: "gobfd-container-id", frrContainer: "frr-container-id"}
	if err := writeLifecycleReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}
	if err := cleanupTopology(t.Context(), options, runner, receipt); !errors.Is(err, ErrBootstrapFailed) {
		t.Fatalf("destroy failure was lost: %v", err)
	}
	if len(runner.containers) != 2 || len(runner.images.images) != 2 {
		t.Fatalf("failed destroy removed retry identities: containers=%v images=%v", runner.containers, runner.images.images)
	}
	if _, err := os.Stat(receiptPath(root)); err != nil {
		t.Fatalf("failed destroy lost receipt: %v", err)
	}
	runner.failDestroy = false
	if err := cleanupTopology(t.Context(), options, runner, receipt); err != nil {
		t.Fatalf("retry cleanup failed: %v", err)
	}
	if len(runner.containers) != 0 || len(runner.images.images) != 0 {
		t.Fatal("successful retry retained managed resources")
	}
	if _, err := os.Stat(receiptPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful retry retained receipt: %v", err)
	}
}

func TestManagedContainerDiscoveryUsesTopologyDirectory(t *testing.T) {
	root := t.TempDir()
	arguments := managedContainerArguments(DefaultOptions(root), "path-test", gobfdContainer)
	want := "clab-node-lab-dir=" + filepath.Join(root, "test", "interop-clab",
		"clab-gobfd-vendors", "clab-gobfd-vendors-gobfd")
	if !slices.Contains(arguments, want) {
		t.Fatalf("discovery path does not match Containerlab topology-relative lab directory: %v", arguments)
	}
}
