package bfdmetrics_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestSocketDropMetricsRegistered(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metrics := bfdmetrics.NewCollector(registry)
	metrics.ObserveSocketDrops(false, bfd.SocketDropInitial, 100)
	metrics.ObserveSocketDrops(false, bfd.SocketDropSample, 3)
	metrics.ObserveSocketDrops(true, bfd.SocketDropUnavailable, 500)
	metrics.ObserveSocketDrops(true, bfd.SocketDropSample, 7)
	metrics.ObserveSocketDrops(false, bfd.SocketDropInvalid, 88)
	metrics.ObserveSocketDrops(false, bfd.SocketDropAmbiguous, 90)
	metrics.ObserveSocketDrops(false, bfd.SocketDropOutcomeCount, 1)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, family := range families {
		switch family.GetName() {
		case "gobfd_bfd_socket_drop_samples_total":
			found++
			if len(family.GetMetric()) != 10 {
				t.Fatalf("socket-drop sample series=%d, want10", len(family.GetMetric()))
			}
			for _, metric := range family.GetMetric() {
				var kind, outcome string
				for _, label := range metric.GetLabel() {
					switch label.GetName() {
					case "kind":
						kind = label.GetValue()
					case "outcome":
						outcome = label.GetValue()
					default:
						t.Fatalf("unbounded label: %v", label)
					}
				}
				want := float64(0)
				if outcome == "sample" ||
					(kind == "control" && (outcome == "initial" || outcome == "invalid" || outcome == "ambiguous")) ||
					(kind == "echo" && outcome == "unavailable") {
					want = 1
				}
				if metric.GetCounter().GetValue() != want {
					t.Fatalf("socket-drop sample=%v,want%g", metric, want)
				}
			}
		case "gobfd_bfd_socket_drops_total":
			found++
			if len(family.GetMetric()) != 2 {
				t.Fatalf("socket-drop counter series=%d,want2", len(family.GetMetric()))
			}
			for _, metric := range family.GetMetric() {
				if len(metric.GetLabel()) != 1 || metric.GetLabel()[0].GetName() != "kind" {
					t.Fatalf("unbounded drop labels: %v", metric)
				}
				want := float64(3)
				if metric.GetLabel()[0].GetValue() == "echo" {
					want = 7
				}
				if metric.GetCounter().GetValue() != want {
					t.Fatalf("unbased/invalid sample counted as loss: %v", metric)
				}
			}
		}
	}
	if found != 2 {
		t.Fatalf("socket-drop metric families=%d, want2", found)
	}
}

func BenchmarkSocketDropMetrics(b *testing.B) {
	metrics := bfdmetrics.NewCollector(prometheus.NewRegistry())
	observe := func() { metrics.ObserveSocketDrops(false, bfd.SocketDropSample, 3) }
	if allocs := testing.AllocsPerRun(100, observe); allocs != 0 {
		b.Fatalf("socket-drop allocations=%g,want0", allocs)
	}
	b.ReportAllocs()
	for b.Loop() {
		metrics.ObserveSocketDrops(false, bfd.SocketDropSample, 3)
	}
}
