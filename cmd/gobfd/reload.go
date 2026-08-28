package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
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
	if err := validateCompleteControlSessionCandidate(newCfg); err != nil {
		logger.Error("invalid control-session candidate, keeping current settings",
			slog.String("error", err.Error()))
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
	if err := validateCompleteControlSessionCandidate(cfg); err != nil {
		logger.Error("invalid startup control-session candidate, skipping reconciliation",
			slog.String("error", err.Error()))
		return
	}
	reconcileSessions(ctx, cfg, mgr, sf, logger)
	reconcileEchoSessions(ctx, cfg, mgr, sf, logger)
	reconcileMicroBFDGroups(ctx, cfg, mgr, sf, logger)
	reconcileOverlayTunnels(ctx, cfg, mgr, overlayRuntime, logger)
}

func validateCompleteControlSessionCandidate(cfg *config.Config) error {
	if _, err := compileBaseSessionCandidates(cfg); err != nil {
		return err
	}
	if _, err := compileEchoSessionCandidates(cfg); err != nil {
		return err
	}
	if _, _, err := compileMicroBFDCandidates(cfg); err != nil {
		return err
	}
	for _, params := range buildOverlayTunnelParams(cfg, nil) {
		if _, err := compileOverlaySessionCandidates(params); err != nil {
			return err
		}
	}
	return nil
}

// reconcileSessions diffs the declarative sessions from the config against
// the current session set and creates/destroys sessions as needed.
type declarativeSenderFactory interface {
	createSenderForSession(
		localAddr netip.Addr,
		multiHop bool,
		logger *slog.Logger,
		senderOpts ...netio.SenderOption,
	) (bfd.PacketSender, uint16, error)
	CloseSender(srcPort uint16) error
}

func reconcileSessions(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logger *slog.Logger,
) {
	candidates, err := compileBaseSessionCandidates(cfg)
	if err != nil {
		logger.Error("invalid declarative session candidate, keeping current sessions",
			slog.String("error", err.Error()),
		)
		return
	}

	desired := make([]bfd.ReconcileConfig, 0, len(candidates))
	for _, candidate := range candidates {
		rc := candidate.reconcile
		rc.SenderLeaseFactory = declarativeSenderLeaseFactoryFor(
			sf,
			candidate.config.LocalAddr,
			candidate.multiHop,
			logger,
			candidate.senderOpts...,
		)
		desired = append(desired, rc)
	}

	result := mgr.ReconcileSessionsForOwnerDetailed(
		ctx,
		bfd.ConfigReconciliationOwner(),
		desired,
	)
	if err := result.Err(); err != nil {
		logger.Error("session reconciliation had errors",
			slog.String("error", err.Error()),
			slog.Int("created", result.Created),
			slog.Int("released", result.Released),
			slog.Int("pending", result.Pending),
			slog.Int("failed", result.Failed),
			slog.Any("error_codes", reconcileErrorCodes(result)),
		)
		return
	}

	logger.Info("session reconciliation complete",
		slog.Int("created", result.Created),
		slog.Int("released", result.Released),
	)
}

func reconcileErrorCodes(result bfd.ReconcileResult) []string {
	codes := make([]string, 0, len(result.Errors))
	for _, reconcileErr := range result.Errors {
		codes = append(codes, reconcileErr.Code.String())
	}
	return codes
}

func declarativeSenderLeaseFactoryFor(
	sf declarativeSenderFactory,
	localAddr netip.Addr,
	multiHop bool,
	logger *slog.Logger,
	senderOpts ...netio.SenderOption,
) bfd.SenderLeaseFactory {
	return func() (*bfd.SenderLease, error) {
		sender, srcPort, err := sf.createSenderForSession(
			localAddr, multiHop, logger, senderOpts...,
		)
		if err != nil {
			return nil, fmt.Errorf("create session sender: %w", err)
		}
		return bfd.NewSenderLease(sender, func() error {
			if err := sf.CloseSender(srcPort); err != nil {
				return fmt.Errorf("close session sender port %d: %w", srcPort, err)
			}
			return nil
		}), nil
	}
}

type baseSessionCandidate struct {
	peer       string
	config     bfd.SessionConfig
	reconcile  bfd.ReconcileConfig
	multiHop   bool
	senderOpts []netio.SenderOption
}

func compileBaseSessionCandidates(cfg *config.Config) ([]baseSessionCandidate, error) {
	candidates := make([]baseSessionCandidate, 0, len(cfg.Sessions))
	desired := make([]bfd.ReconcileConfig, 0, len(cfg.Sessions))
	for _, sc := range cfg.Sessions {
		sessCfg, err := configSessionToBFD(sc, cfg.BFD)
		if err != nil {
			return nil, fmt.Errorf("session %q: %w", sc.Peer, err)
		}

		var senderOpts []netio.SenderOption
		if sessCfg.PaddedPduSize > 0 {
			senderOpts = append(senderOpts, netio.WithDFBit())
		}
		rc := bfd.ReconcileConfig{Key: sc.SessionKey(), SessionConfig: sessCfg}
		candidates = append(candidates, baseSessionCandidate{
			peer:       sc.Peer,
			config:     sessCfg,
			reconcile:  rc,
			multiHop:   sessCfg.Type == bfd.SessionTypeMultiHop,
			senderOpts: senderOpts,
		})
		desired = append(desired, rc)
	}
	if err := bfd.ValidateReconcileConfigs(desired); err != nil {
		return nil, fmt.Errorf("validate complete base session set: %w", err)
	}
	return candidates, nil
}

type echoReconcileOutcome struct {
	Created   int
	Destroyed int
	Err       error
}

type echoSessionCandidate struct {
	key    string
	config bfd.EchoSessionConfig
}

func compileEchoSessionCandidates(cfg *config.Config) ([]echoSessionCandidate, error) {
	if !cfg.Echo.Enabled {
		return nil, nil
	}
	candidates := make([]echoSessionCandidate, 0, len(cfg.Echo.Peers))
	validationSet := make([]bfd.EchoReconcileConfig, 0, len(cfg.Echo.Peers))
	for _, ep := range cfg.Echo.Peers {
		echoCfg, err := configEchoToBFD(ep, cfg.Echo)
		if err != nil {
			return nil, fmt.Errorf("echo session %q: %w", ep.Peer, err)
		}
		candidate := echoSessionCandidate{key: ep.EchoSessionKey(), config: echoCfg}
		candidates = append(candidates, candidate)
		validationSet = append(validationSet, bfd.EchoReconcileConfig{
			Key: candidate.key, EchoSessionConfig: candidate.config,
		})
	}
	if err := bfd.ValidateEchoReconcileConfigs(validationSet); err != nil {
		return nil, fmt.Errorf("validate complete echo session set: %w", err)
	}
	return candidates, nil
}

// reconcileEchoSessions reconciles the complete declarative Echo desired set
// and returns its structured apply outcome.
func reconcileEchoSessions(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logger *slog.Logger,
) echoReconcileOutcome {
	candidates, err := compileEchoSessionCandidates(cfg)
	if err != nil {
		logger.Error("invalid declarative echo candidate, keeping current sessions",
			slog.String("error", err.Error()))
		return echoReconcileOutcome{Err: err}
	}

	desired := make([]bfd.EchoReconcileConfig, 0, len(candidates))
	for _, candidate := range candidates {
		desired = append(desired, bfd.EchoReconcileConfig{
			Key: candidate.key, EchoSessionConfig: candidate.config,
			SenderLeaseFactory: declarativeSenderLeaseFactoryFor(
				sf,
				candidate.config.LocalAddr,
				false,
				logger,
				netio.WithDstPort(netio.PortEcho),
			),
		})
	}

	result := mgr.ReconcileEchoSessionsDetailed(ctx, desired)
	if err := result.Err(); err != nil {
		logger.Error("echo session reconciliation had errors",
			slog.String("error", err.Error()),
			slog.Int("created", result.Created),
			slog.Int("released", result.Released),
			slog.Int("pending", result.Pending),
			slog.Int("failed", result.Failed),
			slog.Any("error_codes", reconcileErrorCodes(result)),
		)
		return echoReconcileOutcome{
			Created: result.Created, Destroyed: result.Released, Err: err,
		}
	}

	logger.Info("echo session reconciliation complete",
		slog.Int("created", result.Created),
		slog.Int("released", result.Released),
	)
	return echoReconcileOutcome{Created: result.Created, Destroyed: result.Released}
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

	if detectMult > maxBFDWireUint8 {
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
