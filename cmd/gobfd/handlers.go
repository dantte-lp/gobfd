package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/gobgp"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// GoBGP Integration — RFC 5882 Section 4.3
// -------------------------------------------------------------------------

// startGoBGPHandler creates and starts the GoBGP handler goroutine if enabled.
// Returns the GoBGP client (for deferred Close) and any initialization error.
// Returns nil client when GoBGP integration is disabled.
func startGoBGPHandler(
	ctx context.Context,
	g *errgroup.Group,
	cfg config.GoBGPConfig,
	mgr *bfd.Manager,
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

	probeErr := probeGoBGPCompatibility(ctx, client.CheckCompatibility, cfg.ActionTimeout)
	if errors.Is(probeErr, gobgp.ErrIncompatibleServer) {
		closeGoBGPClient(client, logger)
		return nil, fmt.Errorf("gobgp endpoint %s requires GoBGP v4: %w", cfg.Addr, probeErr)
	}

	handler, err := newGoBGPHandler(client, cfg, logger)
	if err != nil {
		closeGoBGPClient(client, logger)
		return nil, err
	}

	if err := startGoBGPHandlerRun(ctx, g, mgr, handler); err != nil {
		closeGoBGPClient(client, logger)
		return nil, err
	}

	reportGoBGPCompatibility(ctx, g, client, cfg, probeErr, logger)

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
	client *gobgp.GRPCClient,
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
		watchGoBGPCompatibility(ctx, client.CheckCompatibility, cfg.Addr, cfg.ActionTimeout, logger)
		return nil
	})
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

func newGoBGPHandler(
	client gobgp.Client,
	cfg config.GoBGPConfig,
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
		Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create gobgp handler: %w", err)
	}
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

	g.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case ev, ok := <-mon.Events():
				if !ok {
					return nil
				}
				affected := mgr.HandleInterfaceEvent(ev.IfName, ev.Up)
				logger.Debug("interface event processed",
					slog.String("interface", ev.IfName),
					slog.Int("ifindex", ev.IfIndex),
					slog.Bool("up", ev.Up),
					slog.Int("affected_sessions", affected),
				)
			}
		}
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
