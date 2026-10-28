package bfdmetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func (c *Collector) initTelemetryMetrics() {
	c.ReceiveTimingOutcomes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "control_rx_timing_total",
		Help: "Completed Control packets by fixed timing validity outcome; not queue admissions.",
	}, []string{"outcome"})
	for outcome := range bfd.ReceiveTimingOutcomeCount {
		c.receiveTimingCounters[outcome] = c.ReceiveTimingOutcomes.WithLabelValues(outcome.String())
	}
	c.ReceiveCommitLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "control_rx_commit_seconds",
		Help:    "Software kernel RX to completed Control owner processing; valid clock samples only.",
		Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 5},
	})
	c.initTimerMetrics()
	c.initSocketDropMetrics()
}

// ObserveReceiveTiming never creates a series from packet data or peer labels.
func (c *Collector) ObserveReceiveTiming(outcome bfd.ReceiveTimingOutcome, latency time.Duration) {
	if outcome >= bfd.ReceiveTimingOutcomeCount {
		return
	}
	if outcome == bfd.ReceiveTimingValid && latency < 0 {
		outcome = bfd.ReceiveTimingInvalid
	}
	c.receiveTimingCounters[outcome].Inc()
	if outcome == bfd.ReceiveTimingValid {
		c.ReceiveCommitLatency.Observe(latency.Seconds())
	}
}
