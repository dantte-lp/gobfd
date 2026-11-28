package bfd_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// RFC 7130 Section 2: a member link belongs to exactly one LAG, so a second
// group cannot claim it and dispatch ownership stays unambiguous.
func TestManagerCreateMicroBFDGroupRejectsMemberOwnedByOtherLAG(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	if _, err := mgr.CreateMicroBFDGroup(defaultMicroBFDConfig()); err != nil {
		t.Fatalf("CreateMicroBFDGroup bond0: %v", err)
	}
	other := defaultMicroBFDConfig()
	other.LAGInterface = "bond1"
	other.MemberLinks = []string{"eth1", "eth2"}
	other.PeerAddr = netip.MustParseAddr("10.0.1.1")

	if _, err := mgr.CreateMicroBFDGroup(other); err == nil {
		t.Fatal("CreateMicroBFDGroup accepted eth1 already owned by bond0")
	}
	if _, ok := mgr.LookupMicroBFDGroup("bond1"); ok {
		t.Fatal("rejected group bond1 is registered")
	}
}

// M-09: the direct Manager entry validates the RFC 5880 session contract that
// every member session inherits (RFC 7130 Section 2.2) before any mutation.
func TestManagerCreateMicroBFDGroupValidatesMemberSessionContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*bfd.MicroBFDConfig)
	}{
		{"zero desired min tx", func(c *bfd.MicroBFDConfig) { c.DesiredMinTxInterval = 0 }},
		{"desired min tx above wire", func(c *bfd.MicroBFDConfig) {
			c.DesiredMinTxInterval = bfd.MaxWireInterval + time.Microsecond
		}},
		{"required min rx above wire", func(c *bfd.MicroBFDConfig) {
			c.RequiredMinRxInterval = bfd.MaxWireInterval + time.Microsecond
		}},
		{"zero detect multiplier", func(c *bfd.MicroBFDConfig) { c.DetectMultiplier = 0 }},
		{"invalid peer address", func(c *bfd.MicroBFDConfig) { c.PeerAddr = netip.Addr{} }},
		{"empty member name", func(c *bfd.MicroBFDConfig) { c.MemberLinks = []string{"eth0", ""} }},
		{"empty LAG name", func(c *bfd.MicroBFDConfig) { c.LAGInterface = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultMicroBFDConfig()
			tt.mutate(&cfg)
			if err := bfd.ValidateMicroBFDConfig(cfg); err == nil {
				t.Error("ValidateMicroBFDConfig accepted an invalid member session contract")
			}
			mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
			defer mgr.Close()
			if _, err := mgr.CreateMicroBFDGroup(cfg); err == nil {
				t.Error("CreateMicroBFDGroup accepted an invalid member session contract")
			}
			if got := len(mgr.MicroBFDGroups()); got != 0 {
				t.Errorf("groups after rejected create = %d, want 0", got)
			}
		})
	}
}

// Dispatch resolves the owner by member link and address family. A Micro-BFD
// session of another family on the same link is not a member of the group.
func TestManagerDispatchMicroBFDIgnoresOtherAddressFamily(t *testing.T) {
	actuator := &recordingMicroBFDActuator{events: make(chan bfd.MicroBFDMemberEvent, 8)}
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithMicroBFDActuator(actuator))
	defer mgr.Close()
	if _, err := mgr.CreateMicroBFDGroup(defaultMicroBFDConfig()); err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}
	v6 := microMemberSession(t, mgr, "2001:db8::1", "2001:db8::2")
	v4 := microMemberSession(t, mgr, "10.0.0.1", "10.0.0.2")
	go mgr.RunDispatch(t.Context())

	sendControlState(t, v6, bfd.StateDown, 41)
	sendControlState(t, v4, bfd.StateDown, 42)

	select {
	case ev := <-actuator.events:
		if ev.LocalDiscr != v4.LocalDiscriminator() {
			t.Fatalf("actuated discriminator %d, want IPv4 member %d", ev.LocalDiscr, v4.LocalDiscriminator())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the IPv4 member event")
	}
}

type gateMicroBFDActuator struct {
	entered chan struct{}
	release chan struct{}
}

func (a *gateMicroBFDActuator) HandleMicroBFDMemberEvent(ctx context.Context, _ bfd.MicroBFDMemberEvent) error {
	select {
	case a.entered <- struct{}{}:
	default:
	}
	select {
	case <-a.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// M-10: a blocked actuator does not hold Manager locks, so group CRUD proceeds.
func TestManagerMicroBFDActuatorRunsOutsideManagerLock(t *testing.T) {
	actuator := &gateMicroBFDActuator{entered: make(chan struct{}, 1), release: make(chan struct{})}
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithMicroBFDActuator(actuator))
	defer mgr.Close()
	defer close(actuator.release)
	if _, err := mgr.CreateMicroBFDGroup(defaultMicroBFDConfig()); err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}
	sess := microMemberSession(t, mgr, "10.0.0.1", "10.0.0.2")
	go mgr.RunDispatch(t.Context())

	sendControlState(t, sess, bfd.StateDown, 42)
	select {
	case <-actuator.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("actuator was not called")
	}

	done := make(chan error, 1)
	go func() {
		other := defaultMicroBFDConfig()
		other.LAGInterface = "bond1"
		other.MemberLinks = []string{"eth2", "eth3"}
		_, err := mgr.CreateMicroBFDGroup(other)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CreateMicroBFDGroup during actuation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CreateMicroBFDGroup blocked while the actuator ran")
	}
}

type deadlineMicroBFDActuator struct {
	calls chan bfd.MicroBFDMemberEvent
	errs  chan error
}

func (a *deadlineMicroBFDActuator) HandleMicroBFDMemberEvent(ctx context.Context, ev bfd.MicroBFDMemberEvent) error {
	<-ctx.Done()
	a.calls <- ev
	a.errs <- ctx.Err()
	return ctx.Err()
}

// M-10: each actuator call is bounded by a deadline; a hung backend delays
// but never stops the ordered delivery of later member transitions.
func TestManagerMicroBFDActuatorDeadlineBoundsDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		actuator := &deadlineMicroBFDActuator{
			calls: make(chan bfd.MicroBFDMemberEvent, 4), errs: make(chan error, 4),
		}
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithMicroBFDActuator(actuator))
		defer mgr.Close()
		if _, err := mgr.CreateMicroBFDGroup(defaultMicroBFDConfig()); err != nil {
			t.Fatalf("CreateMicroBFDGroup: %v", err)
		}
		sess := microMemberSession(t, mgr, "10.0.0.1", "10.0.0.2")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go mgr.RunDispatch(ctx)

		for _, step := range []struct{ remote, want bfd.State }{
			{bfd.StateDown, bfd.StateInit},
			{bfd.StateInit, bfd.StateUp},
		} {
			start := time.Now()
			sendControlState(t, sess, step.remote, 42)
			select {
			case ev := <-actuator.calls:
				if ev.NewState != step.want {
					t.Fatalf("actuated state %s, want %s", ev.NewState, step.want)
				}
			case <-time.After(time.Minute):
				t.Fatal("actuator call has no deadline")
			}
			if err := <-actuator.errs; !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("actuator context error = %v, want deadline", err)
			}
			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Fatalf("actuator deadline after %v, want <= 10s", elapsed)
			}
		}
	})
}

// microMemberSession creates a Micro-BFD session on member link eth0.
func microMemberSession(t *testing.T, mgr *bfd.Manager, peer, local string) *bfd.Session {
	t.Helper()
	cfg := defaultManagerConfig()
	cfg.PeerAddr = netip.MustParseAddr(peer)
	cfg.LocalAddr = netip.MustParseAddr(local)
	cfg.Type, cfg.Interface, cfg.Role = bfd.SessionTypeMicroBFD, "eth0", bfd.RolePassive
	// A 90 s detection time keeps member state stable across actuator deadlines.
	cfg.RequiredMinRxInterval = 30 * time.Second
	sess, err := mgr.CreateSession(t.Context(), cfg, bfd.NonOwningSenderLeaseFactory(noopSender{}))
	if err != nil {
		t.Fatalf("CreateSession %s%%eth0: %v", peer, err)
	}
	return sess
}

var errMicroTestLease = errors.New("micro test sender lease failure")

// microLeaseFactories opens no-op member senders except for failing members.
func microLeaseFactories(failing ...string) func(string) bfd.SenderLeaseFactory {
	return func(member string) bfd.SenderLeaseFactory {
		if slices.Contains(failing, member) {
			return func() (*bfd.SenderLease, error) { return nil, errMicroTestLease }
		}
		return bfd.NonOwningSenderLeaseFactory(noopSender{})
	}
}

func microDesired(
	factory func(string) bfd.SenderLeaseFactory, cfgs ...bfd.MicroBFDConfig,
) []bfd.MicroBFDReconcileConfig {
	desired := make([]bfd.MicroBFDReconcileConfig, 0, len(cfgs))
	for _, cfg := range cfgs {
		desired = append(desired, bfd.MicroBFDReconcileConfig{
			Key: cfg.LAGInterface, Config: cfg, SenderLeaseFactory: factory,
		})
	}
	return desired
}

func sessionDiscriminators(mgr *bfd.Manager) []uint32 {
	snaps := mgr.Sessions()
	discrs := make([]uint32, 0, len(snaps))
	for _, snap := range snaps {
		discrs = append(discrs, snap.LocalDiscr)
	}
	slices.Sort(discrs)
	return discrs
}

// H-24: a group and its member sessions commit as one unit.
func TestManagerReconcileMicroBFDCommitsGroupWithMemberSessions(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	result := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories(), defaultMicroBFDConfig()))
	if err := result.Err(); err != nil || result.Created != 1 {
		t.Fatalf("reconcile = created %d err %v, want 1 group", result.Created, err)
	}
	if _, ok := mgr.LookupMicroBFDGroup(testLAG); !ok {
		t.Fatal("group bond0 is not committed")
	}
	for _, snap := range mgr.Sessions() {
		if snap.Type != bfd.SessionTypeMicroBFD {
			t.Fatalf("session type %s, want MicroBFD", snap.Type)
		}
	}
	if got := len(mgr.Sessions()); got != 2 {
		t.Fatalf("member sessions = %d, want 2", got)
	}

	result = mgr.ReconcileMicroBFDGroupsDetailed(t.Context(), nil)
	if err := result.Err(); err != nil || result.Released != 1 {
		t.Fatalf("empty reconcile = released %d err %v, want 1 group", result.Released, err)
	}
	if got := len(mgr.MicroBFDGroups()) + len(mgr.Sessions()); got != 0 {
		t.Fatalf("groups plus sessions after removal = %d, want 0", got)
	}
}

// H-24: a failed member claim rolls back the group and the claimed members.
func TestManagerReconcileMicroBFDRollsBackGroupWhenMemberClaimFails(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	result := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories("eth1"), defaultMicroBFDConfig()))
	if !errors.Is(result.Err(), errMicroTestLease) || result.Created != 0 {
		t.Fatalf("reconcile = created %d err %v, want lease failure and no group", result.Created, result.Err())
	}
	if _, ok := mgr.LookupMicroBFDGroup(testLAG); ok {
		t.Fatal("group committed without its member sessions")
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Fatalf("member sessions after rollback = %d, want 0", got)
	}
}

// H-24: a failed replacement keeps the committed group and its sessions.
func TestManagerReconcileMicroBFDReplacementFailureKeepsCommittedGroup(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories(), defaultMicroBFDConfig())).Err(); err != nil {
		t.Fatalf("seed reconcile: %v", err)
	}
	seedGroup, _ := mgr.LookupMicroBFDGroup(testLAG)
	seedSessions := sessionDiscriminators(mgr)

	replacement := defaultMicroBFDConfig()
	replacement.LAGInterface = "bond1"
	replacement.MemberLinks = []string{"eth2", "eth3"}
	result := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories("eth3"), replacement))
	if result.Err() == nil || result.Created != 0 || result.Released != 0 {
		t.Fatalf("replacement = created %d released %d err %v, want failure without changes",
			result.Created, result.Released, result.Err())
	}
	if group, ok := mgr.LookupMicroBFDGroup(testLAG); !ok || group != seedGroup {
		t.Fatal("committed group bond0 changed after failed replacement")
	}
	if _, ok := mgr.LookupMicroBFDGroup("bond1"); ok {
		t.Fatal("failed replacement group bond1 is registered")
	}
	if got := sessionDiscriminators(mgr); !slices.Equal(got, seedSessions) {
		t.Fatalf("member sessions = %v, want unchanged %v", got, seedSessions)
	}
}

// RFC 7130 Section 2: one LAG owns each member link in the desired set.
func TestManagerReconcileMicroBFDRejectsMemberReusedAcrossLAGs(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	other := defaultMicroBFDConfig()
	other.LAGInterface = "bond1"
	other.MemberLinks = []string{"eth1", "eth2"}
	other.PeerAddr = netip.MustParseAddr("10.0.1.1")

	result := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(microLeaseFactories(), defaultMicroBFDConfig(), other))
	if !errors.Is(result.Err(), bfd.ErrMicroBFDMemberOwned) {
		t.Fatalf("reconcile err = %v, want ErrMicroBFDMemberOwned", result.Err())
	}
	if got := len(mgr.MicroBFDGroups()) + len(mgr.Sessions()); got != 0 {
		t.Fatalf("groups plus sessions after rejected candidate = %d, want 0", got)
	}
	if _, err := mgr.CreateMicroBFDGroup(defaultMicroBFDConfig()); err != nil {
		t.Fatalf("CreateMicroBFDGroup bond0: %v", err)
	}
	if _, err := mgr.CreateMicroBFDGroup(other); !errors.Is(err, bfd.ErrMicroBFDMemberOwned) {
		t.Fatalf("CreateMicroBFDGroup bond1 err = %v, want ErrMicroBFDMemberOwned", err)
	}
}

// H-24: a removal whose sender close fails still detaches the group and its
// member sessions together and reports the failure as cleanup.
func TestManagerReconcileMicroBFDRemovalCleanupFailureDetachesGroup(t *testing.T) {
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	closeErr := errors.New("micro test sender close failure")
	failingClose := func(string) bfd.SenderLeaseFactory {
		return func() (*bfd.SenderLease, error) {
			return bfd.NewSenderLease(noopSender{}, func() error { return closeErr }), nil
		}
	}
	if err := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(),
		microDesired(failingClose, defaultMicroBFDConfig())).Err(); err != nil {
		t.Fatalf("seed reconcile: %v", err)
	}

	result := mgr.ReconcileMicroBFDGroupsDetailed(t.Context(), nil)
	if !errors.Is(result.Err(), closeErr) || result.Released != 1 {
		t.Fatalf("removal = released %d err %v, want 1 group and close failure", result.Released, result.Err())
	}
	if !slices.ContainsFunc(result.Errors, func(e bfd.ReconcileError) bool {
		return e.Code == bfd.ReconcileErrorCleanup
	}) {
		t.Fatalf("removal errors = %+v, want cleanup code", result.Errors)
	}
	if got := len(mgr.MicroBFDGroups()) + len(mgr.Sessions()); got != 0 {
		t.Fatalf("groups plus sessions after removal = %d, want 0", got)
	}
}
