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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

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
// Fresh timeout contexts bound the BFD notification window and HTTP separately.
func gracefulShutdown(
	ctx context.Context,
	mgr *bfd.Manager,
	logger *slog.Logger,
	fr *trace.FlightRecorder,
	servers ...*http.Server,
) error {
	logger.Info("initiating graceful shutdown")
	notifyStopping(logger)

	// The drain deadline is independent of the cancelled signal context.
	drainCtx, cancelDrain := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	var shutdownErr error
	if err := localDrainErrors(logger, mgr.DrainAllSessions(drainCtx)); err != nil {
		shutdownErr = fmt.Errorf("drain BFD sessions: %w", err)
	}
	cancelDrain()

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

	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown server: %w", err))
		}
	}
	return shutdownErr
}

// localDrainErrors logs drains that only lack a reachable or known peer as
// warnings, so an orderly stop exits 0, and returns every other drain failure.
func localDrainErrors(logger *slog.Logger, err error) error {
	errs := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	}
	local := make([]error, 0, len(errs))
	for _, drainErr := range errs {
		if incomplete, ok := errors.AsType[*bfd.DrainIncompleteError](drainErr); ok && incomplete.PeerAttributed() {
			logger.Warn("AdminDown drain incomplete for an unreachable or unknown peer",
				slog.String("error", drainErr.Error()))
			continue
		}
		local = append(local, drainErr)
	}
	return errors.Join(local...)
}

// -------------------------------------------------------------------------
// Flight Recorder — runtime/trace
// -------------------------------------------------------------------------

// startFlightRecorder initializes and starts the runtime FlightRecorder
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

// maxHTTPHeaderValueCount bounds repeated HTTP header values on both public
// server surfaces. BFD's ConnectRPC and metrics requests need far fewer than
// the net/http default of 500.
const maxHTTPHeaderValueCount = 128

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
func newMetricsServer(cfg config.MetricsConfig, reg *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(cfg.Path, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	return &http.Server{
		Addr:                cfg.Addr,
		Handler:             mux,
		ReadHeaderTimeout:   10 * time.Second,
		MaxHeaderValueCount: maxHTTPHeaderValueCount,
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
// The server explicitly enables h2c, which is required for gRPC clients that
// connect over plaintext (for example, gobfdctl), alongside HTTP/1.1.
// Includes standard gRPC health checking (grpc.health.v1).
func newGRPCServer(
	cfg config.GRPCConfig,
	mgr *bfd.Manager,
	sf server.SenderFactory,
	checker *grpchealth.StaticChecker,
	fr *trace.FlightRecorder,
	logger *slog.Logger,
) *http.Server {
	mux := http.NewServeMux()

	// BFD, Echo (RFC 9747) and Micro-BFD (RFC 7130) services share one server
	// with logging and panic recovery. connect v2 bounds each message to its
	// default read limit (4 MiB) instead of the unbounded v1 behavior.
	// Anonymous loopback TCP callers may only read unless the unsafe opt-in
	// is set (gobfd-330.3).
	policy := server.MutationPolicy{
		AllowAnonymous:     cfg.UnsafeLoopbackMutations,
		MutatingIdentities: cfg.MutatingIdentities,
	}
	rpc := connect.NewServer(server.LoggingInterceptor(logger), server.RecoveryInterceptor(logger),
		server.MutationGuardInterceptor(policy))
	if cfg.FlightRecorder && fr != nil {
		mux.Handle("/debug/flightrecorder", server.RequirePrincipal(policy, flightRecorderHandler(fr)))
	}
	server.Register(rpc, mgr, sf, logger)
	server.RegisterEcho(rpc, mgr, sf, logger)
	server.RegisterMicroBFD(rpc, mgr, logger)
	connecthttp.Mount(mux, rpc)

	// gRPC health check (grpc.health.v1) on its own server without the RPC
	// interceptors, as in v1. daemonReadiness writes the empty service and
	// the bfd.v1 services as the AND of their reconciliation, runtime and
	// listener components; grpc.health.v1.Health stays SERVING as liveness.
	health := connect.NewServer()
	reportManagementSafety(checker, cfg)
	if cfg.UnsafeLoopbackMutations {
		logger.Warn("anonymous loopback TCP clients may mutate sessions (grpc.unsafe_loopback_mutations)",
			slog.String("addr", cfg.Addr))
	}
	grpchealth.Register(health, checker)
	connecthttp.Mount(mux, health)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Server{
		Addr:                cfg.Addr,
		Handler:             mux,
		Protocols:           protocols,
		ReadHeaderTimeout:   10 * time.Second,
		MaxHeaderValueCount: maxHTTPHeaderValueCount,
		// Requests over the management socket carry the peer UID principal.
		ConnContext: server.ConnContext,
	}
}
