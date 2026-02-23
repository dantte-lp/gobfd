package bfd_test

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// mockSender captures sent BFD Control packets for test verification.
type mockSender struct {
	mu      sync.Mutex
	packets [][]byte
}

// SendPacket implements bfd.PacketSender by capturing a copy of the buffer.
func (m *mockSender) SendPacket(_ context.Context, buf []byte, _ netip.Addr) error {
	cp := make([]byte, len(buf))
	copy(cp, buf)
	m.mu.Lock()
	m.packets = append(m.packets, cp)
	m.mu.Unlock()
	return nil
}

// packetCount returns the number of captured packets.
func (m *mockSender) packetCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.packets)
}

// lastPacket returns the most recently sent packet, decoded.
func (m *mockSender) lastPacket(t *testing.T) bfd.ControlPacket {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.packets) == 0 {
		t.Fatal("no packets sent")
	}
	raw := m.packets[len(m.packets)-1]
	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(raw, &pkt); err != nil {
		t.Fatalf("unmarshal last packet: %v", err)
	}
	return pkt
}

// defaultSessionConfig returns a valid SessionConfig for testing.
func defaultSessionConfig() bfd.SessionConfig {
	return bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.1"),
		LocalAddr:             netip.MustParseAddr("192.0.2.2"),
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
	}
}

// newTestSession creates a session with default config for testing.
func newTestSession(t *testing.T) (*bfd.Session, *mockSender) {
	t.Helper()
	sender := &mockSender{}
	logger := slog.Default()
	sess, err := bfd.NewSession(
		defaultSessionConfig(),
		42, // localDiscr
		sender,
		nil, // no notifications
		logger,
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sess, sender
}

// makeControlPacket builds a minimal valid BFD Control packet for injection.
func makeControlPacket(
	state bfd.State,
	myDiscr uint32,
	yourDiscr uint32,
) *bfd.ControlPacket {
	return &bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 state,
		DetectMult:            3,
		MyDiscriminator:       myDiscr,
		YourDiscriminator:     yourDiscr,
		DesiredMinTxInterval:  100000, // 100ms in microseconds
		RequiredMinRxInterval: 100000, // 100ms in microseconds
	}
}

// -------------------------------------------------------------------------
// TestNewSession — RFC 5880 Section 6.8.1 initial state
// -------------------------------------------------------------------------

// TestNewSession verifies that all initial state variables match
// RFC 5880 Section 6.8.1 mandatory initialization values.
func TestNewSession(t *testing.T) {
	t.Parallel()

	sess, _ := newTestSession(t)

	// bfd.SessionState MUST be initialized to Down.
	if sess.State() != bfd.StateDown {
		t.Errorf("initial State = %s, want Down", sess.State())
	}

	// bfd.RemoteSessionState MUST be initialized to Down.
	if sess.RemoteState() != bfd.StateDown {
		t.Errorf("initial RemoteState = %s, want Down", sess.RemoteState())
	}

	// bfd.LocalDiag MUST be initialized to zero (No Diagnostic).
	if sess.LocalDiag() != bfd.DiagNone {
		t.Errorf("initial LocalDiag = %s, want None", sess.LocalDiag())
	}

	// bfd.LocalDiscr MUST be nonzero.
	if sess.LocalDiscriminator() == 0 {
		t.Error("LocalDiscriminator is zero, must be nonzero")
	}

	// PeerAddr must match config.
	want := netip.MustParseAddr("192.0.2.1")
	if sess.PeerAddr() != want {
		t.Errorf("PeerAddr = %s, want %s", sess.PeerAddr(), want)
	}
}

// -------------------------------------------------------------------------
// TestNewSession_ValidationErrors — config validation
// -------------------------------------------------------------------------

// TestNewSessionValidationErrors verifies that invalid configurations
// are rejected with appropriate sentinel errors.
func TestNewSessionValidationErrors(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	sender := &mockSender{}

	tests := []struct {
		name       string
		cfg        bfd.SessionConfig
		localDiscr uint32
		wantErr    string
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
			localDiscr: 1,
			wantErr:    "detect multiplier",
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
			localDiscr: 1,
			wantErr:    "desired min TX interval",
		},
		{
			name: "zero discriminator",
			cfg: bfd.SessionConfig{
				PeerAddr:              netip.MustParseAddr("192.0.2.1"),
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  bfd.SessionTypeSingleHop,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
			localDiscr: 0,
			wantErr:    "local discriminator",
		},
		{
			name: "invalid session type",
			cfg: bfd.SessionConfig{
				PeerAddr:              netip.MustParseAddr("192.0.2.1"),
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  0, // invalid
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
			localDiscr: 1,
			wantErr:    "session type",
		},
		{
			name: "invalid session role",
			cfg: bfd.SessionConfig{
				PeerAddr:              netip.MustParseAddr("192.0.2.1"),
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  bfd.SessionTypeSingleHop,
				Role:                  0, // invalid
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			},
			localDiscr: 1,
			wantErr:    "session role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := bfd.NewSession(tt.cfg, tt.localDiscr, sender, nil, logger)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestSessionThreeWayHandshake — RFC 5880 Section 6.2
// -------------------------------------------------------------------------

// TestSessionThreeWayHandshake verifies a full BFD three-way handshake
// between two sessions using synctest fake time.
//
// Sequence (RFC 5880 Section 6.2):
//  1. Both sessions start in Down.
//  2. Session A sends Down -> Session B receives Down -> B transitions to Init.
//  3. Session B sends Init -> Session A receives Init -> A transitions to Up.
//  4. Session A sends Up -> Session B receives Up -> B transitions to Up.
func TestSessionThreeWayHandshake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		senderA := &mockSender{}
		senderB := &mockSender{}
		logger := slog.Default()

		notifyCh := make(chan bfd.StateChange, 16)

		sessA := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 100, senderA, notifyCh, logger)

		sessB := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.1"),
			LocalAddr:             netip.MustParseAddr("10.0.0.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 200, senderB, notifyCh, logger)

		ctxA, cancelA := context.WithCancel(context.Background())
		defer cancelA()
		ctxB, cancelB := context.WithCancel(context.Background())
		defer cancelB()

		go sessA.Run(ctxA)
		go sessB.Run(ctxB)

		// Phase 1: Both start in Down. After first TX (>= 1s slow rate),
		// A sends a Down packet. Inject A's Down into B.
		time.Sleep(2 * time.Second)

		// B receives Down from A -> B goes to Init.
		sessB.RecvPacket(makeControlPacket(bfd.StateDown, 100, 0))
		time.Sleep(50 * time.Millisecond)

		if sessB.State() != bfd.StateInit {
			t.Errorf("after recv Down: B state = %s, want Init", sessB.State())
		}

		// A receives Init from B -> A goes to Up.
		sessA.RecvPacket(makeControlPacket(bfd.StateInit, 200, 100))
		time.Sleep(50 * time.Millisecond)

		if sessA.State() != bfd.StateUp {
			t.Errorf("after recv Init: A state = %s, want Up", sessA.State())
		}

		// B receives Up from A -> B goes to Up.
		sessB.RecvPacket(makeControlPacket(bfd.StateUp, 100, 200))
		time.Sleep(50 * time.Millisecond)

		if sessB.State() != bfd.StateUp {
			t.Errorf("after recv Up: B state = %s, want Up", sessB.State())
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionTimerNegotiation — RFC 5880 Section 6.8.2
// -------------------------------------------------------------------------

// TestSessionTimerNegotiation verifies that the TX interval is negotiated
// as max(local DesiredMinTx, remote RequiredMinRx).
//
// RFC 5880 Section 6.8.7: "a system MUST NOT transmit BFD Control packets
// at an interval less than the larger of bfd.DesiredMinTxInterval and
// bfd.RemoteMinRxInterval.".
func TestSessionTimerNegotiation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()

		// Local desired = 100ms, but remote will advertise 200ms min RX.
		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		// Bring session to Up state.
		// First wait for slow-rate timer to fire at least once.
		time.Sleep(2 * time.Second)

		// Inject Init packet with RequiredMinRxInterval = 200ms (200000 us).
		// High DetectMult to prevent detection timeout during measurement.
		pkt := makeControlPacket(bfd.StateInit, 99, 42)
		pkt.RequiredMinRxInterval = 200000 // 200ms
		pkt.DetectMult = 50                // detection time = 50 * 100ms = 5s
		sess.RecvPacket(pkt)
		time.Sleep(50 * time.Millisecond)

		// Session should be Up now (Down + RecvInit -> Up).
		if sess.State() != bfd.StateUp {
			t.Fatalf("state = %s, want Up", sess.State())
		}

		// Clear sent packets to measure interval.
		sender.mu.Lock()
		sender.packets = nil
		sender.mu.Unlock()

		// Wait enough time for packets at 200ms intervals (3 seconds).
		time.Sleep(3 * time.Second)

		count := sender.packetCount()
		// At 200ms intervals with jitter (150-200ms), expect ~15-20 packets in 3s.
		// At 100ms intervals, we would expect ~30+.
		// Verify count is consistent with ~200ms, not ~100ms.
		if count > 25 {
			t.Errorf("sent %d packets in 3s (expected ~15 at 200ms interval)", count)
		}
		if count < 5 {
			t.Errorf("sent only %d packets in 3s (expected ~15 at 200ms interval)", count)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionDetectionTimeout — RFC 5880 Section 6.8.4
// -------------------------------------------------------------------------

// TestSessionDetectionTimeout verifies that the session transitions to Down
// when the detection timer expires without receiving packets.
//
// RFC 5880 Section 6.8.4: "If a period of time equal to the Detection Time
// passes without receiving a BFD Control packet from the remote system,
// and bfd.SessionState is Init or Up, the session has gone down.".
func TestSessionDetectionTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()
		notifyCh := make(chan bfd.StateChange, 16)

		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, notifyCh, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		// Bring session to Up.
		time.Sleep(2 * time.Second)

		pkt := makeControlPacket(bfd.StateInit, 99, 42)
		pkt.DesiredMinTxInterval = 100000  // 100ms
		pkt.RequiredMinRxInterval = 100000 // 100ms
		sess.RecvPacket(pkt)
		time.Sleep(50 * time.Millisecond)

		if sess.State() != bfd.StateUp {
			t.Fatalf("state = %s, want Up", sess.State())
		}

		// Detection time = remoteDetectMult(3) * max(100ms, 100ms) = 300ms.
		// Stop sending packets. Wait for detection timeout.
		time.Sleep(500 * time.Millisecond)

		if sess.State() != bfd.StateDown {
			t.Errorf("after timeout: state = %s, want Down", sess.State())
		}
		if sess.LocalDiag() != bfd.DiagControlTimeExpired {
			t.Errorf("diag = %s, want ControlTimeExpired", sess.LocalDiag())
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionSlowTxRate — RFC 5880 Section 6.8.3
// -------------------------------------------------------------------------

// TestSessionSlowTxRate verifies that the TX interval is at least 1 second
// when the session state is not Up.
//
// RFC 5880 Section 6.8.3: "When bfd.SessionState is not Up, the system
// MUST set bfd.DesiredMinTxInterval to a value of not less than one second.".
func TestSessionSlowTxRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()

		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond, // desired < 1s
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		// Session is in Down state (not Up). Even though desired is 100ms,
		// the actual TX interval MUST be >= 1 second.

		// After 500ms, no packet should have been sent (first TX at ~750ms-1s
		// due to jitter).
		time.Sleep(500 * time.Millisecond)
		countAt500ms := sender.packetCount()

		// After 3 seconds, should have sent ~2-3 packets at 1s slow rate
		// (not 30 at 100ms).
		time.Sleep(2500 * time.Millisecond)
		countAt3s := sender.packetCount()

		if countAt500ms > 1 {
			t.Errorf("sent %d packets in first 500ms (slow rate should prevent this)", countAt500ms)
		}
		if countAt3s > 5 {
			t.Errorf("sent %d packets in 3s at slow rate (expected <= 5 at ~1s interval)", countAt3s)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionJitter — RFC 5880 Section 6.8.7
// -------------------------------------------------------------------------

// TestApplyJitter verifies the jitter function produces values within the
// RFC-specified ranges.
//
// RFC 5880 Section 6.8.7:
//   - Normal: interval MUST be reduced by 0-25% (result in 75%-100%).
//   - DetectMult == 1: interval MUST be between 75% and 90%.
func TestApplyJitter(t *testing.T) {
	t.Parallel()

	const interval = 1000 * time.Millisecond
	const iterations = 10000

	t.Run("normal jitter detectMult=3", func(t *testing.T) {
		t.Parallel()
		for range iterations {
			result := bfd.ApplyJitter(interval, 3)
			minAllowed := interval * 75 / 100
			if result < minAllowed || result > interval {
				t.Fatalf("jitter result %v outside [%v, %v]",
					result, minAllowed, interval)
			}
		}
	})

	t.Run("strict jitter detectMult=1", func(t *testing.T) {
		t.Parallel()
		for range iterations {
			result := bfd.ApplyJitter(interval, 1)
			minAllowed := interval * 75 / 100
			maxAllowed := interval * 90 / 100
			if result < minAllowed || result > maxAllowed {
				t.Fatalf("jitter result %v outside [%v, %v]",
					result, minAllowed, maxAllowed)
			}
		}
	})

	t.Run("zero interval", func(t *testing.T) {
		t.Parallel()
		result := bfd.ApplyJitter(0, 3)
		if result != 0 {
			t.Errorf("jitter of zero interval = %v, want 0", result)
		}
	})
}

// -------------------------------------------------------------------------
// TestSessionPollSequence — RFC 5880 Section 6.5
// -------------------------------------------------------------------------

// TestSessionPollSequence verifies the P/F bit exchange mechanism.
//
// RFC 5880 Section 6.5: "When the other system receives a Poll, it
// immediately transmits a BFD Control packet with the Final (F) bit set.".
func TestSessionPollSequence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()

		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		// Bring session Up.
		time.Sleep(2 * time.Second)
		pkt := makeControlPacket(bfd.StateInit, 99, 42)
		pkt.DesiredMinTxInterval = 100000
		pkt.RequiredMinRxInterval = 100000
		pkt.DetectMult = 50 // High detect mult to prevent timeout during test.
		sess.RecvPacket(pkt)
		time.Sleep(50 * time.Millisecond)

		if sess.State() != bfd.StateUp {
			t.Fatalf("state = %s, want Up", sess.State())
		}

		// Send a packet with Poll bit set.
		pollPkt := makeControlPacket(bfd.StateUp, 99, 42)
		pollPkt.Poll = true
		pollPkt.DesiredMinTxInterval = 100000
		pollPkt.RequiredMinRxInterval = 100000
		pollPkt.DetectMult = 50
		sess.RecvPacket(pollPkt)

		// Wait for the session to process and send a response.
		time.Sleep(2 * time.Second)

		// The session should have sent a packet with Final bit set.
		found := checkFinalBitSent(t, sender)
		if !found {
			t.Error("no packet with Final bit set was sent in response to Poll")
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// checkFinalBitSent scans all sent packets for one with the Final bit set.
func checkFinalBitSent(t *testing.T, sender *mockSender) bool {
	t.Helper()
	sender.mu.Lock()
	defer sender.mu.Unlock()
	for _, raw := range sender.packets {
		var pkt bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(raw, &pkt); err != nil {
			continue
		}
		if pkt.Final {
			return true
		}
	}
	return false
}

// -------------------------------------------------------------------------
// TestSessionRecvPacketUpdatesState — RFC 5880 Section 6.8.6 steps 13-17
// -------------------------------------------------------------------------

// TestSessionRecvPacketUpdatesState verifies that processing a received
// packet correctly updates session state variables per RFC 5880 Section
// 6.8.6 steps 13-17.
func TestSessionRecvPacketUpdatesState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()

		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		time.Sleep(2 * time.Second)

		// Inject a packet with specific values.
		pkt := &bfd.ControlPacket{
			Version:               bfd.Version,
			State:                 bfd.StateDown,
			DetectMult:            5,
			MyDiscriminator:       0xABCD1234,
			YourDiscriminator:     0,
			DesiredMinTxInterval:  200000, // 200ms
			RequiredMinRxInterval: 150000, // 150ms
		}
		sess.RecvPacket(pkt)
		time.Sleep(50 * time.Millisecond)

		// Step 14: RemoteState should be updated.
		// After Down+RecvDown -> session goes to Init.
		if sess.State() != bfd.StateInit {
			t.Errorf("state = %s, want Init", sess.State())
		}

		// Step 14: RemoteState should reflect the received state.
		if sess.RemoteState() != bfd.StateDown {
			t.Errorf("remote state = %s, want Down", sess.RemoteState())
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionCachedPacketRebuild — cached packet correctness
// -------------------------------------------------------------------------

// TestSessionCachedPacketRebuild verifies that the cached BFD Control
// packet is rebuilt when state changes occur, and its fields reflect the
// current session state per RFC 5880 Section 6.8.7.
func TestSessionCachedPacketRebuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()

		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		// Wait for first TX in Down state.
		time.Sleep(2 * time.Second)

		// Verify initial packet fields.
		pkt1 := sender.lastPacket(t)
		if pkt1.State != bfd.StateDown {
			t.Errorf("initial packet State = %s, want Down", pkt1.State)
		}
		if pkt1.MyDiscriminator != 42 {
			t.Errorf("MyDiscriminator = %d, want 42", pkt1.MyDiscriminator)
		}
		if pkt1.YourDiscriminator != 0 {
			t.Errorf("initial YourDiscriminator = %d, want 0", pkt1.YourDiscriminator)
		}

		// Inject Init packet to transition to Up. High detect mult to
		// prevent detection timeout before assertions.
		initPkt := makeControlPacket(bfd.StateInit, 99, 42)
		initPkt.DesiredMinTxInterval = 100000
		initPkt.RequiredMinRxInterval = 100000
		initPkt.DetectMult = 50
		sess.RecvPacket(initPkt)
		// Wait for TX timer to fire so at least one Up packet is sent.
		time.Sleep(200 * time.Millisecond)

		// Session should be Up now.
		if sess.State() != bfd.StateUp {
			t.Fatalf("state = %s, want Up", sess.State())
		}

		// Verify cached packet reflects Up state and YourDiscriminator.
		pkt2 := sender.lastPacket(t)
		if pkt2.State != bfd.StateUp {
			t.Errorf("after Up: packet State = %s, want Up", pkt2.State)
		}
		if pkt2.YourDiscriminator != 99 {
			t.Errorf("after Up: YourDiscriminator = %d, want 99", pkt2.YourDiscriminator)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionPassiveRole — RFC 5880 Section 6.8.7
// -------------------------------------------------------------------------

// TestSessionPassiveRole verifies that a passive session does not transmit
// until it receives a packet from the remote system.
//
// RFC 5880 Section 6.8.7: "A system MUST NOT transmit BFD Control packets
// if bfd.RemoteDiscr is zero and the system is taking the Passive role.".
func TestSessionPassiveRole(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()

		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RolePassive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		// Wait well past the TX interval. No packets should be sent.
		time.Sleep(3 * time.Second)

		if sender.packetCount() != 0 {
			t.Errorf("passive session sent %d packets before receiving any", sender.packetCount())
		}

		// Now inject a packet (sets remoteDiscr). High DetectMult to
		// prevent Init->Down timeout before passive session can send.
		pkt := makeControlPacket(bfd.StateDown, 99, 0)
		pkt.DetectMult = 50
		sess.RecvPacket(pkt)
		time.Sleep(3 * time.Second)

		// Now the session should start sending.
		if sender.packetCount() == 0 {
			t.Error("passive session did not send after receiving a packet")
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionStateChangeNotification — StateChange channel
// -------------------------------------------------------------------------

// TestSessionStateChangeNotification verifies that state transitions emit
// StateChange notifications on the notifyCh channel.
func TestSessionStateChangeNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &mockSender{}
		logger := slog.Default()
		notifyCh := make(chan bfd.StateChange, 16)

		sess := mustNewSession(t, bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("10.0.0.2"),
			LocalAddr:             netip.MustParseAddr("10.0.0.1"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}, 42, sender, notifyCh, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go sess.Run(ctx)

		time.Sleep(2 * time.Second)

		// Inject Init to trigger Down -> Up transition.
		sess.RecvPacket(makeControlPacket(bfd.StateInit, 99, 42))
		time.Sleep(50 * time.Millisecond)

		// Should receive state change notification.
		var gotNotify bool
		for range len(notifyCh) {
			sc := <-notifyCh
			if sc.NewState == bfd.StateUp {
				gotNotify = true
				if sc.LocalDiscr != 42 {
					t.Errorf("notification LocalDiscr = %d, want 42", sc.LocalDiscr)
				}
				break
			}
		}
		if !gotNotify {
			t.Error("did not receive Up notification on notifyCh")
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// -------------------------------------------------------------------------
// TestSessionTypeString — verify SessionType.String()
// -------------------------------------------------------------------------

// TestSessionTypeString verifies human-readable output for SessionType.
func TestSessionTypeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		st   bfd.SessionType
		want string
	}{
		{bfd.SessionTypeSingleHop, "SingleHop"},
		{bfd.SessionTypeMultiHop, "MultiHop"},
		{bfd.SessionTypeMicroBFD, "MicroBFD"},
		{bfd.SessionType(0), "Unknown"},
		{bfd.SessionType(255), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.st.String(); got != tt.want {
				t.Errorf("SessionType(%d).String() = %q, want %q", tt.st, got, tt.want)
			}
		})
	}
}

// TestSessionRoleString verifies human-readable output for SessionRole.
func TestSessionRoleString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sr   bfd.SessionRole
		want string
	}{
		{bfd.RoleActive, "Active"},
		{bfd.RolePassive, "Passive"},
		{bfd.SessionRole(0), "Unknown"},
		{bfd.SessionRole(255), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.sr.String(); got != tt.want {
				t.Errorf("SessionRole(%d).String() = %q, want %q", tt.sr, got, tt.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------

// mustNewSession creates a session or fails the test.
func mustNewSession(
	t *testing.T,
	cfg bfd.SessionConfig,
	localDiscr uint32,
	sender bfd.PacketSender,
	notifyCh chan<- bfd.StateChange,
	logger *slog.Logger,
) *bfd.Session {
	t.Helper()
	sess, err := bfd.NewSession(cfg, localDiscr, sender, notifyCh, logger)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sess
}
