package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/gobgp"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// GoBGP Integration — RFC 5882 Section 10.2
// -------------------------------------------------------------------------

// startGoBGPHandler creates and starts the GoBGP handler goroutine if enabled.
// Returns the GoBGP client (for deferred Close) and any initialization error.
// Returns nil client when GoBGP integration is disabled.
func startGoBGPHandler(
	ctx context.Context,
	g *errgroup.Group,
	cfg config.GoBGPConfig,
	managedPeers []netip.Addr,
	mgr *bfd.Manager,
	status *goBGPStatus,
	logger *slog.Logger,
) (gobgp.Client, error) {
	if !cfg.Enabled {
		logger.Info("gobgp integration disabled")
		return nil, nil //nolint:nilnil // Nil client is valid when GoBGP is disabled; caller handles nil.
	}

	if config.GoBGPPlaintextNonLoopback(cfg) {
		logger.Warn("gobgp plaintext grpc configured for non-loopback address",
			slog.String("addr", cfg.Addr),
			slog.String("mitigation", "enable gobgp.tls.enabled for remote GoBGP API endpoints"),
		)
	}

	client, err := gobgp.NewGRPCClient(gobgp.GRPCClientConfig{
		Addr: cfg.Addr,
		TLS: gobgp.GRPCClientTLSConfig{
			Enabled:    cfg.TLS.Enabled,
			CAFile:     cfg.TLS.CAFile,
			ServerName: cfg.TLS.ServerName,
		},
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create gobgp client: %w", err)
	}

	preflight := status.observeProbe(goBGPPreflight(client, managedPeers))
	probeErr := probeGoBGPCompatibility(ctx, preflight, cfg.ActionTimeout)
	if goBGPPreflightConclusive(probeErr) {
		closeGoBGPClient(client, logger)
		return nil, goBGPStartupError(cfg.Addr, probeErr)
	}

	handler, err := newGoBGPHandler(client, cfg, mgr, status, logger)
	if err != nil {
		closeGoBGPClient(client, logger)
		return nil, err
	}

	if err := startGoBGPHandlerRun(ctx, g, mgr, handler); err != nil {
		closeGoBGPClient(client, logger)
		return nil, err
	}

	reportGoBGPCompatibility(ctx, g, preflight, cfg, probeErr, logger)

	logger.Info("gobgp integration enabled",
		slog.String("addr", cfg.Addr),
		slog.String("strategy", cfg.Strategy),
		slog.Duration("action_timeout", cfg.ActionTimeout),
		slog.Bool("tls", cfg.TLS.Enabled),
		slog.Bool("dampening", cfg.Dampening.Enabled),
	)

	return client, nil
}

// GoBGP compatibility re-check backoff while the endpoint is not yet reachable.
const (
	goBGPCompatRetryInitial = time.Second
	goBGPCompatRetryMax     = 30 * time.Second
)

// reportGoBGPCompatibility logs the startup probe result and, when it was
// inconclusive, re-checks in the background.
func reportGoBGPCompatibility(
	ctx context.Context,
	g *errgroup.Group,
	preflight func(context.Context) error,
	cfg config.GoBGPConfig,
	probeErr error,
	logger *slog.Logger,
) {
	if probeErr == nil {
		logger.Info("gobgp compatibility preflight passed", slog.String("addr", cfg.Addr))
		return
	}
	logger.Warn("gobgp compatibility preflight inconclusive; re-checking in background",
		slog.String("addr", cfg.Addr),
		slog.String("error", probeErr.Error()),
	)
	g.Go(func() error {
		watchGoBGPCompatibility(ctx, preflight, cfg.Addr, cfg.ActionTimeout, logger)
		return nil
	})
}

// errNativeBFDConflict reports GoBGP neighbors that run native BFD for peers
// whose BFD sessions GoBFD owns.
var errNativeBFDConflict = errors.New("gobgp native BFD is enabled for GoBFD-managed peers")

// goBGPPreflight checks the GoBGP v4 API and then rejects managed peers for
// which GoBGP native BFD is enabled (v1 design: one BFD owner per peer).
func goBGPPreflight(client *gobgp.GRPCClient, managedPeers []netip.Addr) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := client.CheckCompatibility(ctx); err != nil {
			return fmt.Errorf("gobgp preflight: %w", err)
		}
		nativePeers, err := client.NativeBFDPeers(ctx)
		if err != nil {
			return fmt.Errorf("check gobgp native BFD: %w", err)
		}
		conflicts := slices.DeleteFunc(nativePeers, func(peer netip.Addr) bool {
			return !slices.Contains(managedPeers, peer)
		})
		if len(conflicts) > 0 {
			return fmt.Errorf("%w: %v", errNativeBFDConflict, conflicts)
		}
		return nil
	}
}

// goBGPStartupError names the required action for a conclusive preflight failure.
func goBGPStartupError(addr string, err error) error {
	if errors.Is(err, gobgp.ErrIncompatibleServer) {
		return fmt.Errorf("gobgp endpoint %s requires GoBGP v4: %w", addr, err)
	}
	return fmt.Errorf("gobgp endpoint %s: %w", addr, err)
}

// goBGPPreflightConclusive reports whether a preflight error is final.
func goBGPPreflightConclusive(err error) bool {
	return errors.Is(err, gobgp.ErrIncompatibleServer) || errors.Is(err, errNativeBFDConflict)
}

// probeGoBGPCompatibility runs one bounded capability probe.
func probeGoBGPCompatibility(ctx context.Context, check func(context.Context) error, timeout time.Duration) error {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return check(probeCtx)
}

// watchGoBGPCompatibility repeats the capability probe with exponential backoff
// until it is conclusive. GoBGP often starts after GoBFD (for example as a
// sidecar), so an inconclusive startup probe must not hide a GoBGP v3 daemon
// until the first BFD Down actuation.
func watchGoBGPCompatibility(
	ctx context.Context,
	check func(context.Context) error,
	addr string,
	timeout time.Duration,
	logger *slog.Logger,
) {
	delay := goBGPCompatRetryInitial
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		err := probeGoBGPCompatibility(ctx, check, timeout)
		switch {
		case errors.Is(err, gobgp.ErrIncompatibleServer):
			logger.Error("gobgp endpoint requires GoBGP v4; peer actuation will fail",
				slog.String("addr", addr),
				slog.String("error", err.Error()),
			)
			return
		case errors.Is(err, errNativeBFDConflict):
			logger.Error("gobgp native BFD conflicts with GoBFD sessions; disable it for these peers",
				slog.String("addr", addr),
				slog.String("error", err.Error()),
			)
			return
		case err == nil:
			logger.Info("gobgp compatibility preflight passed", slog.String("addr", addr))
			return
		}
		delay = min(2*delay, goBGPCompatRetryMax)
		logger.Debug("gobgp compatibility preflight still inconclusive",
			slog.String("addr", addr),
			slog.String("error", err.Error()),
			slog.Duration("retry_in", delay),
		)
		timer.Reset(delay)
	}
}

// goBGPActuates reports whether a base session is the canonical BFD session
// for its BGP neighbor: it still exists and is the only single-hop or
// multi-hop session to the peer address (H-31). Session removal publishes no
// state change, so the GoBGP handler also asks before applying a late Down.
// Two base sessions to one neighbor leave no canonical session: neither
// actuates, and the gobfd.gobgp health service reports NOT_SERVING.
func goBGPActuates(mgr *bfd.Manager, status *goBGPStatus, logger *slog.Logger) func(bfd.StateChange) bool {
	return func(sc bfd.StateChange) bool {
		peer := sc.PeerAddr.Unmap()
		found, allowed, base := false, false, 0
		for _, snap := range mgr.Sessions() {
			if snap.PeerAddr.Unmap() != peer ||
				(snap.Type != bfd.SessionTypeSingleHop && snap.Type != bfd.SessionTypeMultiHop) {
				continue
			}
			base++
			if snap.LocalDiscr == sc.LocalDiscr && (sc.Ref == (bfd.SessionRef{}) || snap.Ref == sc.Ref) {
				found = true
				allowed = goBGPAllowsDown(snap)
			}
		}
		if found && base > 1 {
			logger.Error("gobgp peer has several BFD sessions, no BGP action taken",
				slog.String("peer", peer.String()),
				slog.Int("sessions", base),
			)
			status.ambiguous()
			return false
		}
		return found && allowed
	}
}

func goBGPAllowsDown(snap bfd.SessionSnapshot) bool {
	return snap.Cause == bfd.CausePathDown ||
		(snap.State != bfd.StateAdminDown && snap.Cause != bfd.CauseLocalAdmin &&
			snap.Cause != bfd.CauseRemoteAdminDown && (!snap.RemoteKnown || snap.RemoteState != bfd.StateAdminDown))
}

func newGoBGPHandler(
	client gobgp.Client,
	cfg config.GoBGPConfig,
	mgr *bfd.Manager,
	status *goBGPStatus,
	logger *slog.Logger,
) (*gobgp.Handler, error) {
	handler, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client:        client,
		Strategy:      gobgp.Strategy(cfg.Strategy),
		ActionTimeout: cfg.ActionTimeout,
		Dampening: gobgp.DampeningConfig{
			Enabled:           cfg.Dampening.Enabled,
			SuppressThreshold: cfg.Dampening.SuppressThreshold,
			ReuseThreshold:    cfg.Dampening.ReuseThreshold,
			MaxSuppressTime:   cfg.Dampening.MaxSuppressTime,
			HalfLife:          cfg.Dampening.HalfLife,
		},
		Actuates: goBGPActuates(mgr, status, logger),
		Snapshot: mgr.SessionsWithRevision,
		OnAction: status.onAction(),
		Logger:   logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create gobgp handler: %w", err)
	}
	status.observeHandler(handler)
	return handler, nil
}

func startGoBGPHandlerRun(
	ctx context.Context,
	g *errgroup.Group,
	mgr *bfd.Manager,
	handler *gobgp.Handler,
) error {
	stateChanges, err := mgr.SubscribeStateChanges(ctx)
	if err != nil {
		return fmt.Errorf("subscribe GoBGP handler to BFD state changes: %w", err)
	}
	g.Go(func() error {
		return handler.Run(ctx, stateChanges)
	})
	return nil
}

func startInterfaceMonitor(
	ctx context.Context,
	g *errgroup.Group,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	coordinator *reconciliationCoordinator,
	logger *slog.Logger,
) {
	mon, err := netio.NewInterfaceMonitor(logger)
	if err != nil {
		logger.Warn("interface monitoring disabled",
			slog.String("error", err.Error()),
		)
		return
	}

	g.Go(func() error {
		defer func() {
			if err := mon.Close(); err != nil {
				logger.Warn("failed to close interface monitor",
					slog.String("error", err.Error()),
				)
			}
		}()
		return mon.Run(ctx)
	})

	queue := newInterfaceEventCoalescer()
	g.Go(func() error {
		consumeInterfaceEvents(ctx, mon.Events(), mgr, coordinator, queue, logger)
		return nil
	})
	g.Go(func() error {
		coordinator.runInterfaceReconciliation(ctx, queue, netio.CheckInterfacesAvailable,
			func(ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate) sourceApplyResult {
				return applyCompiledSource(ctx, source, candidate, mgr, sf, logger)
			})
		return nil
	})
}

// loadConfig loads configuration from a file path or returns defaults.
func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		cfg, err := config.Load(path)
		if err != nil {
			return nil, fmt.Errorf("load config from %s: %w", path, err)
		}
		return cfg, nil
	}
	return config.DefaultConfig(), nil
}

// goBGPManagedPeers returns the peer addresses of configured BFD sessions, the
// peers whose GoBGP state the handler actuates. Sessions are validated at load.
func goBGPManagedPeers(sessions []config.SessionConfig) []netip.Addr {
	peers := make([]netip.Addr, 0, len(sessions))
	for _, session := range sessions {
		if peer, err := session.PeerAddr(); err == nil {
			peers = append(peers, peer.Unmap())
		}
	}
	return peers
}
