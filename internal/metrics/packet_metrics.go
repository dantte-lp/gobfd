package bfdmetrics

import (
	"net/netip"

	"github.com/prometheus/client_golang/prometheus"
)

type packetMetricKey struct {
	peer, local netip.Addr
}

type packetCounters struct {
	sent, received, dropped, authFailures prometheus.Counter
	registrations                         int
}

func (c *Collector) registerPacketMetrics(peer, local netip.Addr) {
	c.packetMu.Lock()
	defer c.packetMu.Unlock()
	if c.activePackets == nil {
		c.activePackets = make(map[packetMetricKey]*packetCounters)
	}
	key := packetMetricKey{peer: peer, local: local}
	counters := c.activePackets[key]
	if counters == nil {
		peerLabel, localLabel := peer.String(), local.String()
		counters = &packetCounters{
			sent:         c.PacketsSent.WithLabelValues(peerLabel, localLabel),
			received:     c.PacketsReceived.WithLabelValues(peerLabel, localLabel),
			dropped:      c.PacketsDropped.WithLabelValues(peerLabel, localLabel),
			authFailures: c.AuthFailures.WithLabelValues(peerLabel, localLabel),
		}
		c.activePackets[key] = counters
	}
	counters.registrations++
}

func (c *Collector) unregisterPacketMetrics(peer, local netip.Addr) {
	c.packetMu.Lock()
	defer c.packetMu.Unlock()
	key := packetMetricKey{peer: peer, local: local}
	if counters := c.activePackets[key]; counters != nil {
		counters.registrations--
		if counters.registrations == 0 {
			// Historical CounterVec series survive churn; only active handles retire.
			delete(c.activePackets, key)
		}
	}
}

func (c *Collector) packetMetrics(peer, local netip.Addr) *packetCounters {
	c.packetMu.RLock()
	counters := c.activePackets[packetMetricKey{peer: peer, local: local}]
	c.packetMu.RUnlock()
	return counters
}
