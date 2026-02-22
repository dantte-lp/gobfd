package bfdmetrics_test

import (
	"net/netip"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

// testPeers returns common test addresses.
func testPeers() (peer, local netip.Addr) {
	return netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2")
}

func TestNewCollector(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg)

	if c.Sessions == nil {
		t.Error("Sessions is nil")
	}
	if c.PacketsSent == nil {
		t.Error("PacketsSent is nil")
	}
	if c.PacketsReceived == nil {
		t.Error("PacketsReceived is nil")
	}
	if c.PacketsDropped == nil {
		t.Error("PacketsDropped is nil")
	}
	if c.StateTransitions == nil {
		t.Error("StateTransitions is nil")
	}
	if c.AuthFailures == nil {
		t.Error("AuthFailures is nil")
	}

	// Verify all metrics are registered by gathering them.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	// No data yet, so families may be empty -- but registration must not panic.
	_ = families
}

func TestRegisterUnregisterSession(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg)

	peer, local := testPeers()

	// Register a session -- gauge should go to 1.
	c.RegisterSession(peer, local, "single_hop")

	val := gaugeValue(t, c.Sessions, peer.String(), local.String(), "single_hop")
	if val != 1 {
		t.Errorf("after RegisterSession: sessions gauge = %v, want 1", val)
	}

	// Register another session with different type.
	c.RegisterSession(peer, local, "multi_hop")

	val = gaugeValue(t, c.Sessions, peer.String(), local.String(), "multi_hop")
	if val != 1 {
		t.Errorf("after second RegisterSession: multi_hop gauge = %v, want 1", val)
	}

	// Unregister single_hop -- gauge should go back to 0.
	c.UnregisterSession(peer, local, "single_hop")

	val = gaugeValue(t, c.Sessions, peer.String(), local.String(), "single_hop")
	if val != 0 {
		t.Errorf("after UnregisterSession: sessions gauge = %v, want 0", val)
	}

	// multi_hop should still be 1.
	val = gaugeValue(t, c.Sessions, peer.String(), local.String(), "multi_hop")
	if val != 1 {
		t.Errorf("multi_hop gauge = %v, want 1 (should be unaffected)", val)
	}
}

func TestPacketCounters(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg)

	peer, local := testPeers()

	// Increment sent counter 3 times.
	c.IncPacketsSent(peer, local)
	c.IncPacketsSent(peer, local)
	c.IncPacketsSent(peer, local)

	val := counterValue(t, c.PacketsSent, peer.String(), local.String())
	if val != 3 {
		t.Errorf("PacketsSent = %v, want 3", val)
	}

	// Increment received counter 2 times.
	c.IncPacketsReceived(peer, local)
	c.IncPacketsReceived(peer, local)

	val = counterValue(t, c.PacketsReceived, peer.String(), local.String())
	if val != 2 {
		t.Errorf("PacketsReceived = %v, want 2", val)
	}

	// Increment dropped counter once.
	c.IncPacketsDropped(peer, local)

	val = counterValue(t, c.PacketsDropped, peer.String(), local.String())
	if val != 1 {
		t.Errorf("PacketsDropped = %v, want 1", val)
	}
}

func TestStateTransition(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg)

	peer, local := testPeers()

	// Record a Down->Init transition.
	c.RecordStateTransition(peer, local, "Down", "Init")

	val := counterValue(t, c.StateTransitions,
		peer.String(), local.String(), "Down", "Init")
	if val != 1 {
		t.Errorf("StateTransitions(Down->Init) = %v, want 1", val)
	}

	// Record an Init->Up transition.
	c.RecordStateTransition(peer, local, "Init", "Up")

	val = counterValue(t, c.StateTransitions,
		peer.String(), local.String(), "Init", "Up")
	if val != 1 {
		t.Errorf("StateTransitions(Init->Up) = %v, want 1", val)
	}

	// Record another Down->Init -- counter should be 2.
	c.RecordStateTransition(peer, local, "Down", "Init")

	val = counterValue(t, c.StateTransitions,
		peer.String(), local.String(), "Down", "Init")
	if val != 2 {
		t.Errorf("StateTransitions(Down->Init) = %v, want 2", val)
	}
}

func TestAuthFailures(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	c := bfdmetrics.NewCollector(reg)

	peer, local := testPeers()

	c.IncAuthFailures(peer, local)
	c.IncAuthFailures(peer, local)

	val := counterValue(t, c.AuthFailures, peer.String(), local.String())
	if val != 2 {
		t.Errorf("AuthFailures = %v, want 2", val)
	}
}

// -------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------

// gaugeValue reads the current value of a GaugeVec with the given labels.
func gaugeValue(t *testing.T, vec *prometheus.GaugeVec, labels ...string) float64 {
	t.Helper()

	gauge, err := vec.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labels, err)
	}

	m := &dto.Metric{}
	if err := gauge.Write(m); err != nil {
		t.Fatalf("Write metric: %v", err)
	}

	return m.GetGauge().GetValue()
}

// counterValue reads the current value of a CounterVec with the given labels.
func counterValue(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()

	counter, err := vec.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labels, err)
	}

	m := &dto.Metric{}
	if err := counter.Write(m); err != nil {
		t.Fatalf("Write metric: %v", err)
	}

	return m.GetCounter().GetValue()
}
