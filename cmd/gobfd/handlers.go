package main

import (
	"context"
	"fmt"
	"log/slog"

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
		closeGoBGPClient(client, logger)
		return nil, fmt.Errorf("create gobgp handler: %w", err)
	}

	g.Go(func() error {
		return handler.Run(ctx, mgr.SubscribeStateChanges(ctx))
	})

	logger.Info("gobgp integration enabled",
		slog.String("addr", cfg.Addr),
		slog.String("strategy", cfg.Strategy),
		slog.Duration("action_timeout", cfg.ActionTimeout),
		slog.Bool("tls", cfg.TLS.Enabled),
		slog.Bool("dampening", cfg.Dampening.Enabled),
	)

	return client, nil
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
