package clabbootstrap

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	testNetworkRun     = "123456789-1234"
	testNetworkID      = "7d8d17484db26d642502e6ba7f4fcb8f18c571c7ba5b456f38675261b154d25b"
	testNetworkOtherID = "8d8d17484db26d642502e6ba7f4fcb8f18c571c7ba5b456f38675261b154d25b"
)

type networkStep struct {
	args   []string
	result Result
	err    error
}

type networkScript struct {
	t     *testing.T
	steps []networkStep
}

func (script *networkScript) Run(_ context.Context, command Command) (Result, error) {
	script.t.Helper()
	if len(script.steps) == 0 {
		script.t.Fatalf("unexpected command: %s %v", command.Executable, command.Arguments)
	}
	step := script.steps[0]
	script.steps = script.steps[1:]
	if command.Executable != executablePodman || !slices.Equal(command.Arguments, step.args) {
		script.t.Fatalf("command = %s %v, want podman %v", command.Executable, command.Arguments, step.args)
	}
	return step.result, step.err
}

func newNetworkScript(t *testing.T, steps ...networkStep) *networkScript {
	t.Helper()
	script := &networkScript{
		t:     t,
		steps: steps,
	}
	t.Cleanup(func() {
		if len(script.steps) != 0 {
			t.Errorf("%d expected network commands were not executed", len(script.steps))
		}
	})
	return script
}

func networkExistsStep(reference string, present bool) networkStep {
	code := 1
	if present {
		code = 0
	}
	return networkStep{
		args:   []string{"network", "exists", reference},
		result: Result{ExitCode: code},
	}
}

func networkInspectStep(id, owner, run string) networkStep {
	return networkStep{
		args:   []string{"network", "inspect", "--format", inspectNetworkOwnershipFormat, networkName(testNetworkRun)},
		result: Result{Stdout: id + "|" + networkName(testNetworkRun) + "|" + owner + "|" + run},
	}
}

func networkEmptyStep(output string) networkStep {
	return networkStep{
		args:   []string{"ps", "--all", "--quiet", "--no-trunc", "--filter", "network=" + testNetworkID},
		result: Result{Stdout: output},
	}
}

func TestStageManagementNetwork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		createCode int
		createErr  error
		wantErr    bool
	}{
		{
			name: "created",
		},
		{
			name: "nonzero exit after creation", createCode: 125,
			wantErr: true,
		},
		{
			name: "runner error after creation", createErr: context.DeadlineExceeded,
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := newNetworkScript(t,
				networkExistsStep(networkName(testNetworkRun), false),
				networkStep{
					args: []string{
						"network", "create", "--label", ownerLabel + "=" + labName,
						"--label", runLabel + "=" + testNetworkRun, networkName(testNetworkRun),
					},
					result: Result{ExitCode: test.createCode},
					err:    test.createErr,
				},
				networkInspectStep(testNetworkID, labName, testNetworkRun),
			)
			network, err := stageManagementNetwork(t.Context(), script, testNetworkRun)
			if (err != nil) != test.wantErr || network.Name != networkName(testNetworkRun) || network.ID != testNetworkID {
				t.Fatalf("stage = %#v, %v; want exact identity and error=%v", network, err, test.wantErr)
			}
			if test.createErr != nil && !errors.Is(err, test.createErr) {
				t.Fatalf("create error lost: %v", err)
			}
		})
	}
}

func TestStageManagementNetworkRefusesCollision(t *testing.T) {
	t.Parallel()
	script := newNetworkScript(t,
		networkExistsStep(networkName(testNetworkRun), true))
	if _, err := stageManagementNetwork(t.Context(), script, testNetworkRun); !errors.Is(err, errLifecycleState) {
		t.Fatalf("collision error = %v", err)
	}
}

func TestStageManagementNetworkRejectsUnprovenIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, id, owner, run string }{
		{
			name: "foreign owner", id: testNetworkID, owner: "foreign", run: testNetworkRun,
		},
		{
			name: "different run", id: testNetworkID, owner: labName, run: "other",
		},
		{
			name: "invalid ID", id: "short", owner: labName, run: testNetworkRun,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := newNetworkScript(t,
				networkExistsStep(networkName(testNetworkRun), false),
				networkStep{args: []string{
					"network", "create", "--label", ownerLabel + "=" + labName,
					"--label", runLabel + "=" + testNetworkRun, networkName(testNetworkRun),
				}},
				networkInspectStep(test.id, test.owner, test.run),
			)
			network, err := stageManagementNetwork(t.Context(), script, testNetworkRun)
			if !errors.Is(err, errLifecycleState) || network.ID != "" {
				t.Fatalf("unproven identity = %#v, %v", network, err)
			}
		})
	}
}

func TestValidateOwnedNetwork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                        string
		network                     lifecycleNetwork
		steps                       []networkStep
		empty, wantPresent, wantErr bool
	}{
		{
			name:    "owned",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, labName, testNetworkRun),
			},
			wantPresent: true,
		},
		{
			name:    "partial checkpoint",
			network: lifecycleNetwork{Name: networkName(testNetworkRun)},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, labName, testNetworkRun),
			},
			wantPresent: true,
		},
		{
			name:    "removed",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), false),
				networkExistsStep(testNetworkID, false),
			},
		},
		{
			name:    "never created",
			network: lifecycleNetwork{Name: networkName(testNetworkRun)},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), false),
			},
		},
		{
			name:    "lost name",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), false),
				networkExistsStep(testNetworkID, true),
			},
			wantErr: true,
		},
		{
			name:    "substituted ID",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkOtherID, labName, testNetworkRun),
			},
			wantErr: true,
		},
		{
			name:    "foreign owner",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, "foreign", testNetworkRun),
			},
			wantErr: true,
		},
		{
			name:    "foreign run",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, labName, "other"),
			},
			wantErr: true,
		},
		{
			name:    "foreign name",
			network: lifecycleNetwork{Name: "clab", ID: testNetworkID},
			wantErr: true,
		},
		{
			name:    "invalid recorded ID",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: "short"},
			wantErr: true,
		},
		{
			name:    "empty",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, labName, testNetworkRun),
				networkEmptyStep(""),
			},
			empty:       true,
			wantPresent: true,
		},
		{
			name:    "occupied",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, labName, testNetworkRun),
				networkEmptyStep("foreign-container\n"),
			},
			empty:   true,
			wantErr: true,
		},
		{
			name:    "query failure",
			network: lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID},
			steps: []networkStep{
				{
					args:   []string{"network", "exists", networkName(testNetworkRun)},
					result: Result{ExitCode: 125, Stderr: "foreign secret"},
				},
			},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			present, err := validateOwnedNetwork(
				t.Context(), newNetworkScript(t, test.steps...), test.network, testNetworkRun, test.empty,
			)
			if (err != nil) != test.wantErr || present != test.wantPresent {
				t.Fatalf("validate = %v, %v; want %v/error=%v", present, err, test.wantPresent, test.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "foreign secret") {
				t.Fatalf("foreign metadata disclosed: %v", err)
			}
		})
	}
}

func TestRemoveOwnedNetwork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, id string }{
		{
			name: "checkpointed", id: testNetworkID,
		},
		{
			name: "partial checkpoint",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := newNetworkScript(t,
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, labName, testNetworkRun),
				networkEmptyStep(""),
				networkStep{args: []string{"network", "rm", testNetworkID}},
				networkExistsStep(networkName(testNetworkRun), false),
				networkExistsStep(testNetworkID, false),
			)

			network := lifecycleNetwork{Name: networkName(testNetworkRun), ID: test.id}
			if err := removeOwnedNetwork(t.Context(), script, network, testNetworkRun); err != nil {
				t.Fatalf("remove: %v", err)
			}
		})
	}
}

func TestRemoveOwnedNetworkRefusesOccupied(t *testing.T) {
	t.Parallel()
	script := newNetworkScript(t,
		networkExistsStep(networkName(testNetworkRun), true),
		networkInspectStep(testNetworkID, labName, testNetworkRun),
		networkEmptyStep("attached\n"))
	network := lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID}
	if err := removeOwnedNetwork(t.Context(), script, network, testNetworkRun); !errors.Is(err, errLifecycleState) {
		t.Fatalf("occupied removal = %v", err)
	}
}

func TestRemoveOwnedNetworkVerifiesAbsence(t *testing.T) {
	t.Parallel()
	for _, reference := range []string{networkName(testNetworkRun), testNetworkID} {
		t.Run(reference, func(t *testing.T) {
			t.Parallel()
			steps := []networkStep{
				networkExistsStep(networkName(testNetworkRun), true),
				networkInspectStep(testNetworkID, labName, testNetworkRun),
				networkEmptyStep(""),
				{args: []string{"network", "rm", testNetworkID}},
			}
			if reference == testNetworkID {
				steps = append(steps,
					networkExistsStep(networkName(testNetworkRun), false))
			}
			steps = append(steps,
				networkExistsStep(reference, true))
			network := lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID}
			err := removeOwnedNetwork(t.Context(), newNetworkScript(t, steps...), network, testNetworkRun)
			if !errors.Is(err, errLifecycleState) {
				t.Fatalf("remaining reference = %v", err)
			}
		})
	}
}

func TestStageManagementNetworkRetainsUncertainCreation(t *testing.T) {
	t.Parallel()
	step := networkInspectStep(testNetworkID, labName, testNetworkRun)
	step.result = Result{ExitCode: 125}
	script := newNetworkScript(t,
		networkExistsStep(networkName(testNetworkRun), false),
		networkStep{args: []string{
			"network", "create", "--label", ownerLabel + "=" + labName,
			"--label", runLabel + "=" + testNetworkRun, networkName(testNetworkRun),
		}},
		step,
	)
	network, err := stageManagementNetwork(t.Context(), script, testNetworkRun)
	if !errors.Is(err, ErrBootstrapFailed) || network.Name != networkName(testNetworkRun) || network.ID != "" {
		t.Fatalf("uncertain creation = %#v, %v", network, err)
	}
}

func TestRemoveOwnedNetworkRetainsFailedRemoval(t *testing.T) {
	t.Parallel()
	script := newNetworkScript(t,
		networkExistsStep(networkName(testNetworkRun), true),
		networkInspectStep(testNetworkID, labName, testNetworkRun),
		networkEmptyStep(""),
		networkStep{args: []string{"network", "rm", testNetworkID}, result: Result{ExitCode: 125}},
	)
	network := lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID}
	if err := removeOwnedNetwork(t.Context(), script, network, testNetworkRun); !errors.Is(err, ErrBootstrapFailed) {
		t.Fatalf("failed removal = %v", err)
	}
}

func TestManagementNetworkRejectsInvalidRun(t *testing.T) {
	t.Parallel()
	for _, run := range []string{"", "foreign|run", "../foreign", "-foreign", "run\n"} {
		t.Run(run, func(t *testing.T) {
			t.Parallel()
			script := newNetworkScript(t)
			if _, err := stageManagementNetwork(t.Context(), script, run); !errors.Is(err, errLifecycleState) {
				t.Fatalf("invalid run accepted: %v", err)
			}
		})
	}
}

func TestRemoveOwnedNetworkRejectsWrongRun(t *testing.T) {
	t.Parallel()
	network := lifecycleNetwork{Name: networkName(testNetworkRun), ID: testNetworkID}
	err := removeOwnedNetwork(t.Context(), newNetworkScript(t), network, "another-run")
	if !errors.Is(err, errLifecycleState) {
		t.Fatalf("wrong cleanup run accepted: %v", err)
	}
}
