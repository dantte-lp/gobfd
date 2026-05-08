package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/trace"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/gobgp"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/sdnotify"
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
	mgr, err := newManager(cfg, collector, logger)
	if err != nil {
		logger.Error("failed to create BFD manager",
			slog.String("error", err.Error()),
		)
		return 1
	}
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

	metricsSrv := newMetricsServer(cfg.Metrics, reg, fr)
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

	startInterfaceMonitor(gCtx, g, mgr, logger)

	// Start BFD packet listeners and receivers for incoming packets.
	lnCleanup, err := startListenersAndReceivers(gCtx, g, cfg, mgr, logger)
	if err != nil {
		return fmt.Errorf("create BFD listeners: %w", err)
	}
	defer lnCleanup()

	startHTTPServers(gCtx, g, cfg, grpcSrv, metricsSrv, logger)
	// GoBGP integration goroutine (RFC 5882 Section 4.3).
	bgpCloser, err := startGoBGPHandler(gCtx, g, cfg.GoBGP, mgr, logger)
	if err != nil {
		return fmt.Errorf("start gobgp handler: %w", err)
	}
	defer closeGoBGPClient(bgpCloser, logger)

	// Start overlay tunnel receivers (VXLAN on port 4789, Geneve on port 6081).
	overlayRuntime, overlayCleanup := startOverlayReceivers(gCtx, g, cfg, mgr, sf, logger)
	defer overlayCleanup()

	startDaemonGoroutines(gCtx, g, configPath, logLevel, mgr, sf, overlayRuntime, logger)

	reconcileAllSessions(gCtx, cfg, mgr, sf, overlayRuntime, logger)

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
	overlayRuntime *overlayRuntime,
	logger *slog.Logger,
) {
	g.Go(func() error {
		return runWatchdog(ctx, logger)
	})

	sigHUP := make(chan os.Signal, 1)
	signal.Notify(sigHUP, syscall.SIGHUP)
	g.Go(func() error {
		defer signal.Stop(sigHUP)
		handleSIGHUP(ctx, sigHUP, configPath, logLevel, mgr, sf, overlayRuntime, logger)
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
	sent, err := sdnotify.Notify(sdnotify.Ready)
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
	sent, err := sdnotify.Notify(sdnotify.Stopping)
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
	interval, err := sdnotify.WatchdogEnabled()
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
			if _, wdErr := sdnotify.Notify(sdnotify.Watchdog); wdErr != nil {
				logger.Warn("failed to send watchdog keepalive",
					slog.String("error", wdErr.Error()),
				)
			}
		}
	}
}

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
