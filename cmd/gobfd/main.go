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
	flag.Parse()

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

	// 6. Create BFD session manager with metrics wired in.
	mgr := bfd.NewManager(logger, bfd.WithManagerMetrics(collector))
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

	// Start BFD packet listeners and receiver for incoming packets.
	listeners, err := createListeners(cfg, logger)
	if err != nil {
		return fmt.Errorf("create BFD listeners: %w", err)
	}
	defer closeListeners(listeners, logger)

	if len(listeners) > 0 {
		recv := netio.NewReceiver(mgr, logger)
		g.Go(func() error {
			return recv.Run(gCtx, listeners...)
		})
	}

	startHTTPServers(gCtx, g, cfg, grpcSrv, metricsSrv, logger)
	startDaemonGoroutines(gCtx, g, configPath, logLevel, mgr, sf, logger)

	// GoBGP integration goroutine (RFC 5882 Section 4.3).
	bgpCloser, err := startGoBGPHandler(gCtx, g, cfg.GoBGP, mgr, logger)
	if err != nil {
		return fmt.Errorf("start gobgp handler: %w", err)
	}
	defer closeGoBGPClient(bgpCloser, logger)

	// Reconcile declarative sessions from config at startup.
	reconcileSessions(gCtx, cfg, mgr, sf, logger)

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
		//nolint:contextcheck // Socket creation is a quick local operation; SenderFactory API is context-free.
		sender, err := sf.createSenderForSession(sessCfg.LocalAddr, multiHop, logger)
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
func (f *udpSenderFactory) createSenderForSession(
	localAddr netip.Addr,
	multiHop bool,
	logger *slog.Logger,
) (bfd.PacketSender, error) {
	sender, _, err := f.CreateSender(localAddr, multiHop, logger)
	return sender, err
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

	return bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Interface:             sc.Interface,
		Type:                  sessType,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
	}, nil
}

// -------------------------------------------------------------------------
// BFD Listeners — receive incoming BFD Control packets
// -------------------------------------------------------------------------

// createListeners inspects the declared sessions and creates the necessary
// BFD packet listeners. For each unique (localAddr, type) pair a single
// listener is created on the appropriate port (3784 for single-hop, 4784
// for multi-hop). Returns the listeners and any error.
func createListeners(cfg *config.Config, logger *slog.Logger) ([]*netio.Listener, error) {
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
			// Close already-created listeners on failure.
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
		return nil, nil
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
