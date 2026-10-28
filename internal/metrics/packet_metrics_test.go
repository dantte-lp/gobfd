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
