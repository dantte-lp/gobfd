package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
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
	case "compose":
		runner.testInvoked = true
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
	if name == gobfdContainer {
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
			root := t.TempDir()
			if err := ensureRuntimeDirectory(root); err != nil {
				t.Fatalf("create runtime directory: %v", err)
			}
			receipt := lifecycleReceipt{
				SchemaVersion: ownedReceiptV2,
				RunID:         runID,
				Topology:      filepath.Join(root, "gobfd-vendors.generated.clab.yml"),
				Containers: map[string]string{
					gobfdContainer:           gobfdID,
					"clab-gobfd-vendors-frr": frrID,
				},
				Image: lifecycleImage{
					Reference: gobfdImageRepo + ":" + runID,
					ID:        testImageID,
				},
			}
			if err := createLifecycleReceipt(root, receipt); err != nil {
				t.Fatalf("create lifecycle receipt: %v", err)
			}
			runner := &testOnlyIdentityRunner{containers: test.containers, runID: runID}
			options := DefaultOptions(root)
			options.TestOnly = true

			err := runTopologyLocked(t.Context(), options, runner)
			if test.wantErr && !errors.Is(err, errLifecycleState) && !errors.Is(err, ErrBootstrapFailed) {
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
