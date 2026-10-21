package main

import (
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestGoBGPPeerMetricsFollowValidatedConfiguration(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	reg := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(reg)
	checker := newDaemonHealthChecker()
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{{Peer: "192.0.2.210", Local: "127.0.0.1", Interface: "lo"}}
	coordinator := newReconciliationCoordinator(cfg, logger, checker)
	status := newGoBGPStatus(checker, collector)
	coordinator.onConfiguredPeers = status.configuredPeers
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	if _, err := newGoBGPHandler(nil, cfg.GoBGP, mgr, status, logger); err != nil {
		t.Fatalf("newGoBGPHandler: %v", err)
	}
	assertPeers := func(want int) {
		t.Helper()
		families, err := reg.Gather()
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		peers := 0
		for _, family := range families {
			if family.GetName() == "gobfd_gobgp_dampening_penalty" {
				peers = len(family.GetMetric())
				for _, metric := range family.GetMetric() {
					if metric.GetLabel()[0].GetValue() != "192.0.2.210" {
						t.Fatalf("unconfigured peer metric %v", metric)
					}
				}
			}
		}
		if peers != want {
			t.Fatalf("configured peer samples = %d, want %d", peers, want)
		}
	}
	assertPeers(0)
	if err := coordinator.reconcile(t.Context(), cfg, mgr,
		newNthFailureDeclarativeSenderFactory(0), nil); err != nil {
		t.Fatalf("reconcile configured peer: %v", err)
	}
	assertPeers(1)
	cfg.Sessions = []config.SessionConfig{{Peer: "not-an-address"}}
	if err := coordinator.reconcile(t.Context(), cfg, mgr,
		newNthFailureDeclarativeSenderFactory(0), nil); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	assertPeers(1)
	cfg.Sessions = nil
	if err := coordinator.reconcile(t.Context(), cfg, mgr,
		newNthFailureDeclarativeSenderFactory(0), nil); err != nil {
		t.Fatalf("reconcile removed peer: %v", err)
	}
	assertPeers(0)
}
