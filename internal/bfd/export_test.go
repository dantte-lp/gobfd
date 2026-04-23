package bfd

import "time"

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
