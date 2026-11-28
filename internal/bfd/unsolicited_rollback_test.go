package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
)

var errInjectedSender = errors.New("injected sender failure")

type sessionSender = func(PacketMeta) (*SenderLease, error)

// H-19: every failure after admission reserved a slot returns the slot,
// closes the sender lease it opened exactly once, leaks no discriminator and
// leaves no session, so the next valid packet is admitted under
// MaxSessions = 1.
func TestUnsolicitedCreateFailureRollsBackReservation(t *testing.T) {
	t.Parallel()

	meta := admissionMeta("192.0.2.10", "192.0.2.1")
	tests := []struct {
		name string
		// failing replaces the working sender; lease is the test lease.
		failing func(lease *SenderLease) sessionSender
		// policy breaks the session defaults instead.
		policy func(*UnsolicitedPolicy)
		// existing creates a configured session with other parameters on the
		// same key first.
		existing   bool
		wantCloses int
	}{
		{name: "sender error with a lease", wantCloses: 1, failing: func(lease *SenderLease) sessionSender {
			return func(PacketMeta) (*SenderLease, error) { return lease, errInjectedSender }
		}},
		{name: "sender error without a lease", failing: func(*SenderLease) sessionSender {
			return func(PacketMeta) (*SenderLease, error) { return nil, errInjectedSender }
		}},
		{name: "nil lease", failing: func(*SenderLease) sessionSender {
			return func(PacketMeta) (*SenderLease, error) { return nil, nil }
		}},
		{name: "lease without a sender", wantCloses: 1, failing: func(lease *SenderLease) sessionSender {
			return func(PacketMeta) (*SenderLease, error) { return NewSenderLease(nil, lease.Close), nil }
		}},
		{name: "session build failure", wantCloses: 1, policy: func(p *UnsolicitedPolicy) {
			p.SessionDefaults.DetectMultiplier = 0
		}},
		{name: "configured session with other parameters", existing: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			closes := 0
			newLease := func() *SenderLease {
				return NewSenderLease(senderLeaseTestSender{}, func() error {
					closes++
					return nil
				})
			}
			// A nil failing sender is the working one.
			var failing sessionSender
			if tt.failing != nil {
				failing = tt.failing(newLease())
			}
			policy := admissionPolicy(1)
			if tt.policy != nil {
				tt.policy(policy)
			}
			mgr := NewManager(
				slog.New(slog.DiscardHandler),
				WithUnsolicitedPolicy(policy),
				WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
				WithUnsolicitedSessionSender(func(m PacketMeta) (*SenderLease, error) {
					if failing != nil {
						return failing(m)
					}
					return newLease(), nil
				}),
			)
			t.Cleanup(mgr.Close)

			wantSessions := 0
			if tt.existing {
				cfg := senderLeaseTestConfig(meta.SrcAddr.String())
				cfg.LocalAddr, cfg.Interface = meta.DstAddr, meta.IfName
				if _, err := mgr.CreateSession(context.Background(), cfg, ownershipSenderLeaseFactory()); err != nil {
					t.Fatalf("CreateSession: %v", err)
				}
				wantSessions = 1
			}

			if _, err := mgr.tryCreateUnsolicited(ownershipDownPacket(61), meta, nil); err == nil {
				t.Fatal("tryCreateUnsolicited succeeded, want the injected failure")
			}
			if got := mgr.unsolicited.sessionCount.Load(); got != 0 {
				t.Fatalf("reserved quota = %d, want 0", got)
			}
			if got := len(mgr.Sessions()); got != wantSessions {
				t.Fatalf("sessions = %d, want %d", got, wantSessions)
			}
			mgr.discriminators.mu.Lock()
			allocated := len(mgr.discriminators.allocated)
			mgr.discriminators.mu.Unlock()
			if allocated != wantSessions {
				t.Fatalf("allocated discriminators = %d, want %d", allocated, wantSessions)
			}
			if closes != tt.wantCloses {
				t.Fatalf("lease closes = %d, want %d", closes, tt.wantCloses)
			}
			if tt.existing {
				discr := mgr.Sessions()[0].LocalDiscr
				mgr.mu.RLock()
				entry := mgr.sessions[discr]
				_, claimed := entry.owners[unsolicitedSessionOwner()]
				owners, marked := len(entry.owners), entry.unsolicited
				mgr.mu.RUnlock()
				if claimed || marked || owners != 1 {
					t.Fatalf("configured session after rollback: owners=%d unsolicited claim=%t marked=%t, want 1, no claim, unmarked",
						owners, claimed, marked)
				}
			}

			if tt.failing == nil {
				return // the fault is in the policy or the key, not the sender
			}
			failing = nil
			other := meta
			other.SrcAddr = netip.MustParseAddr("192.0.2.11")
			if _, err := mgr.tryCreateUnsolicited(ownershipDownPacket(62), other, nil); err != nil {
				t.Fatalf("tryCreateUnsolicited after rollback: %v", err)
			}
		})
	}
}
