package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/vendorprofiles"
)

type testOnlyIdentityRunner struct {
	containers  map[string]string
	runID       string
	testInvoked bool
}

func (runner *testOnlyIdentityRunner) Run(_ context.Context, command Command) (Result, error) {
	if command.Executable != executablePodman || len(command.Arguments) == 0 {
		return Result{}, nil
	}
	switch command.Arguments[0] {
	case "network":
		return Result{ExitCode: 1}, nil
	case "image":
		if command.Arguments[1] == "inspect" {
			id := testImageID
			if strings.HasPrefix(command.Arguments[len(command.Arguments)-1], frrImageRepo+":") {
				id = testFRRImageID
			}
			return Result{Stdout: id + "|" + labName + "|" + runner.runID}, nil
		}
		return Result{}, nil
	case "start":
		if len(command.Arguments) > 1 && command.Arguments[1] == "--attach" {
			runner.testInvoked = true
		}
		return Result{}, nil
	case "container":
		name := command.Arguments[len(command.Arguments)-1]
		if _, exists := runner.containers[name]; exists {
			return Result{}, nil
		}
		return Result{ExitCode: 1}, nil
	case "inspect":
	default:
		return Result{}, nil
	}
	name := command.Arguments[len(command.Arguments)-1]
	id, exists := runner.containers[name]
	if !exists {
		return Result{ExitCode: 1}, nil
	}
	if name == gobfdContainer || name == frrContainer {
		return Result{Stdout: fmt.Sprintf("%s||%s|%s", id, labName, runner.runID)}, nil
	}
	return Result{Stdout: fmt.Sprintf("%s|%s||", id, labName)}, nil
}

func TestTestOnlyValidatesLiveContainerIdentitiesBeforeEvidence(t *testing.T) {
	const (
		gobfdID = "gobfd-container-id"
		frrID   = "frr-container-id"
		runID   = "test-only-run"
	)
	tests := []struct {
		name        string
		containers  map[string]string
		wantErr     bool
		wantInvoked bool
		networkID   string
		legacy      bool
		recordedNOS bool
	}{
		{
			name: "exact identities",
			containers: map[string]string{
				gobfdContainer:           gobfdID,
				"clab-gobfd-vendors-frr": frrID,
			},
			wantInvoked: true,
		},
		{
			name: "substituted identity",
			containers: map[string]string{
				gobfdContainer:           "foreign-container-id",
				"clab-gobfd-vendors-frr": frrID,
			},
			wantErr: true,
		},
		{
			name: "missing identity",
			containers: map[string]string{
				gobfdContainer: gobfdID,
			},
			wantErr: true,
		},
		{
			name:       "missing checkpointed network",
			containers: map[string]string{gobfdContainer: gobfdID, frrContainer: frrID},
			networkID:  testNetworkID,
			wantErr:    true,
		},
		{
			name:       "legacy one-image receipt",
			containers: map[string]string{gobfdContainer: gobfdID, frrContainer: frrID},
			legacy:     true,
			wantErr:    true,
		},
		{
			name: "recorded unqualified NOS",
			containers: map[string]string{
				gobfdContainer: gobfdID, frrContainer: frrID,
				"clab-gobfd-vendors-arista": "owned-arista-id",
			},
			recordedNOS: true,
			wantErr:     true,
		},
		{
			name: "unrecorded live vendor",
			containers: map[string]string{
				gobfdContainer:              gobfdID,
				"clab-gobfd-vendors-frr":    frrID,
				"clab-gobfd-vendors-arista": "foreign-arista-id",
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := bootstrapTestRoot(t)
			if err := ensureRuntimeDirectory(root); err != nil {
				t.Fatalf("create runtime directory: %v", err)
			}
			receipt := lifecycleReceipt{
				SchemaVersion:      ownedReceiptV3,
				RunID:              runID,
				Topology:           runtimeTopologyPath(root),
				Network:            lifecycleNetwork{Name: networkName(runID)},
				ExpectedContainers: []string{gobfdContainer, frrContainer},
				Containers: map[string]string{
					gobfdContainer:           gobfdID,
					"clab-gobfd-vendors-frr": frrID,
				},
				Image: lifecycleImage{
					Reference: gobfdImageRepo + ":" + runID,
					ID:        testImageID,
				},
				FRR: lifecycleImage{Reference: frrImageRepo + ":" + runID, ID: testFRRImageID},
			}
			receipt.Network.ID = test.networkID
			if test.recordedNOS {
				receipt.ExpectedContainers = append(receipt.ExpectedContainers, "clab-gobfd-vendors-arista")
				receipt.Containers["clab-gobfd-vendors-arista"] = "owned-arista-id"
			}
			if test.legacy {
				receipt.SchemaVersion = ownedReceiptV2
				receipt.FRR = lifecycleImage{}
				receipt.Network = lifecycleNetwork{}
				receipt.ExpectedContainers = nil
			}
			if err := createLifecycleReceipt(root, receipt); err != nil {
				t.Fatalf("create lifecycle receipt: %v", err)
			}
			runner := &testOnlyIdentityRunner{containers: test.containers, runID: runID}
			options := DefaultOptions(root)
			options.TestOnly = true
			options.DryRun = true

			err := runTopologyLocked(t.Context(), options, runner)
			if test.wantErr && !errors.Is(err, errLifecycleState) && !errors.Is(err, ErrBootstrapFailed) &&
				!errors.Is(err, vendorprofiles.ErrUnavailable) {
				t.Fatalf("test-only error = %v, want identity failure", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("test-only validation: %v", err)
			}
			if runner.testInvoked != test.wantInvoked {
				t.Fatalf("test command invoked = %t, want %t", runner.testInvoked, test.wantInvoked)
			}
		})
	}
}
