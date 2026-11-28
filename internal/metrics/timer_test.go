package bfdmetrics_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestTimerMetricsBounded(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metrics := bfdmetrics.NewCollector(registry)
	metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerDispatched, 10*time.Millisecond)
	metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerEffect, 25*time.Millisecond)
	metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerNoEffect, 0)
	metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerDispatched, -time.Nanosecond)
	metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerEffect, -time.Nanosecond)
	metrics.ObserveTimerEvent(bfd.TimerKindCount, bfd.TimerDispatched, time.Second)
	metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerEventCount, time.Second)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, family := range families {
		switch family.GetName() {
		case "gobfd_bfd_timer_events_total":
			found++
			if len(family.GetMetric()) != 20 {
				t.Fatalf("timer counter series=%d, want20", len(family.GetMetric()))
			}
			for _, metric := range family.GetMetric() {
				want := float64(0)
				for _, label := range metric.GetLabel() {
					if label.GetName() == "timer" && label.GetValue() == "control_tx" {
						want = 1
					}
					if label.GetName() != "timer" && label.GetName() != "event" {
						t.Fatalf("unbounded counter label: %v", label)
					}
				}
				if metric.GetCounter().GetValue() != want {
					t.Fatalf("wrong timer outcome count: %v", metric)
				}
			}
		case "gobfd_bfd_timer_delay_seconds":
			found++
			if len(family.GetMetric()) != 8 {
				t.Fatalf("timer histogram series=%d, want8", len(family.GetMetric()))
			}
			for _, metric := range family.GetMetric() {
				var timer, stage string
				for _, label := range metric.GetLabel() {
					switch label.GetName() {
					case "timer":
						timer = label.GetValue()
					case "stage":
						stage = label.GetValue()
					default:
						t.Fatalf("unbounded histogram label: %v", label)
					}
				}
				var count uint64
				var sum float64
				if timer == "control_tx" {
					count, sum = 1, 0.010
					if stage == "effect" {
						sum = 0.025
					}
				}
				if metric.GetHistogram().GetSampleCount() != count || metric.GetHistogram().GetSampleSum() != sum {
					t.Fatalf("invalid or no-effect sample entered histogram: %v", metric)
				}
			}
		}
	}
	if found != 2 {
		t.Fatalf("timer metric families=%d, want2", found)
	}
}

func BenchmarkTimerMetrics(b *testing.B) {
	metrics := bfdmetrics.NewCollector(prometheus.NewRegistry())
	if allocs := testing.AllocsPerRun(100, func() {
		metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerEffect, time.Millisecond)
	}); allocs != 0 {
		b.Fatalf("timer observation allocations=%g, want0", allocs)
	}
	b.ReportAllocs()
	for b.Loop() {
		metrics.ObserveTimerEvent(bfd.TimerControlTX, bfd.TimerEffect, time.Millisecond)
	}
}
