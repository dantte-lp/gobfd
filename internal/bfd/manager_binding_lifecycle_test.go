package bfd

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

type blockingInterfaceSender struct {
	started chan struct{}
}

func (*blockingInterfaceSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

func (s *blockingInterfaceSender) RebindInterface(ctx context.Context, _ string, _ int) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestManagerInterfaceBindingCloseCancelsOwnerOperation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var index atomic.Int64
		index.Store(7)
		mgr := newTransportManager(t, WithSessionInterfaceResolver(func(netip.Addr, string) (int, error) {
			return int(index.Load()), nil
		}))
		family := transportFamilies()[0]
		cfg := transportSessionConfig(family.peer, family.local, SessionTypeSingleHop)
		cfg.Interface = "eth9"
		sender := &blockingInterfaceSender{started: make(chan struct{})}
		s, err := mgr.CreateSession(t.Context(), cfg, NonOwningSenderLeaseFactory(sender))
		if err != nil {
			t.Fatal(err)
		}
		index.Store(11)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			if _, refreshErr := mgr.RefreshInterfaceBindings(t.Context(), nil, nil); refreshErr != nil &&
				!errors.Is(refreshErr, context.Canceled) && !errors.Is(refreshErr, ErrSessionStopped) {
				t.Errorf("close refresh: %v", refreshErr)
			}
		}()
		<-sender.started
		mgr.Close()
		<-finished
		if _, found := mgr.LookupByDiscriminator(s.LocalDiscriminator()); found || len(mgr.Sessions()) != 0 {
			t.Fatal("late binding completion resurrected a closed entry")
		}
		if binding := s.receiveBinding.Load(); binding == nil || !binding.pending {
			t.Fatal("shutdown committed the new binding")
		}
	})
}

func TestManagerInterfaceBindingUnsupportedStaysPending(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var index atomic.Int64
		index.Store(7)
		mgr := newTransportManager(t, WithSessionInterfaceResolver(func(netip.Addr, string) (int, error) {
			return int(index.Load()), nil
		}))
		family := transportFamilies()[0]
		cfg := transportSessionConfig(family.peer, family.local, SessionTypeSingleHop)
		cfg.Interface = "eth9"
		s := createTransportSession(t, mgr, cfg)
		index.Store(11)
		pending, err := mgr.RefreshInterfaceBindings(t.Context(), nil, nil)
		if pending != 1 || !errors.Is(err, ErrInterfaceRebindUnsupported) || !mgr.InterfaceBindingPending() {
			t.Fatalf("unsupported refresh pending=%d error=%v", pending, err)
		}
		if s.transportOutcome(PacketMeta{TTL: 255, IfName: "eth9", IfIndex: 7}) != DeliveryScopeMismatch {
			t.Fatal("pending unsupported transport admitted old RX")
		}
	})
}

func TestManagerInterfaceBindingPreservesAdminDown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var index atomic.Int64
		index.Store(7)
		mgr := newTransportManager(t, WithSessionInterfaceResolver(func(netip.Addr, string) (int, error) {
			return int(index.Load()), nil
		}))
		family := transportFamilies()[0]
		cfg := transportSessionConfig(family.peer, family.local, SessionTypeSingleHop)
		cfg.Interface = "eth9"
		sender := &interfaceBindingTestSender{}
		s, err := mgr.CreateSession(t.Context(), cfg, NonOwningSenderLeaseFactory(sender))
		if err != nil {
			t.Fatal(err)
		}
		if adminErr := s.SetAdminDown(t.Context()); adminErr != nil {
			t.Fatal(adminErr)
		}
		synctest.Wait()
		index.Store(11)
		if pending, refreshErr := mgr.RefreshInterfaceBindings(t.Context(), nil, nil); pending != 0 || refreshErr != nil {
			t.Fatalf("AdminDown refresh: pending=%d error=%v", pending, refreshErr)
		}
		if s.State() != StateAdminDown || s.LocalDiag() != DiagAdminDown || s.receiveBinding.Load().ifIndex != 11 {
			t.Fatalf("AdminDown changed during recovery: state=%s diag=%s", s.State(), s.LocalDiag())
		}
	})
}

func TestSessionInterfaceBindingCanceledBeforeAdmission(t *testing.T) {
	t.Parallel()
	mgr := newTransportManager(t)
	family := transportFamilies()[0]
	cfg := transportSessionConfig(family.peer, family.local, SessionTypeSingleHop)
	cfg.IfIndex = 7
	s := createTransportSession(t, mgr, cfg)
	prior := s.receiveBinding.Load()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.rebindInterface(ctx, 11, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled binding=%v", err)
	}
	if s.receiveBinding.Load() != prior {
		t.Fatal("canceled request changed binding")
	}
	if err := s.rebindInterface(t.Context(), 0, nil); !errors.Is(err, ErrInterfaceUnresolved) {
		t.Fatalf("invalid binding=%v", err)
	}
	if s.receiveBinding.Load() != prior {
		t.Fatal("invalid request changed binding")
	}
}

func TestUnsolicitedInterfaceBindingRefreshDropsOldIndex(t *testing.T) {
	t.Parallel()
	var index atomic.Int64
	index.Store(7)
	policy := admissionPolicy(4)
	policy.Interfaces = map[string]UnsolicitedInterfaceConfig{"eth9": {Enabled: true, IfIndex: 7}}
	mgr := newTransportManager(t, WithUnsolicitedPolicy(policy),
		WithSessionInterfaceResolver(func(netip.Addr, string) (int, error) { return int(index.Load()), nil }))
	index.Store(11)
	if pending, err := mgr.RefreshInterfaceBindings(t.Context(), nil, nil); pending != 0 || err != nil {
		t.Fatalf("policy refresh pending=%d error=%v", pending, err)
	}
	names := *mgr.unsolicited.interfaceNames.Load()
	if names[11] != "eth9" || names[7] != "" || len(names) != 1 {
		t.Fatalf("policy mapping=%v", names)
	}
	index.Store(0)
	if pending, err := mgr.RefreshInterfaceBindings(t.Context(), nil, nil); pending != 1 ||
		!errors.Is(err, ErrInterfaceUnresolved) {
		t.Fatalf("missing policy interface pending=%d error=%v", pending, err)
	}
	if len(*mgr.unsolicited.interfaceNames.Load()) != 0 {
		t.Fatal("failed inventory retained an old ingress authorization")
	}
}
