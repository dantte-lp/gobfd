package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

var errSenderLeaseTestFactory = errors.New("sender lease test factory failure")

type senderLeaseTestSender struct{}

func (senderLeaseTestSender) SendPacket(context.Context, []byte, netip.Addr) error {
	return nil
}

func TestManagerUnchangedReconcileDoesNotOpenSenderLease(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	var opens int
	desired := []ReconcileConfig{{
		Key: "config",
		SessionConfig: SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Interface:             "eth0",
			Type:                  SessionTypeSingleHop,
			Role:                  RoleActive,
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
		SenderLeaseFactory: func() (*SenderLease, error) {
			opens++
			return NewSenderLease(senderLeaseTestSender{}, nil), nil
		},
	}}

	if _, _, err := mgr.ReconcileSessions(context.Background(), desired); err != nil {
		t.Fatalf("initial ReconcileSessions: %v", err)
	}
	if opens != 1 {
		t.Fatalf("sender lease opens after initial reconcile = %d, want 1", opens)
	}

	if _, _, err := mgr.ReconcileSessions(context.Background(), desired); err != nil {
		t.Fatalf("unchanged ReconcileSessions: %v", err)
	}
	if opens != 1 {
		t.Fatalf("sender lease opens after unchanged reconcile = %d, want 1", opens)
	}
}

func TestManagerCrossSourceShareDoesNotOpenAdditionalSenderLease(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	cfg := senderLeaseTestConfig("192.0.2.10")
	var configOpens, microOpens int
	configDesired := []ReconcileConfig{{
		Key:           "config",
		SessionConfig: cfg,
		SenderLeaseFactory: func() (*SenderLease, error) {
			configOpens++
			return NewSenderLease(senderLeaseTestSender{}, nil), nil
		},
	}}
	microDesired := []ReconcileConfig{{
		Key:           "micro",
		SessionConfig: cfg,
		SenderLeaseFactory: func() (*SenderLease, error) {
			microOpens++
			return NewSenderLease(senderLeaseTestSender{}, nil), nil
		},
	}}

	if _, _, err := mgr.ReconcileSessionsForOwner(
		context.Background(), ConfigReconciliationOwner(), configDesired,
	); err != nil {
		t.Fatalf("config reconcile: %v", err)
	}
	if _, _, err := mgr.ReconcileSessionsForOwner(
		context.Background(), MicroBFDReconciliationOwner(), microDesired,
	); err != nil {
		t.Fatalf("micro reconcile: %v", err)
	}
	if configOpens != 1 || microOpens != 0 {
		t.Fatalf("sender lease opens = config %d, micro %d; want 1, 0", configOpens, microOpens)
	}
}

func TestManagerFailedSenderLeaseAcquisitionClosesPartialLeaseOnce(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	var closes int
	desired := []ReconcileConfig{{
		Key:           "config",
		SessionConfig: senderLeaseTestConfig("192.0.2.11"),
		SenderLeaseFactory: func() (*SenderLease, error) {
			return NewSenderLease(senderLeaseTestSender{}, func() error {
				closes++
				return nil
			}), errSenderLeaseTestFactory
		},
	}}

	if _, _, err := mgr.ReconcileSessions(context.Background(), desired); !errors.Is(
		err, errSenderLeaseTestFactory,
	) {
		t.Fatalf("ReconcileSessions error = %v, want %v", err, errSenderLeaseTestFactory)
	}
	if closes != 1 {
		t.Fatalf("partial sender lease closes = %d, want 1", closes)
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Fatalf("sessions after failed sender factory = %d, want 0", got)
	}
}

func TestManagerSessionConstructionFailureClosesSenderLeaseOnce(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	cfg := senderLeaseTestConfig("192.0.2.111")
	cfg.DetectMultiplier = 0
	var closes int
	_, err := mgr.CreateSession(
		context.Background(), cfg,
		func() (*SenderLease, error) {
			return NewSenderLease(senderLeaseTestSender{}, func() error {
				closes++
				return nil
			}), nil
		},
	)
	if !errors.Is(err, ErrInvalidDetectMult) {
		t.Fatalf("CreateSession error = %v, want %v", err, ErrInvalidDetectMult)
	}
	if closes != 1 {
		t.Fatalf("sender lease closes after session construction failure = %d, want 1", closes)
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Fatalf("sessions after construction failure = %d, want 0", got)
	}
}

func TestManagerSenderLeaseClosesOnlyAfterLastClaimRelease(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	cfg := senderLeaseTestConfig("192.0.2.12")
	var closes int
	desired := []ReconcileConfig{{
		Key:           "config",
		SessionConfig: cfg,
		SenderLeaseFactory: func() (*SenderLease, error) {
			return NewSenderLease(senderLeaseTestSender{}, func() error {
				closes++
				return nil
			}), nil
		},
	}}
	if _, _, err := mgr.ReconcileSessions(context.Background(), desired); err != nil {
		t.Fatalf("config reconcile: %v", err)
	}
	sess, err := mgr.CreateSession(
		context.Background(), cfg,
		func() (*SenderLease, error) {
			t.Fatal("shared API claim opened an additional sender lease")
			return nil, errSenderLeaseTestFactory
		},
	)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := mgr.DestroySession(context.Background(), sess.LocalDiscriminator()); err != nil {
		t.Fatalf("DestroySession non-last claim: %v", err)
	}
	if closes != 0 {
		t.Fatalf("sender lease closes after non-last release = %d, want 0", closes)
	}

	if _, _, err := mgr.ReconcileSessions(context.Background(), nil); err != nil {
		t.Fatalf("release last config claim: %v", err)
	}
	if closes != 1 {
		t.Fatalf("sender lease closes after last release = %d, want 1", closes)
	}
}

func TestManagerDestroySessionDoesNotDoubleCloseSenderLease(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))

	var closes int
	sess, err := mgr.CreateSession(
		context.Background(), senderLeaseTestConfig("192.0.2.13"),
		func() (*SenderLease, error) {
			return NewSenderLease(senderLeaseTestSender{}, func() error {
				closes++
				return nil
			}), nil
		},
	)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := mgr.DestroySession(context.Background(), sess.LocalDiscriminator()); err != nil {
		t.Fatalf("DestroySession: %v", err)
	}
	if err := mgr.DestroySession(context.Background(), sess.LocalDiscriminator()); !errors.Is(
		err, ErrSessionNotFound,
	) {
		t.Fatalf("second DestroySession error = %v, want %v", err, ErrSessionNotFound)
	}
	mgr.Close()
	if closes != 1 {
		t.Fatalf("sender lease closes after delete and Manager.Close = %d, want 1", closes)
	}
}

func TestManagerCloseReleasesEveryAcceptedSenderLeaseOnce(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	closes := make([]int, 2)
	desired := make([]ReconcileConfig, 0, len(closes))
	for i := range closes {
		index := i
		desired = append(desired, ReconcileConfig{
			Key:           "config",
			SessionConfig: senderLeaseTestConfig(netip.AddrFrom4([4]byte{192, 0, 2, byte(20 + i)}).String()),
			SenderLeaseFactory: func() (*SenderLease, error) {
				return NewSenderLease(senderLeaseTestSender{}, func() error {
					closes[index]++
					return nil
				}), nil
			},
		})
	}
	if _, _, err := mgr.ReconcileSessions(context.Background(), desired); err != nil {
		t.Fatalf("ReconcileSessions: %v", err)
	}

	mgr.Close()
	mgr.Close()
	for i, got := range closes {
		if got != 1 {
			t.Errorf("sender lease %d closes = %d, want 1", i, got)
		}
	}
}

func TestUnsolicitedLastClaimCleanupPreservesSharedSenderLease(t *testing.T) {
	policy := &UnsolicitedPolicy{
		Enabled:     true,
		MaxSessions: 1,
		Interfaces: map[string]UnsolicitedInterfaceConfig{
			"eth0": {Enabled: true},
		},
		SessionDefaults: UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
	var closes int
	sharedLease := NewSenderLease(senderLeaseTestSender{}, func() error {
		closes++
		return nil
	})
	mgr := NewManager(
		slog.New(slog.DiscardHandler),
		WithUnsolicitedPolicy(policy),
		WithUnsolicitedSenderLease(sharedLease),
	)

	meta := PacketMeta{
		SrcAddr: netip.MustParseAddr("192.0.2.30"),
		DstAddr: netip.MustParseAddr("192.0.2.254"),
		IfName:  "eth0",
		TTL:     255,
	}
	if err := mgr.tryCreateUnsolicited(ownershipDownPacket(301), meta, nil); err != nil {
		t.Fatalf("tryCreateUnsolicited: %v", err)
	}
	snapshots := mgr.Sessions()
	if len(snapshots) != 1 {
		t.Fatalf("unsolicited sessions = %d, want 1", len(snapshots))
	}
	discr := snapshots[0].LocalDiscr
	mgr.mu.RLock()
	mgr.sessions[discr].session.state.Store(uint32(StateDown))
	mgr.mu.RUnlock()

	mgr.cleanupUnsolicitedSession(context.Background(), discr)
	if closes != 0 {
		t.Fatalf("shared unsolicited sender closes during session cleanup = %d, want 0", closes)
	}
	mgr.Close()
	if closes != 1 {
		t.Fatalf("shared unsolicited sender closes after Manager.Close = %d, want 1", closes)
	}
}

func senderLeaseTestConfig(peer string) SessionConfig {
	return SessionConfig{
		PeerAddr:              netip.MustParseAddr(peer),
		LocalAddr:             netip.MustParseAddr("192.0.2.254"),
		Interface:             "eth0",
		Type:                  SessionTypeSingleHop,
		Role:                  RoleActive,
		DesiredMinTxInterval:  time.Second,
		RequiredMinRxInterval: time.Second,
		DetectMultiplier:      3,
	}
}
