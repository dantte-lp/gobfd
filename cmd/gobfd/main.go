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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/server"
	appversion "github.com/dantte-lp/gobfd/internal/version"
)

// shutdownTimeout is the maximum time to wait for HTTP servers to drain
// active connections during graceful shutdown.
const shutdownTimeout = 10 * time.Second

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

	// 4. Create BFD session manager.
	mgr := bfd.NewManager(logger)
	defer mgr.Close()

	// 5-7. Run servers.
	if err := runServers(cfg, mgr, logger, *configPath, logLevel); err != nil {
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
	logger *slog.Logger,
	configPath string,
	logLevel *slog.LevelVar,
) error {
	// Metrics collector with a dedicated registry.
	reg := prometheus.NewRegistry()
	_ = bfdmetrics.NewCollector(reg)

	metricsSrv := newMetricsServer(cfg.Metrics, reg)
	grpcSrv := newGRPCServer(cfg.GRPC, mgr, logger)

	// errgroup with signal-aware context.
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	g, gCtx := errgroup.WithContext(ctx)

	lc := net.ListenConfig{}

	// gRPC server goroutine.
	g.Go(func() error {
		logger.Info("gRPC server listening", slog.String("addr", cfg.GRPC.Addr))
		return listenAndServe(gCtx, &lc, grpcSrv, cfg.GRPC.Addr)
	})

	// Metrics server goroutine.
	g.Go(func() error {
		logger.Info("metrics server listening",
			slog.String("addr", cfg.Metrics.Addr),
			slog.String("path", cfg.Metrics.Path),
		)
		return listenAndServe(gCtx, &lc, metricsSrv, cfg.Metrics.Addr)
	})

	// SIGHUP reload goroutine: reloads configuration on SIGHUP without
	// restarting servers. Currently supports dynamic log level changes.
	sigHUP := make(chan os.Signal, 1)
	signal.Notify(sigHUP, syscall.SIGHUP)
	g.Go(func() error {
		defer signal.Stop(sigHUP)
		handleSIGHUP(gCtx, sigHUP, configPath, logLevel, logger)
		return nil
	})

	// Shutdown goroutine: waits for context cancellation, then gracefully
	// shuts down both HTTP servers.
	g.Go(func() error {
		<-gCtx.Done()
		logger.Info("initiating graceful shutdown")
		return shutdownServers(grpcSrv, metricsSrv) //nolint:contextcheck // Intentional: gCtx is cancelled; shutdownServers creates a fresh timeout for drain.
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("run servers: %w", err)
	}
	return nil
}

// handleSIGHUP listens for SIGHUP signals and reloads configuration.
// On reload, the log level is updated dynamically via the shared LevelVar.
// Blocks until the context is cancelled (graceful shutdown).
func handleSIGHUP(
	ctx context.Context,
	sigHUP <-chan os.Signal,
	configPath string,
	logLevel *slog.LevelVar,
	logger *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sigHUP:
			logger.Info("received SIGHUP, reloading configuration")
			reloadConfig(configPath, logLevel, logger)
		}
	}
}

// reloadConfig loads a fresh configuration from the given path and updates
// the dynamic log level. Errors during reload are logged but do not stop
// the daemon -- the previous configuration remains in effect.
func reloadConfig(configPath string, logLevel *slog.LevelVar, logger *slog.Logger) {
	newCfg, err := loadConfig(configPath)
	if err != nil {
		logger.Error("failed to reload configuration, keeping current settings",
			slog.String("error", err.Error()),
		)
		return
	}

	oldLevel := logLevel.Level()
	newLevel := config.ParseLogLevel(newCfg.Log.Level)
	logLevel.Set(newLevel)

	logger.Info("configuration reloaded",
		slog.String("old_log_level", oldLevel.String()),
		slog.String("new_log_level", newLevel.String()),
	)
}

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

// shutdownServers gracefully shuts down the provided HTTP servers.
// A fresh context with timeout is created because this function is called
// after the parent signal context has been cancelled.
func shutdownServers(servers ...*http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var shutdownErr error
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown server: %w", err))
		}
	}
	return shutdownErr
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
func newGRPCServer(cfg config.GRPCConfig, mgr *bfd.Manager, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	path, handler := server.New(mgr, logger,
		server.LoggingInterceptorOption(logger),
		server.RecoveryInterceptorOption(logger),
	)
	mux.Handle(path, handler)
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
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
