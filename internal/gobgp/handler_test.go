package gobgp_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

// Method name constants for mock call assertions.
const (
	methodDisablePeer = "DisablePeer"
	methodEnablePeer  = "EnablePeer"
)

// -------------------------------------------------------------------------
// Mock GoBGP Client
// -------------------------------------------------------------------------

// mockClient records GoBGP API calls for test assertions.
type mockClient struct {
	mu     sync.Mutex
	calls  []mockCall
	err    error // if set, all calls return this error
	closed bool
}

type mockCall struct {
	method        string
	addr          string
	communication string
}

func newMockClient() *mockClient {
	return &mockClient{}
}

func (m *mockClient) DisablePeer(_ context.Context, addr string, communication string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	m.calls = append(m.calls, mockCall{
		method:        methodDisablePeer,
		addr:          addr,
		communication: communication,
	})

	return nil
}

func (m *mockClient) EnablePeer(_ context.Context, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	m.calls = append(m.calls, mockCall{
		method: methodEnablePeer,
		addr:   addr,
	})

	return nil
}

func (m *mockClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true

	return nil
}

func (m *mockClient) getCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]mockCall, len(m.calls))
	copy(result, m.calls)

	return result
}

func (m *mockClient) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.err = err
}

// -------------------------------------------------------------------------
// Handler Tests -- BFD Down -> BGP DisablePeer
// -------------------------------------------------------------------------

func TestHandlerBFDDownDisablesPeer(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{})

	events := make(chan bfd.StateChange, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	// BFD Up -> Down transition (RFC 5882 Section 4.3).
	events <- bfd.StateChange{
		LocalDiscr: 1,
		PeerAddr:   netip.MustParseAddr("10.0.0.1"),
		OldState:   bfd.StateUp,
		NewState:   bfd.StateDown,
		Diag:       bfd.DiagControlTimeExpired,
		Timestamp:  time.Now(),
	}

	// Wait for processing.
	waitForCalls(t, mock, 1)

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}

	if calls[0].method != methodDisablePeer {
		t.Errorf("expected %s, got %s", methodDisablePeer, calls[0].method)
	}

	if calls[0].addr != "10.0.0.1" {
		t.Errorf("expected addr 10.0.0.1, got %s", calls[0].addr)
	}

	// RFC 9384: communication must contain Cease/10 context and diagnostic.
	wantComm := gobgp.FormatBFDDownCommunication(bfd.DiagControlTimeExpired)
	if calls[0].communication != wantComm {
		t.Errorf("communication mismatch\n  got:  %q\n  want: %q", calls[0].communication, wantComm)
	}

	cancel()
	<-done
}

// -------------------------------------------------------------------------
// Handler Tests -- BFD Up -> BGP EnablePeer
// -------------------------------------------------------------------------

func TestHandlerBFDUpEnablesPeer(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{})

	events := make(chan bfd.StateChange, 2)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	// BFD Init -> Up transition.
	events <- bfd.StateChange{
		LocalDiscr: 1,
		PeerAddr:   netip.MustParseAddr("10.0.0.1"),
		OldState:   bfd.StateInit,
		NewState:   bfd.StateUp,
		Diag:       bfd.DiagNone,
		Timestamp:  time.Now(),
	}

	waitForCalls(t, mock, 1)

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}

	if calls[0].method != methodEnablePeer {
		t.Errorf("expected %s, got %s", methodEnablePeer, calls[0].method)
	}

	if calls[0].addr != "10.0.0.1" {
		t.Errorf("expected addr 10.0.0.1, got %s", calls[0].addr)
	}

	cancel()
	<-done
}

// -------------------------------------------------------------------------
// Handler Tests -- Non-actionable transitions are ignored
// -------------------------------------------------------------------------

func TestHandlerIgnoresNonActionableTransitions(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{})

	events := make(chan bfd.StateChange, 4)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	// Down -> Init: informational only, no BGP action.
	events <- bfd.StateChange{
		LocalDiscr: 1,
		PeerAddr:   netip.MustParseAddr("10.0.0.1"),
		OldState:   bfd.StateDown,
		NewState:   bfd.StateInit,
		Diag:       bfd.DiagNone,
		Timestamp:  time.Now(),
	}

	// Up -> AdminDown: intentional local action, not a failure.
	events <- bfd.StateChange{
		LocalDiscr: 2,
		PeerAddr:   netip.MustParseAddr("10.0.0.2"),
		OldState:   bfd.StateUp,
		NewState:   bfd.StateAdminDown,
		Diag:       bfd.DiagAdminDown,
		Timestamp:  time.Now(),
	}

	// AdminDown -> Down: also not actionable.
	events <- bfd.StateChange{
		LocalDiscr: 3,
		PeerAddr:   netip.MustParseAddr("10.0.0.3"),
		OldState:   bfd.StateAdminDown,
		NewState:   bfd.StateDown,
		Diag:       bfd.DiagNone,
		Timestamp:  time.Now(),
	}

	// Give time for processing then verify no calls made.
	time.Sleep(100 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected 0 calls, got %d: %+v", len(calls), calls)
	}

	cancel()
	<-done
}

// -------------------------------------------------------------------------
// Handler Tests -- Channel close stops handler
// -------------------------------------------------------------------------

func TestHandlerStopsOnChannelClose(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{})

	events := make(chan bfd.StateChange)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		done <- handler.Run(ctx, events)
	}()

	close(events)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not stop after channel close")
	}
}

// -------------------------------------------------------------------------
// Handler Tests -- Context cancellation stops handler
// -------------------------------------------------------------------------

func TestHandlerStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{})

	events := make(chan bfd.StateChange)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- handler.Run(ctx, events)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not stop after context cancel")
	}
}

// -------------------------------------------------------------------------
// Handler Tests -- GoBGP client error is logged, not fatal
// -------------------------------------------------------------------------

func TestHandlerGoBGPErrorNonFatal(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	mock.setError(errors.New("connection refused"))

	handler := newTestHandler(t, mock, gobgp.DampeningConfig{})

	events := make(chan bfd.StateChange, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	// BFD Down event should not crash the handler even if GoBGP call fails.
	events <- bfd.StateChange{
		LocalDiscr: 1,
		PeerAddr:   netip.MustParseAddr("10.0.0.1"),
		OldState:   bfd.StateUp,
		NewState:   bfd.StateDown,
		Diag:       bfd.DiagControlTimeExpired,
		Timestamp:  time.Now(),
	}

	// Give time for processing.
	time.Sleep(100 * time.Millisecond)

	// Handler should still be running despite the error.
	cancel()

	select {
	case <-done:
		// Success: handler stopped gracefully.
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not stop after context cancel")
	}
}

// -------------------------------------------------------------------------
// Handler Tests -- Invalid strategy rejected
// -------------------------------------------------------------------------

func TestNewHandlerInvalidStrategy(t *testing.T) {
	t.Parallel()

	_, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client:   newMockClient(),
		Strategy: "bogus",
		Logger:   slog.Default(),
	})

	if err == nil {
		t.Fatal("expected error for invalid strategy")
	}
}

// -------------------------------------------------------------------------
// Handler Tests -- Withdraw routes strategy unsupported
// -------------------------------------------------------------------------

func TestNewHandlerWithdrawRoutesUnsupported(t *testing.T) {
	t.Parallel()

	_, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client:   newMockClient(),
		Strategy: gobgp.StrategyWithdrawRoutes,
		Logger:   slog.Default(),
	})

	if err == nil {
		t.Fatal("expected error for unsupported withdraw-routes strategy")
	}
}

// -------------------------------------------------------------------------
// Handler Dampening Integration -- rapid flaps are suppressed
// -------------------------------------------------------------------------

// TestHandlerDampeningIntegration tests the full handler with dampening
// using a high suppress threshold to avoid floating-point timing issues.
// The handler creates its own dampener with real time, so we use a
// threshold of 4 to ensure 3 events pass through and the 4th+ are suppressed.
func TestHandlerDampeningIntegration(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 4,
		ReuseThreshold:    2,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	})

	events := make(chan bfd.StateChange, 10)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	peer := netip.MustParseAddr("10.0.0.1")

	// Send 6 rapid Down events.
	for range 6 {
		events <- bfd.StateChange{
			LocalDiscr: 1,
			PeerAddr:   peer,
			OldState:   bfd.StateUp,
			NewState:   bfd.StateDown,
			Diag:       bfd.DiagControlTimeExpired,
			Timestamp:  time.Now(),
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for processing.
	time.Sleep(200 * time.Millisecond)

	calls := mock.getCalls()

	// With threshold=4 and 15s half-life, the tiny decay between rapid
	// calls is negligible. Events 1-3 pass (penalties ~1,2,3), event 4
	// reaches threshold (penalty ~4) and is suppressed.
	if len(calls) < 2 || len(calls) > 4 {
		t.Errorf("expected 2-4 calls before suppression, got %d: %+v", len(calls), calls)
	}

	for _, c := range calls {
		if c.method != methodDisablePeer {
			t.Errorf("expected %s, got %s", methodDisablePeer, c.method)
		}
	}

	cancel()
	<-done
}

// -------------------------------------------------------------------------
// Handler Dampening Integration -- Up events suppressed during dampening
// -------------------------------------------------------------------------

func TestHandlerDampeningUpSuppressed(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	})

	events := make(chan bfd.StateChange, 10)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	peer := netip.MustParseAddr("10.0.0.1")

	// Send 3 rapid Down events to trigger suppression.
	for range 3 {
		events <- bfd.StateChange{
			LocalDiscr: 1,
			PeerAddr:   peer,
			OldState:   bfd.StateUp,
			NewState:   bfd.StateDown,
			Diag:       bfd.DiagControlTimeExpired,
			Timestamp:  time.Now(),
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for Down events to be processed.
	time.Sleep(100 * time.Millisecond)

	// Now send an Up event -- it should be suppressed because the peer
	// is still dampened.
	events <- bfd.StateChange{
		LocalDiscr: 1,
		PeerAddr:   peer,
		OldState:   bfd.StateDown,
		NewState:   bfd.StateUp,
		Diag:       bfd.DiagNone,
		Timestamp:  time.Now(),
	}

	time.Sleep(200 * time.Millisecond)

	calls := mock.getCalls()

	// Verify no EnablePeer call was made.
	hasEnablePeer := false
	for _, c := range calls {
		if c.method == methodEnablePeer {
			hasEnablePeer = true
		}
	}

	if hasEnablePeer {
		t.Error("EnablePeer should be suppressed during dampening")
	}

	cancel()
	<-done
}

// -------------------------------------------------------------------------
// Handler Tests -- Disabled dampening passes all events
// -------------------------------------------------------------------------

func TestDampeningDisabledPassesAll(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{
		Enabled: false,
	})

	events := make(chan bfd.StateChange, 10)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	peer := netip.MustParseAddr("10.0.0.1")

	// Send 5 rapid Down events.
	for range 5 {
		events <- bfd.StateChange{
			LocalDiscr: 1,
			PeerAddr:   peer,
			OldState:   bfd.StateUp,
			NewState:   bfd.StateDown,
			Diag:       bfd.DiagControlTimeExpired,
			Timestamp:  time.Now(),
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for processing.
	waitForCalls(t, mock, 5)

	calls := mock.getCalls()
	if len(calls) != 5 {
		t.Errorf("expected 5 calls with dampening disabled, got %d", len(calls))
	}

	cancel()
	<-done
}

// -------------------------------------------------------------------------
// Dampening Unit Tests -- using fixed clock for determinism
// -------------------------------------------------------------------------

func TestDampenerShouldSuppressBasic(t *testing.T) {
	t.Parallel()

	// Use a fixed clock to eliminate floating-point decay between calls.
	fixedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 3,
		ReuseThreshold:    2,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}

	d := gobgp.NewDampener(cfg, slog.Default(),
		gobgp.WithClock(func() time.Time { return fixedTime }),
	)

	// First call: penalty=1 -> not suppressed.
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("should not suppress on first flap")
	}

	// Second call: penalty=2 -> not suppressed.
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("should not suppress on second flap")
	}

	// Third call: penalty=3 -> suppress threshold reached.
	if !d.ShouldSuppress("10.0.0.1") {
		t.Error("should suppress on third flap (threshold=3)")
	}

	// Fourth call: still suppressed.
	if !d.ShouldSuppress("10.0.0.1") {
		t.Error("should remain suppressed")
	}
}

func TestDampenerDecayOverTime(t *testing.T) {
	t.Parallel()

	var now atomic.Int64
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now.Store(baseTime.UnixNano())

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 3,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}

	d := gobgp.NewDampener(cfg, slog.Default(),
		gobgp.WithClock(func() time.Time {
			return time.Unix(0, now.Load())
		}),
	)

	// Accumulate penalty to 3 (suppressed).
	d.ShouldSuppress("10.0.0.1")
	d.ShouldSuppress("10.0.0.1")

	if !d.ShouldSuppress("10.0.0.1") {
		t.Fatal("should be suppressed at penalty=3")
	}

	// Advance time by 2 half-lives (30s). Penalty decays: 4 * 0.25 = 1.0
	// which is below the reuse threshold of 1 (we need < 1, so penalty 1.0
	// is not below threshold). Advance 3 half-lives to ensure below reuse.
	now.Store(baseTime.Add(45 * time.Second).UnixNano())

	// ShouldSuppressUp checks decay and unsuppresses if penalty < reuse.
	if d.ShouldSuppressUp("10.0.0.1") {
		t.Error("should be unsuppressed after 3 half-lives (penalty decayed below reuse)")
	}
}

func TestDampenerDifferentPeersIndependent(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}

	d := gobgp.NewDampener(cfg, slog.Default(),
		gobgp.WithClock(func() time.Time { return fixedTime }),
	)

	// Flap peer1 to suppression.
	d.ShouldSuppress("10.0.0.1")
	d.ShouldSuppress("10.0.0.1")

	// Peer2 should not be affected.
	if d.ShouldSuppress("10.0.0.2") {
		t.Error("peer2 should not be suppressed by peer1 flaps")
	}
}

func TestDampenerReset(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}

	d := gobgp.NewDampener(cfg, slog.Default(),
		gobgp.WithClock(func() time.Time { return fixedTime }),
	)

	// Flap to suppression.
	d.ShouldSuppress("10.0.0.1")
	d.ShouldSuppress("10.0.0.1")

	if !d.ShouldSuppress("10.0.0.1") {
		t.Error("should be suppressed before reset")
	}

	// Reset clears the penalty.
	d.Reset("10.0.0.1")

	// Should start fresh.
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("should not be suppressed after reset")
	}
}

func TestDampenerDisabled(t *testing.T) {
	t.Parallel()

	cfg := gobgp.DampeningConfig{
		Enabled: false,
	}

	d := gobgp.NewDampener(cfg, slog.Default())

	// Should never suppress when disabled.
	for range 100 {
		if d.ShouldSuppress("10.0.0.1") {
			t.Fatal("should never suppress when disabled")
		}
	}
}

func TestDampenerMaxSuppressTime(t *testing.T) {
	t.Parallel()

	var now atomic.Int64
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now.Store(baseTime.UnixNano())

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   30 * time.Second,
		HalfLife:          60 * time.Second, // Long half-life so decay alone won't unsuppress.
	}

	d := gobgp.NewDampener(cfg, slog.Default(),
		gobgp.WithClock(func() time.Time {
			return time.Unix(0, now.Load())
		}),
	)

	// Suppress the peer.
	d.ShouldSuppress("10.0.0.1")

	if !d.ShouldSuppress("10.0.0.1") {
		t.Fatal("should be suppressed at penalty >= 2")
	}

	// Advance past MaxSuppressTime.
	now.Store(baseTime.Add(31 * time.Second).UnixNano())

	// ShouldSuppress should unsuppress due to MaxSuppressTime.
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("should be unsuppressed after MaxSuppressTime exceeded")
	}
}

// -------------------------------------------------------------------------
// Full Integration Scenario -- Down/Up/Down cycle with dampening
// -------------------------------------------------------------------------

// TestHandlerFullCycleDamped tests a realistic BFD flap scenario.
// Uses a fractional threshold (2.5) so that exactly 2 Down events pass
// before the 3rd triggers suppression. The half-life is 15s, and the
// total test elapsed time is ~100ms, so the cumulative decay is less
// than 0.01 penalty units -- far below the 0.5 margin built into the
// threshold value.
func TestHandlerFullCycleDamped(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	handler := newTestHandler(t, mock, gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2.5,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	})

	events := make(chan bfd.StateChange, 20)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = handler.Run(ctx, events)
	}()

	peer := netip.MustParseAddr("10.0.0.1")

	// Send 4 Down/Up cycles. The first 2 Down events pass (penalties ~1, ~2).
	// The 3rd Down event reaches ~3 which is > 2.5 threshold, so it's suppressed.
	for i := range 4 {
		events <- bfd.StateChange{
			LocalDiscr: 1, PeerAddr: peer,
			OldState: bfd.StateUp, NewState: bfd.StateDown,
			Diag: bfd.DiagControlTimeExpired, Timestamp: time.Now(),
		}
		time.Sleep(10 * time.Millisecond)

		events <- bfd.StateChange{
			LocalDiscr: 1, PeerAddr: peer,
			OldState: bfd.StateDown, NewState: bfd.StateUp,
			Diag: bfd.DiagNone, Timestamp: time.Now(),
		}

		if i < 3 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	time.Sleep(200 * time.Millisecond)

	calls := mock.getCalls()

	disableCount := 0
	enableCount := 0

	for _, c := range calls {
		switch c.method {
		case methodDisablePeer:
			disableCount++
		case methodEnablePeer:
			enableCount++
		}
	}

	// Cycles 1-2: DisablePeer+EnablePeer each (penalties ~1, ~2).
	// Cycles 3-4: suppressed (penalties ~3+, all > 2.5).
	if disableCount != 2 {
		t.Errorf("expected 2 DisablePeer calls (before dampening), got %d", disableCount)
	}

	if enableCount != 2 {
		t.Errorf("expected 2 EnablePeer calls (before dampening), got %d", enableCount)
	}

	cancel()
	<-done
}

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// newTestHandler creates a Handler with the given mock and dampening config.
// All tests use the disable-peer strategy (the only supported strategy).
func newTestHandler(
	t *testing.T,
	client gobgp.Client,
	dampening gobgp.DampeningConfig,
) *gobgp.Handler {
	t.Helper()

	h, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client:    client,
		Strategy:  gobgp.StrategyDisablePeer,
		Dampening: dampening,
		Logger:    slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	return h
}

// waitForCalls waits until the mock has accumulated at least n calls,
// with a timeout to prevent test hangs.
func waitForCalls(t *testing.T, mock *mockClient, n int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		calls := mock.getCalls()
		if len(calls) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %d calls, got %d", n, len(mock.getCalls()))
}
