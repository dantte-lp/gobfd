package bfdload

import (
	"slices"
	"testing"
)

func TestHistogramDeltaRejectsResetAndOwnsWindow(t *testing.T) {
	t.Parallel()
	before := Histogram{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{100, 50}}
	after := Histogram{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{107, 52}}
	got, err := histogramDelta(before, after)
	if err != nil || !slices.Equal(got.Counts, []uint64{7, 2}) {
		t.Fatalf("window uses lifetime counts or loses a bucket: %+v: %v", got, err)
	}
	after.Counts[0], after.BoundsSeconds[1] = 999, "2"
	if got.Counts[0] != 7 || got.BoundsSeconds[1] != "0.001" {
		t.Fatal("window aliases a later snapshot")
	}
	for _, invalid := range []Histogram{
		{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{99, 52}},
		{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{107}},
		{BoundsSeconds: []string{"0", "2", "+Inf"}, Counts: []uint64{107, 52}},
		{},
	} {
		if _, err := histogramDelta(before, invalid); err == nil {
			t.Fatalf("reset or changed runtime histogram accepted: %+v", invalid)
		}
	}
}
