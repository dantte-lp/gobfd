package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// SIGHUP Reload — log level + session reconciliation
// -------------------------------------------------------------------------

// handleSIGHUP listens for SIGHUP signals and reloads configuration.
// On reload, the log level is updated dynamically via the shared LevelVar,
// and declarative sessions are reconciled (new sessions created, removed
// sessions destroyed).
// Blocks until the context is cancelled (graceful shutdown).
func handleSIGHUP(
	ctx context.Context,
	sigHUP <-chan os.Signal,
	configPath string,
	logLevel *slog.LevelVar,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	overlayRuntime *overlayRuntime,
	logger *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sigHUP:
			logger.Info("received SIGHUP, reloading configuration")
			reloadConfig(ctx, configPath, logLevel, mgr, sf, overlayRuntime, logger)
		}
	}
}

// reloadConfig loads a fresh configuration from the given path, updates
// the dynamic log level, and reconciles declarative BFD sessions.
// Errors during reload are logged but do not stop the daemon -- the
// previous configuration remains in effect.
func reloadConfig(
	ctx context.Context,
	configPath string,
	logLevel *slog.LevelVar,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	overlayRuntime *overlayRuntime,
	logger *slog.Logger,
) {
	newCfg, err := loadConfig(configPath)
	if err != nil {
		logger.Error("failed to reload configuration, keeping current settings",
			slog.String("error", err.Error()),
		)
		return
	}

	// Update log level.
	oldLevel := logLevel.Level()
	newLevel := config.ParseLogLevel(newCfg.Log.Level)
	logLevel.Set(newLevel)

	logger.Info("configuration reloaded",
		slog.String("old_log_level", oldLevel.String()),
		slog.String("new_log_level", newLevel.String()),
	)

	// Reconcile declarative sessions.
	reconcileSessions(ctx, newCfg, mgr, sf, logger)

	// Reconcile declarative echo sessions (RFC 9747).
	reconcileEchoSessions(ctx, newCfg, mgr, sf, logger)

	// Reconcile micro-BFD groups (RFC 7130).
	reconcileMicroBFDGroups(ctx, newCfg, mgr, sf, logger)

	// Reconcile overlay tunnel BFD sessions (VXLAN RFC 8971, Geneve RFC 9521).
	reconcileOverlayTunnels(ctx, newCfg, mgr, overlayRuntime, logger)
}

// reconcileAllSessions reconciles all declarative session types at startup.
func reconcileAllSessions(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	overlayRuntime *overlayRuntime,
	logger *slog.Logger,
) {
	reconcileSessions(ctx, cfg, mgr, sf, logger)
	reconcileEchoSessions(ctx, cfg, mgr, sf, logger)
	reconcileMicroBFDGroups(ctx, cfg, mgr, sf, logger)
	reconcileOverlayTunnels(ctx, cfg, mgr, overlayRuntime, logger)
}

// reconcileSessions diffs the declarative sessions from the config against
// the current session set and creates/destroys sessions as needed.
func reconcileSessions(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	if len(cfg.Sessions) == 0 {
		logger.Debug("no declarative sessions in config, skipping reconciliation")
		return
	}

	desired := make([]bfd.ReconcileConfig, 0, len(cfg.Sessions))
	for _, sc := range cfg.Sessions {
		sessCfg, err := configSessionToBFD(sc, cfg.BFD)
		if err != nil {
			logger.Error("invalid session config, skipping",
				slog.String("peer", sc.Peer),
				slog.String("error", err.Error()),
			)
			continue
		}

		multiHop := sessCfg.Type == bfd.SessionTypeMultiHop
		// RFC 9764: set DF bit on the sender socket when padding is configured.
		var senderOpts []netio.SenderOption
		if sessCfg.PaddedPduSize > 0 {
			senderOpts = append(senderOpts, netio.WithDFBit())
		}
		//nolint:contextcheck // Socket creation is a quick local operation; SenderFactory API is context-free.
		sender, err := sf.createSenderForSession(sessCfg.LocalAddr, multiHop, logger, senderOpts...)
		if err != nil {
			logger.Error("failed to create sender for session, skipping",
				slog.String("peer", sc.Peer),
				slog.String("error", err.Error()),
			)
			continue
		}

		desired = append(desired, bfd.ReconcileConfig{
			Key:           sc.SessionKey(),
			SessionConfig: sessCfg,
			Sender:        sender,
		})
	}

	created, destroyed, err := mgr.ReconcileSessions(ctx, desired)
	if err != nil {
		logger.Error("session reconciliation had errors",
			slog.String("error", err.Error()),
		)
	}

	logger.Info("session reconciliation complete",
		slog.Int("created", created),
		slog.Int("destroyed", destroyed),
	)
}

// reconcileEchoSessions diffs the declarative echo sessions from the config
// against the current echo session set and creates/destroys sessions as needed.
func reconcileEchoSessions(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	if !cfg.Echo.Enabled || len(cfg.Echo.Peers) == 0 {
		logger.Debug("no declarative echo sessions in config, skipping echo reconciliation")
		return
	}

	desired := make([]bfd.EchoReconcileConfig, 0, len(cfg.Echo.Peers))
	for _, ep := range cfg.Echo.Peers {
		echoCfg, err := configEchoToBFD(ep, cfg.Echo)
		if err != nil {
			logger.Error("invalid echo session config, skipping",
				slog.String("peer", ep.Peer),
				slog.String("error", err.Error()),
			)
			continue
		}

		// Echo sessions send on port 3785 (RFC 5881 Section 4, RFC 9747).
		//nolint:contextcheck // Socket creation is a quick local operation; SenderFactory API is context-free.
		sender, err := sf.createSenderForSession(echoCfg.LocalAddr, false, logger, netio.WithDstPort(netio.PortEcho))
		if err != nil {
			logger.Error("failed to create sender for echo session, skipping",
				slog.String("peer", ep.Peer),
				slog.String("error", err.Error()),
			)
			continue
		}

		desired = append(desired, bfd.EchoReconcileConfig{
			Key:               ep.EchoSessionKey(),
			EchoSessionConfig: echoCfg,
			Sender:            sender,
		})
	}

	created, destroyed, err := mgr.ReconcileEchoSessions(ctx, desired)
	if err != nil {
		logger.Error("echo session reconciliation had errors",
			slog.String("error", err.Error()),
		)
	}

	logger.Info("echo session reconciliation complete",
		slog.Int("created", created),
		slog.Int("destroyed", destroyed),
	)
}

// configEchoToBFD converts a config.EchoPeerConfig to a bfd.EchoSessionConfig,
// applying defaults from EchoConfig where per-peer values are zero.
func configEchoToBFD(ep config.EchoPeerConfig, defaults config.EchoConfig) (bfd.EchoSessionConfig, error) {
	peerAddr, err := ep.PeerAddr()
	if err != nil {
		return bfd.EchoSessionConfig{}, fmt.Errorf("parse echo peer address: %w", err)
	}

	localAddr, err := ep.LocalAddr()
	if err != nil {
		return bfd.EchoSessionConfig{}, fmt.Errorf("parse echo local address: %w", err)
	}

	txInterval := ep.TxInterval
	if txInterval == 0 {
		txInterval = defaults.DefaultTxInterval
	}

	detectMult := ep.DetectMult
	if detectMult == 0 {
		detectMult = defaults.DefaultDetectMultiplier
	}

	if detectMult > 255 {
		return bfd.EchoSessionConfig{}, fmt.Errorf("echo detect_mult %d: %w", detectMult, errDetectMultOverflow)
	}

	return bfd.EchoSessionConfig{
		PeerAddr:         peerAddr,
		LocalAddr:        localAddr,
		Interface:        ep.Interface,
		TxInterval:       txInterval,
		DetectMultiplier: uint8(detectMult),
	}, nil
}

// udpSenderFactory implements server.SenderFactory using real UDP sockets
// with RFC 5881 source port allocation and TTL=255 (GTSM).
type udpSenderFactory struct {
	portAlloc *netio.SourcePortAllocator
	senders   map[uint16]*netio.UDPSender
	mu        sync.Mutex
}
