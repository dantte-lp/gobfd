package gobgp_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/gobgp"
)

// -------------------------------------------------------------------------
// Dampening Config Tests
// -------------------------------------------------------------------------

func TestDefaultDampeningConfig(t *testing.T) {
	t.Parallel()

	cfg := gobgp.DefaultDampeningConfig()

	if cfg.Enabled {
		t.Error("default dampening should be disabled")
	}
	if cfg.SuppressThreshold != 3 {
		t.Errorf("SuppressThreshold = %f, want 3", cfg.SuppressThreshold)
	}
	if cfg.ReuseThreshold != 2 {
		t.Errorf("ReuseThreshold = %f, want 2", cfg.ReuseThreshold)
	}
	if cfg.MaxSuppressTime != 60*time.Second {
		t.Errorf("MaxSuppressTime = %v, want 60s", cfg.MaxSuppressTime)
	}
	if cfg.HalfLife != 15*time.Second {
		t.Errorf("HalfLife = %v, want 15s", cfg.HalfLife)
	}
}

// -------------------------------------------------------------------------
// Dampener Tests — disabled mode
// -------------------------------------------------------------------------

func TestDampener_DisabledAlwaysFalse(t *testing.T) {
	t.Parallel()

	d := gobgp.NewDampener(gobgp.DampeningConfig{Enabled: false}, slog.Default())

	for range 10 {
		if d.ShouldSuppress("10.0.0.1") {
			t.Fatal("ShouldSuppress should return false when disabled")
		}
	}
}

func TestDampener_DisabledShouldSuppressUpAlwaysFalse(t *testing.T) {
	t.Parallel()

	d := gobgp.NewDampener(gobgp.DampeningConfig{Enabled: false}, slog.Default())

	if d.ShouldSuppressUp("10.0.0.1") {
		t.Fatal("ShouldSuppressUp should return false when disabled")
	}
}

// -------------------------------------------------------------------------
// Dampener Tests — enabled: basic suppression
// -------------------------------------------------------------------------

func TestDampener_SuppressAfterThreshold(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 3,
		ReuseThreshold:    2,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// First two Down events: penalty 1, 2 — below threshold 3.
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("event 1: should not suppress (penalty=1 < threshold=3)")
	}
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("event 2: should not suppress (penalty=2 < threshold=3)")
	}
	// Third event: penalty reaches 3 — should suppress.
	if !d.ShouldSuppress("10.0.0.1") {
		t.Error("event 3: should suppress (penalty=3 >= threshold=3)")
	}
	// Fourth event: still suppressed.
	if !d.ShouldSuppress("10.0.0.1") {
		t.Error("event 4: should still be suppressed")
	}
}

func TestDampener_ShouldSuppressUpWhileSuppressed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Build up penalty.
	d.ShouldSuppress("10.0.0.1") // penalty=1
	d.ShouldSuppress("10.0.0.1") // penalty=2 → suppressed

	// Up should also be suppressed.
	if !d.ShouldSuppressUp("10.0.0.1") {
		t.Error("ShouldSuppressUp should return true while peer is suppressed")
	}
}

func TestDampener_ShouldSuppressUpUnknownPeer(t *testing.T) {
	t.Parallel()

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 3,
		ReuseThreshold:    2,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default())

	// Unknown peer should not be suppressed.
	if d.ShouldSuppressUp("192.168.1.1") {
		t.Error("ShouldSuppressUp should return false for unknown peer")
	}
}

// -------------------------------------------------------------------------
// Dampener Tests — exponential decay
// -------------------------------------------------------------------------

func TestDampener_DecayBelowReuseUnsuppresses(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 3,
		ReuseThreshold:    1,
		MaxSuppressTime:   300 * time.Second,
		HalfLife:          10 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Accumulate penalty to 4 (above threshold 3).
	for range 4 {
		d.ShouldSuppress("10.0.0.1")
	}

	// Now advance time enough for penalty to decay below reuse threshold.
	// penalty=4, halfLife=10s. After 20s: 4 * 0.25 = 1.0.
	// After 21s: ~0.93 < reuse threshold 1.
	now = now.Add(21 * time.Second)

	// Up event should now unsuppress (penalty < reuse threshold).
	if d.ShouldSuppressUp("10.0.0.1") {
		t.Error("ShouldSuppressUp should return false after decay below reuse threshold")
	}
}

func TestDampener_DecayHalvesPenalty(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 100, // High threshold so we don't suppress.
		ReuseThreshold:    1,
		MaxSuppressTime:   300 * time.Second,
		HalfLife:          10 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Add 8 events → penalty = 8.
	for range 8 {
		d.ShouldSuppress("10.0.0.1")
	}

	// Advance 1 half-life (10s): penalty should be ~4 + 1 (new event) = ~5.
	now = now.Add(10 * time.Second)

	// The next ShouldSuppress will decay to ~4, then add 1 → ~5.
	// Not suppressed (below threshold 100).
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("should not suppress below high threshold")
	}

	// Advance 3 more half-lives: ~5 * 0.125 = ~0.625 → clamped to 0.
	now = now.Add(30 * time.Second)

	// After long decay, penalty should be near zero. New event adds 1.
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("penalty should have decayed to near zero")
	}
}

// -------------------------------------------------------------------------
// Dampener Tests — MaxSuppressTime
// -------------------------------------------------------------------------

func TestDampener_MaxSuppressTimeUnsuppresses(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   30 * time.Second,
		HalfLife:          300 * time.Second, // Very long, so penalty barely decays.
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Trigger suppression.
	d.ShouldSuppress("10.0.0.1") // penalty=1
	d.ShouldSuppress("10.0.0.1") // penalty=2 → suppressed

	// Still suppressed before MaxSuppressTime.
	now = now.Add(29 * time.Second)
	if !d.ShouldSuppress("10.0.0.1") {
		t.Error("should still be suppressed before MaxSuppressTime")
	}

	// After MaxSuppressTime, unsuppress on Down event.
	now = now.Add(2 * time.Second) // total 31s > 30s MaxSuppressTime
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("should unsuppress after MaxSuppressTime")
	}
}

func TestDampener_MaxSuppressTimeUnsuppressesUpEvent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   20 * time.Second,
		HalfLife:          300 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Trigger suppression.
	d.ShouldSuppress("10.0.0.1")
	d.ShouldSuppress("10.0.0.1")

	// Advance past MaxSuppressTime.
	now = now.Add(21 * time.Second)
	if d.ShouldSuppressUp("10.0.0.1") {
		t.Error("ShouldSuppressUp should unsuppress after MaxSuppressTime")
	}
}

// -------------------------------------------------------------------------
// Dampener Tests — per-peer isolation
// -------------------------------------------------------------------------

func TestDampener_PerPeerIsolation(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Suppress peer A.
	d.ShouldSuppress("10.0.0.1")
	d.ShouldSuppress("10.0.0.1")

	// Peer B should not be affected.
	if d.ShouldSuppress("10.0.0.2") {
		t.Error("peer B should not be suppressed by peer A's flaps")
	}
}

// -------------------------------------------------------------------------
// Dampener Tests — Reset
// -------------------------------------------------------------------------

func TestDampener_Reset(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 2,
		ReuseThreshold:    1,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Suppress peer.
	d.ShouldSuppress("10.0.0.1")
	d.ShouldSuppress("10.0.0.1")

	// Reset clears all state.
	d.Reset("10.0.0.1")

	// After reset, first event should not suppress.
	if d.ShouldSuppress("10.0.0.1") {
		t.Error("should not suppress after reset")
	}
}

func TestDampener_ResetUnknownPeerNoOp(t *testing.T) {
	t.Parallel()

	d := gobgp.NewDampener(gobgp.DampeningConfig{Enabled: true}, slog.Default())

	// Should not panic.
	d.Reset("unknown-peer")
}

// -------------------------------------------------------------------------
// Dampener Tests — edge cases
// -------------------------------------------------------------------------

func TestDampener_ZeroHalfLifeNoDecay(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 3,
		ReuseThreshold:    2,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          0, // Zero half-life: no decay.
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))

	// Build penalty.
	d.ShouldSuppress("10.0.0.1") // penalty=1
	d.ShouldSuppress("10.0.0.1") // penalty=2

	// Even after time passes, penalty should not decay.
	now = now.Add(time.Hour)
	d.ShouldSuppress("10.0.0.1") // penalty=3 → suppressed

	if !d.ShouldSuppressUp("10.0.0.1") {
		t.Error("should be suppressed with zero half-life (no decay)")
	}
}

func TestDampener_WithClockOption(t *testing.T) {
	t.Parallel()

	called := false
	clock := func() time.Time {
		called = true
		return time.Now()
	}

	cfg := gobgp.DampeningConfig{
		Enabled:           true,
		SuppressThreshold: 100,
		HalfLife:          time.Second,
	}
	d := gobgp.NewDampener(cfg, slog.Default(), gobgp.WithClock(clock))
	d.ShouldSuppress("10.0.0.1")

	if !called {
		t.Error("WithClock function was not called")
	}
}
