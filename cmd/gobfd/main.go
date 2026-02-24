// GoBFD daemon -- BFD protocol implementation (RFC 5880/5881).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"runtime/trace"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/gobgp"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/server"
	appversion "github.com/dantte-lp/gobfd/internal/version"
)

// shutdownTimeout is the maximum time to wait for HTTP servers to drain
// active connections during graceful shutdown.
const shutdownTimeout = 10 * time.Second

// errDetectMultOverflow indicates the detect multiplier exceeds uint8 range.
var errDetectMultOverflow = errors.New("detect multiplier exceeds maximum 255")

// drainTimeout is the time to wait after setting sessions to AdminDown
// before proceeding with shutdown. This ensures the final AdminDown
// packets are transmitted to peers (RFC 5880 Section 6.8.16).
const drainTimeout = 2 * time.Second

// flightRecorderMinAge is the minimum window age for the flight recorder.
// Captures the last 500ms of execution traces for debugging BFD failures.
const flightRecorderMinAge = 500 * time.Millisecond

// flightRecorderMaxBytes is the upper bound on flight recorder window size.
const flightRecorderMaxBytes = 2 * 1024 * 1024 // 2 MiB

func main() {
	os.Exit(run())
}

func run() int {
	// 1. Parse flags.
	configPath := flag.String("config", "", "path to configuration file (YAML)")
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(appversion.Full("gobfd"))
		return 0
	}

	// 2. Load config.
	cfg, err := loadConfig(*configPath)
	if err != nil {
		// Logger is not set up yet; use a temporary stderr logger.
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("failed to load configuration",
			slog.String("error", err.Error()),
		)
		return 1
	}

	// 3. Set up logger with dynamic level support for SIGHUP reload.
	logLevel := new(slog.LevelVar)
	logLevel.Set(config.ParseLogLevel(cfg.Log.Level))
	logger := newLoggerWithLevel(cfg.Log, logLevel)

	logger.Info("gobfd starting",
		slog.String("version", appversion.Version),
		slog.String("grpc_addr", cfg.GRPC.Addr),
		slog.String("metrics_addr", cfg.Metrics.Addr),
	)

	// 4. Start flight recorder for post-mortem debugging of BFD failures.
	fr := startFlightRecorder(logger)

	// 5. Create Prometheus metrics collector.
	reg := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(reg)

	// 6. Create BFD session manager with metrics and unsolicited BFD (RFC 9468).
	mgrOpts := []bfd.ManagerOption{bfd.WithManagerMetrics(collector)}
	if cfg.Unsolicited.Enabled {
		policy, err := buildUnsolicitedPolicy(cfg.Unsolicited)
		if err != nil {
			logger.Error("invalid unsolicited BFD config",
				slog.String("error", err.Error()),
			)
			return 1
		}
		mgrOpts = append(mgrOpts, bfd.WithUnsolicitedPolicy(policy))

		sf := newUDPSenderFactory()
		// Unsolicited sessions are single-hop only (RFC 9468 Section 6.1).
		// Use wildcard address; the sender resolves local addr per-packet.
		sender, err := sf.createSenderForSession(netip.IPv4Unspecified(), false, logger)
		if err != nil {
			logger.Error("failed to create unsolicited BFD sender",
				slog.String("error", err.Error()),
			)
			return 1
		}
		mgrOpts = append(mgrOpts, bfd.WithUnsolicitedSender(sender))
		logger.Info("unsolicited BFD enabled (RFC 9468)",
			slog.Int("max_sessions", policy.MaxSessions),
		)
	}
	mgr := bfd.NewManager(logger, mgrOpts...)
	defer mgr.Close()

	// 7. Run servers.
	if err := runServers(cfg, mgr, reg, logger, *configPath, logLevel, fr); err != nil {
		logger.Error("gobfd exited with error",
			slog.String("error", err.Error()),
		)
		return 1
	}

	logger.Info("gobfd stopped")
	return 0
}

// runServers sets up and runs the gRPC and metrics HTTP servers using an
// errgroup with signal-aware context for graceful shutdown.
func runServers(
	cfg *config.Config,
	mgr *bfd.Manager,
	reg *prometheus.Registry,
	logger *slog.Logger,
	configPath string,
	logLevel *slog.LevelVar,
	fr *trace.FlightRecorder,
) error {
	// Create real UDP sender factory backed by SourcePortAllocator.
	sf := newUDPSenderFactory()

	metricsSrv := newMetricsServer(cfg.Metrics, reg)
	grpcSrv := newGRPCServer(cfg.GRPC, mgr, sf, logger)

	// errgroup with signal-aware context.
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	g, gCtx := errgroup.WithContext(ctx)

	// Start the Manager dispatch goroutine. This MUST run before any
	// sessions are created so that state change notifications are forwarded
	// from the internal rawNotifyCh to the public StateChanges channel.
	// Also handles micro-BFD aggregate state dispatch (RFC 7130).
	g.Go(func() error {
		mgr.RunDispatch(gCtx)
		return nil
	})

	// Start BFD packet listeners and receivers for incoming packets.
	lnCleanup, err := startListenersAndReceivers(gCtx, g, cfg, mgr, logger)
	if err != nil {
		return fmt.Errorf("create BFD listeners: %w", err)
	}
	defer lnCleanup()

	startHTTPServers(gCtx, g, cfg, grpcSrv, metricsSrv, logger)
	startDaemonGoroutines(gCtx, g, configPath, logLevel, mgr, sf, logger)

	// GoBGP integration goroutine (RFC 5882 Section 4.3).
	bgpCloser, err := startGoBGPHandler(gCtx, g, cfg.GoBGP, mgr, logger)
	if err != nil {
		return fmt.Errorf("start gobgp handler: %w", err)
	}
	defer closeGoBGPClient(bgpCloser, logger)

	// Start overlay tunnel receivers (VXLAN on port 4789, Geneve on port 6081).
	overlayCleanup := startOverlayReceivers(gCtx, g, cfg, mgr, sf, logger)
	defer overlayCleanup()

	// Reconcile declarative sessions from config at startup.
	reconcileSessions(gCtx, cfg, mgr, sf, logger)

	// Reconcile declarative echo sessions from config at startup (RFC 9747).
	reconcileEchoSessions(gCtx, cfg, mgr, sf, logger)

	// Reconcile micro-BFD groups from config at startup (RFC 7130).
	reconcileMicroBFDGroups(gCtx, cfg, mgr, sf, logger)

	// Reconcile overlay tunnel BFD sessions (VXLAN RFC 8971, Geneve RFC 9521).
	reconcileOverlayTunnels(gCtx, cfg, mgr, sf, logger)

	notifyReady(logger)

	// Shutdown goroutine: waits for context cancellation.
	g.Go(func() error {
		<-gCtx.Done()
		return gracefulShutdown(gCtx, mgr, logger, fr, grpcSrv, metricsSrv)
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("run servers: %w", err)
	}
	return nil
}

// startHTTPServers registers the gRPC and metrics HTTP server goroutines.
func startHTTPServers(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	grpcSrv *http.Server,
	metricsSrv *http.Server,
	logger *slog.Logger,
) {
	lc := net.ListenConfig{}

	g.Go(func() error {
		logger.Info("gRPC server listening", slog.String("addr", cfg.GRPC.Addr))
		return listenAndServe(ctx, &lc, grpcSrv, cfg.GRPC.Addr)
	})

	g.Go(func() error {
		logger.Info("metrics server listening",
			slog.String("addr", cfg.Metrics.Addr),
			slog.String("path", cfg.Metrics.Path),
		)
		return listenAndServe(ctx, &lc, metricsSrv, cfg.Metrics.Addr)
	})
}

// startDaemonGoroutines registers the watchdog and SIGHUP reload goroutines.
func startDaemonGoroutines(
	ctx context.Context,
	g *errgroup.Group,
	configPath string,
	logLevel *slog.LevelVar,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	g.Go(func() error {
		return runWatchdog(ctx, logger)
	})

	sigHUP := make(chan os.Signal, 1)
	signal.Notify(sigHUP, syscall.SIGHUP)
	g.Go(func() error {
		defer signal.Stop(sigHUP)
		handleSIGHUP(ctx, sigHUP, configPath, logLevel, mgr, sf, logger)
		return nil
	})
}

// closeGoBGPClient closes the GoBGP client if non-nil, logging any error.
func closeGoBGPClient(client gobgp.Client, logger *slog.Logger) {
	if client == nil {
		return
	}
	if err := client.Close(); err != nil {
		logger.Warn("failed to close gobgp client",
			slog.String("error", err.Error()),
		)
	}
}

// -------------------------------------------------------------------------
// Systemd Integration — sd_notify + watchdog
// -------------------------------------------------------------------------

// notifyReady sends READY=1 to systemd, indicating the daemon has
// completed initialization and is ready to serve.
func notifyReady(logger *slog.Logger) {
	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		logger.Warn("failed to notify systemd readiness",
			slog.String("error", err.Error()),
		)
		return
	}
	if sent {
		logger.Info("notified systemd: READY")
	}
}

// notifyStopping sends STOPPING=1 to systemd, indicating the daemon
// is beginning graceful shutdown.
func notifyStopping(logger *slog.Logger) {
	sent, err := daemon.SdNotify(false, daemon.SdNotifyStopping)
	if err != nil {
		logger.Warn("failed to notify systemd stopping",
			slog.String("error", err.Error()),
		)
		return
	}
	if sent {
		logger.Info("notified systemd: STOPPING")
	}
}

// runWatchdog sends periodic watchdog keepalives to systemd.
// The interval is WatchdogSec/2 as recommended by the systemd documentation.
// If watchdog is not configured, the goroutine exits immediately.
func runWatchdog(ctx context.Context, logger *slog.Logger) error {
	interval, err := daemon.SdWatchdogEnabled(false)
	if err != nil {
		logger.Warn("failed to check systemd watchdog",
			slog.String("error", err.Error()),
		)
		return nil
	}
	if interval == 0 {
		logger.Debug("systemd watchdog not configured, skipping keepalive")
		return nil
	}

	// Send keepalive at half the watchdog interval.
	tickInterval := interval / 2
	logger.Info("systemd watchdog enabled",
		slog.Duration("watchdog_sec", interval),
		slog.Duration("keepalive_interval", tickInterval),
	)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, wdErr := daemon.SdNotify(false, daemon.SdNotifyWatchdog); wdErr != nil {
				logger.Warn("failed to send watchdog keepalive",
					slog.String("error", wdErr.Error()),
				)
			}
		}
	}
}

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
	logger *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sigHUP:
			logger.Info("received SIGHUP, reloading configuration")
			reloadConfig(ctx, configPath, logLevel, mgr, sf, logger)
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
	reconcileOverlayTunnels(ctx, newCfg, mgr, sf, logger)
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

func newUDPSenderFactory() *udpSenderFactory {
	return &udpSenderFactory{
		portAlloc: netio.NewSourcePortAllocator(),
		senders:   make(map[uint16]*netio.UDPSender),
	}
}

func (f *udpSenderFactory) CreateSender(
	localAddr netip.Addr,
	multiHop bool,
	logger *slog.Logger,
) (bfd.PacketSender, uint16, error) {
	srcPort, err := f.portAlloc.Allocate()
	if err != nil {
		return nil, 0, fmt.Errorf("allocate source port: %w", err)
	}

	sender, err := netio.NewUDPSender(localAddr, srcPort, multiHop, logger)
	if err != nil {
		f.portAlloc.Release(srcPort)
		return nil, 0, fmt.Errorf("create UDP sender %s:%d: %w", localAddr, srcPort, err)
	}

	f.mu.Lock()
	f.senders[srcPort] = sender
	f.mu.Unlock()

	return sender, srcPort, nil
}

func (f *udpSenderFactory) CloseSender(srcPort uint16) error {
	f.mu.Lock()
	sender, ok := f.senders[srcPort]
	if ok {
		delete(f.senders, srcPort)
	}
	f.mu.Unlock()

	if !ok {
		return nil
	}

	f.portAlloc.Release(srcPort)

	if err := sender.Close(); err != nil {
		return fmt.Errorf("close sender port %d: %w", srcPort, err)
	}
	return nil
}

// createSenderForSession allocates a source port and creates a UDPSender
// for a declarative session. Used by reconcileSessions.
// When paddedPduSize is nonzero, the DF bit is set on the socket (RFC 9764).
func (f *udpSenderFactory) createSenderForSession(
	localAddr netip.Addr,
	multiHop bool,
	logger *slog.Logger,
	senderOpts ...netio.SenderOption,
) (bfd.PacketSender, error) {
	srcPort, err := f.portAlloc.Allocate()
	if err != nil {
		return nil, fmt.Errorf("allocate source port: %w", err)
	}

	sender, err := netio.NewUDPSender(localAddr, srcPort, multiHop, logger, senderOpts...)
	if err != nil {
		f.portAlloc.Release(srcPort)
		return nil, fmt.Errorf("create UDP sender %s:%d: %w", localAddr, srcPort, err)
	}

	f.mu.Lock()
	f.senders[srcPort] = sender
	f.mu.Unlock()

	return sender, nil
}

// configSessionToBFD converts a config.SessionConfig to a bfd.SessionConfig,
// applying defaults from BFDConfig where per-session values are zero.
func configSessionToBFD(sc config.SessionConfig, defaults config.BFDConfig) (bfd.SessionConfig, error) {
	peerAddr, err := sc.PeerAddr()
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("parse peer address: %w", err)
	}

	localAddr, err := sc.LocalAddr()
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("parse local address: %w", err)
	}

	sessType := bfd.SessionTypeSingleHop
	if sc.Type == "multi_hop" {
		sessType = bfd.SessionTypeMultiHop
	}

	desiredMinTx := sc.DesiredMinTx
	if desiredMinTx == 0 {
		desiredMinTx = defaults.DefaultDesiredMinTx
	}

	requiredMinRx := sc.RequiredMinRx
	if requiredMinRx == 0 {
		requiredMinRx = defaults.DefaultRequiredMinRx
	}

	detectMult := sc.DetectMult
	if detectMult == 0 {
		detectMult = defaults.DefaultDetectMultiplier
	}

	if detectMult > 255 {
		return bfd.SessionConfig{}, fmt.Errorf("detect_mult %d: %w", detectMult, errDetectMultOverflow)
	}

	// RFC 7419: align intervals to common set for hardware interop.
	if defaults.AlignIntervals {
		desiredMinTx = bfd.AlignToCommonInterval(desiredMinTx)
		requiredMinRx = bfd.AlignToCommonInterval(requiredMinRx)
	}

	// RFC 9764: per-session padded PDU size, falling back to global default.
	paddedPduSize := sc.PaddedPduSize
	if paddedPduSize == 0 {
		paddedPduSize = defaults.DefaultPaddedPduSize
	}

	return bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Interface:             sc.Interface,
		Type:                  sessType,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
		PaddedPduSize:         paddedPduSize,
	}, nil
}

// buildUnsolicitedPolicy converts config.UnsolicitedConfig to bfd.UnsolicitedPolicy.
func buildUnsolicitedPolicy(cfg config.UnsolicitedConfig) (*bfd.UnsolicitedPolicy, error) {
	interfaces := make(map[string]bfd.UnsolicitedInterfaceConfig, len(cfg.Interfaces))
	for name, ifCfg := range cfg.Interfaces {
		prefixes := make([]netip.Prefix, 0, len(ifCfg.AllowedPrefixes))
		for _, s := range ifCfg.AllowedPrefixes {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("interface %s: parse prefix %q: %w", name, s, err)
			}
			prefixes = append(prefixes, p)
		}
		interfaces[name] = bfd.UnsolicitedInterfaceConfig{
			Enabled:         ifCfg.Enabled,
			AllowedPrefixes: prefixes,
		}
	}

	detectMult := cfg.SessionDefaults.DetectMult
	if detectMult > 255 {
		return nil, fmt.Errorf("unsolicited detect_mult %d: %w", detectMult, errDetectMultOverflow)
	}

	return &bfd.UnsolicitedPolicy{
		Enabled:     cfg.Enabled,
		Interfaces:  interfaces,
		MaxSessions: cfg.MaxSessions,
		SessionDefaults: bfd.UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  cfg.SessionDefaults.DesiredMinTx,
			RequiredMinRxInterval: cfg.SessionDefaults.RequiredMinRx,
			DetectMultiplier:      uint8(detectMult),
		},
		CleanupTimeout: cfg.CleanupTimeout,
	}, nil
}

// -------------------------------------------------------------------------
// BFD Listeners — receive incoming BFD Control packets
// -------------------------------------------------------------------------

// startListenersAndReceivers creates all BFD listeners and starts receiver
// goroutines in the errgroup. Returns a cleanup function that closes all
// listeners when called.
func startListenersAndReceivers(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	mgr *bfd.Manager,
	logger *slog.Logger,
) (func(), error) {
	//nolint:contextcheck // Listener socket creation uses context.Background() internally.
	lnResult, err := createListeners(cfg, logger)
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		closeListeners(lnResult.control, logger)
		closeListeners(lnResult.echo, logger)
		closeListeners(lnResult.microBFD, logger)
	}

	if len(lnResult.control) > 0 {
		recv := netio.NewReceiver(mgr, logger)
		g.Go(func() error { return recv.Run(ctx, lnResult.control...) })
	}

	// Start echo receiver for RFC 9747 echo packets (port 3785).
	if len(lnResult.echo) > 0 {
		echoRecv := netio.NewEchoReceiver(mgr, logger)
		g.Go(func() error { return echoRecv.Run(ctx, lnResult.echo...) })
	}

	// Start micro-BFD receiver for RFC 7130 packets (port 6784).
	// Micro-BFD uses the same BFD Control packet format as single-hop,
	// so we reuse the standard Receiver with the micro-BFD listeners.
	if len(lnResult.microBFD) > 0 {
		microRecv := netio.NewReceiver(mgr, logger)
		g.Go(func() error { return microRecv.Run(ctx, lnResult.microBFD...) })
	}

	return cleanup, nil
}

// listenersResult holds the listeners created by createListeners, separated
// by type (control, echo, micro-BFD) for independent receiver goroutines.
type listenersResult struct {
	control  []*netio.Listener
	echo     []*netio.Listener
	microBFD []*netio.Listener
}

// createListeners inspects the declared sessions and echo peers and creates
// the necessary BFD packet listeners. For each unique (localAddr, type) pair
// a single listener is created on the appropriate port (3784 for single-hop,
// 4784 for multi-hop, 3785 for echo). Returns the listeners and any error.
func createListeners(cfg *config.Config, logger *slog.Logger) (listenersResult, error) {
	var result listenersResult

	controlListeners, err := createControlListeners(cfg, logger)
	if err != nil {
		return listenersResult{}, err
	}
	result.control = controlListeners

	// Create echo listeners (port 3785) if echo is enabled with peers.
	if cfg.Echo.Enabled && len(cfg.Echo.Peers) > 0 {
		echoListeners, err := createEchoListeners(cfg, logger)
		if err != nil {
			closeListeners(result.control, logger)
			return listenersResult{}, fmt.Errorf("create echo listeners: %w", err)
		}
		result.echo = echoListeners
	}

	// Create micro-BFD listeners (port 6784) for each member link (RFC 7130).
	if len(cfg.MicroBFD.Groups) > 0 {
		microListeners, err := createMicroBFDListeners(cfg, logger)
		if err != nil {
			closeListeners(result.control, logger)
			closeListeners(result.echo, logger)
			return listenersResult{}, fmt.Errorf("create micro-BFD listeners: %w", err)
		}
		result.microBFD = microListeners
	}

	return result, nil
}

// createControlListeners creates BFD Control packet listeners on port 3784
// (single-hop) and 4784 (multi-hop) for each unique (localAddr, type) pair.
func createControlListeners(cfg *config.Config, logger *slog.Logger) ([]*netio.Listener, error) {
	type listenerKey struct {
		addr     netip.Addr
		multiHop bool
	}

	seen := make(map[listenerKey]struct{})
	var listeners []*netio.Listener

	for _, sc := range cfg.Sessions {
		localAddr, err := sc.LocalAddr()
		if err != nil || !localAddr.IsValid() {
			continue
		}

		multiHop := sc.Type == "multi_hop"
		key := listenerKey{addr: localAddr, multiHop: multiHop}

		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		lnCfg := netio.ListenerConfig{
			Addr:     localAddr,
			IfName:   sc.Interface,
			MultiHop: multiHop,
		}
		if multiHop {
			lnCfg.Port = netio.PortMultiHop
		} else {
			lnCfg.Port = netio.PortSingleHop
		}

		ln, err := netio.NewListener(lnCfg)
		if err != nil {
			closeListeners(listeners, logger)
			return nil, fmt.Errorf("create listener on %s (multihop=%v): %w", localAddr, multiHop, err)
		}

		logger.Info("BFD listener started",
			slog.String("addr", localAddr.String()),
			slog.Bool("multi_hop", multiHop),
			slog.String("interface", sc.Interface),
		)

		listeners = append(listeners, ln)
	}

	return listeners, nil
}

// createEchoListeners creates listeners on port 3785 for echo packet
// reception. One listener per unique local address.
func createEchoListeners(cfg *config.Config, logger *slog.Logger) ([]*netio.Listener, error) {
	type echoKey struct {
		addr netip.Addr
	}

	seen := make(map[echoKey]struct{})
	var listeners []*netio.Listener

	for _, ep := range cfg.Echo.Peers {
		localAddr, err := ep.LocalAddr()
		if err != nil || !localAddr.IsValid() {
			continue
		}

		key := echoKey{addr: localAddr}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		lnCfg := netio.ListenerConfig{
			Addr:     localAddr,
			IfName:   ep.Interface,
			Port:     netio.PortEcho,
			MultiHop: false, // Echo uses single-hop TTL semantics.
		}

		ln, err := netio.NewListener(lnCfg)
		if err != nil {
			closeListeners(listeners, logger)
			return nil, fmt.Errorf("create echo listener on %s: %w", localAddr, err)
		}

		logger.Info("BFD echo listener started",
			slog.String("addr", localAddr.String()),
			slog.String("interface", ep.Interface),
		)

		listeners = append(listeners, ln)
	}

	return listeners, nil
}

// closeListeners closes all provided listeners, logging any errors.
func closeListeners(listeners []*netio.Listener, logger *slog.Logger) {
	for _, ln := range listeners {
		if err := ln.Close(); err != nil {
			logger.Warn("failed to close BFD listener",
				slog.String("error", err.Error()),
			)
		}
	}
}

// -------------------------------------------------------------------------
// Graceful Shutdown — drain sessions + stop servers
// -------------------------------------------------------------------------

// gracefulShutdown performs an orderly shutdown: signals systemd, drains
// BFD sessions to AdminDown (RFC 5880 Section 6.8.16), dumps flight
// recorder trace, then shuts down HTTP servers.
//
// The parent context is already cancelled when this function is called.
// A fresh timeout context is created internally for server drain.
func gracefulShutdown(
	ctx context.Context,
	mgr *bfd.Manager,
	logger *slog.Logger,
	fr *trace.FlightRecorder,
	servers ...*http.Server,
) error {
	logger.Info("initiating graceful shutdown")
	notifyStopping(logger)

	// Drain all BFD sessions: set to AdminDown with DiagAdminDown.
	// This ensures peers see an intentional shutdown, not a failure.
	mgr.DrainAllSessions()

	// Wait for final AdminDown packets to be transmitted.
	time.Sleep(drainTimeout)

	// Explicitly cancel all session goroutines after the drain period.
	// Sessions use context.WithoutCancel so they don't see SIGTERM directly;
	// Close cancels their individual contexts.
	mgr.Close()

	// Stop flight recorder.
	if fr != nil {
		fr.Stop()
		logger.Debug("flight recorder stopped")
	}

	// Derive a fresh shutdown context from the parent (which is cancelled).
	// context.WithoutCancel detaches from the parent's cancellation so we
	// can enforce our own drain timeout.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	var shutdownErr error
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown server: %w", err))
		}
	}
	return shutdownErr
}

// -------------------------------------------------------------------------
// Flight Recorder — Go 1.26 runtime/trace
// -------------------------------------------------------------------------

// startFlightRecorder initializes and starts the Go 1.26 FlightRecorder
// for post-mortem debugging of BFD session failures. The recorder maintains
// a rolling window of execution trace data that can be dumped on demand.
func startFlightRecorder(logger *slog.Logger) *trace.FlightRecorder {
	fr := trace.NewFlightRecorder(trace.FlightRecorderConfig{
		MinAge:   flightRecorderMinAge,
		MaxBytes: flightRecorderMaxBytes,
	})

	if err := fr.Start(); err != nil {
		logger.Warn("failed to start flight recorder",
			slog.String("error", err.Error()),
		)
		return nil
	}

	logger.Info("flight recorder started",
		slog.Duration("min_age", flightRecorderMinAge),
		slog.Uint64("max_bytes", flightRecorderMaxBytes),
	)

	return fr
}

// -------------------------------------------------------------------------
// Server Setup
// -------------------------------------------------------------------------

// listenAndServe creates a TCP listener using the ListenConfig (for noctx
// compliance) and serves HTTP requests until the server is shut down.
func listenAndServe(ctx context.Context, lc *net.ListenConfig, srv *http.Server, addr string) error {
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve on %s: %w", addr, err)
	}
	return nil
}

// newMetricsServer creates an HTTP server for the Prometheus metrics endpoint.
func newMetricsServer(cfg config.MetricsConfig, reg *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(cfg.Path, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// newGRPCServer creates an HTTP server for the ConnectRPC gRPC endpoint.
// The handler is wrapped with h2c to support HTTP/2 without TLS, which is
// required for gRPC clients that connect over plaintext (e.g., gobfdctl).
// Includes standard gRPC health checking (grpc.health.v1).
func newGRPCServer(cfg config.GRPCConfig, mgr *bfd.Manager, sf server.SenderFactory, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()

	// BFD service handler.
	path, handler := server.New(mgr, sf, logger,
		server.LoggingInterceptorOption(logger),
		server.RecoveryInterceptorOption(logger),
	)
	mux.Handle(path, handler)

	// gRPC health check handler (grpc.health.v1).
	// Reports SERVING for the overall server and the BFD service.
	checker := grpchealth.NewStaticChecker(
		grpchealth.HealthV1ServiceName,
		"bfd.v1.BfdService",
	)
	mux.Handle(grpchealth.NewHandler(checker))

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

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

	client, err := gobgp.NewGRPCClient(gobgp.GRPCClientConfig{
		Addr: cfg.Addr,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create gobgp client: %w", err)
	}

	handler, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client:   client,
		Strategy: gobgp.Strategy(cfg.Strategy),
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
		return handler.Run(ctx, mgr.StateChanges())
	})

	logger.Info("gobgp integration enabled",
		slog.String("addr", cfg.Addr),
		slog.String("strategy", cfg.Strategy),
		slog.Bool("dampening", cfg.Dampening.Enabled),
	)

	return client, nil
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

// -------------------------------------------------------------------------
// Micro-BFD — RFC 7130 LAG member link sessions
// -------------------------------------------------------------------------

// createMicroBFDListeners creates listeners on port 6784 for each unique
// (localAddr, memberLink) pair across all micro-BFD groups.
// RFC 7130 Section 2.1: each member link has its own BFD session on port 6784.
func createMicroBFDListeners(cfg *config.Config, logger *slog.Logger) ([]*netio.Listener, error) {
	type microKey struct {
		addr   netip.Addr
		ifName string
	}

	seen := make(map[microKey]struct{})
	var listeners []*netio.Listener

	for _, group := range cfg.MicroBFD.Groups {
		localAddr, err := netip.ParseAddr(group.LocalAddr)
		if err != nil || !localAddr.IsValid() {
			logger.Warn("skipping micro-BFD group with invalid local address",
				slog.String("lag", group.LAGInterface),
				slog.String("local_addr", group.LocalAddr),
			)
			continue
		}

		for _, member := range group.MemberLinks {
			key := microKey{addr: localAddr, ifName: member}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			lnCfg := netio.ListenerConfig{
				Addr:     localAddr,
				IfName:   member,
				Port:     netio.PortMicroBFD,
				MultiHop: false, // Micro-BFD uses single-hop TTL=255 (RFC 7130 Section 2).
			}

			ln, err := netio.NewListener(lnCfg)
			if err != nil {
				closeListeners(listeners, logger)
				return nil, fmt.Errorf("create micro-BFD listener on %s%%%s: %w",
					localAddr, member, err)
			}

			logger.Info("micro-BFD listener started",
				slog.String("addr", localAddr.String()),
				slog.String("member", member),
				slog.Uint64("port", uint64(netio.PortMicroBFD)),
			)

			listeners = append(listeners, ln)
		}
	}

	return listeners, nil
}

// reconcileMicroBFDGroups creates and destroys micro-BFD groups and their
// per-member BFD sessions based on the current configuration.
//
// For each group in the config:
//  1. Create the MicroBFDGroup in the Manager (aggregate state tracker)
//  2. For each member link: create a BFD session with SessionTypeMicroBFD,
//     bound to the member interface via SO_BINDTODEVICE, on port 6784
//
// On SIGHUP reload, groups not in the new config are destroyed along
// with their member sessions.
func reconcileMicroBFDGroups(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	if len(cfg.MicroBFD.Groups) == 0 {
		logger.Debug("no micro-BFD groups in config, skipping reconciliation")
		return
	}

	reconcileMicroBFDGroupState(cfg, mgr, logger)
	reconcileMicroBFDMemberSessions(ctx, cfg, mgr, sf, logger)
}

// reconcileMicroBFDGroupState performs Step 1 of micro-BFD reconciliation:
// create/destroy MicroBFDGroup objects in the Manager based on config.
func reconcileMicroBFDGroupState(
	cfg *config.Config,
	mgr *bfd.Manager,
	logger *slog.Logger,
) {
	desired := make([]bfd.MicroBFDReconcileConfig, 0, len(cfg.MicroBFD.Groups))
	for _, group := range cfg.MicroBFD.Groups {
		microCfg, err := configMicroBFDToBFD(group)
		if err != nil {
			logger.Error("invalid micro-BFD group config, skipping",
				slog.String("lag", group.LAGInterface),
				slog.String("error", err.Error()),
			)
			continue
		}
		desired = append(desired, bfd.MicroBFDReconcileConfig{
			Key:    group.LAGInterface,
			Config: microCfg,
		})
	}

	created, destroyed, err := mgr.ReconcileMicroBFDGroups(desired)
	if err != nil {
		logger.Error("micro-BFD group reconciliation had errors",
			slog.String("error", err.Error()),
		)
	}

	logger.Info("micro-BFD group reconciliation complete",
		slog.Int("groups_created", created),
		slog.Int("groups_destroyed", destroyed),
	)
}

// reconcileMicroBFDMemberSessions performs Step 2 of micro-BFD reconciliation:
// create/destroy per-member-link BFD sessions with SO_BINDTODEVICE on port 6784.
func reconcileMicroBFDMemberSessions(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	desiredSessions := make([]bfd.ReconcileConfig, 0, len(cfg.MicroBFD.Groups)*2)

	for _, group := range cfg.MicroBFD.Groups {
		//nolint:contextcheck // Socket creation is a quick local operation.
		sessions := buildMemberSessions(cfg, group, sf, logger)
		desiredSessions = append(desiredSessions, sessions...)
	}

	if len(desiredSessions) == 0 {
		return
	}

	sessCreated, sessDestroyed, sessErr := mgr.ReconcileSessions(ctx, desiredSessions)
	if sessErr != nil {
		logger.Error("micro-BFD session reconciliation had errors",
			slog.String("error", sessErr.Error()),
		)
	}
	logger.Info("micro-BFD session reconciliation complete",
		slog.Int("sessions_created", sessCreated),
		slog.Int("sessions_destroyed", sessDestroyed),
	)
}

// buildMemberSessions builds ReconcileConfig entries for all member links
// in a single micro-BFD group. Each member gets its own BFD session with
// SessionTypeMicroBFD, SO_BINDTODEVICE, and destination port 6784.
func buildMemberSessions(
	cfg *config.Config,
	group config.MicroBFDGroupConfig,
	sf *udpSenderFactory,
	logger *slog.Logger,
) []bfd.ReconcileConfig {
	peerAddr, err := netip.ParseAddr(group.PeerAddr)
	if err != nil {
		return nil
	}
	localAddr, err := netip.ParseAddr(group.LocalAddr)
	if err != nil {
		return nil
	}

	detectMult := group.DetectMult
	if detectMult > 255 {
		logger.Error("micro-BFD detect_mult exceeds uint8 range, skipping",
			slog.String("lag", group.LAGInterface),
			slog.Uint64("detect_mult", uint64(detectMult)),
		)
		return nil
	}

	desiredMinTx := group.DesiredMinTx
	if desiredMinTx == 0 {
		desiredMinTx = cfg.BFD.DefaultDesiredMinTx
	}
	requiredMinRx := group.RequiredMinRx
	if requiredMinRx == 0 {
		requiredMinRx = cfg.BFD.DefaultRequiredMinRx
	}
	if detectMult == 0 {
		detectMult = cfg.BFD.DefaultDetectMultiplier
	}

	var sessions []bfd.ReconcileConfig
	for _, member := range group.MemberLinks {
		rc := buildMemberReconcile(
			peerAddr, localAddr, member, group.LAGInterface,
			desiredMinTx, requiredMinRx, uint8(detectMult), // Range validated: detectMult <= 255.
			sf, logger,
		)
		if rc != nil {
			sessions = append(sessions, *rc)
		}
	}
	return sessions
}

// buildMemberReconcile creates a single ReconcileConfig for one member link.
func buildMemberReconcile(
	peerAddr, localAddr netip.Addr,
	member, lagIface string,
	desiredMinTx, requiredMinRx time.Duration,
	detectMult uint8,
	sf *udpSenderFactory,
	logger *slog.Logger,
) *bfd.ReconcileConfig {
	sessCfg := bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Interface:             member,
		Type:                  bfd.SessionTypeMicroBFD,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      detectMult,
	}

	// Create sender with SO_BINDTODEVICE per member link and
	// destination port 6784 (RFC 7130 Section 2.1).
	sender, sErr := sf.createSenderForSession(
		localAddr, false, logger,
		netio.WithDstPort(netio.PortMicroBFD),
		netio.WithBindDevice(member),
	)
	if sErr != nil {
		logger.Error("failed to create sender for micro-BFD member, skipping",
			slog.String("lag", lagIface),
			slog.String("member", member),
			slog.String("error", sErr.Error()),
		)
		return nil
	}

	key := peerAddr.String() + "|" + localAddr.String() + "|" + member
	return &bfd.ReconcileConfig{
		Key:           key,
		SessionConfig: sessCfg,
		Sender:        sender,
	}
}

// configMicroBFDToBFD converts a config.MicroBFDGroupConfig to a bfd.MicroBFDConfig.
func configMicroBFDToBFD(gc config.MicroBFDGroupConfig) (bfd.MicroBFDConfig, error) {
	peerAddr, err := netip.ParseAddr(gc.PeerAddr)
	if err != nil {
		return bfd.MicroBFDConfig{}, fmt.Errorf("parse micro-BFD peer address %q: %w", gc.PeerAddr, err)
	}

	localAddr, err := netip.ParseAddr(gc.LocalAddr)
	if err != nil {
		return bfd.MicroBFDConfig{}, fmt.Errorf("parse micro-BFD local address %q: %w", gc.LocalAddr, err)
	}

	if gc.DetectMult > 255 {
		return bfd.MicroBFDConfig{}, fmt.Errorf("micro-BFD detect_mult %d: %w", gc.DetectMult, errDetectMultOverflow)
	}

	return bfd.MicroBFDConfig{
		LAGInterface:          gc.LAGInterface,
		MemberLinks:           gc.MemberLinks,
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		DesiredMinTxInterval:  gc.DesiredMinTx,
		RequiredMinRxInterval: gc.RequiredMinRx,
		DetectMultiplier:      uint8(gc.DetectMult), // Range validated above: gc.DetectMult <= 255.
		MinActiveLinks:        gc.MinActiveLinks,
	}, nil
}

// -------------------------------------------------------------------------
// Overlay Tunnel Wiring — VXLAN (RFC 8971) + Geneve (RFC 9521)
// -------------------------------------------------------------------------

// startOverlayReceivers creates overlay tunnel connections and starts
// OverlayReceiver goroutines in the errgroup. Returns a cleanup function
// that closes all overlay connections when called.
//
// For each enabled tunnel type (VXLAN/Geneve), one UDP socket is bound
// (port 4789 / 6081) and a dedicated receiver goroutine reads packets,
// strips encapsulation, and delivers inner BFD payloads to the Manager.
func startOverlayReceivers(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) func() {
	var conns []netio.OverlayConn

	// VXLAN overlay receiver (RFC 8971, port 4789).
	if cfg.VXLAN.Enabled && len(cfg.VXLAN.Peers) > 0 {
		vxlanConn, err := createVXLANConn(cfg, sf, logger)
		if err != nil {
			logger.Error("failed to create VXLAN connection, skipping overlay",
				slog.String("error", err.Error()),
			)
		} else {
			conns = append(conns, vxlanConn)
			recv := netio.NewOverlayReceiver(vxlanConn, mgr, logger)
			g.Go(func() error { return recv.Run(ctx) })
			logger.Info("VXLAN overlay receiver started (RFC 8971)",
				slog.Uint64("management_vni", uint64(cfg.VXLAN.ManagementVNI)),
			)
		}
	}

	// Geneve overlay receiver (RFC 9521, port 6081).
	if cfg.Geneve.Enabled && len(cfg.Geneve.Peers) > 0 {
		geneveConn, err := createGeneveConn(cfg, sf, logger)
		if err != nil {
			logger.Error("failed to create Geneve connection, skipping overlay",
				slog.String("error", err.Error()),
			)
		} else {
			conns = append(conns, geneveConn)
			recv := netio.NewOverlayReceiver(geneveConn, mgr, logger)
			g.Go(func() error { return recv.Run(ctx) })
			logger.Info("Geneve overlay receiver started (RFC 9521)",
				slog.Uint64("default_vni", uint64(cfg.Geneve.DefaultVNI)),
			)
		}
	}

	return func() {
		for _, c := range conns {
			if err := c.Close(); err != nil {
				logger.Warn("failed to close overlay connection",
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

// createVXLANConn creates a VXLANConn bound to the first VXLAN peer's local address.
// All VXLAN peers share a single UDP socket on port 4789.
func createVXLANConn(
	cfg *config.Config,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (*netio.VXLANConn, error) {
	// Use the first peer's local address for binding the socket.
	localAddr, err := cfg.VXLAN.Peers[0].LocalAddr()
	if err != nil || !localAddr.IsValid() {
		return nil, fmt.Errorf("vxlan: first peer has invalid local address: %w", err)
	}

	// Allocate an ephemeral source port for the inner UDP header.
	srcPort, err := sf.portAlloc.Allocate()
	if err != nil {
		return nil, fmt.Errorf("vxlan: allocate inner src port: %w", err)
	}

	conn, err := netio.NewVXLANConn(localAddr, cfg.VXLAN.ManagementVNI, srcPort, logger)
	if err != nil {
		sf.portAlloc.Release(srcPort)
		return nil, fmt.Errorf("vxlan: create conn: %w", err)
	}

	return conn, nil
}

// createGeneveConn creates a GeneveConn bound to the first Geneve peer's local address.
// All Geneve peers share a single UDP socket on port 6081.
func createGeneveConn(
	cfg *config.Config,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (*netio.GeneveConn, error) {
	localAddr, err := cfg.Geneve.Peers[0].LocalAddr()
	if err != nil || !localAddr.IsValid() {
		return nil, fmt.Errorf("geneve: first peer has invalid local address: %w", err)
	}

	// Resolve VNI: first peer's VNI, falling back to default.
	vni := cfg.Geneve.Peers[0].VNI
	if vni == 0 {
		vni = cfg.Geneve.DefaultVNI
	}

	srcPort, err := sf.portAlloc.Allocate()
	if err != nil {
		return nil, fmt.Errorf("geneve: allocate inner src port: %w", err)
	}

	conn, err := netio.NewGeneveConn(localAddr, vni, srcPort, logger)
	if err != nil {
		sf.portAlloc.Release(srcPort)
		return nil, fmt.Errorf("geneve: create conn: %w", err)
	}

	return conn, nil
}

// overlayPeerEntry holds the raw per-peer data before conversion to
// bfd.SessionConfig. Used by the common reconcileOverlayTunnel helper.
type overlayPeerEntry struct {
	key        string
	peerName   string
	peerStr    string
	localStr   string
	peerTx     time.Duration
	peerRx     time.Duration
	peerDetect uint32
}

// overlayTimerDefaults holds the default timer values shared by VXLAN and Geneve
// overlay config. Extracted to deduplicate configVXLANToBFD/configGeneveToBFD.
type overlayTimerDefaults struct {
	desiredMinTx  time.Duration
	requiredMinRx time.Duration
	detectMult    uint32
}

// reconcileOverlayTunnels reconciles both VXLAN (RFC 8971) and Geneve (RFC 9521)
// BFD sessions from the configuration. Each enabled tunnel type is processed
// through the shared reconcileOverlayTunnel path.
func reconcileOverlayTunnels(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	for _, tp := range buildOverlayTunnelParams(cfg, sf, logger) {
		reconcileOverlayTunnel(ctx, mgr, sf, logger, tp)
	}
}

// buildOverlayTunnelParams builds overlayTunnelParams for each enabled tunnel
// type. Returns an empty slice if neither VXLAN nor Geneve is configured.
func buildOverlayTunnelParams(
	cfg *config.Config,
	sf *udpSenderFactory,
	logger *slog.Logger,
) []overlayTunnelParams {
	var params []overlayTunnelParams

	if cfg.VXLAN.Enabled && len(cfg.VXLAN.Peers) > 0 {
		entries := make([]overlayPeerEntry, 0, len(cfg.VXLAN.Peers))
		for _, peer := range cfg.VXLAN.Peers {
			entries = append(entries, overlayPeerEntry{
				key: peer.VXLANSessionKey(), peerName: peer.Peer,
				peerStr: peer.Peer, localStr: peer.Local,
				peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
				peerDetect: peer.DetectMult,
			})
		}
		params = append(params, overlayTunnelParams{
			rfc: "RFC 8971", sessType: bfd.SessionTypeVXLAN,
			defaults: overlayTimerDefaults{
				desiredMinTx:  cfg.VXLAN.DefaultDesiredMinTx,
				requiredMinRx: cfg.VXLAN.DefaultRequiredMinRx,
				detectMult:    cfg.VXLAN.DefaultDetectMultiplier,
			},
			createConn: func() (netio.OverlayConn, error) { return createVXLANConn(cfg, sf, logger) },
			entries:    entries,
		})
	}

	if cfg.Geneve.Enabled && len(cfg.Geneve.Peers) > 0 {
		entries := make([]overlayPeerEntry, 0, len(cfg.Geneve.Peers))
		for _, peer := range cfg.Geneve.Peers {
			entries = append(entries, overlayPeerEntry{
				key: peer.GeneveSessionKey(), peerName: peer.Peer,
				peerStr: peer.Peer, localStr: peer.Local,
				peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
				peerDetect: peer.DetectMult,
			})
		}
		params = append(params, overlayTunnelParams{
			rfc: "RFC 9521", sessType: bfd.SessionTypeGeneve,
			defaults: overlayTimerDefaults{
				desiredMinTx:  cfg.Geneve.DefaultDesiredMinTx,
				requiredMinRx: cfg.Geneve.DefaultRequiredMinRx,
				detectMult:    cfg.Geneve.DefaultDetectMultiplier,
			},
			createConn: func() (netio.OverlayConn, error) { return createGeneveConn(cfg, sf, logger) },
			entries:    entries,
		})
	}

	if len(params) == 0 {
		logger.Debug("no overlay tunnel BFD sessions in config, skipping reconciliation")
	}

	return params
}

// overlayTunnelParams holds the parameters for reconcileOverlayTunnel,
// capturing the differences between VXLAN and Geneve reconciliation.
type overlayTunnelParams struct {
	rfc        string
	sessType   bfd.SessionType
	defaults   overlayTimerDefaults
	createConn func() (netio.OverlayConn, error)
	entries    []overlayPeerEntry
}

// reconcileOverlayTunnel is the shared implementation for overlay BFD session
// reconciliation. It creates the tunnel connection, converts peer entries to
// session configs, and calls mgr.ReconcileSessions.
func reconcileOverlayTunnel(
	ctx context.Context,
	mgr *bfd.Manager,
	_ *udpSenderFactory,
	logger *slog.Logger,
	params overlayTunnelParams,
) {
	conn, connErr := params.createConn()
	if connErr != nil {
		logger.Error("failed to create overlay sender conn, skipping reconciliation",
			slog.String("rfc", params.rfc), slog.String("error", connErr.Error()))
		return
	}

	sender := netio.NewOverlaySender(conn)
	desired := make([]bfd.ReconcileConfig, 0, len(params.entries))
	for _, e := range params.entries {
		sessCfg, cfgErr := buildOverlaySessionConfig(
			e.peerStr, e.localStr, e.peerTx, e.peerRx, e.peerDetect,
			params.defaults, params.sessType)
		if cfgErr != nil {
			logger.Error("invalid overlay peer config, skipping",
				slog.String("rfc", params.rfc), slog.String("peer", e.peerName),
				slog.String("error", cfgErr.Error()))
			continue
		}
		desired = append(desired, bfd.ReconcileConfig{
			Key: e.key, SessionConfig: sessCfg, Sender: sender,
		})
	}

	reconcileOverlaySessions(ctx, mgr, conn, desired, params.rfc, logger)
}

// reconcileOverlaySessions performs the common reconciliation loop for overlay
// BFD sessions (VXLAN or Geneve), calling mgr.ReconcileSessions with the
// pre-built desired set.
func reconcileOverlaySessions(
	ctx context.Context,
	mgr *bfd.Manager,
	_ netio.OverlayConn,
	desired []bfd.ReconcileConfig,
	rfc string,
	logger *slog.Logger,
) {
	created, destroyed, err := mgr.ReconcileSessions(ctx, desired)
	if err != nil {
		logger.Error("overlay session reconciliation had errors",
			slog.String("rfc", rfc), slog.String("error", err.Error()))
	}

	logger.Info("overlay session reconciliation complete",
		slog.String("rfc", rfc),
		slog.Int("created", created), slog.Int("destroyed", destroyed))
}

// buildOverlaySessionConfig converts per-peer overlay fields (address strings,
// timer overrides) into a bfd.SessionConfig, applying defaults from
// overlayTimerDefaults. Shared by VXLAN and Geneve config paths.
func buildOverlaySessionConfig(
	peerStr, localStr string,
	peerTx, peerRx time.Duration,
	peerDetect uint32,
	defaults overlayTimerDefaults,
	sessType bfd.SessionType,
) (bfd.SessionConfig, error) {
	peerAddr, err := netip.ParseAddr(peerStr)
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("parse peer address %q: %w", peerStr, err)
	}

	var localAddr netip.Addr
	if localStr != "" {
		localAddr, err = netip.ParseAddr(localStr)
		if err != nil {
			return bfd.SessionConfig{}, fmt.Errorf("parse local address %q: %w", localStr, err)
		}
	}

	desiredMinTx := peerTx
	if desiredMinTx == 0 {
		desiredMinTx = defaults.desiredMinTx
	}
	requiredMinRx := peerRx
	if requiredMinRx == 0 {
		requiredMinRx = defaults.requiredMinRx
	}
	detectMult := peerDetect
	if detectMult == 0 {
		detectMult = defaults.detectMult
	}
	if detectMult > 255 {
		return bfd.SessionConfig{}, fmt.Errorf("detect_mult %d: %w", detectMult, errDetectMultOverflow)
	}

	return bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Type:                  sessType,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
	}, nil
}

// newLoggerWithLevel creates a structured logger using a shared LevelVar
// for dynamic log level changes via SIGHUP reload.
func newLoggerWithLevel(cfg config.LogConfig, level *slog.LevelVar) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch cfg.Format {
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
