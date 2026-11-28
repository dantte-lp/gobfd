package bfdmetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func (c *Collector) initSocketDropMetrics() {
	c.SocketDropSamples = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "socket_drop_samples_total",
		Help: "Consumed datagram socket-drop samples by validity; independent of packet acceptance.",
	}, []string{"kind", "outcome"})
	c.SocketDrops = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "socket_drops_total",
		Help: "Observed socket-drop increments between ordered samples; unbased and unavailable samples excluded.",
	}, []string{"kind"})
	for kind, name := range [...]string{"control", "echo"} {
		for outcome := range bfd.SocketDropOutcomeCount {
			c.socketDropCounters[kind][outcome] = c.SocketDropSamples.WithLabelValues(name, outcome.String())
		}
		c.socketDropTotals[kind] = c.SocketDrops.WithLabelValues(name)
	}
}

// ObserveSocketDrops uses cached handles and ignores deltas without a valid sample.
func (c *Collector) ObserveSocketDrops(echo bool, outcome bfd.SocketDropOutcome, delta uint32) {
	if outcome >= bfd.SocketDropOutcomeCount {
		return
	}
	kind := 0
	if echo {
		kind = 1
	}
	c.socketDropCounters[kind][outcome].Inc()
	if outcome == bfd.SocketDropSample {
		c.socketDropTotals[kind].Add(float64(delta))
	}
}
