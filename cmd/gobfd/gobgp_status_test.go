package main

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/grpchealth/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/gobgp"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

// GitHub #86: an incompatible, unreachable or failing GoBGP endpoint is
// visible in the gobfd.gobgp health service and in metrics.
func TestGoBGPStatusReflectsEndpointAndActions(t *testing.T) {
	t.Parallel()

	checker := newDaemonHealthChecker()
	collector := bfdmetrics.NewCollector(prometheus.NewRegistry())
	status := newGoBGPStatus(checker, collector)

	health := func() grpchealth.Status {
		t.Helper()
		resp, err := checker.Check(context.Background(), &grpchealth.CheckRequest{Service: goBGPHealthServiceName})
		if err != nil {
			t.Fatalf("Check(%s): %v", goBGPHealthServiceName, err)
		}
		return resp.Status
	}
	value := func(c prometheus.Metric) float64 {
		t.Helper()
		m := &dto.Metric{}
		if err := c.Write(m); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		return m.GetGauge().GetValue() + m.GetCounter().GetValue()
	}
	errors0 := func(rpc string) float64 { return value(collector.GoBGPRPCErrors.WithLabelValues(rpc)) }

	if got := health(); got != grpchealth.StatusNotServing {
		t.Fatalf("health before the first probe = %v, want NOT_SERVING", got)
	}

	status.probe(errors.New("connection refused"))
	if got := health(); got != grpchealth.StatusNotServing {
		t.Fatalf("health after an inconclusive probe = %v, want NOT_SERVING", got)
	}
	if got := errors0(goBGPRPCPreflight); got != 1 {
		t.Fatalf("preflight errors = %v, want 1", got)
	}

	status.probe(nil)
	if got := health(); got != grpchealth.StatusServing {
		t.Fatalf("health after a passing probe = %v, want SERVING", got)
	}
	if got := value(collector.GoBGPEndpointCompatible); got != 1 {
		t.Fatalf("endpoint compatible = %v, want 1", got)
	}

	status.action("DisablePeer", errors.New("unavailable"))
	if got := health(); got != grpchealth.StatusNotServing {
		t.Fatalf("health after a failed action = %v, want NOT_SERVING", got)
	}
	if got := errors0("DisablePeer"); got != 1 {
		t.Fatalf("DisablePeer errors = %v, want 1", got)
	}
	status.action("DisablePeer", nil)
	if got := health(); got != grpchealth.StatusServing {
		t.Fatalf("health after a successful action = %v, want SERVING", got)
	}

	status.probe(gobgp.ErrIncompatibleServer)
	if got := health(); got != grpchealth.StatusNotServing {
		t.Fatalf("health after an incompatible probe = %v, want NOT_SERVING", got)
	}
	if got := value(collector.GoBGPEndpointCompatible); got != 0 {
		t.Fatalf("endpoint compatible = %v, want 0", got)
	}
}
