package bfdmetrics

import (
	"net/netip"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// Prometheus Metric Constants
// -------------------------------------------------------------------------

const (
	namespace = "gobfd"
	subsystem = "bfd"

	// DefaultMaxInactivePeerSeries bounds retained historical per-peer series
	// after UnregisterSession while active sessions remain unbounded.
	DefaultMaxInactivePeerSeries = 4096
)

// CollectorOption configures a Collector during construction.
type CollectorOption func(*Collector)

// WithMaxInactivePeerSeries sets the maximum number of inactive (peer_addr, local_addr)
// keys retained across per-peer metric vectors before FIFO/LRU eviction.
func WithMaxInactivePeerSeries(limit int) CollectorOption {
	return func(c *Collector) {
		c.maxInactivePeers = max(limit, 0)
	}
}

// Label names for BFD metrics.
const (
	labelPeerAddr    = "peer_addr"
	labelLocalAddr   = "local_addr"
	labelSessionType = "session_type"
	labelFromState   = "from_state"
	labelToState     = "to_state"
)

// -------------------------------------------------------------------------
// Collector — Prometheus BFD Metrics
// -------------------------------------------------------------------------

// Collector holds all BFD Prometheus metrics.
//
// Metrics are designed for production ISP/DC monitoring:
//   - Session gauges track currently active sessions.
//   - Packet counters track TX/RX/drop volumes per peer.
//   - State transition counters record FSM changes for alerting.
//   - Auth failure counters flag potential security issues.
type Collector struct {
	// Sessions tracks the number of currently active BFD sessions.
	// Incremented on session creation, decremented on session destruction.
	Sessions *prometheus.GaugeVec

	// PacketsSent counts the total BFD Control packets transmitted per peer.
	PacketsSent *prometheus.CounterVec

	// PacketsReceived counts the total BFD Control packets received per peer.
	PacketsReceived *prometheus.CounterVec

	// PacketsDropped counts BFD Control packets dropped (validation failures,
	// full receive channel, closed admission or abandonment) per session peer.
	PacketsDropped *prometheus.CounterVec

	// StateTransitions counts FSM state transitions. Each counter is labeled
	// with the old state and new state for precise alerting (e.g., Up->Down).
	StateTransitions *prometheus.CounterVec

	// AuthFailures counts authentication verification failures per peer.
	// RFC 5880 Section 6.7: auth failures cause packet discard.
	AuthFailures *prometheus.CounterVec

	// EvictedPeerSeries counts inactive (peer_addr, local_addr) keys evicted
	// across per-peer metric vectors once inactive cardinality exceeds the cap.
	EvictedPeerSeries prometheus.Counter

	// DeliveryOutcomes counts only the fixed Control stage/outcome pairs.
	DeliveryOutcomes *prometheus.CounterVec
	deliveryCounters map[[2]uint8]prometheus.Counter

	// ReceiveTimingOutcomes counts every Control commit by measurement validity.
	ReceiveTimingOutcomes *prometheus.CounterVec
	ReceiveCommitLatency  prometheus.Histogram
	receiveTimingCounters [bfd.ReceiveTimingOutcomeCount]prometheus.Counter

	// Timer observations use fixed owner/kind and event dimensions only.
	TimerEvents     *prometheus.CounterVec
	TimerDelay      *prometheus.HistogramVec
	timerCounters   [bfd.TimerKindCount][bfd.TimerEventCount]prometheus.Counter
	timerHistograms [bfd.TimerKindCount][2]prometheus.Observer

	// Socket telemetry retains only fixed Control/Echo sample dimensions.
	SocketDropSamples  *prometheus.CounterVec
	SocketDrops        *prometheus.CounterVec
	socketDropCounters [2][bfd.SocketDropOutcomeCount]prometheus.Counter
	socketDropTotals   [2]prometheus.Counter

	// GoBGPRPCErrors counts failed GoBGP API calls by RPC, including the
	// startup and background compatibility preflight.
	GoBGPRPCErrors *prometheus.CounterVec

	// GoBGPEndpointCompatible is 1 after the GoBGP v4 preflight passed and
	// 0 while the endpoint is incompatible or not yet checked.
	GoBGPEndpointCompatible prometheus.Gauge

	goBGPPeers *goBGPPeerCollector

	packetMu         sync.RWMutex
	activePackets    map[packetMetricKey]*packetMetricEntry
	inactivePeers    map[packetMetricKey]*packetMetricEntry
	inactiveHead     *packetMetricEntry
	inactiveTail     *packetMetricEntry
	maxInactivePeers int
}

// NewCollector creates a Collector with all BFD metrics registered against
// the provided prometheus.Registerer. If reg is nil, prometheus.DefaultRegisterer
// is used.
//
// All metrics are created with the "gobfd_bfd_" prefix (namespace_subsystem)
// to avoid collisions with other exporters.
func NewCollector(reg prometheus.Registerer, opts ...CollectorOption) *Collector {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	c := newMetrics()
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}

	reg.MustRegister(
		c.Sessions,
		c.PacketsSent,
		c.PacketsReceived,
		c.PacketsDropped,
		c.StateTransitions,
		c.AuthFailures,
		c.EvictedPeerSeries,
		c.DeliveryOutcomes,
		c.ReceiveTimingOutcomes,
		c.ReceiveCommitLatency,
		c.TimerEvents,
		c.TimerDelay,
		c.SocketDropSamples,
		c.SocketDrops,
		c.GoBGPRPCErrors,
		c.GoBGPEndpointCompatible,
		c.goBGPPeers,
	)

	return c
}

// newMetrics creates all Prometheus metric vectors without registering them.
func newMetrics() *Collector {
	deliveryOutcomes, deliveryCounters := newDeliveryMetrics()
	rpcErrors, endpointCompatible := newGoBGPMetrics()
	c := &Collector{
		goBGPPeers:              newGoBGPPeerCollector(),
		DeliveryOutcomes:        deliveryOutcomes,
		deliveryCounters:        deliveryCounters,
		GoBGPRPCErrors:          rpcErrors,
		GoBGPEndpointCompatible: endpointCompatible,
		maxInactivePeers:        DefaultMaxInactivePeerSeries,
	}
	c.initPeerMetrics()
	c.initTelemetryMetrics()
	return c
}

func newGoBGPMetrics() (*prometheus.CounterVec, prometheus.Gauge) {
	rpcErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "gobgp",
		Name:      "rpc_errors_total",
		Help:      "Total GoBGP API calls that failed, by RPC.",
	}, []string{"rpc"})
	compatible := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "gobgp",
		Name:      "endpoint_compatible",
		Help:      "1 when the GoBGP endpoint passed the v4 compatibility preflight, else 0.",
	})
	return rpcErrors, compatible
}

func (c *Collector) initPeerMetrics() {
	peerLabels := []string{labelPeerAddr, labelLocalAddr}
	c.Sessions = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "sessions",
		Help: "Number of currently active BFD sessions.",
	}, []string{labelPeerAddr, labelLocalAddr, labelSessionType})
	c.PacketsSent = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "packets_sent_total",
		Help: "Total BFD Control packets transmitted.",
	}, peerLabels)
	c.PacketsReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "packets_received_total",
		Help: "Total BFD Control packets received.",
	}, peerLabels)
	c.PacketsDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "packets_dropped_total",
		Help: "Total BFD Control packets dropped due to validation or buffer overflow.",
	}, peerLabels)
	c.StateTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "state_transitions_total",
		Help: "Total BFD session FSM state transitions.",
	}, []string{labelPeerAddr, labelLocalAddr, labelFromState, labelToState})
	c.AuthFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "auth_failures_total",
		Help: "Total BFD authentication verification failures (RFC 5880 Section 6.7).",
	}, peerLabels)
	c.EvictedPeerSeries = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "evicted_peer_series_total",
		Help: "Total inactive per-peer metric keys evicted due to historical cardinality limit.",
	})
}

func newDeliveryMetrics() (*prometheus.CounterVec, map[[2]uint8]prometheus.Counter) {
	vector := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: subsystem, Name: "delivery_outcomes_total",
		Help: "Pipeline decisions by stage and outcome; stages have distinct denominators.",
	}, []string{"source", "outcome"})
	counters := make(map[[2]uint8]prometheus.Counter)
	for _, pair := range bfd.DeliveryCounterPairs() {
		key := [2]uint8{uint8(pair.Source), uint8(pair.Outcome)}
		counters[key] = vector.WithLabelValues(pair.Source.String(), pair.Outcome.String())
	}
	return vector, counters
}

// RecordDelivery never creates a series from untrusted input or unsupported pairs.
func (c *Collector) RecordDelivery(source bfd.DeliverySource, outcome bfd.DeliveryOutcome) {
	if counter, ok := c.deliveryCounters[[2]uint8{uint8(source), uint8(outcome)}]; ok {
		counter.Inc()
	}
}

// -------------------------------------------------------------------------
// Session Lifecycle
// -------------------------------------------------------------------------

// RegisterSession increments the active sessions gauge for the given peer.
// Called when a new BFD session is created by the Manager.
func (c *Collector) RegisterSession(peer, local netip.Addr, sessionType string) {
	c.packetMu.Lock()
	defer c.packetMu.Unlock()

	entry := c.registerPacketMetricsLocked(peer, local, sessionType)
	// Publish the alert-critical counter at zero before its first transition.
	// PromQL increase() requires a baseline sample to observe the first Up->Down.
	entry.recordTransition("Up", "Down")
	c.StateTransitions.WithLabelValues(entry.peerLabel, entry.localLabel, "Up", "Down").Add(0)
	c.Sessions.WithLabelValues(entry.peerLabel, entry.localLabel, sessionType).Inc()
}

// UnregisterSession decrements the active sessions gauge for the given peer.
// Called when a BFD session is destroyed by the Manager.
func (c *Collector) UnregisterSession(peer, local netip.Addr, sessionType string) {
	c.packetMu.Lock()
	defer c.packetMu.Unlock()

	entry := c.unregisterPacketMetricsLocked(peer, local, sessionType)
	c.Sessions.WithLabelValues(entry.peerLabel, entry.localLabel, sessionType).Dec()
	c.evictInactivePeersLocked()
}

// SetMaxInactivePeerSeries updates the maximum retained inactive per-peer keys
// and immediately evicts the oldest inactive keys if the cap was lowered.
func (c *Collector) SetMaxInactivePeerSeries(limit int) {
	c.packetMu.Lock()
	defer c.packetMu.Unlock()
	c.maxInactivePeers = max(limit, 0)
	c.evictInactivePeersLocked()
}

// -------------------------------------------------------------------------
// Packet Counters
// -------------------------------------------------------------------------

// IncPacketsSent increments the transmitted packets counter for the given peer.
// Called on each successful BFD Control packet transmission.
func (c *Collector) IncPacketsSent(peer, local netip.Addr) {
	if entry := c.packetMetrics(peer, local); entry != nil {
		entry.sent.Inc()
		return
	}
	c.incUnregisteredPacketCounter(peer, local, func(e *packetMetricEntry) { e.sent.Inc() })
}

// IncPacketsReceived increments the received packets counter for the given peer.
// Called on each successfully demultiplexed BFD Control packet.
func (c *Collector) IncPacketsReceived(peer, local netip.Addr) {
	if entry := c.packetMetrics(peer, local); entry != nil {
		entry.received.Inc()
		return
	}
	c.incUnregisteredPacketCounter(peer, local, func(e *packetMetricEntry) { e.received.Inc() })
}

// IncPacketsDropped increments the dropped packets counter for the given peer.
// Called when a packet fails validation or cannot be delivered to a session.
func (c *Collector) IncPacketsDropped(peer, local netip.Addr) {
	if entry := c.packetMetrics(peer, local); entry != nil {
		entry.dropped.Inc()
		return
	}
	c.incUnregisteredPacketCounter(peer, local, func(e *packetMetricEntry) { e.dropped.Inc() })
}

// -------------------------------------------------------------------------
// State Transitions
// -------------------------------------------------------------------------

// RecordStateTransition increments the state transition counter with the
// old and new state labels. Used for alerting on session flaps (e.g.,
// Up->Down transitions triggering BGP route withdrawal).
func (c *Collector) RecordStateTransition(peer, local netip.Addr, from, to string) {
	c.recordStateTransition(peer, local, from, to)
}

// -------------------------------------------------------------------------
// Authentication
// -------------------------------------------------------------------------

// IncAuthFailures increments the authentication failure counter for the
// given peer. RFC 5880 Section 6.7: auth failures MUST cause packet discard.
func (c *Collector) IncAuthFailures(peer, local netip.Addr) {
	if entry := c.packetMetrics(peer, local); entry != nil {
		entry.authFailures.Inc()
		return
	}
	c.incUnregisteredPacketCounter(peer, local, func(e *packetMetricEntry) { e.authFailures.Inc() })
}

// IncGoBGPRPCError counts one failed GoBGP API call.
func (c *Collector) IncGoBGPRPCError(rpc string) {
	c.GoBGPRPCErrors.WithLabelValues(rpc).Inc()
}

// SetGoBGPCompatible records the result of the GoBGP compatibility preflight.
func (c *Collector) SetGoBGPCompatible(compatible bool) {
	if compatible {
		c.GoBGPEndpointCompatible.Set(1)
		return
	}
	c.GoBGPEndpointCompatible.Set(0)
}
