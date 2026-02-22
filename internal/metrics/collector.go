package bfdmetrics

import (
	"net/netip"

	"github.com/prometheus/client_golang/prometheus"
)

// -------------------------------------------------------------------------
// Prometheus Metric Constants
// -------------------------------------------------------------------------

const (
	namespace = "gobfd"
	subsystem = "bfd"
)

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
	// full receive channel, demux miss) per peer.
	PacketsDropped *prometheus.CounterVec

	// StateTransitions counts FSM state transitions. Each counter is labeled
	// with the old state and new state for precise alerting (e.g., Up->Down).
	StateTransitions *prometheus.CounterVec

	// AuthFailures counts authentication verification failures per peer.
	// RFC 5880 Section 6.7: auth failures cause packet discard.
	AuthFailures *prometheus.CounterVec
}

// NewCollector creates a Collector with all BFD metrics registered against
// the provided prometheus.Registerer. If reg is nil, prometheus.DefaultRegisterer
// is used.
//
// All metrics are created with the "gobfd_bfd_" prefix (namespace_subsystem)
// to avoid collisions with other exporters.
func NewCollector(reg prometheus.Registerer) *Collector {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	c := newMetrics()

	reg.MustRegister(
		c.Sessions,
		c.PacketsSent,
		c.PacketsReceived,
		c.PacketsDropped,
		c.StateTransitions,
		c.AuthFailures,
	)

	return c
}

// newMetrics creates all Prometheus metric vectors without registering them.
func newMetrics() *Collector {
	sessionLabels := []string{labelPeerAddr, labelLocalAddr, labelSessionType}
	peerLabels := []string{labelPeerAddr, labelLocalAddr}
	transitionLabels := []string{labelPeerAddr, labelLocalAddr, labelFromState, labelToState}

	return &Collector{
		Sessions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "sessions",
			Help:      "Number of currently active BFD sessions.",
		}, sessionLabels),

		PacketsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "packets_sent_total",
			Help:      "Total BFD Control packets transmitted.",
		}, peerLabels),

		PacketsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "packets_received_total",
			Help:      "Total BFD Control packets received.",
		}, peerLabels),

		PacketsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "packets_dropped_total",
			Help:      "Total BFD Control packets dropped due to validation or buffer overflow.",
		}, peerLabels),

		StateTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "state_transitions_total",
			Help:      "Total BFD session FSM state transitions.",
		}, transitionLabels),

		AuthFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "auth_failures_total",
			Help:      "Total BFD authentication verification failures (RFC 5880 Section 6.7).",
		}, peerLabels),
	}
}

// -------------------------------------------------------------------------
// Session Lifecycle
// -------------------------------------------------------------------------

// RegisterSession increments the active sessions gauge for the given peer.
// Called when a new BFD session is created by the Manager.
func (c *Collector) RegisterSession(peer, local netip.Addr, sessionType string) {
	c.Sessions.WithLabelValues(peer.String(), local.String(), sessionType).Inc()
}

// UnregisterSession decrements the active sessions gauge for the given peer.
// Called when a BFD session is destroyed by the Manager.
func (c *Collector) UnregisterSession(peer, local netip.Addr, sessionType string) {
	c.Sessions.WithLabelValues(peer.String(), local.String(), sessionType).Dec()
}

// -------------------------------------------------------------------------
// Packet Counters
// -------------------------------------------------------------------------

// IncPacketsSent increments the transmitted packets counter for the given peer.
// Called on each successful BFD Control packet transmission.
func (c *Collector) IncPacketsSent(peer, local netip.Addr) {
	c.PacketsSent.WithLabelValues(peer.String(), local.String()).Inc()
}

// IncPacketsReceived increments the received packets counter for the given peer.
// Called on each successfully demultiplexed BFD Control packet.
func (c *Collector) IncPacketsReceived(peer, local netip.Addr) {
	c.PacketsReceived.WithLabelValues(peer.String(), local.String()).Inc()
}

// IncPacketsDropped increments the dropped packets counter for the given peer.
// Called when a packet fails validation or cannot be delivered to a session.
func (c *Collector) IncPacketsDropped(peer, local netip.Addr) {
	c.PacketsDropped.WithLabelValues(peer.String(), local.String()).Inc()
}

// -------------------------------------------------------------------------
// State Transitions
// -------------------------------------------------------------------------

// RecordStateTransition increments the state transition counter with the
// old and new state labels. Used for alerting on session flaps (e.g.,
// Up->Down transitions triggering BGP route withdrawal).
func (c *Collector) RecordStateTransition(peer, local netip.Addr, from, to string) {
	c.StateTransitions.WithLabelValues(peer.String(), local.String(), from, to).Inc()
}

// -------------------------------------------------------------------------
// Authentication
// -------------------------------------------------------------------------

// IncAuthFailures increments the authentication failure counter for the
// given peer. RFC 5880 Section 6.7: auth failures MUST cause packet discard.
func (c *Collector) IncAuthFailures(peer, local netip.Addr) {
	c.AuthFailures.WithLabelValues(peer.String(), local.String()).Inc()
}
