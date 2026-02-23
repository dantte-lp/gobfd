// RFC 7419 — Common Interval Support in BFD.
//
// RFC 7419 defines a set of common BFD timer interval values that all
// implementations SHOULD support: 3.3ms, 10ms, 20ms, 50ms, 100ms, 1s.
// Additionally, 10s is recommended for graceful restart support.
//
// Supporting these values prevents negotiation mismatches between
// software-based and hardware-based BFD implementations.

package bfd

import "time"

// CommonIntervals is the RFC 7419 Section 3 common interval set.
// All values are sorted ascending. An implementation should support
// all values equal to or larger than its fastest supported interval.
//
//nolint:gochecknoglobals // Lookup table is intentionally package-level.
var CommonIntervals = [...]time.Duration{
	3300 * time.Microsecond, // 3.3 ms — MPLS-TP (GR-253-CORE)
	10 * time.Millisecond,   // 10 ms — general consensus
	20 * time.Millisecond,   // 20 ms — software-based minimum
	50 * time.Millisecond,   // 50 ms — widely deployed
	100 * time.Millisecond,  // 100 ms — G.8013/Y.1731 reuse
	1 * time.Second,         // 1 s   — RFC 5880 slow rate
}

// GracefulRestartInterval is the recommended interval for graceful restart
// scenarios (RFC 7419 Section 3). With multiplier 255, this allows a
// detection timeout of 42.5 minutes.
const GracefulRestartInterval = 10 * time.Second

// IsCommonInterval reports whether d exactly matches one of the RFC 7419
// common interval values.
func IsCommonInterval(d time.Duration) bool {
	for _, ci := range CommonIntervals {
		if d == ci {
			return true
		}
	}
	return false
}

// AlignToCommonInterval rounds d UP to the nearest RFC 7419 common interval.
// If d is larger than the largest common interval (1s), it is returned
// unchanged — the caller may use any value above the common set.
// If d is zero or negative, it is returned unchanged.
func AlignToCommonInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	for _, ci := range CommonIntervals {
		if d <= ci {
			return ci
		}
	}
	// d exceeds 1s — return as-is, per RFC 7419: "free to support
	// additional values outside of the Common Interval set."
	return d
}

// NearestCommonInterval returns the closest RFC 7419 common interval
// to d. Ties are broken by choosing the smaller interval. If d is zero
// or negative, returns the smallest common interval (3.3ms).
func NearestCommonInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return CommonIntervals[0]
	}

	best := CommonIntervals[0]
	bestDelta := absDuration(d - best)

	for _, ci := range CommonIntervals[1:] {
		delta := absDuration(d - ci)
		if delta < bestDelta {
			best = ci
			bestDelta = delta
		}
	}

	return best
}

// absDuration returns the absolute value of a time.Duration.
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
