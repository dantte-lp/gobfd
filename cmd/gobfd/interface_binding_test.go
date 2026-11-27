package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestRuntimeBindingsRejectLateListenerCallback(t *testing.T) {
	t.Parallel()
	coordinator, checker := newConvergedReadinessCoordinator(t)
	var late func() error
	coordinator.refreshTransports = func(check func() error) error {
		late = check
		return context.DeadlineExceeded
	}
	if err := coordinator.checkListenerBindings(nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := late(); !errors.Is(err, bfd.ErrInterfaceUnresolved) {
		t.Fatalf("late callback accepted: %v", err)
	}
	assertReadiness(t, checker, readinessWant{
		grpchealth.StatusNotServing, grpchealth.StatusNotServing, grpchealth.StatusServing, grpchealth.StatusServing,
	})
}

func TestRuntimeBindingsJoinTimedOutListenerCallback(t *testing.T) {
	t.Parallel()
	t.Run("join", func(t *testing.T) {
		t.Parallel()
		coordinator, checker := newConvergedReadinessCoordinator(t)
		coordinator.readiness.bindings = []listenerBinding{{
			component: componentControlBindings, ifName: "eth9", ifIndex: 7,
		}}
		started, release := make(chan struct{}), make(chan struct{})
		timedOut := make(chan struct{})
		ownerDone := make(chan error, 1)
		coordinator.refreshTransports = func(check func() error) error {
			go func() { ownerDone <- check() }()
			<-started
			close(timedOut)
			return context.DeadlineExceeded
		}
		calls := 0
		inventory := interfaceInventory(func() ([]net.Interface, error) {
			calls++
			if calls == 1 {
				close(started)
				<-release
			}
			return []net.Interface{upInterface("eth9", 7)}, nil
		})
		finished := make(chan error, 1)
		go func() { finished <- coordinator.checkListenerBindings(inventory) }()
		<-started
		<-timedOut
		select {
		case err := <-finished:
			close(release)
			t.Fatalf("runtime check returned before owner callback joined: %v", err)
		case <-time.After(25 * time.Millisecond):
			// Mutex blocking cannot advance a synctest clock; bound this negative assertion in real time.
		}
		close(release)
		if err := <-finished; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if err := <-ownerDone; err != nil || calls != 2 {
			t.Fatalf("listener proof: calls=%d error=%v", calls, err)
		}
		assertReadiness(t, checker, readinessWant{
			grpchealth.StatusNotServing, grpchealth.StatusNotServing, grpchealth.StatusServing, grpchealth.StatusServing,
		})
	})
}

func TestRuntimeBindingsRecheckListenerBeforeEachCommit(t *testing.T) {
	t.Parallel()
	coordinator, checker := newConvergedReadinessCoordinator(t)
	coordinator.readiness.bindings = []listenerBinding{{
		component: componentControlBindings, ifName: "eth9", ifIndex: 7,
	}}
	index, checks := 7, 0
	inventory := func() ([]net.Interface, error) {
		checks++
		return []net.Interface{upInterface("eth9", index)}, nil
	}
	coordinator.refreshTransports = func(check func() error) error {
		if err := check(); err != nil {
			return err
		}
		index = 11
		return check()
	}
	if err := coordinator.checkListenerBindings(inventory); !errors.Is(err, bfd.ErrInterfaceUnresolved) || checks != 3 {
		t.Fatalf("stale listener proof: checks=%d error=%v", checks, err)
	}
	assertReadiness(t, checker, readinessWant{
		grpchealth.StatusNotServing, grpchealth.StatusNotServing, grpchealth.StatusServing, grpchealth.StatusServing,
	})
}

type daemonBindingSender struct {
	fail  atomic.Bool
	calls atomic.Int64
}

func (*daemonBindingSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

func (s *daemonBindingSender) RebindInterface(context.Context, string, int) error {
	s.calls.Add(1)
	if s.fail.Load() {
		return bfd.ErrInterfaceUnresolved
	}
	return nil
}

func TestRuntimeBindingsRequireBothListenerAndSessionRecovery(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var index atomic.Int64
		index.Store(7)
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler),
			bfd.WithSessionInterfaceResolver(func(netip.Addr, string) (int, error) { return int(index.Load()), nil }))
		t.Cleanup(mgr.Close)
		coordinator, checker := newConvergedReadinessCoordinator(t)
		coordinator.watchSessionBindings(t.Context(), mgr)
		inventory := &fakeInterfaceInventory{}
		inventory.set(nil, upInterface("eth9", 7))
		owner := newListenerOwner(mgr, coordinator.readiness, 0, slog.New(slog.DiscardHandler))
		owner.open = newFakeListenerOpener().open
		owner.interfaces = inventory.list
		t.Cleanup(owner.close)
		coordinator.listeners = owner
		listenerRebinds := 0
		listenerFailure := false
		owner.rebind = func(_ *netio.Listener, target int) error {
			listenerRebinds++
			if target != 11 {
				t.Errorf("listener expected index=%d, want11", target)
			}
			if listenerFailure {
				return bfd.ErrInterfaceUnresolved
			}
			return nil
		}
		configs := []netio.ListenerConfig{singleHopListener("192.0.2.20", "eth9")}
		if err := owner.acquire(sourceClaim(sourceBase), configs); err != nil {
			t.Fatal(err)
		}
		sender := &daemonBindingSender{}
		cfg := bfd.SessionConfig{
			PeerAddr: netip.MustParseAddr("192.0.2.10"), LocalAddr: netip.MustParseAddr("192.0.2.20"),
			Interface: "eth9", Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
			DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
		}
		factory := bfd.NonOwningSenderLeaseFactory(sender)
		session, err := mgr.CreateSessionAs(t.Context(), cfg, factory, bfd.APISessionOwner("alice"))
		if err != nil {
			t.Fatal(err)
		}
		before := mgr.Sessions()[0]
		generation := coordinator.Snapshot().DesiredGeneration
		if err = coordinator.checkListenerBindings(inventory.list); err != nil {
			t.Fatal(err)
		}
		assertReadiness(t, checker, readinessAllServing())
		index.Store(11)
		inventory.set(nil, upInterface("eth9", 11))
		listenerFailure = true
		sender.fail.Store(true)
		if err = coordinator.checkListenerBindings(inventory.list); !errors.Is(err, bfd.ErrInterfaceUnresolved) {
			t.Fatalf("failed sender refresh=%v", err)
		}
		serving, notServing := grpchealth.StatusServing, grpchealth.StatusNotServing
		assertReadiness(t, checker, readinessWant{notServing, notServing, serving, serving})
		if listenerRebinds != 1 {
			t.Fatalf("listener rebind count=%d", listenerRebinds)
		}
		sender.fail.Store(false)
		if err = coordinator.checkListenerBindings(inventory.list); !errors.Is(err, bfd.ErrInterfaceUnresolved) {
			t.Fatalf("failed listener refresh=%v", err)
		}
		if !mgr.InterfaceBindingPending() {
			t.Fatal("sender success reopened the session before receive listener proof")
		}
		assertReadiness(t, checker, readinessWant{notServing, notServing, serving, serving})
		listenerFailure = false
		if err = coordinator.checkListenerBindings(inventory.list); err != nil {
			t.Fatal(err)
		}
		assertReadiness(t, checker, readinessAllServing())
		pkt := bfd.ControlPacket{
			Version: bfd.Version, State: bfd.StateDown, DetectMult: 3,
			MyDiscriminator: 100, YourDiscriminator: before.LocalDiscr,
			DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
		}
		outcome, receiveErr := mgr.DemuxWithWire(pkt,
			bfd.PacketMeta{SrcAddr: cfg.PeerAddr, DstAddr: cfg.LocalAddr, IfName: "eth9", IfIndex: 11, TTL: 255}, nil)
		if outcome != bfd.DeliveryQueued || receiveErr != nil {
			t.Fatalf("recovered RX=%s error=%v", outcome, receiveErr)
		}
		retained, found := mgr.LookupByDiscriminator(before.LocalDiscr)
		if !found || retained != session || mgr.Sessions()[0].Ref != before.Ref ||
			coordinator.Snapshot().DesiredGeneration != generation {
			t.Fatal("binding recovery changed session or desired generation")
		}
		if err = coordinator.checkListenerBindings(inventory.list); err != nil ||
			sender.calls.Load() != 3 || listenerRebinds != 4 {
			t.Fatalf("duplicate runtime check replayed work: sender=%d listener=%d error=%v",
				sender.calls.Load(), listenerRebinds, err)
		}
	})
}
