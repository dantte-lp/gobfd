package netio_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestLAGActuatorDryRunDoesNotCallBackend(t *testing.T) {
	t.Parallel()

	backend := &recordingLAGBackend{}
	actuator, err := netio.NewLAGActuator(netio.LAGActuatorConfig{
		Mode:       netio.LAGActuatorModeDryRun,
		DownAction: netio.LAGActuatorActionRemoveMember,
		UpAction:   netio.LAGActuatorActionAddMember,
	}, backend, slog.Default())
	if err != nil {
		t.Fatalf("NewLAGActuator: %v", err)
	}

	err = actuator.HandleMicroBFDMemberEvent(context.Background(), bfd.MicroBFDMemberEvent{
		LAGInterface:    "bond0",
		MemberInterface: "eth0",
		OldState:        bfd.StateUp,
		NewState:        bfd.StateDown,
	})
	if err != nil {
		t.Fatalf("HandleMicroBFDMemberEvent: %v", err)
	}
	if got := backend.calls; len(got) != 0 {
		t.Fatalf("backend calls = %v, want none in dry-run mode", got)
	}
}

func TestLAGActuatorEnforceRemovesAfterUpToDownAndAddsOnRecovery(t *testing.T) {
	t.Parallel()

	backend := &recordingLAGBackend{}
	actuator, err := netio.NewLAGActuator(netio.LAGActuatorConfig{
		Mode:       netio.LAGActuatorModeEnforce,
		DownAction: netio.LAGActuatorActionRemoveMember,
		UpAction:   netio.LAGActuatorActionAddMember,
	}, backend, slog.Default())
	if err != nil {
		t.Fatalf("NewLAGActuator: %v", err)
	}

	down := bfd.MicroBFDMemberEvent{
		LAGInterface:    "bond0",
		MemberInterface: "eth0",
		OldState:        bfd.StateUp,
		NewState:        bfd.StateDown,
	}
	if err := actuator.HandleMicroBFDMemberEvent(context.Background(), down); err != nil {
		t.Fatalf("HandleMicroBFDMemberEvent down: %v", err)
	}

	up := bfd.MicroBFDMemberEvent{
		LAGInterface:    "bond0",
		MemberInterface: "eth0",
		OldState:        bfd.StateDown,
		NewState:        bfd.StateUp,
	}
	if err := actuator.HandleMicroBFDMemberEvent(context.Background(), up); err != nil {
		t.Fatalf("HandleMicroBFDMemberEvent up: %v", err)
	}

	want := []string{"remove bond0 eth0", "add bond0 eth0"}
	if !stringSlicesEqual(backend.calls, want) {
		t.Fatalf("backend calls = %v, want %v", backend.calls, want)
	}
}

func TestLAGActuatorIgnoresInitialNonUpState(t *testing.T) {
	t.Parallel()

	backend := &recordingLAGBackend{}
	actuator, err := netio.NewLAGActuator(netio.LAGActuatorConfig{
		Mode:       netio.LAGActuatorModeEnforce,
		DownAction: netio.LAGActuatorActionRemoveMember,
		UpAction:   netio.LAGActuatorActionAddMember,
	}, backend, slog.Default())
	if err != nil {
		t.Fatalf("NewLAGActuator: %v", err)
	}

	err = actuator.HandleMicroBFDMemberEvent(context.Background(), bfd.MicroBFDMemberEvent{
		LAGInterface:    "bond0",
		MemberInterface: "eth0",
		OldState:        bfd.StateDown,
		NewState:        bfd.StateInit,
	})
	if err != nil {
		t.Fatalf("HandleMicroBFDMemberEvent: %v", err)
	}
	if got := backend.calls; len(got) != 0 {
		t.Fatalf("backend calls = %v, want none before first Up", got)
	}
}

func TestLAGActuatorValidatesBackendAndOwnerPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     netio.LAGActuatorConfig
		wantErr error
	}{
		{
			name: "invalid backend",
			cfg: netio.LAGActuatorConfig{
				Mode:        netio.LAGActuatorModeDryRun,
				Backend:     "ifupdown",
				OwnerPolicy: netio.LAGOwnerPolicyRefuseIfManaged,
			},
			wantErr: netio.ErrInvalidLAGActuatorBackend,
		},
		{
			name: "invalid owner policy",
			cfg: netio.LAGActuatorConfig{
				Mode:        netio.LAGActuatorModeDryRun,
				Backend:     netio.LAGActuatorBackendKernelBond,
				OwnerPolicy: "overwrite",
			},
			wantErr: netio.ErrInvalidLAGOwnerPolicy,
		},
		{
			name: "networkmanager backend",
			cfg: netio.LAGActuatorConfig{
				Mode:        netio.LAGActuatorModeDryRun,
				Backend:     netio.LAGActuatorBackendNetworkManager,
				OwnerPolicy: netio.LAGOwnerPolicyNetworkManagerDBus,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := netio.NewLAGActuator(tt.cfg, nil, slog.Default())
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("NewLAGActuator: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("NewLAGActuator returned nil, want error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewLAGActuator error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

type recordingLAGBackend struct {
	calls []string
}

func (b *recordingLAGBackend) RemoveMember(_ context.Context, lagInterface, memberInterface string) error {
	b.calls = append(b.calls, "remove "+lagInterface+" "+memberInterface)
	return nil
}

func (b *recordingLAGBackend) AddMember(_ context.Context, lagInterface, memberInterface string) error {
	b.calls = append(b.calls, "add "+lagInterface+" "+memberInterface)
	return nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
