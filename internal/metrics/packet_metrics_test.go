package bfdmetrics_test

import (
	"net/netip"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestPacketMetricsRegisteredAllocations(t *testing.T) {
	for _, peer := range []netip.Addr{
		netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("fe80::1%eth0"),
	} {
		t.Run(peer.String(), func(t *testing.T) {
			c := bfdmetrics.NewCollector(prometheus.NewRegistry())
			local := netip.MustParseAddr("192.0.2.2")
			c.RegisterSession(peer, local, "single_hop")
			c.RegisterSession(peer, local, "multi_hop")
			c.UnregisterSession(peer, local, "single_hop")
			t.Cleanup(func() { c.UnregisterSession(peer, local, "multi_hop") })
			allocs := testing.AllocsPerRun(100, func() {
				c.IncPacketsSent(peer, local)
				c.IncPacketsReceived(peer, local)
				c.IncPacketsDropped(peer, local)
				c.IncAuthFailures(peer, local)
			})
			if allocs != 0 {
				t.Fatalf("registered packet counters allocate=%g, want0", allocs)
			}
		})
	}
}

func TestPacketMetricsConcurrentChurnRetainsCounters(t *testing.T) {
	t.Parallel()
	c := bfdmetrics.NewCollector(prometheus.NewRegistry())
	peer, local := testPeers()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 100 {
				c.RegisterSession(peer, local, "single_hop")
				c.IncPacketsSent(peer, local)
				c.IncPacketsReceived(peer, local)
				c.IncPacketsDropped(peer, local)
				c.IncAuthFailures(peer, local)
				c.UnregisterSession(peer, local, "single_hop")
			}
		})
	}
	workers.Wait()
	for _, vector := range []*prometheus.CounterVec{c.PacketsSent, c.PacketsReceived, c.PacketsDropped, c.AuthFailures} {
		if got := counterValue(t, vector, peer.String(), local.String()); got != 400 {
			t.Fatalf("counter after concurrent churn=%v, want400", got)
		}
	}
	if got := gaugeValue(t, c.Sessions, peer.String(), local.String(), "single_hop"); got != 0 {
		t.Fatalf("sessions after final unregister=%v, want0", got)
	}
	c.IncPacketsReceived(peer, local)
	c.RegisterSession(peer, local, "single_hop")
	c.IncPacketsReceived(peer, local)
	c.UnregisterSession(peer, local, "single_hop")
	if got := counterValue(t, c.PacketsReceived, peer.String(), local.String()); got != 402 {
		t.Fatalf("counter reset or omitted an unregistered update=%v, want402", got)
	}
}

func TestPacketMetricsBoundedInactiveSeriesChurn(t *testing.T) {
	t.Parallel()
	const inactiveLimit = 4
	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg, bfdmetrics.WithMaxInactivePeerSeries(inactiveLimit))
	local := netip.MustParseAddr("192.0.2.254")

	// Keep 3 active peers unbounded while churning ephemeral peers past inactiveLimit.
	activePeers := []netip.Addr{
		netip.MustParseAddr("198.51.100.1"),
		netip.MustParseAddr("198.51.100.2"),
		netip.MustParseAddr("198.51.100.3"),
	}
	for _, peer := range activePeers {
		c.RegisterSession(peer, local, "single_hop")
		c.IncPacketsSent(peer, local)
	}

	// Register and retire 4 inactive peers (within limit): no evictions yet.
	ephemeralBase := netip.MustParseAddr("203.0.113.1")
	ephemeral := make([]netip.Addr, 0, 10)
	cur := ephemeralBase
	for range 10 {
		ephemeral = append(ephemeral, cur)
		cur = cur.Next()
	}
	for i := range inactiveLimit {
		c.RegisterSession(ephemeral[i], local, "single_hop")
		c.IncPacketsSent(ephemeral[i], local)
		c.IncPacketsReceived(ephemeral[i], local)
		c.IncPacketsDropped(ephemeral[i], local)
		c.IncAuthFailures(ephemeral[i], local)
		c.RecordStateTransition(ephemeral[i], local, "Down", "Up")
		c.UnregisterSession(ephemeral[i], local, "single_hop")
	}

	// Re-register ephemeral[0] before eviction: its counter must survive and it leaves the inactive queue!
	c.RegisterSession(ephemeral[0], local, "single_hop")
	c.IncPacketsSent(ephemeral[0], local)
	if got := counterValue(t, c.PacketsSent, ephemeral[0].String(), local.String()); got != 2 {
		t.Fatalf("re-registered peer PacketsSent = %v, want 2", got)
	}
	c.UnregisterSession(ephemeral[0], local, "single_hop")

	// Now inactive queue order is [ephemeral[1], ephemeral[2], ephemeral[3], ephemeral[0]] (4 items, 0 evicted).
	if evicted := evictedPeerSeriesTotal(t, reg); evicted != 0 {
		t.Fatalf("evicted series before cap overflow = %v, want 0", evicted)
	}

	// Churn 6 more ephemeral peers sequentially + concurrently: 6 oldest inactive peers must be evicted.
	var workers sync.WaitGroup
	for i := inactiveLimit; i < len(ephemeral); i++ {
		peer := ephemeral[i]
		workers.Go(func() {
			c.RegisterSession(peer, local, "single_hop")
			c.IncPacketsSent(peer, local)
			c.RecordStateTransition(peer, local, "Up", "Down")
			c.UnregisterSession(peer, local, "single_hop")
		})
	}
	workers.Wait()

	const wantEvicted = 6
	if evicted := evictedPeerSeriesTotal(t, reg); evicted != wantEvicted {
		t.Fatalf("gobfd_bfd_evicted_peer_series_total = %v, want %d", evicted, wantEvicted)
	}
	assertGatheredPeerCardinality(t, reg, activePeers, inactiveLimit)

	// Shrinking limit via SetMaxInactivePeerSeries evicts excess historical keys immediately.
	c.SetMaxInactivePeerSeries(2)
	if evicted := evictedPeerSeriesTotal(t, reg); evicted != wantEvicted+2 {
		t.Fatalf("evicted series after shrinking cap = %v, want %d", evicted, wantEvicted+2)
	}
	assertGatheredPeerCardinality(t, reg, activePeers, 2)
}

func TestPacketMetricsUnregisteredFallbackCallsStayBounded(t *testing.T) {
	t.Parallel()
	const inactiveLimit = 3
	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg, bfdmetrics.WithMaxInactivePeerSeries(inactiveLimit))
	local := netip.MustParseAddr("192.0.2.254")

	activePeer := netip.MustParseAddr("198.51.100.10")
	c.RegisterSession(activePeer, local, "custom_overlay")
	c.IncPacketsSent(activePeer, local)

	// Drive 12 unregistered peers across all fallback paths without RegisterSession.
	peer := netip.MustParseAddr("203.0.113.50")
	for range 12 {
		c.IncPacketsSent(peer, local)
		c.IncPacketsReceived(peer, local)
		c.IncPacketsDropped(peer, local)
		c.IncAuthFailures(peer, local)
		c.RecordStateTransition(peer, local, "Down", "Init")
		c.RecordStateTransition(peer, local, "CustomFrom", "CustomTo")
		peer = peer.Next()
	}

	const wantEvicted = 12 - inactiveLimit
	if evicted := evictedPeerSeriesTotal(t, reg); evicted != wantEvicted {
		t.Fatalf("unregistered fallback evicted = %v, want %d", evicted, wantEvicted)
	}
	assertGatheredPeerCardinality(t, reg, []netip.Addr{activePeer}, inactiveLimit)

	// Setting limit to 0 evicts all remaining inactive fallback peers while keeping activePeer intact.
	c.SetMaxInactivePeerSeries(0)
	c.IncPacketsDropped(peer, local)
	c.RecordStateTransition(peer, local, "Up", "Down")
	assertGatheredPeerCardinality(t, reg, []netip.Addr{activePeer}, 0)
}

func evictedPeerSeriesTotal(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() == "gobfd_bfd_evicted_peer_series_total" && len(family.GetMetric()) == 1 {
			return family.GetMetric()[0].GetCounter().GetValue()
		}
	}
	return 0
}

func assertGatheredPeerCardinality(
	t *testing.T,
	reg *prometheus.Registry,
	activePeers []netip.Addr,
	maxInactive int,
) {
	t.Helper()
	activeSet := make(map[string]struct{}, len(activePeers))
	for _, peer := range activePeers {
		activeSet[peer.String()] = struct{}{}
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	perPeerFamilies := map[string]bool{
		"gobfd_bfd_sessions":                true,
		"gobfd_bfd_packets_sent_total":      true,
		"gobfd_bfd_packets_received_total":  true,
		"gobfd_bfd_packets_dropped_total":   true,
		"gobfd_bfd_state_transitions_total": true,
		"gobfd_bfd_auth_failures_total":     true,
	}
	inactiveSeen := make(map[string]struct{})
	activeSeen := make(map[string]struct{})
	for _, family := range families {
		if !perPeerFamilies[family.GetName()] {
			continue
		}
		familyInactive := make(map[string]struct{})
		for _, metric := range family.GetMetric() {
			var peerAddr string
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "peer_addr" {
					peerAddr = lp.GetValue()
				}
			}
			if _, isActive := activeSet[peerAddr]; isActive {
				activeSeen[peerAddr] = struct{}{}
			} else {
				inactiveSeen[peerAddr] = struct{}{}
				familyInactive[peerAddr] = struct{}{}
			}
		}
		if len(familyInactive) > maxInactive {
			t.Fatalf("%s retained %d inactive peers, want <= %d",
				family.GetName(), len(familyInactive), maxInactive)
		}
	}
	if len(activeSeen) != len(activePeers) {
		t.Fatalf("gathered active peers = %d, want %d", len(activeSeen), len(activePeers))
	}
	if len(inactiveSeen) > maxInactive {
		t.Fatalf("total distinct inactive peers across families = %d, want <= %d",
			len(inactiveSeen), maxInactive)
	}
}
