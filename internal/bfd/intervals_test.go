package bfd_test

import (
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestIsCommonInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want bool
	}{
		{"3.3ms", 3300 * time.Microsecond, true},
		{"10ms", 10 * time.Millisecond, true},
		{"20ms", 20 * time.Millisecond, true},
		{"50ms", 50 * time.Millisecond, true},
		{"100ms", 100 * time.Millisecond, true},
		{"1s", 1 * time.Second, true},
		{"0", 0, false},
		{"negative", -1 * time.Millisecond, false},
		{"5ms not common", 5 * time.Millisecond, false},
		{"15ms not common", 15 * time.Millisecond, false},
		{"30ms not common", 30 * time.Millisecond, false},
		{"200ms not common", 200 * time.Millisecond, false},
		{"300ms not common", 300 * time.Millisecond, false},
		{"2s not common", 2 * time.Second, false},
		{"10s graceful restart", 10 * time.Second, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := bfd.IsCommonInterval(tt.d); got != tt.want {
				t.Errorf("IsCommonInterval(%v) = %v, want %v", tt.d, got, tt.want)
			}
		})
	}
}

func TestAlignToCommonInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want time.Duration
	}{
		// Exact matches stay as-is.
		{"exact 3.3ms", 3300 * time.Microsecond, 3300 * time.Microsecond},
		{"exact 10ms", 10 * time.Millisecond, 10 * time.Millisecond},
		{"exact 20ms", 20 * time.Millisecond, 20 * time.Millisecond},
		{"exact 50ms", 50 * time.Millisecond, 50 * time.Millisecond},
		{"exact 100ms", 100 * time.Millisecond, 100 * time.Millisecond},
		{"exact 1s", 1 * time.Second, 1 * time.Second},

		// Round UP to nearest common interval.
		{"1us -> 3.3ms", 1 * time.Microsecond, 3300 * time.Microsecond},
		{"1ms -> 3.3ms", 1 * time.Millisecond, 3300 * time.Microsecond},
		{"3ms -> 3.3ms", 3 * time.Millisecond, 3300 * time.Microsecond},
		{"4ms -> 10ms", 4 * time.Millisecond, 10 * time.Millisecond},
		{"5ms -> 10ms", 5 * time.Millisecond, 10 * time.Millisecond},
		{"15ms -> 20ms", 15 * time.Millisecond, 20 * time.Millisecond},
		{"25ms -> 50ms", 25 * time.Millisecond, 50 * time.Millisecond},
		{"75ms -> 100ms", 75 * time.Millisecond, 100 * time.Millisecond},
		{"150ms -> 1s", 150 * time.Millisecond, 1 * time.Second},
		{"500ms -> 1s", 500 * time.Millisecond, 1 * time.Second},
		{"999ms -> 1s", 999 * time.Millisecond, 1 * time.Second},

		// Beyond 1s — returned as-is.
		{"1.5s -> 1.5s", 1500 * time.Millisecond, 1500 * time.Millisecond},
		{"2s -> 2s", 2 * time.Second, 2 * time.Second},
		{"10s -> 10s", 10 * time.Second, 10 * time.Second},

		// Edge cases.
		{"zero", 0, 0},
		{"negative", -1 * time.Millisecond, -1 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := bfd.AlignToCommonInterval(tt.d); got != tt.want {
				t.Errorf("AlignToCommonInterval(%v) = %v, want %v", tt.d, got, tt.want)
			}
		})
	}
}

func TestNearestCommonInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want time.Duration
	}{
		// Exact matches.
		{"exact 3.3ms", 3300 * time.Microsecond, 3300 * time.Microsecond},
		{"exact 50ms", 50 * time.Millisecond, 50 * time.Millisecond},
		{"exact 1s", 1 * time.Second, 1 * time.Second},

		// Nearest rounding.
		{"1ms -> 3.3ms", 1 * time.Millisecond, 3300 * time.Microsecond},
		{"7ms -> 10ms (closer to 10 than 3.3)", 7 * time.Millisecond, 10 * time.Millisecond},
		{"6ms -> 3.3ms (closer to 3.3 than 10)", 6 * time.Millisecond, 3300 * time.Microsecond},
		{"14ms -> 10ms", 14 * time.Millisecond, 10 * time.Millisecond},
		{"16ms -> 20ms", 16 * time.Millisecond, 20 * time.Millisecond},
		{"35ms -> 20ms (tie breaks smaller)", 35 * time.Millisecond, 20 * time.Millisecond},
		{"36ms -> 50ms", 36 * time.Millisecond, 50 * time.Millisecond},
		{"74ms -> 50ms", 74 * time.Millisecond, 50 * time.Millisecond},
		{"76ms -> 100ms", 76 * time.Millisecond, 100 * time.Millisecond},
		{"500ms -> 100ms (closer to 100ms)", 500 * time.Millisecond, 100 * time.Millisecond},
		{"600ms -> 1s (closer to 1s)", 600 * time.Millisecond, 1 * time.Second},

		// Zero/negative.
		{"zero -> 3.3ms", 0, 3300 * time.Microsecond},
		{"negative -> 3.3ms", -5 * time.Millisecond, 3300 * time.Microsecond},

		// Large values -> 1s (closest common).
		{"2s -> 1s", 2 * time.Second, 1 * time.Second},
		{"10s -> 1s", 10 * time.Second, 1 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := bfd.NearestCommonInterval(tt.d); got != tt.want {
				t.Errorf("NearestCommonInterval(%v) = %v, want %v", tt.d, got, tt.want)
			}
		})
	}
}

func TestAlignToCommonIntervalIdempotent(t *testing.T) {
	t.Parallel()

	for _, ci := range bfd.CommonIntervals {
		aligned := bfd.AlignToCommonInterval(ci)
		if aligned != ci {
			t.Errorf("AlignToCommonInterval(%v) = %v, want %v (not idempotent)", ci, aligned, ci)
		}
	}
}

func TestGracefulRestartInterval(t *testing.T) {
	t.Parallel()

	if bfd.GracefulRestartInterval != 10*time.Second {
		t.Errorf("GracefulRestartInterval = %v, want 10s", bfd.GracefulRestartInterval)
	}
}

func TestCommonIntervalsAreSorted(t *testing.T) {
	t.Parallel()

	for i := 1; i < len(bfd.CommonIntervals); i++ {
		if bfd.CommonIntervals[i] <= bfd.CommonIntervals[i-1] {
			t.Errorf("CommonIntervals not sorted: [%d]=%v >= [%d]=%v",
				i-1, bfd.CommonIntervals[i-1], i, bfd.CommonIntervals[i])
		}
	}
}

func TestCommonIntervalsCount(t *testing.T) {
	t.Parallel()

	// RFC 7419 defines exactly 6 common intervals.
	if got := len(bfd.CommonIntervals); got != 6 {
		t.Errorf("len(CommonIntervals) = %d, want 6", got)
	}
}
