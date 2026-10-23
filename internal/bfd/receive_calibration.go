package bfd

import (
	"errors"
	"math"
	"time"
)

var errReceiveClockResolution = errors.New("native clock resolution unavailable")

const receiveCalibrationAssumption = "native realtime/monotonic clocks; constant offset; " +
	"each clock's errors stay in one fixed interval of width at most its nominal resolution during the run"

// ReceiveCalibration bounds software-clock pairing under its stated assumption.
// Nominal resolutions do not establish absolute physical clock accuracy.
type ReceiveCalibration struct {
	before, after         time.Time `json:"-"`
	AnchorUnixNS          int64     `json:"anchor_unix_ns"`
	SpanNS                int64     `json:"span_ns"`
	RealtimeResolutionNS  int64     `json:"realtime_resolution_ns"`
	MonotonicResolutionNS int64     `json:"monotonic_resolution_ns"`
	ConditionalBoundNS    int64     `json:"conditional_bound_ns"`
	Known                 bool      `json:"known"`
	Revoked               bool      `json:"revoked"`
	Assumption            string    `json:"assumption"`
	UnknownReason         string    `json:"unknown_reason,omitempty"`
}

func newReceiveCalibration(before, anchor, after time.Time, realtime, monotonic time.Duration) ReceiveCalibration {
	result := ReceiveCalibration{
		RealtimeResolutionNS: int64(realtime), MonotonicResolutionNS: int64(monotonic),
		Assumption:    receiveCalibrationAssumption,
		UnknownReason: "invalid or unavailable clock pairing bracket",
	}
	wall, valid := receiveUnixNano(anchor)
	if !valid || wall <= 0 || !hasReceiveMonotonic(before) || !hasReceiveMonotonic(anchor) ||
		!hasReceiveMonotonic(after) || anchor.Before(before) || anchor.After(after) {
		return result
	}
	result.AnchorUnixNS = wall
	span := after.Sub(before)
	result.SpanNS = int64(span)
	bound, valid := receiveCalibrationErrorBound(span, realtime, monotonic)
	if !valid {
		return result
	}
	result.ConditionalBoundNS = int64(bound)
	result.before, result.after = before, after
	result.Known = true
	result.UnknownReason = ""
	return result
}

// CoveredBy requires process-local monotonic provenance for the entire bracket.
// A JSON round trip intentionally cannot recreate this lifecycle evidence.
func (c ReceiveCalibration) CoveredBy(start, end time.Time) bool {
	return hasReceiveMonotonic(start) && hasReceiveMonotonic(end) &&
		hasReceiveMonotonic(c.before) && hasReceiveMonotonic(c.after) &&
		!end.Before(start) && !c.before.Before(start) && !c.after.After(end)
}

func receiveCalibrationErrorBound(span, realtime, monotonic time.Duration) (time.Duration, bool) {
	if span < 0 || realtime <= 0 || monotonic <= 0 || span > math.MaxInt64-realtime ||
		span+realtime > math.MaxInt64-monotonic {
		return 0, false
	}
	return span + realtime + monotonic, true
}

// Calibration returns a value copy; revocation invalidates its conditional bound.
func (c *ReceiveClock) Calibration() ReceiveCalibration {
	if c == nil {
		return ReceiveCalibration{UnknownReason: "receive clock is unavailable"}
	}
	result := c.calibration
	if !result.Known && result.UnknownReason == "" {
		result.UnknownReason = "receive clock calibration is unavailable"
	}
	if c.revoked.Load() {
		result.Known = false
		result.Revoked = true
		result.UnknownReason = "receive clock epoch revoked"
	}
	return result
}

// ConditionalInterval requires independent evidence for the stated clock model.
// It does not by itself qualify a workload run or establish its clock epoch.
func (c ReceiveCalibration) ConditionalInterval(latency time.Duration) (time.Duration, time.Duration, bool) {
	if !c.Known || c.Revoked || c.UnknownReason != "" || c.AnchorUnixNS <= 0 ||
		c.Assumption != receiveCalibrationAssumption {
		return 0, 0, false
	}
	bound, valid := receiveCalibrationErrorBound(time.Duration(c.SpanNS),
		time.Duration(c.RealtimeResolutionNS), time.Duration(c.MonotonicResolutionNS))
	if !valid || int64(bound) != c.ConditionalBoundNS || latency < 0 || latency > math.MaxInt64-bound {
		return 0, 0, false
	}
	return max(0, latency-bound), latency + bound, true
}
