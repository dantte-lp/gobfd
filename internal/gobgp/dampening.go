package gobgp

import (
	"log/slog"
	"math"
	"sync"
	"time"
)

// -------------------------------------------------------------------------
// RFC 5882 Section 3.2 — BFD Flap Dampening
// -------------------------------------------------------------------------
//
// "BFD is a relatively aggressive mechanism for detecting failures.
//  Because of this, implementations SHOULD provide a flap dampening
//  mechanism to prevent rapid oscillation of the BFD session from
//  causing excessive route churn."
//
// The dampening algorithm follows the classic route flap dampening model
// (RFC 2439) adapted for BFD: each Down event accumulates a penalty
// that decays exponentially. When the penalty exceeds the suppress
// threshold, subsequent events are suppressed until the penalty decays
// below the reuse threshold.

// -------------------------------------------------------------------------
// Dampening Configuration
// -------------------------------------------------------------------------

// DampeningConfig configures the BFD flap dampening parameters.
//
// The algorithm tracks a penalty counter per peer address. Each BFD Down
// event adds 1 to the penalty. The penalty decays exponentially with the
// configured half-life. When the penalty exceeds SuppressThreshold, events
// are suppressed. When it decays below ReuseThreshold, events are allowed
// again.
type DampeningConfig struct {
	// Enabled controls whether flap dampening is active.
	// When false, all state changes are passed through immediately.
	Enabled bool

	// SuppressThreshold is the penalty value above which events are suppressed.
	// Typical value: 3 (suppress after 3 rapid flaps).
	SuppressThreshold float64

	// ReuseThreshold is the penalty value below which suppressed events
	// are allowed again. Must be less than SuppressThreshold.
	// Typical value: 2.
	ReuseThreshold float64

	// MaxSuppressTime is the maximum duration events can be suppressed
	// for a single peer. After this time, the peer is unsuppressed
	// regardless of penalty level.
	// Typical value: 60s.
	MaxSuppressTime time.Duration

	// HalfLife is the time for the penalty to decay by half.
	// Typical value: 15s.
	HalfLife time.Duration
}

// DefaultDampeningConfig returns a sensible default dampening configuration
// suitable for production DC/ISP deployments.
//
// These values balance responsiveness (detect real failures quickly) with
// stability (suppress flapping peers from churning BGP routes).
func DefaultDampeningConfig() DampeningConfig {
	return DampeningConfig{
		Enabled:           false,
		SuppressThreshold: 3,
		ReuseThreshold:    2,
		MaxSuppressTime:   60 * time.Second,
		HalfLife:          15 * time.Second,
	}
}

// -------------------------------------------------------------------------
// Dampener — per-peer penalty tracker
// -------------------------------------------------------------------------

// Dampener tracks flap penalties per peer and decides whether state changes
// should be suppressed. Thread-safe for concurrent access from the handler
// goroutine.
type Dampener struct {
	cfg    DampeningConfig
	peers  map[string]*peerPenalty
	mu     sync.Mutex
	logger *slog.Logger
	now    func() time.Time // injectable clock for testing
}

// peerPenalty holds the dampening state for a single peer.
type peerPenalty struct {
	// penalty is the current accumulated penalty value.
	penalty float64

	// lastUpdate is when the penalty was last updated (for decay calculation).
	lastUpdate time.Time

	// suppressed is true when the penalty exceeds the suppress threshold.
	suppressed bool

	// suppressedSince is when suppression started. Used to enforce
	// MaxSuppressTime.
	suppressedSince time.Time
}

// DampenerOption configures optional Dampener parameters.
type DampenerOption func(*Dampener)

// WithClock sets a custom time function for the dampener. This is used in
// tests to control time progression without sleeping.
func WithClock(now func() time.Time) DampenerOption {
	return func(d *Dampener) {
		d.now = now
	}
}

// NewDampener creates a new flap dampener with the given configuration.
func NewDampener(cfg DampeningConfig, logger *slog.Logger, opts ...DampenerOption) *Dampener {
	d := &Dampener{
		cfg:    cfg,
		peers:  make(map[string]*peerPenalty),
		logger: logger.With(slog.String("component", "gobgp.dampener")),
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// ShouldSuppress returns true if the given peer's Down event should be
// suppressed due to excessive flapping. It also records the Down event
// by incrementing the penalty.
//
// If dampening is disabled, always returns false.
//
// The algorithm:
//  1. Decay existing penalty based on elapsed time since last update.
//  2. Add 1.0 to the penalty (one Down event).
//  3. If penalty > SuppressThreshold and not yet suppressed, start suppression.
//  4. If suppressed and MaxSuppressTime exceeded, unsuppress.
//  5. Return the suppressed state.
func (d *Dampener) ShouldSuppress(peerAddr string) bool {
	if !d.cfg.Enabled {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()

	pp := d.getOrCreatePeer(peerAddr, now)
	d.decayPenalty(pp, now)

	// Record the Down event.
	pp.penalty += 1.0
	pp.lastUpdate = now

	// Check if MaxSuppressTime has been exceeded.
	if pp.suppressed && now.Sub(pp.suppressedSince) >= d.cfg.MaxSuppressTime {
		d.unsuppress(pp, peerAddr)
		return false
	}

	// Check if penalty exceeds suppress threshold.
	if !pp.suppressed && pp.penalty >= d.cfg.SuppressThreshold {
		pp.suppressed = true
		pp.suppressedSince = now
		d.logger.Warn("peer suppressed due to flap dampening",
			slog.String("peer", peerAddr),
			slog.Float64("penalty", pp.penalty),
			slog.Float64("threshold", d.cfg.SuppressThreshold),
		)
	}

	return pp.suppressed
}

// ShouldSuppressUp returns true if an Up event for the given peer should
// be suppressed. Up events are suppressed while the peer is in suppressed
// state to prevent partial recovery signals.
//
// If dampening is disabled, always returns false.
func (d *Dampener) ShouldSuppressUp(peerAddr string) bool {
	if !d.cfg.Enabled {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()

	pp, exists := d.peers[peerAddr]
	if !exists {
		return false
	}

	d.decayPenalty(pp, now)

	// Check if MaxSuppressTime has been exceeded.
	if pp.suppressed && now.Sub(pp.suppressedSince) >= d.cfg.MaxSuppressTime {
		d.unsuppress(pp, peerAddr)
		return false
	}

	// Check if penalty has decayed below reuse threshold.
	if pp.suppressed && pp.penalty < d.cfg.ReuseThreshold {
		d.unsuppress(pp, peerAddr)
		return false
	}

	return pp.suppressed
}

// Reset removes the penalty tracking for a peer. Used when a peer is
// explicitly removed from configuration.
func (d *Dampener) Reset(peerAddr string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.peers, peerAddr)
}

// -------------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------------

// getOrCreatePeer returns the penalty state for a peer, creating it if needed.
// Caller must hold d.mu.
func (d *Dampener) getOrCreatePeer(peerAddr string, now time.Time) *peerPenalty {
	pp, exists := d.peers[peerAddr]
	if !exists {
		pp = &peerPenalty{
			lastUpdate: now,
		}
		d.peers[peerAddr] = pp
	}
	return pp
}

// decayPenalty applies exponential decay to the penalty based on elapsed time.
// Caller must hold d.mu.
//
// Decay formula: penalty = penalty * 2^(-elapsed/halfLife)
// This ensures the penalty halves every halfLife duration.
func (d *Dampener) decayPenalty(pp *peerPenalty, now time.Time) {
	if d.cfg.HalfLife <= 0 || pp.penalty == 0 {
		return
	}

	elapsed := now.Sub(pp.lastUpdate)
	if elapsed <= 0 {
		return
	}

	// Exponential decay: penalty * 2^(-elapsed/halfLife)
	halfLives := float64(elapsed) / float64(d.cfg.HalfLife)
	decayFactor := math.Pow(0.5, halfLives)
	pp.penalty *= decayFactor
	pp.lastUpdate = now

	// Clamp near-zero values to avoid floating-point noise.
	if pp.penalty < 0.001 {
		pp.penalty = 0
	}
}

// unsuppress clears the suppression state for a peer.
// Caller must hold d.mu.
func (d *Dampener) unsuppress(pp *peerPenalty, peerAddr string) {
	pp.suppressed = false
	pp.suppressedSince = time.Time{}
	pp.penalty = 0

	d.logger.Info("peer unsuppressed, flap dampening cleared",
		slog.String("peer", peerAddr),
	)
}
