package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"syscall"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	appversion "github.com/dantte-lp/gobfd/internal/version"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(appversion.Full("gobfd-exabgp-bridge"))
		return 0
	}

	gobfdAddr := envOrDefault("GOBFD_ADDR", "unix:///run/gobfd/gobfd.sock")
	peer := os.Getenv("GOBFD_PEER")
	prefix := os.Getenv("ANYCAST_PREFIX")

	// ExaBGP convention: log to STDERR, commands to STDOUT.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if peer == "" || prefix == "" {
		logger.Error("GOBFD_PEER and ANYCAST_PREFIX environment variables are required")
		return 1
	}
	selector, err := apiclient.ParseSessionSelector(peer, os.Getenv("GOBFD_LOCAL"),
		os.Getenv("GOBFD_INTERFACE"), os.Getenv("GOBFD_SESSION_TYPE"))
	if err != nil {
		logger.Error("invalid BFD selector", slog.Any("error", err))
		return 1
	}
	route, err := netip.ParsePrefix(prefix)
	if err != nil {
		logger.Error("invalid ANYCAST_PREFIX", slog.Any("error", err))
		return 1
	}

	logger.Info("gobfd-exabgp-bridge starting",
		slog.String("gobfd_addr", gobfdAddr),
		slog.String("peer", peer),
		slog.String("prefix", prefix),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := watchAndAnnounce(ctx, gobfdAddr, selector, route.Masked().String(), os.Stdout, logger); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("gobfd-exabgp-bridge stopped")
			return 0
		}
		logger.Error("bridge exited with error", slog.String("error", err.Error()))
		return 1
	}

	return 0
}

// watchAndAnnounce connects to GoBFD, watches BFD events for the specified peer,
// and writes ExaBGP route commands to STDOUT. Reconnects on stream errors with
// exponential backoff.
func watchAndAnnounce(
	ctx context.Context,
	gobfdAddr string,
	selector apiclient.SessionSelector,
	prefix string,
	output io.Writer,
	logger *slog.Logger,
) error {
	ep, err := apiclient.ParseEndpoint(gobfdAddr)
	if err != nil {
		return fmt.Errorf("GOBFD_ADDR: %w", err)
	}
	httpClient, err := ep.HTTPClientTLS(apiclient.TLSFilesFromEnv())
	if err != nil {
		return fmt.Errorf("GOBFD_TLS_*: %w", err)
	}
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, ep.BaseURL)))

	route := routeState{output: output, prefix: prefix}
	return apiclient.WatchSessions(ctx, client, func(sessions []*bfdv1.BfdSession) error {
		session, selectErr := selector.Select(sessions)
		if selectErr != nil {
			logger.Warn("BFD session is ambiguous, withdrawing route", slog.Any("error", selectErr))
		}
		return route.apply(session.GetLocalState() == bfdv1.SessionState_SESSION_STATE_UP)
	}, logger)
}

// routeState survives Watch reconnects. Unknown process state forces an initial
// withdrawal, so restarting the bridge also reconciles a stale ExaBGP route.
type routeState struct {
	output    io.Writer
	prefix    string
	known     bool
	announced bool
}

func (r *routeState) apply(up bool) error {
	if r.known && r.announced == up {
		return nil
	}
	verb := "withdraw"
	if up {
		verb = "announce"
	}
	command := fmt.Sprintf("%s route %s next-hop self\n", verb, r.prefix)
	n, err := io.WriteString(r.output, command)
	if err == nil && n != len(command) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("%s ExaBGP route %s: %w", verb, r.prefix, err)
	}
	r.known, r.announced = true, up
	return nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
