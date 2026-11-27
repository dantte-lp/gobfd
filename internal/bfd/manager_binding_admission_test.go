package bfd

import (
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestUnsolicitedCreationRejectsRecreatedIngress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var index atomic.Int64
		index.Store(7)
		policy := admissionPolicy(4)
		policy.Interfaces["eth0"] = UnsolicitedInterfaceConfig{Enabled: true, IfIndex: 7}
		started, release := make(chan struct{}), make(chan struct{})
		var closed atomic.Int64
		mgr := newTransportManager(t, WithUnsolicitedPolicy(policy),
			WithSessionInterfaceResolver(func(netip.Addr, string) (int, error) { return int(index.Load()), nil }),
			WithUnsolicitedSessionSender(func(PacketMeta) (*SenderLease, error) {
				close(started)
				<-release
				return NewSenderLease(senderLeaseTestSender{}, func() error { closed.Add(1); return nil }), nil
			}))
		mgr.unsolicited.connectedPrefix = connectedTo("192.0.2.1/24")
		finished := make(chan DeliveryOutcome, 1)
		go func() {
			meta := admissionMeta("192.0.2.10", "192.0.2.1")
			meta.IfIndex = 7
			outcome, err := mgr.DemuxWithWire(*ownershipDownPacket(101), meta, nil)
			if err == nil && outcome != DeliveryQueued {
				t.Error("failed unsolicited outcome omitted its error")
			}
			finished <- outcome
		}()
		<-started
		index.Store(11)
		if pending, err := mgr.RefreshInterfaceBindings(t.Context(), nil, nil); pending != 0 || err != nil {
			t.Fatalf("refresh pending=%d error=%v", pending, err)
		}
		close(release)
		outcome := <-finished
		if outcome == DeliveryQueued || len(mgr.Sessions()) != 0 ||
			mgr.unsolicited.sessionCount.Load() != 0 || closed.Load() != 1 {
			t.Fatalf("stale ingress outcome=%s sessions=%d quota=%d closed=%d",
				outcome, len(mgr.Sessions()), mgr.unsolicited.sessionCount.Load(), closed.Load())
		}
	})
}
