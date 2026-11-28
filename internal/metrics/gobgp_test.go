package bfdmetrics_test

import (
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func goBGPPeerGaugeValues(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	values := make(map[string]float64)
	for _, family := range families {
		name := family.GetName()
		if !strings.HasPrefix(name, "gobfd_gobgp_dampening_") && !strings.HasPrefix(name, "gobfd_gobgp_pending_") {
			continue
		}
		for _, metric := range family.GetMetric() {
			var peer, action string
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case "peer_addr":
					peer = label.GetValue()
				case "action":
					action = label.GetValue()
				default:
					t.Fatalf("unexpected label %v", label)
				}
			}
			values[name+"/"+peer+"/"+action] = metric.GetGauge().GetValue()
		}
	}
	return values
}

func TestGoBGPPeerMetricsUseConfiguredPeersAndFreshSource(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(reg)
	collector.SetGoBGPConfiguredPeers([]netip.Addr{
		netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("::ffff:192.0.2.1"), {},
	})
	state := bfdmetrics.GoBGPPeerState{
		Penalty: 2, Suppressed: true, SuppressionDeadline: time.Unix(300, 0),
		Action: "up", NextAttempt: time.Unix(200, 500_000_000),
	}
	calls := 0
	collector.SetGoBGPPeerSource(func(peer string) bfdmetrics.GoBGPPeerState {
		if peer != "192.0.2.1" {
			t.Errorf("source called for unconfigured peer %s", peer)
		}
		calls++
		return state
	})
	values := goBGPPeerGaugeValues(t, reg)
	want := map[string]float64{
		"gobfd_gobgp_dampening_penalty/192.0.2.1/":                      2,
		"gobfd_gobgp_dampening_suppressed/192.0.2.1/":                   1,
		"gobfd_gobgp_dampening_suppression_deadline_seconds/192.0.2.1/": 300,
		"gobfd_gobgp_pending_action/192.0.2.1/up":                       1,
		"gobfd_gobgp_pending_action/192.0.2.1/down":                     0,
		"gobfd_gobgp_pending_action_deadline_seconds/192.0.2.1/up":      200.5,
		"gobfd_gobgp_pending_action_deadline_seconds/192.0.2.1/down":    0,
	}
	if len(values) != len(want) || calls != 1 {
		t.Fatalf("metrics=%v, source calls=%d, want 7 samples and one source observation", values, calls)
	}
	for key, value := range want {
		if got, exists := values[key]; !exists || got != value {
			t.Errorf("%s = %v, exists=%t, want %v", key, got, exists, value)
		}
	}
	state = bfdmetrics.GoBGPPeerState{}
	values = goBGPPeerGaugeValues(t, reg)
	for key, value := range values {
		if value != 0 {
			t.Errorf("idle metric %s = %v, want 0", key, value)
		}
	}
	if calls != 2 {
		t.Fatalf("source calls after second scrape = %d, want 2", calls)
	}
	collector.SetGoBGPConfiguredPeers(nil)
	if values = goBGPPeerGaugeValues(t, reg); len(values) != 0 {
		t.Fatalf("metrics after removing configured peer = %v, want absent", values)
	}
}

func TestGoBGPPeerMetricsConcurrentScrapeAndConfiguration(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(reg)
	collector.SetGoBGPPeerSource(func(string) bfdmetrics.GoBGPPeerState { return bfdmetrics.GoBGPPeerState{} })
	peer := netip.MustParseAddr("192.0.2.1")
	var writers sync.WaitGroup
	writers.Go(func() {
		for range 100 {
			collector.SetGoBGPConfiguredPeers([]netip.Addr{peer})
			collector.SetGoBGPConfiguredPeers(nil)
		}
	})
	for range 100 {
		for key := range goBGPPeerGaugeValues(t, reg) {
			if !strings.Contains(key, "/192.0.2.1/") {
				t.Errorf("metric for unconfigured peer: %s", key)
			}
		}
	}
	writers.Wait()
}
