package bfdmetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func (c *Collector) initTimerMetrics() {
	c.TimerEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "timer_events_total",
		Help: "Owner timer observations; dispatch and successful effect have separate denominators.",
	}, []string{"timer", "event"})
	c.TimerDelay = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "timer_delay_seconds",
		Help:    "Scheduled timer firing to owner dispatch or published effect; valid samples only.",
		Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 5},
	}, []string{"timer", "stage"})
	for kind := range bfd.TimerKindCount {
		for event := range bfd.TimerEventCount {
			c.timerCounters[kind][event] = c.TimerEvents.WithLabelValues(kind.String(), event.String())
		}
		for event := range bfd.TimerEffect + 1 {
			c.timerHistograms[kind][event] = c.TimerDelay.WithLabelValues(kind.String(), event.String())
		}
	}
}

// ObserveTimerEvent never creates series from runtime or peer data.
func (c *Collector) ObserveTimerEvent(kind bfd.TimerKind, event bfd.TimerEvent, delay time.Duration) {
	if kind >= bfd.TimerKindCount || event >= bfd.TimerEventCount {
		return
	}
	if delay < 0 {
		switch event {
		case bfd.TimerDispatched:
			event = bfd.TimerInvalidDispatch
		case bfd.TimerEffect:
			event = bfd.TimerInvalidEffect
		case bfd.TimerNoEffect, bfd.TimerInvalidDispatch, bfd.TimerInvalidEffect, bfd.TimerEventCount:
			// These outcomes never carry valid latency samples.
		}
	}
	c.timerCounters[kind][event].Inc()
	if event <= bfd.TimerEffect {
		c.timerHistograms[kind][event].Observe(delay.Seconds())
	}
}
