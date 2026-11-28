package bfdmetrics

import (
	"net/netip"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPacketMetricsCacheRetiresLastRegistration(t *testing.T) {
	t.Parallel()
	c := NewCollector(prometheus.NewRegistry())
	peer, local := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")
	c.RegisterSession(peer, local, "single_hop")
	c.RegisterSession(peer, local, "multi_hop")
	c.UnregisterSession(peer, local, "single_hop")
	if len(c.activePackets) != 1 {
		t.Fatalf("shared active pair lost cache: entries=%d, want1", len(c.activePackets))
	}
	c.UnregisterSession(peer, local, "multi_hop")
	for range 64 {
		c.RegisterSession(peer, local, "single_hop")
		c.IncPacketsReceived(peer, local)
		c.UnregisterSession(peer, local, "single_hop")
		if len(c.activePackets) != 0 {
			t.Fatalf("retired pair retained cache: entries=%d, want0", len(c.activePackets))
		}
		peer = peer.Next()
	}
	c.IncPacketsReceived(peer, local)
	if len(c.activePackets) != 0 {
		t.Fatal("unregistered counter call populated active cache")
	}
}
