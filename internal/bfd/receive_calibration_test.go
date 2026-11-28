package bfd

import (
	"math"
	"testing"
	"time"
)

func TestReceiveCalibrationCheckedBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		span, real, mono time.Duration
		want             time.Duration
		valid            bool
	}{
		{"measured", 200, 2, 4, 206, true},
		{"same_tick", 0, 1, 1, 2, true},
		{"negative_span", -1, 1, 1, 0, false},
		{"missing_resolution", 200, 0, 1, 0, false},
		{"negative_resolution", 200, 1, -1, 0, false},
		{"overflow_real", math.MaxInt64, 1, 1, 0, false},
		{"overflow_mono", math.MaxInt64 - 1, 1, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, valid := receiveCalibrationErrorBound(tc.span, tc.real, tc.mono)
			if got != tc.want || valid != tc.valid {
				t.Fatalf("bound=%d valid=%v, want %d/%v", got, valid, tc.want, tc.valid)
			}
		})
	}
}

func TestReceiveCalibrationConditionalInterval(t *testing.T) {
	t.Parallel()
	base := time.Now()
	calibration := newReceiveCalibration(base, base.Add(100*time.Nanosecond),
		base.Add(200*time.Nanosecond), 2, 4)
	if !calibration.Known || calibration.SpanNS != 200 || calibration.ConditionalBoundNS != 206 ||
		calibration.Assumption == "" {
		t.Fatalf("missing measured calibration or assumption: %+v", calibration)
	}
	for _, tc := range []struct {
		latency, low, high time.Duration
		valid              bool
	}{
		{1000, 794, 1206, true},
		{100, 0, 306, true},
		{-1, 0, 0, false},
		{math.MaxInt64, 0, 0, false},
	} {
		low, high, valid := calibration.ConditionalInterval(tc.latency)
		if low != tc.low || high != tc.high || valid != tc.valid {
			t.Fatalf("latency=%d interval=%d..%d/%v, want %d..%d/%v",
				tc.latency, low, high, valid, tc.low, tc.high, tc.valid)
		}
	}
	inconsistent := calibration
	inconsistent.ConditionalBoundNS = 1
	for _, invalid := range []ReceiveCalibration{
		inconsistent,
		{},
		{Known: true},
		{Known: true, SpanNS: 200, RealtimeResolutionNS: 2, MonotonicResolutionNS: 4, ConditionalBoundNS: 1},
		newReceiveCalibration(base.Round(0), base, base.Add(time.Second), 1, 1),
		newReceiveCalibration(base, base.Add(-time.Nanosecond), base.Add(time.Second), 1, 1),
		newReceiveCalibration(base, base.Add(time.Second), base, 1, 1),
		newReceiveCalibration(base, base, base.Add(time.Second), 0, 1),
	} {
		if _, _, valid := invalid.ConditionalInterval(time.Second); valid {
			t.Fatalf("unknown calibration produced a bound: %+v", invalid)
		}
	}
}

func TestReceiveCalibrationRevocation(t *testing.T) {
	t.Parallel()
	base := time.Now()
	clock := &ReceiveClock{
		anchor: base, unixNano: base.UnixNano(),
		calibration: newReceiveCalibration(base, base, base.Add(200*time.Nanosecond), 1, 1),
	}
	if !clock.Calibration().Known {
		t.Fatal("live conditional calibration was lost")
	}
	clock.capture(base.UnixNano()+1, base.Add(time.Millisecond), base.UnixNano()+10000000)
	got := clock.Calibration()
	if got.Known || !got.Revoked || got.UnknownReason == "" {
		t.Fatalf("revoked epoch still has known bounds: %+v", got)
	}
	if _, _, valid := got.ConditionalInterval(time.Second); valid {
		t.Fatal("revoked epoch produced an interval")
	}
	if (*ReceiveClock)(nil).Calibration().Known {
		t.Fatal("nil clock produced known bounds")
	}
}

func TestReceiveCalibrationRejectsAmbiguousQuantizationModel(t *testing.T) {
	t.Parallel()
	calibration := NewReceiveClock().Calibration()
	calibration.Assumption = "native realtime/monotonic clocks; constant offset " +
		"and nominal quantization bounds during the run"
	if _, _, valid := calibration.ConditionalInterval(time.Second); valid {
		t.Fatal("ambiguous quantization metadata produced a known interval")
	}
}

func TestReceiveCalibrationContainsQuantizedConversion(t *testing.T) {
	t.Parallel()
	// Ideal kernel RX=1000 ns, owner commit=2000 ns: true latency is 1000 ns.
	// Realtime errors share [10,12], monotonic errors share [-9,-5].
	for _, tc := range []struct {
		name                                 string
		wallAt, monoAt                       int64
		beforeError, anchorError, afterError int64
		commitError, wallError, kernelError  int64
		span, measured, low, high            time.Duration
	}{
		{"wall_first_low", 50, 150, -9, -5, -5, -9, 10, 12, 204, 894, 684, 1104},
		{"wall_first_high", 50, 150, -5, -9, -9, -5, 12, 10, 196, 906, 704, 1108},
		{"mono_first_low", 150, 50, -9, -5, -5, -9, 10, 12, 204, 1094, 884, 1304},
		{"mono_first_high", 150, 50, -5, -9, -9, -5, 12, 10, 196, 1106, 904, 1308},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := time.Now()
			anchor := base.Add(time.Duration(tc.monoAt + tc.anchorError))
			calibration := newReceiveCalibration(base.Add(time.Duration(tc.beforeError)), anchor,
				base.Add(time.Duration(200+tc.afterError)), 2, 4)
			clock := &ReceiveClock{anchor: anchor, unixNano: base.UnixNano() + 5000 + tc.wallAt + tc.wallError}
			timing := clock.capture(base.UnixNano()+6000+tc.kernelError,
				base.Add(time.Duration(1500+tc.commitError)), base.UnixNano()+6500+tc.kernelError)
			measured, outcome := timing.latency(base.Add(time.Duration(2000+tc.commitError)),
				base.UnixNano()+7000+tc.kernelError)
			low, high, valid := calibration.ConditionalInterval(measured)
			if outcome != ReceiveTimingValid || measured != tc.measured || calibration.SpanNS != int64(tc.span) ||
				!valid || low != tc.low || high != tc.high || low > 1000 || high < 1000 {
				t.Fatalf("conversion=%d/%v span=%d interval=%d..%d/%v; want %d span=%d interval=%d..%d containing 1000",
					measured, outcome, calibration.SpanNS, low, high, valid, tc.measured, tc.span, tc.low, tc.high)
			}
		})
	}
}
