package bfd_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// Test Helpers — Manager
// -------------------------------------------------------------------------

// noopSender is a PacketSender that discards all packets.
type noopSender struct{}

func (noopSender) SendPacket(_ context.Context, _ []byte, _ netip.Addr) error {
	return nil
}

// defaultManagerConfig returns a valid SessionConfig for manager tests.
func defaultManagerConfig() bfd.SessionConfig {
	return bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.1"),
		LocalAddr:             netip.MustParseAddr("192.0.2.2"),
		Interface:             "eth0",
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  time.Second,
		RequiredMinRxInterval: time.Second,
		DetectMultiplier:      3,
	}
}

// newTestManager creates a Manager with a default logger for testing.
func newTestManager(t *testing.T) *bfd.Manager {
	t.Helper()
	logger := slog.Default()
	return bfd.NewManager(logger)
}

// -------------------------------------------------------------------------
// TestManagerCreateSession
// -------------------------------------------------------------------------

// TestManagerCreateSession verifies that CreateSession allocates a
// discriminator, registers the session in both lookup maps, and starts
// the session goroutine.
func TestManagerCreateSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultManagerConfig()
		sess, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		// Session should have a nonzero discriminator.
		if sess.LocalDiscriminator() == 0 {
			t.Error("session local discriminator is zero")
		}

		// Lookup by discriminator should succeed.
		found, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator())
		if !ok {
			t.Fatal("LookupByDiscriminator: not found")
		}
		if found != sess {
			t.Error("LookupByDiscriminator returned different session")
		}

		// Session state should be Down (RFC 5880 Section 6.8.1).
		if sess.State() != bfd.StateDown {
			t.Errorf("initial state = %s, want Down", sess.State())
		}

		// Allow goroutines to settle.
		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestManagerCreateSessionValidation
// -------------------------------------------------------------------------

// TestManagerCreateSessionValidation verifies that invalid configurations
// are rejected with appropriate errors.
func TestManagerCreateSessionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     bfd.SessionConfig
		wantErr string
	}{
		{
			name: "zero detect multiplier",
			cfg: bfd.SessionConfig{
				PeerAddr:              netip.MustParseAddr("192.0.2.1"),
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  bfd.SessionTypeSingleHop,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      0,
			},
			wantErr: "detect multiplier",
		},
		{
			name: "zero TX interval",
			cfg: bfd.SessionConfig{
				PeerAddr:              netip.MustParseAddr("192.0.2.1"),
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  bfd.SessionTypeSingleHop,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  0,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
			wantErr: "desired min TX interval",
		},
		{
			name: "invalid session type",
			cfg: bfd.SessionConfig{
				PeerAddr:              netip.MustParseAddr("192.0.2.1"),
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  0,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
			wantErr: "session type",
		},
		{
			name: "invalid peer addr",
			cfg: bfd.SessionConfig{
				PeerAddr:              netip.Addr{}, // zero value, invalid
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  bfd.SessionTypeSingleHop,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
			wantErr: "peer address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mgr := newTestManager(t)
			defer mgr.Close()

			_, err := mgr.CreateSession(context.Background(), tt.cfg, noopSender{})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if got := err.Error(); !containsSubstring(got, tt.wantErr) {
				t.Errorf("error %q does not contain %q", got, tt.wantErr)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestManagerCreateSessionDuplicate
// -------------------------------------------------------------------------

// TestManagerCreateSessionDuplicate verifies that creating a second session
// with the same peer key is rejected with ErrDuplicateSession.
func TestManagerCreateSessionDuplicate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultManagerConfig()

		_, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("first CreateSession: %v", err)
		}

		// Second creation with same peer key should fail.
		_, err = mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err == nil {
			t.Fatal("expected ErrDuplicateSession, got nil")
		}
		if !errors.Is(err, bfd.ErrDuplicateSession) {
			t.Errorf("error = %v, want ErrDuplicateSession", err)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestManagerDestroySession
// -------------------------------------------------------------------------

// TestManagerDestroySession verifies that destroying a session removes it
// from both lookup maps and cancels the session goroutine.
func TestManagerDestroySession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultManagerConfig()
		sess, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		discr := sess.LocalDiscriminator()

		// Destroy the session.
		if err := mgr.DestroySession(context.Background(), discr); err != nil {
			t.Fatalf("DestroySession: %v", err)
		}

		// Lookup by discriminator should fail.
		if _, ok := mgr.LookupByDiscriminator(discr); ok {
			t.Error("session still found by discriminator after destroy")
		}

		// Sessions snapshot should be empty.
		if snapshots := mgr.Sessions(); len(snapshots) != 0 {
			t.Errorf("expected 0 sessions, got %d", len(snapshots))
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestManagerDestroySessionNotFound
// -------------------------------------------------------------------------

// TestManagerDestroySessionNotFound verifies that destroying a nonexistent
// session returns ErrSessionNotFound.
func TestManagerDestroySessionNotFound(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	err := mgr.DestroySession(context.Background(), 99999)
	if err == nil {
		t.Fatal("expected ErrSessionNotFound, got nil")
	}
	if !errors.Is(err, bfd.ErrSessionNotFound) {
		t.Errorf("error = %v, want ErrSessionNotFound", err)
	}
}

// -------------------------------------------------------------------------
// TestManagerDemuxByDiscriminator
// -------------------------------------------------------------------------

// TestManagerDemuxByDiscriminator verifies that packets with YourDiscriminator
// != 0 are routed to the correct session via the primary discriminator map
// (RFC 5880 Section 6.8.6 tier 1).
func TestManagerDemuxByDiscriminator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultManagerConfig()
		sess, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		// Build a packet targeting this session's discriminator.
		pkt := &bfd.ControlPacket{
			Version:               bfd.Version,
			State:                 bfd.StateDown,
			DetectMult:            3,
			MyDiscriminator:       42,
			YourDiscriminator:     sess.LocalDiscriminator(),
			DesiredMinTxInterval:  1000000,
			RequiredMinRxInterval: 1000000,
		}

		meta := bfd.PacketMeta{
			SrcAddr: netip.MustParseAddr("192.0.2.1"),
			DstAddr: netip.MustParseAddr("192.0.2.2"),
			TTL:     255,
			IfName:  "eth0",
		}

		if err := mgr.Demux(pkt, meta); err != nil {
			t.Fatalf("Demux: %v", err)
		}

		// The session should transition from Down to Init after processing
		// the Down packet (RFC 5880 Section 6.8.6: Down + RecvDown -> Init).
		time.Sleep(50 * time.Millisecond)

		if sess.State() != bfd.StateInit {
			t.Errorf("state = %s, want Init", sess.State())
		}
	})
}

// -------------------------------------------------------------------------
// TestManagerDemuxByPeerKey
// -------------------------------------------------------------------------

// TestManagerDemuxByPeerKey verifies that packets with YourDiscriminator == 0
// are routed by peer key (source IP, dest IP, interface) using the secondary
// lookup map (RFC 5880 Section 6.8.6 tier 2).
func TestManagerDemuxByPeerKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultManagerConfig()
		sess, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		// Build a packet with YourDiscriminator == 0 (initial contact).
		// RFC 5880 Section 6.8.6: Your Discriminator may be zero only when
		// State is Down or AdminDown.
		pkt := &bfd.ControlPacket{
			Version:               bfd.Version,
			State:                 bfd.StateDown,
			DetectMult:            3,
			MyDiscriminator:       42,
			YourDiscriminator:     0,
			DesiredMinTxInterval:  1000000,
			RequiredMinRxInterval: 1000000,
		}

		// Meta must match the session's peer key.
		meta := bfd.PacketMeta{
			SrcAddr: netip.MustParseAddr("192.0.2.1"), // peer addr
			DstAddr: netip.MustParseAddr("192.0.2.2"), // local addr
			TTL:     255,
			IfName:  "eth0",
		}

		if err := mgr.Demux(pkt, meta); err != nil {
			t.Fatalf("Demux: %v", err)
		}

		// Session should transition Down -> Init.
		time.Sleep(50 * time.Millisecond)

		if sess.State() != bfd.StateInit {
			t.Errorf("state = %s, want Init", sess.State())
		}
	})
}

// -------------------------------------------------------------------------
// TestManagerDemuxNoMatch
// -------------------------------------------------------------------------

// TestManagerDemuxNoMatch verifies that packets with no matching session
// return ErrDemuxNoMatch.
func TestManagerDemuxNoMatch(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	t.Cleanup(mgr.Close)

	tests := []struct {
		name string
		pkt  *bfd.ControlPacket
		meta bfd.PacketMeta
	}{
		{
			name: "nonexistent discriminator",
			pkt: &bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateDown,
				DetectMult:            3,
				MyDiscriminator:       42,
				YourDiscriminator:     99999, // no session with this discr
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
			},
			meta: bfd.PacketMeta{
				SrcAddr: netip.MustParseAddr("192.0.2.1"),
				DstAddr: netip.MustParseAddr("192.0.2.2"),
				TTL:     255,
			},
		},
		{
			name: "no peer key match",
			pkt: &bfd.ControlPacket{
				Version:               bfd.Version,
				State:                 bfd.StateDown,
				DetectMult:            3,
				MyDiscriminator:       42,
				YourDiscriminator:     0,
				DesiredMinTxInterval:  1000000,
				RequiredMinRxInterval: 1000000,
			},
			meta: bfd.PacketMeta{
				SrcAddr: netip.MustParseAddr("10.0.0.1"), // no session for this peer
				DstAddr: netip.MustParseAddr("10.0.0.2"),
				TTL:     255,
				IfName:  "eth0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mgr.Demux(tt.pkt, tt.meta)
			if err == nil {
				t.Fatal("expected ErrDemuxNoMatch, got nil")
			}
			if !errors.Is(err, bfd.ErrDemuxNoMatch) {
				t.Errorf("error = %v, want ErrDemuxNoMatch", err)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestManagerSessions
// -------------------------------------------------------------------------

// TestManagerSessions verifies that Sessions() returns a snapshot of all
// active sessions with correct field values.
func TestManagerSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		// Create two sessions with different configs.
		cfg1 := bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Interface:             "eth0",
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		}
		cfg2 := bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("198.51.100.1"),
			LocalAddr:             netip.MustParseAddr("198.51.100.2"),
			Interface:             "eth1",
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RolePassive,
			DesiredMinTxInterval:  500 * time.Millisecond,
			RequiredMinRxInterval: 500 * time.Millisecond,
			DetectMultiplier:      5,
		}

		sess1, err := mgr.CreateSession(context.Background(), cfg1, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession 1: %v", err)
		}
		sess2, err := mgr.CreateSession(context.Background(), cfg2, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession 2: %v", err)
		}

		snapshots := mgr.Sessions()
		if len(snapshots) != 2 {
			t.Fatalf("expected 2 sessions, got %d", len(snapshots))
		}

		// Build a map of snapshots by discriminator for order-independent checks.
		byDiscr := make(map[uint32]bfd.SessionSnapshot, len(snapshots))
		for _, snap := range snapshots {
			byDiscr[snap.LocalDiscr] = snap
		}

		// Verify session 1 snapshot.
		snap1, ok := byDiscr[sess1.LocalDiscriminator()]
		if !ok {
			t.Fatal("session 1 not found in snapshots")
		}
		if snap1.PeerAddr != cfg1.PeerAddr {
			t.Errorf("snap1.PeerAddr = %s, want %s", snap1.PeerAddr, cfg1.PeerAddr)
		}
		if snap1.LocalAddr != cfg1.LocalAddr {
			t.Errorf("snap1.LocalAddr = %s, want %s", snap1.LocalAddr, cfg1.LocalAddr)
		}
		if snap1.Interface != cfg1.Interface {
			t.Errorf("snap1.Interface = %s, want %s", snap1.Interface, cfg1.Interface)
		}
		if snap1.Type != cfg1.Type {
			t.Errorf("snap1.Type = %s, want %s", snap1.Type, cfg1.Type)
		}
		if snap1.State != bfd.StateDown {
			t.Errorf("snap1.State = %s, want Down", snap1.State)
		}
		if snap1.DetectMultiplier != cfg1.DetectMultiplier {
			t.Errorf("snap1.DetectMultiplier = %d, want %d", snap1.DetectMultiplier, cfg1.DetectMultiplier)
		}
		if snap1.DesiredMinTx != cfg1.DesiredMinTxInterval {
			t.Errorf("snap1.DesiredMinTx = %v, want %v", snap1.DesiredMinTx, cfg1.DesiredMinTxInterval)
		}

		// Verify session 2 snapshot.
		snap2, ok := byDiscr[sess2.LocalDiscriminator()]
		if !ok {
			t.Fatal("session 2 not found in snapshots")
		}
		if snap2.PeerAddr != cfg2.PeerAddr {
			t.Errorf("snap2.PeerAddr = %s, want %s", snap2.PeerAddr, cfg2.PeerAddr)
		}
		if snap2.DetectMultiplier != cfg2.DetectMultiplier {
			t.Errorf("snap2.DetectMultiplier = %d, want %d", snap2.DetectMultiplier, cfg2.DetectMultiplier)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestManagerStateChanges
// -------------------------------------------------------------------------

// TestManagerStateChanges verifies that state changes from sessions propagate
// to the manager's aggregated StateChanges channel via RunDispatch.
func TestManagerStateChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		// RunDispatch must be running to forward notifications from the
		// internal rawNotifyCh to the public StateChanges channel.
		dispatchCtx, dispatchCancel := context.WithCancel(context.Background())
		defer dispatchCancel()
		go mgr.RunDispatch(dispatchCtx)

		cfg := defaultManagerConfig()
		sess, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		// Wait for session goroutine to start and fire at least one TX.
		time.Sleep(2 * time.Second)

		// Inject a Down packet to trigger Down -> Init transition.
		pkt := &bfd.ControlPacket{
			Version:               bfd.Version,
			State:                 bfd.StateDown,
			DetectMult:            3,
			MyDiscriminator:       42,
			YourDiscriminator:     sess.LocalDiscriminator(),
			DesiredMinTxInterval:  1000000,
			RequiredMinRxInterval: 1000000,
		}
		sess.RecvPacket(pkt)
		time.Sleep(50 * time.Millisecond)

		// Read state change from the manager's channel.
		ch := mgr.StateChanges()
		var found bool

		for range len(ch) {
			sc := <-ch
			if sc.NewState == bfd.StateInit && sc.LocalDiscr == sess.LocalDiscriminator() {
				found = true
				if sc.OldState != bfd.StateDown {
					t.Errorf("OldState = %s, want Down", sc.OldState)
				}
				break
			}
		}

		if !found {
			t.Error("did not receive Init state change on StateChanges channel")
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestManagerReconcileSessions — Sprint 14 (14.4)
// -------------------------------------------------------------------------

// TestManagerReconcileSessionsCreatesNew verifies that ReconcileSessions
// creates sessions that are in the desired set but not yet active.
func TestManagerReconcileSessionsCreatesNew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		desired := []bfd.ReconcileConfig{
			{
				Key: "192.0.2.1|192.0.2.2|eth0",
				SessionConfig: bfd.SessionConfig{
					PeerAddr:              netip.MustParseAddr("192.0.2.1"),
					LocalAddr:             netip.MustParseAddr("192.0.2.2"),
					Interface:             "eth0",
					Type:                  bfd.SessionTypeSingleHop,
					Role:                  bfd.RoleActive,
					DesiredMinTxInterval:  time.Second,
					RequiredMinRxInterval: time.Second,
					DetectMultiplier:      3,
				},
				Sender: noopSender{},
			},
		}

		created, destroyed, err := mgr.ReconcileSessions(context.Background(), desired)
		if err != nil {
			t.Fatalf("ReconcileSessions: %v", err)
		}
		if created != 1 {
			t.Errorf("created = %d, want 1", created)
		}
		if destroyed != 0 {
			t.Errorf("destroyed = %d, want 0", destroyed)
		}

		// Verify session exists.
		snapshots := mgr.Sessions()
		if len(snapshots) != 1 {
			t.Fatalf("expected 1 session, got %d", len(snapshots))
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerReconcileSessionsDestroysStale verifies that ReconcileSessions
// destroys sessions not present in the desired set.
func TestManagerReconcileSessionsDestroysStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultManagerConfig()
		_, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		// Reconcile with empty desired set: existing session should be destroyed.
		created, destroyed, reconcileErr := mgr.ReconcileSessions(
			context.Background(), nil,
		)
		if reconcileErr != nil {
			t.Fatalf("ReconcileSessions: %v", reconcileErr)
		}
		if created != 0 {
			t.Errorf("created = %d, want 0", created)
		}
		if destroyed != 1 {
			t.Errorf("destroyed = %d, want 1", destroyed)
		}

		if len(mgr.Sessions()) != 0 {
			t.Error("expected 0 sessions after reconciliation")
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerReconcileSessionsKeepsExisting verifies that reconciliation
// does not destroy sessions that exist in both current and desired sets.
func TestManagerReconcileSessionsKeepsExisting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultManagerConfig()
		sess, err := mgr.CreateSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		desired := []bfd.ReconcileConfig{
			{
				Key:           "192.0.2.1|192.0.2.2|eth0",
				SessionConfig: cfg,
				Sender:        noopSender{},
			},
		}

		created, destroyed, reconcileErr := mgr.ReconcileSessions(
			context.Background(), desired,
		)
		if reconcileErr != nil {
			t.Fatalf("ReconcileSessions: %v", reconcileErr)
		}
		if created != 0 {
			t.Errorf("created = %d, want 0 (existing kept)", created)
		}
		if destroyed != 0 {
			t.Errorf("destroyed = %d, want 0 (existing kept)", destroyed)
		}

		// Original session should still exist.
		found, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator())
		if !ok {
			t.Fatal("original session not found after reconciliation")
		}
		if found != sess {
			t.Error("session pointer changed after reconciliation")
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestManagerDrainAllSessions — Sprint 14 (14.10)
// -------------------------------------------------------------------------

// TestManagerDrainAllSessions verifies that DrainAllSessions transitions
// all sessions to AdminDown with DiagAdminDown.
func TestManagerDrainAllSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg1 := defaultManagerConfig()
		cfg2 := bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("198.51.100.1"),
			LocalAddr:             netip.MustParseAddr("198.51.100.2"),
			Interface:             "eth1",
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		}

		sess1, err := mgr.CreateSession(context.Background(), cfg1, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession 1: %v", err)
		}
		sess2, err := mgr.CreateSession(context.Background(), cfg2, noopSender{})
		if err != nil {
			t.Fatalf("CreateSession 2: %v", err)
		}

		// Drain all sessions.
		mgr.DrainAllSessions()

		// Both sessions should be AdminDown.
		if sess1.State() != bfd.StateAdminDown {
			t.Errorf("sess1.State() = %s, want AdminDown", sess1.State())
		}
		if sess2.State() != bfd.StateAdminDown {
			t.Errorf("sess2.State() = %s, want AdminDown", sess2.State())
		}

		// Both sessions should have DiagAdminDown.
		if sess1.LocalDiag() != bfd.DiagAdminDown {
			t.Errorf("sess1.LocalDiag() = %s, want AdminDown", sess1.LocalDiag())
		}
		if sess2.LocalDiag() != bfd.DiagAdminDown {
			t.Errorf("sess2.LocalDiag() = %s, want AdminDown", sess2.LocalDiag())
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// Echo Session Manager Tests — RFC 9747
// -------------------------------------------------------------------------

// defaultEchoConfig returns a valid EchoSessionConfig for manager tests.
func defaultEchoConfig() bfd.EchoSessionConfig {
	return bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		Interface:        "eth0",
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}
}

// TestManagerCreateEchoSession verifies that CreateEchoSession allocates a
// discriminator, stores the session, and starts the goroutine.
func TestManagerCreateEchoSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultEchoConfig()
		discr, err := mgr.CreateEchoSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateEchoSession: %v", err)
		}
		if discr == 0 {
			t.Error("echo session discriminator is zero")
		}

		// Echo session should appear in the snapshot.
		snapshots := mgr.EchoSessions()
		if len(snapshots) != 1 {
			t.Fatalf("expected 1 echo session, got %d", len(snapshots))
		}
		if snapshots[0].LocalDiscr != discr {
			t.Errorf("snapshot discr = %d, want %d", snapshots[0].LocalDiscr, discr)
		}
		if snapshots[0].PeerAddr != cfg.PeerAddr {
			t.Errorf("snapshot peer = %s, want %s", snapshots[0].PeerAddr, cfg.PeerAddr)
		}
		if snapshots[0].State != bfd.StateDown {
			t.Errorf("snapshot state = %s, want Down", snapshots[0].State)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerCreateEchoSessionInvalidPeer verifies that creating an echo
// session with an invalid peer address returns an error.
func TestManagerCreateEchoSessionInvalidPeer(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.Addr{}, // invalid
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}

	_, err := mgr.CreateEchoSession(context.Background(), cfg, noopSender{})
	if err == nil {
		t.Fatal("expected error for invalid peer addr, got nil")
	}
}

// TestManagerDestroyEchoSession verifies that destroying an echo session
// removes it from the map and cancels the goroutine.
func TestManagerDestroyEchoSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultEchoConfig()
		discr, err := mgr.CreateEchoSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateEchoSession: %v", err)
		}

		if err := mgr.DestroyEchoSession(discr); err != nil {
			t.Fatalf("DestroyEchoSession: %v", err)
		}

		snapshots := mgr.EchoSessions()
		if len(snapshots) != 0 {
			t.Errorf("expected 0 echo sessions after destroy, got %d", len(snapshots))
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerDestroyEchoSessionNotFound verifies that destroying a
// nonexistent echo session returns ErrEchoSessionNotFound.
func TestManagerDestroyEchoSessionNotFound(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	err := mgr.DestroyEchoSession(99999)
	if err == nil {
		t.Fatal("expected ErrEchoSessionNotFound, got nil")
	}
	if !errors.Is(err, bfd.ErrEchoSessionNotFound) {
		t.Errorf("error = %v, want ErrEchoSessionNotFound", err)
	}
}

// TestManagerDemuxEcho verifies that DemuxEcho routes a returned echo
// packet to the correct echo session by MyDiscriminator.
func TestManagerDemuxEcho(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultEchoConfig()
		discr, err := mgr.CreateEchoSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateEchoSession: %v", err)
		}

		// DemuxEcho with the echo session's discriminator should succeed.
		if err := mgr.DemuxEcho(discr); err != nil {
			t.Fatalf("DemuxEcho: %v", err)
		}

		// Let the echo recv signal be processed.
		time.Sleep(50 * time.Millisecond)

		// After receiving an echo return, the session should transition
		// Down -> Up (RFC 9747 Section 3.3).
		snapshots := mgr.EchoSessions()
		if len(snapshots) != 1 {
			t.Fatalf("expected 1 echo session, got %d", len(snapshots))
		}
		if snapshots[0].State != bfd.StateUp {
			t.Errorf("echo session state = %s, want Up", snapshots[0].State)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerDemuxEchoNoMatch verifies that DemuxEcho returns
// ErrEchoDemuxNoMatch for an unknown discriminator.
func TestManagerDemuxEchoNoMatch(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	err := mgr.DemuxEcho(99999)
	if err == nil {
		t.Fatal("expected ErrEchoDemuxNoMatch, got nil")
	}
	if !errors.Is(err, bfd.ErrEchoDemuxNoMatch) {
		t.Errorf("error = %v, want ErrEchoDemuxNoMatch", err)
	}
}

// TestManagerEchoSessionsSnapshot verifies that EchoSessions returns
// correct snapshot data for multiple echo sessions.
func TestManagerEchoSessionsSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg1 := bfd.EchoSessionConfig{
			PeerAddr:         netip.MustParseAddr("10.0.0.1"),
			LocalAddr:        netip.MustParseAddr("10.0.0.2"),
			TxInterval:       100 * time.Millisecond,
			DetectMultiplier: 3,
		}
		cfg2 := bfd.EchoSessionConfig{
			PeerAddr:         netip.MustParseAddr("10.0.0.3"),
			LocalAddr:        netip.MustParseAddr("10.0.0.4"),
			TxInterval:       200 * time.Millisecond,
			DetectMultiplier: 5,
		}

		discr1, err := mgr.CreateEchoSession(context.Background(), cfg1, noopSender{})
		if err != nil {
			t.Fatalf("CreateEchoSession 1: %v", err)
		}
		discr2, err := mgr.CreateEchoSession(context.Background(), cfg2, noopSender{})
		if err != nil {
			t.Fatalf("CreateEchoSession 2: %v", err)
		}

		snapshots := mgr.EchoSessions()
		if len(snapshots) != 2 {
			t.Fatalf("expected 2 echo sessions, got %d", len(snapshots))
		}

		byDiscr := make(map[uint32]bfd.EchoSessionSnapshot, len(snapshots))
		for _, snap := range snapshots {
			byDiscr[snap.LocalDiscr] = snap
		}

		snap1, ok := byDiscr[discr1]
		if !ok {
			t.Fatal("echo session 1 not found in snapshots")
		}
		if snap1.PeerAddr != cfg1.PeerAddr {
			t.Errorf("snap1.PeerAddr = %s, want %s", snap1.PeerAddr, cfg1.PeerAddr)
		}
		if snap1.TxInterval != cfg1.TxInterval {
			t.Errorf("snap1.TxInterval = %v, want %v", snap1.TxInterval, cfg1.TxInterval)
		}

		snap2, ok := byDiscr[discr2]
		if !ok {
			t.Fatal("echo session 2 not found in snapshots")
		}
		if snap2.PeerAddr != cfg2.PeerAddr {
			t.Errorf("snap2.PeerAddr = %s, want %s", snap2.PeerAddr, cfg2.PeerAddr)
		}
		if snap2.DetectMultiplier != cfg2.DetectMultiplier {
			t.Errorf("snap2.DetectMult = %d, want %d", snap2.DetectMultiplier, cfg2.DetectMultiplier)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerCloseEchoSessions verifies that Close() cancels echo session
// goroutines and clears the echo session map.
func TestManagerCloseEchoSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)

		cfg := defaultEchoConfig()
		_, err := mgr.CreateEchoSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateEchoSession: %v", err)
		}

		mgr.Close()

		snapshots := mgr.EchoSessions()
		if len(snapshots) != 0 {
			t.Errorf("expected 0 echo sessions after Close, got %d", len(snapshots))
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerReconcileEchoSessionsCreatesNew verifies that
// ReconcileEchoSessions creates echo sessions in the desired set
// but not yet active.
func TestManagerReconcileEchoSessionsCreatesNew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		desired := []bfd.EchoReconcileConfig{
			{
				Key: "echo|10.0.0.1|10.0.0.2|eth0",
				EchoSessionConfig: bfd.EchoSessionConfig{
					PeerAddr:         netip.MustParseAddr("10.0.0.1"),
					LocalAddr:        netip.MustParseAddr("10.0.0.2"),
					Interface:        "eth0",
					TxInterval:       100 * time.Millisecond,
					DetectMultiplier: 3,
				},
				Sender: noopSender{},
			},
		}

		created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), desired)
		if err != nil {
			t.Fatalf("ReconcileEchoSessions: %v", err)
		}
		if created != 1 {
			t.Errorf("created = %d, want 1", created)
		}
		if destroyed != 0 {
			t.Errorf("destroyed = %d, want 0", destroyed)
		}

		snapshots := mgr.EchoSessions()
		if len(snapshots) != 1 {
			t.Fatalf("expected 1 echo session, got %d", len(snapshots))
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestManagerReconcileEchoSessionsDestroysStale verifies that
// ReconcileEchoSessions destroys echo sessions not in the desired set.
func TestManagerReconcileEchoSessionsDestroysStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newTestManager(t)
		defer mgr.Close()

		cfg := defaultEchoConfig()
		_, err := mgr.CreateEchoSession(context.Background(), cfg, noopSender{})
		if err != nil {
			t.Fatalf("CreateEchoSession: %v", err)
		}

		// Reconcile with empty desired set: existing echo session should be destroyed.
		created, destroyed, reconcileErr := mgr.ReconcileEchoSessions(
			context.Background(), nil,
		)
		if reconcileErr != nil {
			t.Fatalf("ReconcileEchoSessions: %v", reconcileErr)
		}
		if created != 0 {
			t.Errorf("created = %d, want 0", created)
		}
		if destroyed != 1 {
			t.Errorf("destroyed = %d, want 1", destroyed)
		}

		if len(mgr.EchoSessions()) != 0 {
			t.Error("expected 0 echo sessions after reconciliation")
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// Micro-BFD Group Manager Tests — RFC 7130
// -------------------------------------------------------------------------

// testLAG is the default LAG interface name used across micro-BFD manager tests.
const testLAG = "bond0"

// defaultMicroBFDConfig returns a valid MicroBFDConfig for manager tests.
func defaultMicroBFDConfig() bfd.MicroBFDConfig {
	return bfd.MicroBFDConfig{
		LAGInterface:          testLAG,
		MemberLinks:           []string{"eth0", "eth1"},
		PeerAddr:              netip.MustParseAddr("10.0.0.1"),
		LocalAddr:             netip.MustParseAddr("10.0.0.2"),
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
		MinActiveLinks:        1,
	}
}

// TestManagerCreateMicroBFDGroup verifies that CreateMicroBFDGroup
// registers the group and it appears in the snapshot.
func TestManagerCreateMicroBFDGroup(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	cfg := defaultMicroBFDConfig()
	group, err := mgr.CreateMicroBFDGroup(cfg)
	if err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}

	if group.LAGInterface() != testLAG {
		t.Errorf("LAGInterface() = %q, want %q", group.LAGInterface(), testLAG)
	}

	// Group should appear in snapshots.
	snapshots := mgr.MicroBFDGroups()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 micro-BFD group, got %d", len(snapshots))
	}
	if snapshots[0].LAGInterface != testLAG {
		t.Errorf("snapshot LAG = %q, want %q", snapshots[0].LAGInterface, testLAG)
	}
}

// TestManagerCreateMicroBFDGroupDuplicate verifies that creating a second
// group for the same LAG returns ErrMicroBFDGroupExists.
func TestManagerCreateMicroBFDGroupDuplicate(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	cfg := defaultMicroBFDConfig()
	if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
		t.Fatalf("first CreateMicroBFDGroup: %v", err)
	}

	_, err := mgr.CreateMicroBFDGroup(cfg)
	if err == nil {
		t.Fatal("expected ErrMicroBFDGroupExists, got nil")
	}
	if !errors.Is(err, bfd.ErrMicroBFDGroupExists) {
		t.Errorf("error = %v, want ErrMicroBFDGroupExists", err)
	}
}

// TestManagerDestroyMicroBFDGroup verifies that destroying a group removes
// it from the Manager.
func TestManagerDestroyMicroBFDGroup(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	cfg := defaultMicroBFDConfig()
	if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}

	if err := mgr.DestroyMicroBFDGroup(testLAG); err != nil {
		t.Fatalf("DestroyMicroBFDGroup: %v", err)
	}

	snapshots := mgr.MicroBFDGroups()
	if len(snapshots) != 0 {
		t.Errorf("expected 0 groups after destroy, got %d", len(snapshots))
	}
}

// TestManagerDestroyMicroBFDGroupNotFound verifies that destroying a
// nonexistent group returns ErrMicroBFDGroupNotFound.
func TestManagerDestroyMicroBFDGroupNotFound(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	err := mgr.DestroyMicroBFDGroup("bond99")
	if err == nil {
		t.Fatal("expected ErrMicroBFDGroupNotFound, got nil")
	}
	if !errors.Is(err, bfd.ErrMicroBFDGroupNotFound) {
		t.Errorf("error = %v, want ErrMicroBFDGroupNotFound", err)
	}
}

// TestManagerLookupMicroBFDGroup verifies the lookup method.
func TestManagerLookupMicroBFDGroup(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	cfg := defaultMicroBFDConfig()
	created, err := mgr.CreateMicroBFDGroup(cfg)
	if err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}

	found, ok := mgr.LookupMicroBFDGroup(testLAG)
	if !ok {
		t.Fatal("LookupMicroBFDGroup: not found")
	}
	if found != created {
		t.Error("LookupMicroBFDGroup returned different group")
	}

	_, ok = mgr.LookupMicroBFDGroup("bond99")
	if ok {
		t.Error("LookupMicroBFDGroup should not find nonexistent group")
	}
}

// TestManagerReconcileMicroBFDGroups verifies reconciliation creates
// and destroys groups correctly.
func TestManagerReconcileMicroBFDGroups(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)
	defer mgr.Close()

	// Create an initial group.
	cfg1 := defaultMicroBFDConfig()
	if _, err := mgr.CreateMicroBFDGroup(cfg1); err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}

	// Reconcile: remove bond0, add bond1.
	cfg2 := bfd.MicroBFDConfig{
		LAGInterface:          "bond1",
		MemberLinks:           []string{"eth2", "eth3"},
		PeerAddr:              netip.MustParseAddr("10.0.0.3"),
		LocalAddr:             netip.MustParseAddr("10.0.0.4"),
		DesiredMinTxInterval:  200 * time.Millisecond,
		RequiredMinRxInterval: 200 * time.Millisecond,
		DetectMultiplier:      3,
		MinActiveLinks:        1,
	}

	desired := []bfd.MicroBFDReconcileConfig{
		{Key: "bond1", Config: cfg2},
	}

	created, destroyed, err := mgr.ReconcileMicroBFDGroups(desired)
	if err != nil {
		t.Fatalf("ReconcileMicroBFDGroups: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1", created)
	}
	if destroyed != 1 {
		t.Errorf("destroyed = %d, want 1", destroyed)
	}

	// Verify bond0 is gone and bond1 exists.
	snapshots := mgr.MicroBFDGroups()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 group, got %d", len(snapshots))
	}
	if snapshots[0].LAGInterface != "bond1" {
		t.Errorf("remaining group LAG = %q, want %q", snapshots[0].LAGInterface, "bond1")
	}
}

// TestManagerCloseCleansMicroBFDGroups verifies that Close clears
// micro-BFD groups.
func TestManagerCloseCleansMicroBFDGroups(t *testing.T) {
	t.Parallel()

	mgr := newTestManager(t)

	cfg := defaultMicroBFDConfig()
	if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
		t.Fatalf("CreateMicroBFDGroup: %v", err)
	}

	mgr.Close()

	snapshots := mgr.MicroBFDGroups()
	if len(snapshots) != 0 {
		t.Errorf("expected 0 groups after Close, got %d", len(snapshots))
	}
}

// -------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------

// containsSubstring reports whether s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

// searchSubstring checks if s contains substr using standard string search.
func searchSubstring(s, substr string) bool {
	for i := range len(s) - len(substr) + 1 {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
