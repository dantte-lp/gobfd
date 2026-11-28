package bfd

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// leaseLedger hands out sender leases and counts how often each one's
// release runs.
type leaseLedger struct {
	mu     sync.Mutex
	closes []int
}

func (l *leaseLedger) lease() *SenderLease {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := len(l.closes)
	l.closes = append(l.closes, 0)
	return NewSenderLease(senderLeaseTestSender{}, func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.closes[id]++
		return nil
	})
}

// C-07 (gobfd-qj0.8.2.1.10.1): concurrent unsolicited admission, cleanup of
// Down sessions, configuration reload claiming and releasing the same
// session keys, and Manager.Close release every per-session sender lease
// exactly once.
func TestUnsolicitedLeasesCloseOnceUnderConcurrentAdmissionAndReload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ledger := &leaseLedger{}
		mgr := NewManager(
			slog.New(slog.DiscardHandler),
			WithUnsolicitedPolicy(admissionPolicy(8)),
			WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
			WithUnsolicitedSessionSender(func(PacketMeta) (*SenderLease, error) { return ledger.lease(), nil }),
		)
		// Stops session timers on a failed assertion; Close is idempotent.
		defer mgr.Close()

		peers := make([]string, 8)
		for i := range peers {
			peers[i] = fmt.Sprintf("192.0.2.%d", 10+i)
		}
		// A configured claim matching the unsolicited session parameters, so
		// reload and admission contend for the same session key.
		configured := func(peer string) ReconcileConfig {
			cfg := senderLeaseTestConfig(peer)
			cfg.LocalAddr = netip.MustParseAddr("192.0.2.1")
			cfg.Role = RolePassive
			return ReconcileConfig{
				Key: peer, SessionConfig: cfg,
				SenderLeaseFactory: func() (*SenderLease, error) { return ledger.lease(), nil },
			}
		}

		const rounds = 40
		var wg sync.WaitGroup
		wg.Go(func() { // admission
			for round := range rounds {
				for i, peer := range peers {
					_, _ = mgr.tryCreateUnsolicited(ownershipDownPacket(uint32(1000+round*len(peers)+i)),
						admissionMeta(peer, "192.0.2.1"), nil)
				}
				time.Sleep(150 * time.Millisecond)
			}
		})
		wg.Go(func() { // cleanup of sessions that timed out to Down
			for range rounds {
				for _, snap := range mgr.Sessions() {
					if session, ok := mgr.LookupByDiscriminator(snap.LocalDiscr); ok {
						mgr.cleanupUnsolicitedSession(context.Background(), session)
					}
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
		wg.Go(func() { // reload alternating between half the peers and none
			for round := range rounds {
				var desired []ReconcileConfig
				if round%2 == 0 {
					for _, peer := range peers[:len(peers)/2] {
						desired = append(desired, configured(peer))
					}
				}
				_, _, _ = mgr.ReconcileSessions(context.Background(), desired)
				time.Sleep(250 * time.Millisecond)
			}
		})
		wg.Wait()

		// Close with live unsolicited sessions, some of them also claimed by
		// configuration.
		desired := make([]ReconcileConfig, 0, len(peers)/2)
		for _, peer := range peers[:len(peers)/2] {
			desired = append(desired, configured(peer))
		}
		if _, _, err := mgr.ReconcileSessions(context.Background(), desired); err != nil {
			t.Fatalf("final ReconcileSessions: %v", err)
		}
		time.Sleep(2 * time.Second) // refill admission tokens
		for i, peer := range peers {
			_, _ = mgr.tryCreateUnsolicited(ownershipDownPacket(uint32(9000+i)), admissionMeta(peer, "192.0.2.1"), nil)
		}
		if got := len(mgr.Sessions()); got != len(peers) {
			t.Fatalf("sessions before Close = %d, want %d", got, len(peers))
		}
		mgr.mu.RLock()
		marked := 0
		for _, entry := range mgr.sessions {
			if entry.unsolicited {
				marked++
			}
		}
		mgr.mu.RUnlock()
		if got := int(mgr.unsolicited.sessionCount.Load()); got != marked {
			t.Errorf("unsolicited quota = %d, want %d live unsolicited sessions", got, marked)
		}
		mgr.Close()

		ledger.mu.Lock()
		defer ledger.mu.Unlock()
		if len(ledger.closes) == 0 {
			t.Fatal("no sender lease was opened")
		}
		for id, n := range ledger.closes {
			if n != 1 {
				t.Errorf("lease %d released %d times, want 1", id, n)
			}
		}
		t.Logf("leases opened and released once: %d", len(ledger.closes))
	})
}
