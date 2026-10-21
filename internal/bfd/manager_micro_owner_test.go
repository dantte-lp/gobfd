package bfd_test

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// slowMicroBFDConfig keeps member sessions Up for 90 s without peer packets.
func slowMicroBFDConfig(lag string, members ...string) bfd.MicroBFDConfig {
	cfg := defaultMicroBFDConfig()
	cfg.LAGInterface = lag
	cfg.MemberLinks = members
	cfg.RequiredMinRxInterval = 30 * time.Second
	return cfg
}

func driveSessionUp(t *testing.T, sess *bfd.Session) {
	t.Helper()
	sendControlState(t, sess, bfd.StateDown, 42)
	sendControlState(t, sess, bfd.StateInit, 42)
	deadline := time.Now().Add(2 * time.Second)
	for sess.State() != bfd.StateUp {
		if time.Now().After(deadline) {
			t.Fatalf("session %d state %s, want Up", sess.LocalDiscriminator(), sess.State())
		}
		time.Sleep(time.Millisecond)
	}
}

func onlySession(t *testing.T, mgr *bfd.Manager) *bfd.Session {
	t.Helper()
	snaps := mgr.Sessions()
	if len(snaps) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snaps))
	}
	sess, ok := mgr.LookupByDiscriminator(snaps[0].LocalDiscr)
	if !ok {
		t.Fatal("session disappeared")
	}
	return sess
}

func memberState(t *testing.T, mgr *bfd.Manager, lag, member string) bfd.MicroBFDGroupSnapshot {
	t.Helper()
	group, ok := mgr.LookupMicroBFDGroup(lag)
	if !ok {
		t.Fatalf("group %s not found", lag)
	}
	snap := group.Snapshot()
	for _, m := range snap.Members {
		if m.Interface == member && m.State != bfd.StateUp {
			t.Fatalf("group %s member %s state %s, want Up", lag, member, m.State)
		}
	}
	return snap
}

func waitActuatorUp(t *testing.T, events <-chan bfd.MicroBFDMemberEvent, lag, member string) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.LAGInterface == lag && ev.MemberInterface == member && ev.NewState == bfd.StateUp {
				if !ev.AggregateUp {
					t.Fatalf("actuator event %+v, want aggregate Up", ev)
				}
				return
			}
		case <-timeout:
			t.Fatalf("no actuator Up event for %s/%s", lag, member)
		}
	}
}

// Н2: a group created over an existing Up member session starts with that
// member Up and tells the actuator, so the member is used by the LAG.
func TestManagerCreateMicroBFDGroupSeedsExistingMemberSession(t *testing.T) {
	actuator := &recordingMicroBFDActuator{events: make(chan bfd.MicroBFDMemberEvent, 16)}
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithMicroBFDActuator(actuator))
	defer mgr.Close()
	go mgr.RunDispatch(t.Context())

	cfg := slowMicroBFDConfig("bond0", "eth0", "eth1")
	sessCfg := defaultManagerConfig()
	sessCfg.PeerAddr, sessCfg.LocalAddr = cfg.PeerAddr, cfg.LocalAddr
	sessCfg.Type, sessCfg.Interface = bfd.SessionTypeMicroBFD, "eth0"
	sessCfg.DesiredMinTxInterval, sessCfg.RequiredMinRxInterval = cfg.DesiredMinTxInterval, cfg.RequiredMinRxInterval
	sess, err := mgr.CreateSession(t.Context(), sessCfg, bfd.NonOwningSenderLeaseFactory(noopSender{}))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	driveSessionUp(t, sess)

	if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}
	if snap := memberState(t, mgr, "bond0", "eth0"); snap.UpCount != 1 || !snap.AggregateUp {
		t.Fatalf("group snapshot = %+v, want eth0 Up and aggregate Up", snap)
	}
	waitActuatorUp(t, actuator.events, "bond0", "eth0")
}

// Н2: a member moved between LAGs in one reload keeps its Up session; the
// new group starts with it Up and the actuator adds it to the new LAG.
func TestManagerReconcileMicroBFDMovedMemberKeepsUpState(t *testing.T) {
	actuator := &recordingMicroBFDActuator{events: make(chan bfd.MicroBFDMemberEvent, 16)}
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithMicroBFDActuator(actuator))
	defer mgr.Close()
	go mgr.RunDispatch(t.Context())

	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories(), slowMicroBFDConfig("bond0", "eth0"))).Err(); err != nil {
		t.Fatalf("seed reconcile: %v", err)
	}
	sess := onlySession(t, mgr)
	driveSessionUp(t, sess)
	waitActuatorUp(t, actuator.events, "bond0", "eth0")

	result := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories(), slowMicroBFDConfig("bond1", "eth0")))
	if err := result.Err(); err != nil {
		t.Fatalf("move reconcile: %v", err)
	}
	if got := onlySession(t, mgr); got != sess {
		t.Fatal("moved member session was replaced")
	}
	if snap := memberState(t, mgr, "bond1", "eth0"); snap.UpCount != 1 || !snap.AggregateUp {
		t.Fatalf("bond1 snapshot = %+v, want eth0 Up and aggregate Up", snap)
	}
	waitActuatorUp(t, actuator.events, "bond1", "eth0")
}

// Н1: reload removes only groups it owns; an API group survives an empty
// desired set, as an API session survives empty base reconciliation.
func TestManagerReconcileMicroBFDKeepsAPIOwnedGroup(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	group, err := mgr.CreateMicroBFDGroup(defaultMicroBFDConfig())
	if err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}
	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(), nil).Err(); err != nil {
		t.Fatalf("empty reconcile: %v", err)
	}
	if got, ok := mgr.LookupMicroBFDGroup(testLAG); !ok || got != group {
		t.Fatal("reload removed an API-owned group")
	}
}

// Н1: a reload group may not take a member link of a surviving API group.
func TestManagerReconcileMicroBFDRejectsMemberOfAPIGroup(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	if _, err := mgr.CreateMicroBFDGroup(defaultMicroBFDConfig()); err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}
	other := defaultMicroBFDConfig()
	other.LAGInterface = "bond1"
	other.MemberLinks = []string{"eth1", "eth2"}
	other.PeerAddr = netip.MustParseAddr("10.0.1.1")
	result := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(), microDesired(microLeaseFactories(), other))
	if result.Err() == nil {
		t.Fatal("reload took eth1 from the API group bond0")
	}
	if _, ok := mgr.LookupMicroBFDGroup("bond1"); ok {
		t.Fatal("rejected group bond1 is registered")
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Fatalf("sessions after rejected reload = %d, want 0", got)
	}
}

// Н1: the API cannot delete a group owned by reload; reload alone removes it.
func TestManagerDestroyMicroBFDGroupRejectsReloadOwnedGroup(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories(), defaultMicroBFDConfig())).Err(); err != nil {
		t.Fatalf("seed reconcile: %v", err)
	}
	if err := mgr.DestroyMicroBFDGroup(testLAG); !errors.Is(err, bfd.ErrMicroBFDGroupOwnerClaimNotFound) {
		t.Fatalf("DestroyMicroBFDGroup err = %v, want ErrMicroBFDGroupOwnerClaimNotFound", err)
	}
	if _, ok := mgr.LookupMicroBFDGroup(testLAG); !ok || len(mgr.Sessions()) != 2 {
		t.Fatalf("reload group or its sessions changed after refused API delete")
	}
	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(), nil).Err(); err != nil {
		t.Fatalf("empty reconcile: %v", err)
	}
	if got := len(mgr.MicroBFDGroups()) + len(mgr.Sessions()); got != 0 {
		t.Fatalf("groups plus sessions after reload removal = %d, want 0", got)
	}
}

// Н1: an identical API and reload group share one group like an exact
// session; each source releases only its own claim.
func TestManagerMicroBFDGroupSharedByAPIAndReload(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	cfg := defaultMicroBFDConfig()
	group, err := mgr.CreateMicroBFDGroup(cfg)
	if err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}
	created, destroyed, err := mgr.ReconcileMicroBFDGroups(t.Context(), microDesired(microLeaseFactories(), cfg))
	if err != nil || created != 0 || destroyed != 0 {
		t.Fatalf("shared reconcile = (%d, %d, %v), want (0, 0, nil)", created, destroyed, err)
	}
	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(), nil).Err(); err != nil {
		t.Fatalf("empty reconcile: %v", err)
	}
	if got, ok := mgr.LookupMicroBFDGroup(testLAG); !ok || got != group {
		t.Fatal("reload release removed the group still claimed by the API")
	}
	if err := mgr.DestroyMicroBFDGroup(testLAG); err != nil {
		t.Fatalf("DestroyMicroBFDGroup after reload release: %v", err)
	}
	if _, ok := mgr.LookupMicroBFDGroup(testLAG); ok {
		t.Fatal("group remains after its last claim")
	}

	desired := microDesired(microLeaseFactories(), cfg)
	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(), desired).Err(); err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
		t.Fatalf("API claim of identical reload group: %v", err)
	}
	if _, err := mgr.CreateMicroBFDGroup(cfg); !errors.Is(err, bfd.ErrMicroBFDGroupExists) {
		t.Fatalf("second API claim err = %v, want ErrMicroBFDGroupExists", err)
	}
	if err := mgr.DestroyMicroBFDGroup(testLAG); err != nil {
		t.Fatalf("API release of shared group: %v", err)
	}
	if _, ok := mgr.LookupMicroBFDGroup(testLAG); !ok {
		t.Fatal("API release removed the group still claimed by reload")
	}
	divergent := cfg
	divergent.MinActiveLinks = 2
	if _, err := mgr.CreateMicroBFDGroup(divergent); !errors.Is(err, bfd.ErrMicroBFDConfigConflict) {
		t.Fatalf("divergent API group err = %v, want ErrMicroBFDConfigConflict", err)
	}
}

// Each API principal releases only its own group claim.
func TestManagerDestroyMicroBFDGroupAsRequiresOwnClaim(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	if _, err := mgr.CreateMicroBFDGroupAs(defaultMicroBFDConfig(), bfd.APISessionOwner("uid:1")); err != nil {
		t.Fatalf("CreateMicroBFDGroupAs: %v", err)
	}
	err := mgr.DestroyMicroBFDGroupAs(testLAG, bfd.APISessionOwner("uid:2"))
	if !errors.Is(err, bfd.ErrMicroBFDGroupOwnerClaimNotFound) {
		t.Fatalf("foreign DestroyMicroBFDGroupAs err = %v, want ErrMicroBFDGroupOwnerClaimNotFound", err)
	}
	if err := mgr.DestroyMicroBFDGroupAs(testLAG, bfd.APISessionOwner("uid:1")); err != nil {
		t.Fatalf("own DestroyMicroBFDGroupAs: %v", err)
	}
}
