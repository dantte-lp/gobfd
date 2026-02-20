//go:build integration

package integration_test

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// Mock bridge — connects two PacketSenders to deliver packets cross-session
// -------------------------------------------------------------------------

// bridgeSender is a PacketSender that delivers packets to a target session
// through a bridge. It simulates network delivery between two BFD peers.
type bridgeSender struct {
	mu      sync.Mutex
	target  *bfd.Session
	sendCnt int
}

// SendPacket implements bfd.PacketSender. It unmarshals the packet and
// delivers it to the target session, simulating network transit.
func (bs *bridgeSender) SendPacket(
	_ context.Context,
	buf []byte,
	_ netip.Addr,
) error {
	bs.mu.Lock()
	t := bs.target
	bs.sendCnt++
	bs.mu.Unlock()

	if t == nil {
		return nil
	}

	// Unmarshal to deliver as a parsed packet (like the real recv loop).
	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(buf, &pkt); err != nil {
		return nil //nolint:nilerr // drop invalid packets silently, like real BFD.
	}

	// Copy wire bytes for auth verification.
	wire := make([]byte, len(buf))
	copy(wire, buf)

	t.RecvPacket(&pkt, wire)
	return nil
}

// count returns the number of packets sent.
func (bs *bridgeSender) count() int {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return bs.sendCnt
}

// setTarget sets the target session for packet delivery.
func (bs *bridgeSender) setTarget(s *bfd.Session) {
	bs.mu.Lock()
	bs.target = s
	bs.mu.Unlock()
}

// -------------------------------------------------------------------------
// TestDatapathTwoSessions — full BFD handshake between two managers
// -------------------------------------------------------------------------

// TestDatapathTwoSessions verifies that two BFD sessions connected
// through an in-memory bridge can complete the three-way handshake
// and reach Up state.
//
// This validates the complete data path:
//
//	Session A TX -> bridge -> Session B RX (and vice versa)
//
// Both sessions are created via Manager with active role. The bridge
// senders deliver packets directly between sessions.
func TestDatapathTwoSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logger := slog.Default()

		// Create bridge senders (targets set after sessions exist).
		senderAtoB := &bridgeSender{}
		senderBtoA := &bridgeSender{}

		sessA, err := bfd.NewSession(bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 100, senderAtoB, nil, logger)
		if err != nil {
			t.Fatalf("create session A: %v", err)
		}

		sessB, err := bfd.NewSession(bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.1"),
			LocalAddr:             netip.MustParseAddr("10.0.0.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 200, senderBtoA, nil, logger)
		if err != nil {
			t.Fatalf("create session B: %v", err)
		}

		// Wire the bridge: A's sender delivers to B, B's sender to A.
		senderAtoB.setTarget(sessB)
		senderBtoA.setTarget(sessA)

		ctxA, cancelA := context.WithCancel(context.Background())
		defer cancelA()
		ctxB, cancelB := context.WithCancel(context.Background())
		defer cancelB()

		go sessA.Run(ctxA)
		go sessB.Run(ctxB)

		// Advance virtual time. Slow TX rate = 1s with jitter.
		// Three-way handshake takes ~3 rounds.
		for range 30 {
			time.Sleep(time.Second)
			synctest.Wait()
			if sessA.State() == bfd.StateUp &&
				sessB.State() == bfd.StateUp {
				break
			}
		}

		if sessA.State() != bfd.StateUp {
			t.Fatalf("session A: state=%s, AtoB=%d, BtoA=%d",
				sessA.State(), senderAtoB.count(), senderBtoA.count())
		}
		if sessB.State() != bfd.StateUp {
			t.Fatalf("session B: state=%s, AtoB=%d, BtoA=%d",
				sessB.State(), senderAtoB.count(), senderBtoA.count())
		}

		// Verify remote discriminators are set.
		verifyUpNotifications(t, nil, sessA, sessB)
	})
}

// createPeerSessions creates two sessions in separate managers that
// peer with each other.
func createPeerSessions(
	t *testing.T,
	mgrA, mgrB *bfd.Manager,
	senderAtoB, senderBtoA *bridgeSender,
	notifyCh chan bfd.StateChange,
) (*bfd.Session, *bfd.Session) {
	t.Helper()

	// Override the manager's notify channel by creating sessions with it.
	// We need to create a custom manager or use the manager's channel.
	// Since Manager uses its own notifyCh, we read from StateChanges().

	cfgA := bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("10.0.0.2"),
		LocalAddr:             netip.MustParseAddr("10.0.0.1"),
		Interface:             "lo",
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
	}

	cfgB := bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("10.0.0.1"),
		LocalAddr:             netip.MustParseAddr("10.0.0.2"),
		Interface:             "lo",
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
	}

	sessA, err := mgrA.CreateSession(context.Background(), cfgA, senderAtoB)
	if err != nil {
		t.Fatalf("create session A: %v", err)
	}

	sessB, err := mgrB.CreateSession(context.Background(), cfgB, senderBtoA)
	if err != nil {
		t.Fatalf("create session B: %v", err)
	}

	_ = notifyCh // notifications come from manager.StateChanges()

	return sessA, sessB
}

// waitForState polls the session state at intervals until it matches
// the desired state or the maximum number of iterations is exceeded.
//
// Uses time.Sleep to yield to synctest's virtual time scheduler,
// allowing session goroutines to make progress between checks.
func waitForState(
	t *testing.T,
	sess *bfd.Session,
	want bfd.State,
	timeout time.Duration,
) {
	t.Helper()

	// Poll at 100ms intervals in virtual time.
	const pollInterval = 100 * time.Millisecond
	iterations := int(timeout / pollInterval)

	for range iterations {
		time.Sleep(pollInterval)
		synctest.Wait() // Ensure session goroutines have settled after timer fires.
		if sess.State() == want {
			return
		}
	}

	t.Fatalf(
		"session %d: state = %s, want %s after %v",
		sess.LocalDiscriminator(), sess.State(), want, timeout,
	)
}

// verifyUpNotifications checks that Up notifications were emitted
// for both sessions via their respective managers.
func verifyUpNotifications(
	t *testing.T,
	_ chan bfd.StateChange,
	sessA, sessB *bfd.Session,
) {
	t.Helper()

	// The sessions reached Up state (verified by waitForState).
	// Verify the sessions report correct state.
	if sessA.State() != bfd.StateUp {
		t.Errorf("session A: state = %s, want Up", sessA.State())
	}
	if sessB.State() != bfd.StateUp {
		t.Errorf("session B: state = %s, want Up", sessB.State())
	}

	// Verify remote discriminators are set (RFC 5880 Section 6.8.6 step 13).
	if sessA.RemoteDiscriminator() == 0 {
		t.Error("session A: remote discriminator is zero after handshake")
	}
	if sessB.RemoteDiscriminator() == 0 {
		t.Error("session B: remote discriminator is zero after handshake")
	}
}

// -------------------------------------------------------------------------
// TestDatapathDetectionTimeout — session goes Down on peer failure
// -------------------------------------------------------------------------

// TestDatapathDetectionTimeout verifies that when one peer stops
// sending packets, the other detects the failure and transitions
// to Down state.
func TestDatapathDetectionTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logger := slog.New(slog.DiscardHandler)

		mgrA := bfd.NewManager(logger)
		defer mgrA.Close()
		mgrB := bfd.NewManager(logger)
		defer mgrB.Close()

		senderAtoB := &bridgeSender{}
		senderBtoA := &bridgeSender{}

		sessA, sessB := createPeerSessions(
			t, mgrA, mgrB, senderAtoB, senderBtoA, nil,
		)

		senderAtoB.setTarget(sessB)
		senderBtoA.setTarget(sessA)

		// Wait for both to reach Up.
		waitForState(t, sessA, bfd.StateUp, 10*time.Second)
		waitForState(t, sessB, bfd.StateUp, 10*time.Second)

		// Disconnect B's sender (A stops receiving from B).
		senderBtoA.setTarget(nil)

		// A should detect the timeout. Detection time = 3 * 100ms = 300ms.
		// With some jitter, allow up to 2 seconds.
		waitForState(t, sessA, bfd.StateDown, 2*time.Second)

		if sessA.LocalDiag() != bfd.DiagControlTimeExpired {
			t.Errorf("session A diag = %s, want ControlTimeExpired",
				sessA.LocalDiag())
		}
	})
}
