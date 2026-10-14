package bfd

import (
	"net/netip"
	"time"
)

// export_test.go exposes internal session methods for benchmarking.
// These exports exist only in test builds and are not part of the public API.

// BenchApplyJitter exposes s.applyJitter for benchmark tests.
// The session-local PRNG avoids global atomic contention.
func (s *Session) BenchApplyJitter(interval time.Duration) time.Duration {
	return s.applyJitter(interval)
}

// BenchCalcTxIntervalHot exposes s.calcTxIntervalHot for benchmark tests.
// Uses cachedState to avoid atomic load on hot path.
func (s *Session) BenchCalcTxIntervalHot() time.Duration {
	return s.calcTxIntervalHot()
}

// BenchCalcDetectionTimeHot exposes s.calcDetectionTimeHot for benchmark tests.
// Uses cachedState to avoid atomic load on hot path.
func (s *Session) BenchCalcDetectionTimeHot() time.Duration {
	return s.calcDetectionTimeHot()
}

// WithUnsolicitedConnectedPrefix replaces the host interface-address lookup
// used by the RFC 9468 Section 2 subnet check, so tests can use documentation
// addresses. It must follow WithUnsolicitedPolicy.
func WithUnsolicitedConnectedPrefix(prefixes ...netip.Prefix) ManagerOption {
	return func(m *Manager) {
		if m.unsolicited == nil {
			return
		}
		m.unsolicited.connectedPrefix = func(local netip.Addr) (netip.Prefix, bool) {
			for _, p := range prefixes {
				if p.Contains(local) {
					return p.Masked(), true
				}
			}
			return netip.Prefix{}, false
		}
	}
}
