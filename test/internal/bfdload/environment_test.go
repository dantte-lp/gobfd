package bfdload

import (
	"encoding/json"
	"math"
	"runtime/metrics"
	"slices"
	"testing"
)

func TestHistogramSnapshotOwnsJSONSafeValues(t *testing.T) {
	t.Parallel()
	raw := &metrics.Float64Histogram{
		Buckets: []float64{math.Inf(-1), 0, 0.001, math.Inf(1)},
		Counts:  []uint64{0, 7, 2},
	}
	got, err := snapshotHistogram(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw.Counts[1] = 99
	raw.Buckets[2] = 3
	if !slices.Equal(got.BoundsSeconds, []string{"-Inf", "0", "0.001", "+Inf"}) ||
		!slices.Equal(got.Counts, []uint64{0, 7, 2}) {
		t.Fatalf("histogram aliases runtime storage or loses bounds: %+v", got)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("histogram is not JSON safe: %v", err)
	}
	for _, invalid := range []*metrics.Float64Histogram{
		nil,
		{Buckets: []float64{0, 1}, Counts: nil},
		{Buckets: []float64{0, math.NaN()}, Counts: []uint64{1}},
		{Buckets: []float64{1, 0}, Counts: []uint64{1}},
		{Buckets: []float64{0, 0}, Counts: []uint64{1}},
	} {
		if _, err := snapshotHistogram(invalid); err == nil {
			t.Fatalf("invalid histogram accepted: %+v", invalid)
		}
	}
}
