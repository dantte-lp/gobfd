package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/trace"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/server"
)

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

// newMetricsServer creates an HTTP server for the Prometheus metrics endpoint
// and optional debug endpoints (flight recorder trace dump).
func newMetricsServer(
	cfg config.MetricsConfig,
	reg *prometheus.Registry,
	fr *trace.FlightRecorder,
) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(cfg.Path, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	if fr != nil {
		mux.HandleFunc("/debug/flightrecorder", flightRecorderHandler(fr))
	}

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// flightRecorderHandler returns an HTTP handler that dumps the current
// FlightRecorder trace snapshot. The output is a Go execution trace that
// can be analyzed with `go tool trace`.
func flightRecorderHandler(fr *trace.FlightRecorder) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="trace.out"`)

		if _, err := fr.WriteTo(w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// newGRPCServer creates an HTTP server for the ConnectRPC gRPC endpoint.
// The handler is wrapped with h2c to support HTTP/2 without TLS, which is
// required for gRPC clients that connect over plaintext (e.g., gobfdctl).
// Includes standard gRPC health checking (grpc.health.v1).
func newGRPCServer(cfg config.GRPCConfig, mgr *bfd.Manager, sf server.SenderFactory, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()

	interceptors := []connect.HandlerOption{
		server.LoggingInterceptorOption(logger),
		server.RecoveryInterceptorOption(logger),
	}

	// BFD service handler.
	path, handler := server.New(mgr, sf, logger, interceptors...)
	mux.Handle(path, handler)

	// Echo service handler (RFC 9747).
	echoPath, echoHandler := server.NewEcho(mgr, sf, logger, interceptors...)
	mux.Handle(echoPath, echoHandler)

	// Micro-BFD service handler (RFC 7130).
	microPath, microHandler := server.NewMicroBFD(mgr, logger, interceptors...)
	mux.Handle(microPath, microHandler)

	// gRPC health check handler (grpc.health.v1).
	// Reports SERVING for the overall server and the registered BFD services.
	checker := grpchealth.NewStaticChecker(
		grpchealth.HealthV1ServiceName,
		"bfd.v1.BfdService",
		"bfd.v1.EchoService",
		"bfd.v1.MicroBFDService",
	)
	mux.Handle(grpchealth.NewHandler(checker))

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
	}
}
