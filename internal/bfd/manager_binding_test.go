package bfd

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type interfaceBindingTestSender struct {
	index atomic.Int64
	calls atomic.Int64
	fail  atomic.Bool
}

func (*interfaceBindingTestSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

func (s *interfaceBindingTestSender) RebindInterface(ctx context.Context, _ string, index int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.calls.Add(1)
	if s.fail.Load() {
		return errFakeResolve
	}
	s.index.Store(int64(index))
	return nil
}

func TestManagerInterfaceBindingPreservesPhysicalOwners(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"config", "shared", "api"} {
		t.Run(mode, func(t *testing.T) {
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
				factory := NonOwningSenderLeaseFactory(sender)
				if mode != "api" {
					result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(),
						[]ReconcileConfig{{Key: "retained", SessionConfig: cfg, SenderLeaseFactory: factory}})
					if result.Err() != nil {
						t.Fatal(result.Err())
					}
				}
				if mode != "config" {
					if _, err := mgr.CreateSessionAs(t.Context(), cfg, factory, APISessionOwner("alice")); err != nil {
						t.Fatal(err)
					}
				}
				before := mgr.Sessions()[0]
				s, ok := mgr.LookupByDiscriminator(before.LocalDiscr)
				if !ok {
					t.Fatal("initial session missing")
				}
				s.RecvPacket(&ControlPacket{
					Version: Version, State: StateInit, DetectMult: 3,
					MyDiscriminator: 100, DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
				})
				synctest.Wait()
				if s.State() != StateUp {
					t.Fatal("initial session did not reach Up")
				}
				index.Store(11)
				callbacks := 0
				pending, err := mgr.RefreshInterfaceBindings(t.Context(), func() {
					callbacks++
					if len(mgr.Sessions()) != 1 {
						t.Error("pending callback lost the registered session")
					}
				}, nil)
				if err != nil || pending != 0 || callbacks != 1 || sender.index.Load() != 11 {
					t.Fatalf("refresh pending=%d callbacks=%d index=%d error=%v", pending, callbacks, sender.index.Load(), err)
				}
				retained, ok := mgr.LookupByDiscriminator(before.LocalDiscr)
				after := mgr.Sessions()[0]
				if !ok || retained != s || before.Ref != after.Ref || s.State() != StateDown || s.LocalDiag() != DiagPathDown {
					t.Fatalf("physical identity/state not retained: before=%+v after=%+v", before, after)
				}
				mgr.mu.RLock()
				owners := len(mgr.sessions[before.LocalDiscr].owners)
				mgr.mu.RUnlock()
				wantOwners := 1
				if mode == "shared" {
					wantOwners = 2
				}
				if owners != wantOwners {
					t.Fatalf("owners=%d, want%d", owners, wantOwners)
				}
				for _, tc := range []struct {
					index int
					want  DeliveryOutcome
				}{
					{11, DeliveryQueued}, {7, DeliveryScopeMismatch},
				} {
					got, deliveryErr := demuxTransport(mgr, s, demuxPathDiscr,
						PacketMeta{SrcAddr: family.peer, DstAddr: family.local, TTL: 255, IfName: "eth9", IfIndex: tc.index})
					if got != tc.want {
						t.Fatalf("RX index%d=%s error=%v, want%s", tc.index, got, deliveryErr, tc.want)
					}
				}
				if pending, err = mgr.RefreshInterfaceBindings(t.Context(), nil, nil); pending != 0 ||
					err != nil || sender.calls.Load() != 1 {
					t.Fatalf("duplicate refresh mutated transport: pending=%d calls=%d error=%v", pending, sender.calls.Load(), err)
				}
			})
		})
	}
}

func TestManagerInterfaceBindingRetriesFailureAndRejectsOldRX(t *testing.T) {
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
		old := s.receiveBinding.Load()
		index.Store(11)
		sender.fail.Store(true)
		pending, err := mgr.RefreshInterfaceBindings(t.Context(), nil, nil)
		if pending != 1 || !errors.Is(err, errFakeResolve) || !mgr.InterfaceBindingPending() {
			t.Fatalf("failed refresh pending=%d error=%v", pending, err)
		}
		pkt := &ControlPacket{
			Version: Version, State: StateInit, DetectMult: 3,
			MyDiscriminator: 100, DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
		}
		if got := s.recvPacket(pkt, nil, ReceiveTiming{}, old); got != DeliveryClosed {
			t.Fatalf("old in-flight admission=%s", got)
		}
		if got := s.checkReceivedPacket(recvItem{pkt: *pkt, binding: old}); got != DeliveryAbandoned {
			t.Fatalf("old queued packet=%s", got)
		}
		if s.RecvPacket(pkt) != DeliveryClosed {
			t.Fatal("pending transport admitted compatibility RX")
		}
		sent := s.PacketsSent()
		synctest.Sleep(2 * time.Second)
		if s.PacketsSent() != sent {
			t.Fatal("pending transport emitted periodic Control")
		}
		sender.fail.Store(false)
		if pending, err = mgr.RefreshInterfaceBindings(t.Context(), nil, nil); pending != 0 ||
			err != nil || mgr.InterfaceBindingPending() {
			t.Fatalf("retry pending=%d error=%v", pending, err)
		}
		if s.checkReceivedPacket(recvItem{pkt: *pkt, binding: old}) != DeliveryAbandoned {
			t.Fatal("rebind revived an old queued packet")
		}
	})
}
