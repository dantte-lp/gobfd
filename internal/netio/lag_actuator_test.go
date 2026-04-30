package netio_test

import (
	"context"
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
