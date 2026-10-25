package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

type recoveryRunner struct {
	cleanupRetryRunner

	legacyLabels bool
	foreignFRR   bool
	destroyed    bool
}

func (runner *recoveryRunner) Run(ctx context.Context, command Command) (Result, error) {
	if command.Executable == executableContainerlab {
		if len(command.Arguments) > 0 && command.Arguments[0] == "version" {
			return Result{Stdout: `{"version":"0.82.0"}`}, nil
		}
		runner.destroyed = true
	}
	if command.Executable != executablePodman || len(command.Arguments) == 0 || command.Arguments[0] != "inspect" {
		return runner.cleanupRetryRunner.Run(ctx, command)
	}
	name := command.Arguments[len(command.Arguments)-1]
	id, present := runner.containers[name]
	if !present {
		return Result{ExitCode: 1}, nil
	}
	if runner.legacyLabels {
		if name == gobfdContainer {
			return Result{Stdout: id + "||" + labName + "|" + runner.images.runID}, nil
		}
		return Result{Stdout: id + "|" + labName + "||"}, nil
	}
	runID := runner.images.runID
	if runner.foreignFRR && name == frrContainer {
		runID = "another-run"
	}
	return Result{Stdout: fmt.Sprintf("%s|%s|%s|%s", id, labName, labName, runID)}, nil
}

func recoveryFixture(t *testing.T) (Options, lifecycleReceipt, *recoveryRunner) {
	t.Helper()
	root := t.TempDir()
	if err := ensureRuntimeDirectory(root); err != nil {
		t.Fatal(err)
	}
	receipt := newImageReceipt("recovery-run")
	receipt.Image.ID = testImageID
	receipt.FRR.ID = testFRRImageID
	receipt.Topology = runtimeTopologyPath(root)
	receipt.Network.Name = networkName(receipt.RunID)
	receipt.ExpectedContainers = []string{gobfdContainer, frrContainer}
	runner := &recoveryRunner{
		images: ownedImageRunner{runID: receipt.RunID, images: map[string]string{
			receipt.Image.Reference: testImageID, receipt.FRR.Reference: testFRRImageID,
		}},
		containers: map[string]string{gobfdContainer: "gobfd-container-id", frrContainer: "frr-container-id"},
	}
	return DefaultOptions(root), receipt, runner
}

func TestDownRecoversIntendedContainersAfterInterruptedCheckpoint(t *testing.T) {
	for _, test := range []struct {
		name        string
		oneRecorded bool
		foreignFRR  bool
		nullMap     bool
	}{
		{name: "both uncheckpointed"},
		{name: "null checkpoint map", nullMap: true},
		{name: "FRR uncheckpointed", oneRecorded: true},
		{name: "foreign FRR is never adopted", foreignFRR: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, receipt, runner := recoveryFixture(t)
			runner.foreignFRR = test.foreignFRR
			if test.nullMap {
				receipt.Containers = nil
			}
			if test.oneRecorded {
				receipt.Containers[gobfdContainer] = "gobfd-container-id"
			}
			if err := createLifecycleReceipt(options.ProjectRoot, receipt); err != nil {
				t.Fatal(err)
			}
			options.Down = true
			err := Run(t.Context(), options, runner)
			if test.foreignFRR {
				if !errors.Is(err, errLifecycleState) || runner.destroyed ||
					len(runner.containers) != 2 || len(runner.images.images) != 2 {
					t.Fatalf("foreign recovery was not fail-closed: error=%v runner=%+v", err, runner)
				}
				return
			}
			if err != nil {
				t.Fatalf("down cannot recover intended run-owned containers: %v", err)
			}
			if len(runner.containers) != 0 || len(runner.images.images) != 0 {
				t.Fatal("recovered down left managed resources")
			}
			if _, err := os.Stat(receiptPath(options.ProjectRoot)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovered down retained receipt: %v", err)
			}
		})
	}
}

func TestLegacySchemasCleanGoBFDWithoutContainerlabLabel(t *testing.T) {
	for _, schema := range []int{legacyReceiptV1, ownedReceiptV2} {
		t.Run(fmt.Sprintf("schema-%d", schema), func(t *testing.T) {
			options, receipt, runner := recoveryFixture(t)
			runner.legacyLabels = true
			delete(runner.images.images, receipt.FRR.Reference)
			receipt.SchemaVersion = schema
			receipt.FRR = lifecycleImage{}
			receipt.Network = lifecycleNetwork{}
			receipt.ExpectedContainers = nil
			receipt.Containers = map[string]string{gobfdContainer: "gobfd-container-id", frrContainer: "frr-container-id"}
			wantImages := 0
			if schema == legacyReceiptV1 {
				receipt.Image = lifecycleImage{}
				wantImages = 1 // Schema 1 never recorded image ownership.
			}
			if err := createLifecycleReceipt(options.ProjectRoot, receipt); err != nil {
				t.Fatal(err)
			}
			options.Down = true
			if err := Run(t.Context(), options, runner); err != nil {
				t.Fatalf("legacy owner/run GoBFD and lab-labeled FRR cleanup rejected: %v", err)
			}
			if len(runner.containers) != 0 || len(runner.images.images) != wantImages {
				t.Fatal("legacy down did not respect recorded resource ownership")
			}
		})
	}
}

type changedNetworkRunner struct {
	network  *networkScript
	started  int
	deployed bool
}

func (runner *changedNetworkRunner) Run(ctx context.Context, command Command) (Result, error) {
	if command.Executable == executableContainerlab {
		runner.deployed = true
		return Result{}, nil
	}
	if command.Executable != executablePodman || len(command.Arguments) == 0 {
		return Result{}, fmt.Errorf("unexpected command: %v", command)
	}
	switch command.Arguments[0] {
	case "network":
		return runner.network.Run(ctx, command)
	case "run":
		runner.started++
		return Result{}, nil
	case "inspect":
		name := command.Arguments[len(command.Arguments)-1]
		return Result{Stdout: name + "-id|" + labName + "|" + labName + "|" + testNetworkRun}, nil
	default:
		return Result{}, fmt.Errorf("unexpected podman command: %v", command.Arguments)
	}
}

func TestDeployRechecksNetworkAfterStartingApplications(t *testing.T) {
	root := t.TempDir()
	if err := ensureRuntimeDirectory(root); err != nil {
		t.Fatal(err)
	}
	receipt := newImageReceipt(testNetworkRun)
	receipt.Image.ID = testImageID
	receipt.FRR.ID = testFRRImageID
	receipt.Topology = runtimeTopologyPath(root)
	receipt.Network.Name = networkName(receipt.RunID)
	receipt.ExpectedContainers = []string{gobfdContainer, frrContainer, "clab-gobfd-vendors-arista"}
	if err := createLifecycleReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}
	runner := &changedNetworkRunner{network: newNetworkScript(t,
		networkExistsStep(networkName(testNetworkRun), false),
		networkStep{args: []string{
			"network", "create", "--label", ownerLabel + "=" + labName,
			"--label", runLabel + "=" + testNetworkRun, networkName(testNetworkRun),
		}},
		networkInspectStep(testNetworkID, labName, testNetworkRun),
		networkExistsStep(networkName(testNetworkRun), true),
		networkInspectStep(testNetworkID, labName, testNetworkRun),
		networkExistsStep(networkName(testNetworkRun), true),
		networkInspectStep(testNetworkOtherID, "foreign", "another-run"),
	)}
	err := activateTopology(t.Context(), DefaultOptions(root), runner, nil, &receipt)
	if !errors.Is(err, errLifecycleState) {
		t.Fatalf("network substitution was not rejected: %v", err)
	}
	if runner.started != 2 || runner.deployed {
		t.Fatalf("network changed during application startup but deploy ran: started=%d deployed=%t",
			runner.started, runner.deployed)
	}
}
