//go:build linux

package bfdload

import (
	"errors"
	"math"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestOwnerRegistrationsRejectInvalidOrMismatchedGauge(t *testing.T) {
	t.Parallel()
	for _, value := range []float64{-1, 0, 0.5, 2, math.NaN(), math.Inf(1)} {
		name := "gobfd_bfd_sessions"
		families := []*dto.MetricFamily{{Name: &name, Metric: []*dto.Metric{{Gauge: &dto.Gauge{Value: &value}}}}}
		if _, err := observeOwnerRegistrations(families, 1); !errors.Is(err, errOwnerConfig) {
			t.Fatalf("invalid or mismatched active gauge %v accepted: %v", value, err)
		}
	}
}
