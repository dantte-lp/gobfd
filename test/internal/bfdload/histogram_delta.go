package bfdload

import (
	"fmt"
	"slices"
)

func histogramDelta(before, after Histogram) (Histogram, error) {
	if len(before.BoundsSeconds) != len(before.Counts)+1 || len(before.Counts) == 0 ||
		!slices.Equal(before.BoundsSeconds, after.BoundsSeconds) || len(before.Counts) != len(after.Counts) {
		return Histogram{}, fmt.Errorf("runtime histogram window dimensions changed: %w", errEnvironmentData)
	}
	result := Histogram{BoundsSeconds: slices.Clone(after.BoundsSeconds), Counts: make([]uint64, len(after.Counts))}
	for i, count := range after.Counts {
		if count < before.Counts[i] {
			return Histogram{}, fmt.Errorf("runtime histogram counter decreased: %w", errEnvironmentData)
		}
		result.Counts[i] = count - before.Counts[i]
	}
	return result, nil
}
