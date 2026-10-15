package main

import (
	"context"
	"net/netip"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/gobgp"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

const (
	// goBGPHealthServiceName is the grpc.health.v1 service that reports the
	// GoBGP integration (GitHub #86).
	goBGPHealthServiceName = "gobfd.gobgp"

	// goBGPRPCPreflight labels compatibility preflight failures.
	goBGPRPCPreflight = "CheckCompatibility"
)

// goBGPStatus publishes the GoBGP endpoint and action state to health and
// metrics. The service is NOT_SERVING until a preflight passes, and after a
// failed preflight or a failed peer action until the next success.
type goBGPStatus struct {
	checker *grpchealth.StaticChecker
	metrics *bfdmetrics.Collector
}

func (s *goBGPStatus) configuredPeers(peers []netip.Addr) {
	if s != nil {
		s.metrics.SetGoBGPConfiguredPeers(peers)
	}
}

func (s *goBGPStatus) observeHandler(handler *gobgp.Handler) {
	if s == nil {
		return
	}
	s.metrics.SetGoBGPPeerSource(func(peer string) bfdmetrics.GoBGPPeerState {
		state := handler.PeerStatus(peer)
		return bfdmetrics.GoBGPPeerState{
			Penalty: state.Penalty, Suppressed: state.Suppressed,
			SuppressionDeadline: state.SuppressionDeadline,
			Action:              state.Action, NextAttempt: state.NextAttempt,
		}
	})
}

// goBGPStatusFor returns nil when the GoBGP integration is disabled, so no
// gobfd.gobgp health service is registered.
func goBGPStatusFor(
	cfg config.GoBGPConfig,
	checker *grpchealth.StaticChecker,
	metrics *bfdmetrics.Collector,
) *goBGPStatus {
	if !cfg.Enabled {
		return nil
	}
	return newGoBGPStatus(checker, metrics)
}

func newGoBGPStatus(checker *grpchealth.StaticChecker, metrics *bfdmetrics.Collector) *goBGPStatus {
	s := &goBGPStatus{checker: checker, metrics: metrics}
	checker.SetStatus(goBGPHealthServiceName, grpchealth.StatusNotServing)
	metrics.SetGoBGPCompatible(false)
	return s
}

// probe records one compatibility preflight result.
func (s *goBGPStatus) probe(err error) {
	if s == nil {
		return
	}
	s.metrics.SetGoBGPCompatible(err == nil)
	s.result(goBGPRPCPreflight, err)
}

// action records one peer action result from the GoBGP handler.
func (s *goBGPStatus) action(rpc string, err error) {
	if s == nil {
		return
	}
	s.result(rpc, err)
}

// ambiguous records a BFD event withheld from GoBGP because the neighbor
// has no canonical BFD session (H-31).
func (s *goBGPStatus) ambiguous() {
	if s == nil {
		return
	}
	s.checker.SetStatus(goBGPHealthServiceName, grpchealth.StatusNotServing)
}

func (s *goBGPStatus) result(rpc string, err error) {
	if err != nil {
		s.metrics.IncGoBGPRPCError(rpc)
		s.checker.SetStatus(goBGPHealthServiceName, grpchealth.StatusNotServing)
		return
	}
	s.checker.SetStatus(goBGPHealthServiceName, grpchealth.StatusServing)
}

// observeProbe reports every preflight result except one cut short by
// shutdown.
func (s *goBGPStatus) observeProbe(check func(context.Context) error) func(context.Context) error {
	if s == nil {
		return check
	}
	return func(ctx context.Context) error {
		err := check(ctx)
		if ctx.Err() == nil {
			s.probe(err)
		}
		return err
	}
}

// onAction returns the handler callback, nil when status is not tracked.
func (s *goBGPStatus) onAction() func(rpc string, err error) {
	if s == nil {
		return nil
	}
	return s.action
}
