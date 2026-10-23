package bfd

import (
	"math"
	"sync/atomic"
	"time"
)

// This is an observed clock-consistency bound, not timestamp precision.
const receiveClockTolerance = time.Millisecond

// ReceiveTimingOutcome describes measurement validity, not packet acceptance.
type ReceiveTimingOutcome uint8

// Measurement outcomes are independent of packet admission and FSM policy.
const (
	ReceiveTimingUnavailable ReceiveTimingOutcome = iota
	ReceiveTimingValid
	ReceiveTimingInvalid
	ReceiveTimingBeforeCalibration
	ReceiveTimingFuture
	ReceiveTimingClockStep
	ReceiveTimingOutcomeCount
)

func (o ReceiveTimingOutcome) String() string {
	switch o {
	case ReceiveTimingUnavailable:
		return "unavailable"
	case ReceiveTimingValid:
		return "valid"
	case ReceiveTimingInvalid:
		return "invalid"
	case ReceiveTimingBeforeCalibration:
		return "before_calibration"
	case ReceiveTimingFuture:
		return "future"
	case ReceiveTimingClockStep:
		return "clock_step"
	default:
		return "unknown"
	}
}

// ReceiveClock holds a listener's immutable calibration and shared revocation.
type ReceiveClock struct {
	anchor      time.Time
	unixNano    int64
	revoked     atomic.Bool
	calibration ReceiveCalibration
}

// NewReceiveClock establishes a paired realtime/monotonic calibration.
func NewReceiveClock() *ReceiveClock {
	before := time.Now()
	now := time.Now()
	after := time.Now()
	wall, _ := receiveUnixNano(now)
	realtime, monotonic, err := receiveClockResolutions()
	calibration := newReceiveCalibration(before, now, after, realtime, monotonic)
	if err != nil {
		calibration.Known = false
		calibration.UnknownReason = err.Error()
	}
	return &ReceiveClock{anchor: now, unixNano: wall, calibration: calibration}
}

// Capture converts only samples from this calibration's observed clock epoch.
func (c *ReceiveClock) Capture(kernelUnixNano int64) ReceiveTiming {
	if kernelUnixNano == 0 {
		return ReceiveTiming{}
	}
	if c == nil {
		return ReceiveTiming{outcome: ReceiveTimingInvalid}
	}
	now := time.Now()
	wall, _ := receiveUnixNano(now)
	return c.capture(kernelUnixNano, now, wall)
}

// ReceiveTiming carries a measurement through queue admission to owner commit.
type ReceiveTiming struct {
	received     time.Time
	read         time.Time
	readUnixNano int64
	outcome      ReceiveTimingOutcome
	epoch        *ReceiveClock
}

func (c *ReceiveClock) capture(kernel int64, now time.Time, wall int64) ReceiveTiming {
	result := ReceiveTiming{outcome: ReceiveTimingInvalid}
	if kernel == 0 {
		return ReceiveTiming{}
	}
	if kernel < 0 || wall <= 0 || c.unixNano <= 0 || !hasReceiveMonotonic(c.anchor) || !hasReceiveMonotonic(now) {
		return result
	}
	if c.revoked.Load() {
		result.outcome = ReceiveTimingClockStep
		return result
	}
	elapsed := now.Sub(c.anchor)
	if elapsed < 0 || elapsed == time.Duration(math.MaxInt64) {
		return result
	}
	if receiveClockStepped(c.unixNano, wall, elapsed) {
		// Queued timestamps cannot identify their epoch after a backward step.
		c.revoked.Store(true)
		result.outcome = ReceiveTimingClockStep
		return result
	}
	if kernel < c.unixNano {
		result.outcome = ReceiveTimingBeforeCalibration
		return result
	}
	if kernel > wall {
		result.outcome = ReceiveTimingFuture
		return result
	}
	received := c.anchor.Add(time.Duration(kernel - c.unixNano))
	if !hasReceiveMonotonic(received) {
		return result
	}
	if received.After(now) {
		result.outcome = ReceiveTimingFuture
		return result
	}
	return ReceiveTiming{received: received, read: now, readUnixNano: wall, outcome: ReceiveTimingValid, epoch: c}
}

func (r ReceiveTiming) latency(now time.Time, wall int64) (time.Duration, ReceiveTimingOutcome) {
	if r.outcome != ReceiveTimingValid {
		return 0, r.outcome
	}
	if r.epoch == nil {
		return 0, ReceiveTimingInvalid
	}
	if r.epoch.revoked.Load() {
		return 0, ReceiveTimingClockStep
	}
	if wall <= 0 || !hasReceiveMonotonic(now) {
		return 0, ReceiveTimingInvalid
	}
	elapsed := now.Sub(r.read)
	if elapsed < 0 || elapsed == time.Duration(math.MaxInt64) {
		return 0, ReceiveTimingInvalid
	}
	if receiveClockStepped(r.readUnixNano, wall, elapsed) {
		r.epoch.revoked.Store(true)
		return 0, ReceiveTimingClockStep
	}
	latency := now.Sub(r.received)
	if latency < 0 || latency == time.Duration(math.MaxInt64) {
		return 0, ReceiveTimingInvalid
	}
	return latency, ReceiveTimingValid
}

func receiveClockStepped(before, after int64, monotonic time.Duration) bool {
	wall := time.Duration(after - before) // Both positive int64 values: subtraction cannot overflow.
	if wall < 0 {
		return true
	}
	if wall >= monotonic {
		return wall-monotonic > receiveClockTolerance
	}
	return monotonic-wall > receiveClockTolerance
}

func hasReceiveMonotonic(t time.Time) bool {
	return t != t.Round(0)
}

func receiveUnixNano(t time.Time) (int64, bool) {
	const nanosPerSecond = int64(time.Second)
	sec, nsec := t.Unix(), int64(t.Nanosecond())
	if sec < 0 || sec > (math.MaxInt64-nsec)/nanosPerSecond {
		return 0, false
	}
	return sec*nanosPerSecond + nsec, true
}

func (s *Session) recordReceiveCommit(timing ReceiveTiming) {
	latency, outcome := time.Duration(0), timing.outcome
	if outcome == ReceiveTimingValid {
		now := time.Now()
		wall, _ := receiveUnixNano(now)
		latency, outcome = timing.latency(now, wall)
	}
	s.recordDelivery(DeliveryPacketOwner, DeliveryCommitted)
	s.metrics.ObserveReceiveTiming(outcome, latency)
}
