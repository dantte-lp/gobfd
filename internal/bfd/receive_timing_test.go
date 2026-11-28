package bfd

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestReceiveClockConversion(t *testing.T) {
	t.Parallel()
	base := time.Now()
	wall := base.UnixNano()
	for _, tc := range []struct {
		name      string
		raw       int64
		wallDelta time.Duration
		want      ReceiveTimingOutcome
	}{
		{"valid", wall + int64(2*time.Millisecond), 10 * time.Millisecond, ReceiveTimingValid},
		{"absent", 0, 10 * time.Millisecond, ReceiveTimingUnavailable},
		{"negative", -1, 10 * time.Millisecond, ReceiveTimingInvalid},
		{"beforeCalibration", wall - 1, 10 * time.Millisecond, ReceiveTimingBeforeCalibration},
		{"future", wall + int64(11*time.Millisecond), 10 * time.Millisecond, ReceiveTimingFuture},
		{"largestTimestamp", math.MaxInt64, 10 * time.Millisecond, ReceiveTimingFuture},
		{"forwardStep", wall + 1, 12 * time.Millisecond, ReceiveTimingClockStep},
		{"toleranceBoundary", wall + int64(2*time.Millisecond), 11 * time.Millisecond, ReceiveTimingValid},
		{"backwardStep", wall + 1, -time.Second, ReceiveTimingClockStep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := ReceiveClock{anchor: base, unixNano: wall}
			timing := clock.capture(tc.raw, base.Add(10*time.Millisecond), wall+int64(tc.wallDelta))
			latency, outcome := timing.latency(base.Add(17*time.Millisecond), wall+int64(17*time.Millisecond))
			if outcome != tc.want {
				t.Fatalf("outcome=%s, want %s", outcome, tc.want)
			}
			if outcome == ReceiveTimingValid && latency != 15*time.Millisecond {
				t.Fatalf("committed latency=%v, want 15ms", latency)
			}
		})
	}
}

func TestReceiveClockRevokesSteppedCalibration(t *testing.T) {
	t.Parallel()
	base := time.Now()
	wall := base.UnixNano()
	clock := ReceiveClock{anchor: base, unixNano: wall}
	first := clock.capture(wall+1, base.Add(time.Second), wall+int64(2*time.Second))
	second := clock.capture(wall+int64(3*time.Second), base.Add(4*time.Second), wall+int64(4*time.Second))
	for _, timing := range []ReceiveTiming{first, second} {
		_, outcome := timing.latency(base.Add(5*time.Second), wall+int64(5*time.Second))
		if outcome != ReceiveTimingClockStep {
			t.Fatalf("revoked calibration outcome=%s, want clock_step", outcome)
		}
	}
}

func TestReceiveTimingRejectsCommitClockStep(t *testing.T) {
	t.Parallel()
	base := time.Now()
	wall := base.UnixNano()
	for _, delta := range []time.Duration{-time.Second, time.Second} {
		t.Run(delta.String(), func(t *testing.T) {
			t.Parallel()
			clock := ReceiveClock{anchor: base, unixNano: wall}
			timing := clock.capture(wall+int64(time.Millisecond), base.Add(2*time.Millisecond), wall+int64(2*time.Millisecond))
			_, outcome := timing.latency(base.Add(3*time.Millisecond), wall+int64(3*time.Millisecond+delta))
			if outcome != ReceiveTimingClockStep {
				t.Fatalf("commit clock step outcome=%s", outcome)
			}
		})
	}
}

func TestReceiveClockRejectsInvalidCalibration(t *testing.T) {
	t.Parallel()
	base := time.Now()
	for _, anchor := range []time.Time{{}, base.Round(0)} {
		clock := ReceiveClock{anchor: anchor, unixNano: base.UnixNano()}
		timing := clock.capture(base.UnixNano()+1, base.Add(time.Millisecond), base.UnixNano()+int64(time.Millisecond))
		_, outcome := timing.latency(base.Add(2*time.Millisecond), base.UnixNano()+int64(2*time.Millisecond))
		if outcome != ReceiveTimingInvalid {
			t.Fatalf("invalid calibration outcome=%s", outcome)
		}
	}
}

func TestReceiveTimingCommitRevokesEpoch(t *testing.T) {
	t.Parallel()
	base := time.Now()
	wall := base.UnixNano()
	clock := ReceiveClock{anchor: base, unixNano: wall}
	first := clock.capture(wall+1, base.Add(time.Millisecond), wall+int64(time.Millisecond))
	second := clock.capture(wall+2, base.Add(2*time.Millisecond), wall+int64(2*time.Millisecond))
	_, firstOutcome := first.latency(base.Add(3*time.Millisecond), wall+int64(time.Second))
	if firstOutcome != ReceiveTimingClockStep {
		t.Fatal("commit failed to detect clock step")
	}
	_, secondOutcome := second.latency(base.Add(4*time.Millisecond), wall+int64(4*time.Millisecond))
	if secondOutcome != ReceiveTimingClockStep {
		t.Fatalf("in-flight sample survived revoked epoch: %s", secondOutcome)
	}
}

func TestReceiveUnixNanoBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		sec, nsec, want int64
		valid           bool
	}{
		{9_223_372_036, 854_775_807, math.MaxInt64, true},
		{9_223_372_036, 854_775_808, 0, false},
		{9_223_372_037, 0, 0, false},
		{-1, 0, 0, false},
	} {
		got, valid := receiveUnixNano(time.Unix(tc.sec, tc.nsec))
		if got != tc.want || valid != tc.valid {
			t.Fatalf("UnixNano boundary=(%d,%t), want(%d,%t)", got, valid, tc.want, tc.valid)
		}
	}
}

func TestReceiveClockConcurrentRevocation(t *testing.T) {
	t.Parallel()
	base := time.Now()
	wall := base.UnixNano()
	clock := ReceiveClock{anchor: base, unixNano: wall}
	timing := clock.capture(wall+1, base.Add(time.Millisecond), wall+int64(time.Millisecond))
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 100 {
				_, outcome := timing.latency(base.Add(2*time.Millisecond), wall+int64(time.Second))
				if outcome != ReceiveTimingClockStep {
					t.Errorf("concurrent revocation=%s", outcome)
				}
			}
		})
	}
	workers.Wait()
	got := clock.capture(wall+1, base.Add(time.Millisecond), wall+int64(time.Millisecond))
	if got.outcome != ReceiveTimingClockStep {
		t.Fatal("receive side did not observe owner revocation")
	}
}
