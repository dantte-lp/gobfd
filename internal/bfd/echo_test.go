package bfd_test

import (
	"context"
	"log/slog"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// EchoSession Configuration Validation Tests
// -------------------------------------------------------------------------

func TestNewEchoSessionValid(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		Interface:        "eth0",
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 42, noopSender{}, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if es.LocalDiscriminator() != 42 {
		t.Errorf("local discriminator = %d, want 42", es.LocalDiscriminator())
	}
	if es.State() != bfd.StateDown {
		t.Errorf("initial state = %v, want Down", es.State())
	}
	if es.LocalDiag() != bfd.DiagNone {
		t.Errorf("initial diag = %v, want None", es.LocalDiag())
	}
	if es.PeerAddr() != cfg.PeerAddr {
		t.Errorf("peer addr = %v, want %v", es.PeerAddr(), cfg.PeerAddr)
	}
	if es.TxInterval() != cfg.TxInterval {
		t.Errorf("tx interval = %v, want %v", es.TxInterval(), cfg.TxInterval)
	}
	if es.DetectMultiplier() != cfg.DetectMultiplier {
		t.Errorf("detect mult = %d, want %d", es.DetectMultiplier(), cfg.DetectMultiplier)
	}
	if es.DetectionTime() != 300*time.Millisecond {
		t.Errorf("detection time = %v, want 300ms", es.DetectionTime())
	}
}

func TestNewEchoSessionZeroDiscriminator(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := bfd.NewEchoSession(cfg, 0, noopSender{}, nil, logger)
	if err == nil {
		t.Fatal("expected error for zero discriminator")
	}
}

func TestNewEchoSessionZeroDetectMult(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 0,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := bfd.NewEchoSession(cfg, 1, noopSender{}, nil, logger)
	if err == nil {
		t.Fatal("expected error for zero detect multiplier")
	}
}

func TestNewEchoSessionZeroTxInterval(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		TxInterval:       0,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := bfd.NewEchoSession(cfg, 1, noopSender{}, nil, logger)
	if err == nil {
		t.Fatal("expected error for zero TX interval")
	}
}

func TestNewEchoSessionInvalidPeerAddr(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.Addr{}, // invalid
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := bfd.NewEchoSession(cfg, 1, noopSender{}, nil, logger)
	if err == nil {
		t.Fatal("expected error for invalid peer address")
	}
}

// -------------------------------------------------------------------------
// EchoSession State Transition Tests
// -------------------------------------------------------------------------

func TestEchoSessionDownToUpOnRecv(t *testing.T) {
	t.Parallel()

	es := createTestEchoSession(t)
	notifyCh := make(chan bfd.StateChange, 8)

	es2 := createTestEchoSessionWithNotify(t, notifyCh)
	go es2.Run(t.Context())

	// Wait for session to start, then send echo return.
	time.Sleep(10 * time.Millisecond)
	es2.RecvEcho()

	// Wait for state change notification.
	select {
	case sc := <-notifyCh:
		if sc.OldState != bfd.StateDown {
			t.Errorf("old state = %v, want Down", sc.OldState)
		}
		if sc.NewState != bfd.StateUp {
			t.Errorf("new state = %v, want Up", sc.NewState)
		}
		if sc.Diag != bfd.DiagNone {
			t.Errorf("diag = %v, want None", sc.Diag)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for state change notification")
	}

	// Verify the state without the running session.
	_ = es
}

func TestEchoSessionUpToDownOnTimeout(t *testing.T) {
	t.Parallel()

	notifyCh := make(chan bfd.StateChange, 8)
	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		TxInterval:       50 * time.Millisecond,
		DetectMultiplier: 2, // Detection time = 100ms
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 100, noopSender{}, notifyCh, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	go es.Run(t.Context())

	// First, transition to Up by sending an echo return.
	time.Sleep(10 * time.Millisecond)
	es.RecvEcho()

	// Wait for Down → Up notification.
	select {
	case sc := <-notifyCh:
		if sc.NewState != bfd.StateUp {
			t.Fatalf("expected Up, got %v", sc.NewState)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for Up")
	}

	// Now wait for detection timeout (100ms + jitter margin).
	select {
	case sc := <-notifyCh:
		if sc.OldState != bfd.StateUp {
			t.Errorf("old state = %v, want Up", sc.OldState)
		}
		if sc.NewState != bfd.StateDown {
			t.Errorf("new state = %v, want Down", sc.NewState)
		}
		if sc.Diag != bfd.DiagEchoFailed {
			t.Errorf("diag = %v, want EchoFailed", sc.Diag)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for Down on detect timeout")
	}
}

func TestEchoSessionStaysUpWithEchoes(t *testing.T) {
	t.Parallel()

	notifyCh := make(chan bfd.StateChange, 16)
	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		TxInterval:       50 * time.Millisecond,
		DetectMultiplier: 3, // Detection time = 150ms
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 101, noopSender{}, notifyCh, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	go es.Run(t.Context())

	// Transition to Up.
	time.Sleep(10 * time.Millisecond)
	es.RecvEcho()

	// Wait for Up notification.
	select {
	case sc := <-notifyCh:
		if sc.NewState != bfd.StateUp {
			t.Fatalf("expected Up, got %v", sc.NewState)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for Up")
	}

	// Keep sending echoes faster than detection time.
	for range 5 {
		time.Sleep(40 * time.Millisecond)
		es.RecvEcho()
	}

	// Verify no Down notification was sent.
	select {
	case sc := <-notifyCh:
		t.Fatalf("unexpected state change: %v → %v", sc.OldState, sc.NewState)
	case <-time.After(50 * time.Millisecond):
		// Good — no state change.
	}

	if es.State() != bfd.StateUp {
		t.Errorf("state = %v, want Up", es.State())
	}
}

func TestEchoSessionCounters(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		TxInterval:       50 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 102, noopSender{}, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	go es.Run(t.Context())

	// Let it send a few echo packets.
	time.Sleep(200 * time.Millisecond)

	if es.EchosSent() == 0 {
		t.Error("expected echos_sent > 0")
	}

	// Send some echo returns.
	es.RecvEcho()
	es.RecvEcho()
	time.Sleep(10 * time.Millisecond)

	if es.EchosReceived() < 2 {
		t.Errorf("echos_received = %d, want >= 2", es.EchosReceived())
	}
}

func TestEchoSessionSnapshot(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		Interface:        "eth0",
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 200, noopSender{}, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	snap := es.Snapshot()
	if snap.LocalDiscr != 200 {
		t.Errorf("snapshot local_discr = %d, want 200", snap.LocalDiscr)
	}
	if snap.PeerAddr != cfg.PeerAddr {
		t.Errorf("snapshot peer_addr = %v, want %v", snap.PeerAddr, cfg.PeerAddr)
	}
	if snap.State != bfd.StateDown {
		t.Errorf("snapshot state = %v, want Down", snap.State)
	}
	if snap.TxInterval != cfg.TxInterval {
		t.Errorf("snapshot tx_interval = %v, want %v", snap.TxInterval, cfg.TxInterval)
	}
	if snap.DetectMultiplier != cfg.DetectMultiplier {
		t.Errorf("snapshot detect_mult = %d, want %d", snap.DetectMultiplier, cfg.DetectMultiplier)
	}
	if snap.DetectionTime != 300*time.Millisecond {
		t.Errorf("snapshot detection_time = %v, want 300ms", snap.DetectionTime)
	}
	if snap.Interface != "eth0" {
		t.Errorf("snapshot interface = %q, want %q", snap.Interface, "eth0")
	}
}

func TestEchoSessionDetectionTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		txInterval time.Duration
		detectMult uint8
		want       time.Duration
	}{
		{"100ms x 3", 100 * time.Millisecond, 3, 300 * time.Millisecond},
		{"50ms x 5", 50 * time.Millisecond, 5, 250 * time.Millisecond},
		{"1s x 1", 1 * time.Second, 1, 1 * time.Second},
		{"10ms x 10", 10 * time.Millisecond, 10, 100 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := bfd.EchoSessionConfig{
				PeerAddr:         netip.MustParseAddr("10.0.0.1"),
				TxInterval:       tt.txInterval,
				DetectMultiplier: tt.detectMult,
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			es, err := bfd.NewEchoSession(cfg, 1, noopSender{}, nil, logger)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := es.DetectionTime(); got != tt.want {
				t.Errorf("DetectionTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEchoSessionIPv6(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("2001:db8::1"),
		LocalAddr:        netip.MustParseAddr("2001:db8::2"),
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 300, noopSender{}, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error creating IPv6 echo session: %v", err)
	}

	if es.PeerAddr() != cfg.PeerAddr {
		t.Errorf("peer addr = %v, want %v", es.PeerAddr(), cfg.PeerAddr)
	}
	if es.LocalAddr() != cfg.LocalAddr {
		t.Errorf("local addr = %v, want %v", es.LocalAddr(), cfg.LocalAddr)
	}
}

func TestEchoSessionShutdown(t *testing.T) {
	t.Parallel()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		TxInterval:       50 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 400, noopSender{}, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		es.Run(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Session stopped cleanly.
	case <-time.After(1 * time.Second):
		t.Fatal("echo session did not stop after context cancellation")
	}
}

func TestSessionTypeEchoString(t *testing.T) {
	t.Parallel()

	if bfd.SessionTypeEcho.String() != "Echo" {
		t.Errorf("SessionTypeEcho.String() = %q, want %q", bfd.SessionTypeEcho.String(), "Echo")
	}
}

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// noopSender is defined in manager_test.go (same package bfd_test).

func createTestEchoSession(t *testing.T) *bfd.EchoSession {
	t.Helper()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		TxInterval:       100 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 42, noopSender{}, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return es
}

func createTestEchoSessionWithNotify(t *testing.T, notifyCh chan bfd.StateChange) *bfd.EchoSession {
	t.Helper()

	cfg := bfd.EchoSessionConfig{
		PeerAddr:         netip.MustParseAddr("10.0.0.1"),
		LocalAddr:        netip.MustParseAddr("10.0.0.2"),
		TxInterval:       50 * time.Millisecond,
		DetectMultiplier: 3,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	es, err := bfd.NewEchoSession(cfg, 42, noopSender{}, notifyCh, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return es
}
