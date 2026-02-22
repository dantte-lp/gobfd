// gobfd-exabgp-bridge is an ExaBGP process that announces/withdraws routes
// based on BFD session state from GoBFD.
//
// ExaBGP invokes this binary as a "process". Communication follows ExaBGP
// conventions: STDOUT = commands to ExaBGP, STDERR = logging.
//
// On BFD Up:   writes "announce route <prefix> next-hop self\n" to STDOUT
// On BFD Down: writes "withdraw route <prefix> next-hop self\n" to STDOUT
//
// Configuration via environment variables:
//
//	GOBFD_ADDR      - GoBFD gRPC address (default: http://127.0.0.1:50052)
//	GOBFD_PEER      - BFD peer address to watch
//	ANYCAST_PREFIX  - route prefix to announce/withdraw (e.g., 198.51.100.1/32)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	gobfdAddr := envOrDefault("GOBFD_ADDR", "http://127.0.0.1:50052")
	peer := os.Getenv("GOBFD_PEER")
	prefix := os.Getenv("ANYCAST_PREFIX")

	// ExaBGP convention: log to STDERR, commands to STDOUT.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if peer == "" || prefix == "" {
		logger.Error("GOBFD_PEER and ANYCAST_PREFIX environment variables are required")
		return 1
	}

	logger.Info("gobfd-exabgp-bridge starting",
		slog.String("gobfd_addr", gobfdAddr),
		slog.String("peer", peer),
		slog.String("prefix", prefix),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := watchAndAnnounce(ctx, gobfdAddr, peer, prefix, logger); err != nil {
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
	peer string,
	prefix string,
	logger *slog.Logger,
) error {
	client := bfdv1connect.NewBfdServiceClient(http.DefaultClient, gobfdAddr)

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		err := streamEvents(ctx, client, peer, prefix, logger)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}

		logger.Warn("stream disconnected, reconnecting",
			slog.String("error", err.Error()),
			slog.Duration("backoff", backoff),
		)

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for reconnect: %w", ctx.Err())
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// streamEvents opens a single WatchSessionEvents stream and processes events.
func streamEvents(
	ctx context.Context,
	client bfdv1connect.BfdServiceClient,
	peer string,
	prefix string,
	logger *slog.Logger,
) error {
	stream, err := client.WatchSessionEvents(ctx, &bfdv1.WatchSessionEventsRequest{
		IncludeCurrent: true,
	})
	if err != nil {
		return fmt.Errorf("watch session events: %w", err)
	}

	announced := false

	for stream.Receive() {
		msg := stream.Msg()
		if msg.GetSession() == nil {
			continue
		}

		eventPeer := msg.GetSession().GetPeerAddress()
		if eventPeer != peer {
			continue
		}

		announced = handleStateChange(msg.GetSession().GetLocalState(), announced, peer, prefix, logger)
	}

	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("event stream error: %w", err)
	}
	return nil
}

// handleStateChange processes a BFD state change and writes ExaBGP commands to STDOUT.
// Returns the updated announced state.
func handleStateChange(
	state bfdv1.SessionState,
	announced bool,
	peer string,
	prefix string,
	logger *slog.Logger,
) bool {
	switch state {
	case bfdv1.SessionState_SESSION_STATE_UP:
		if !announced {
			fmt.Fprintf(os.Stdout, "announce route %s next-hop self\n", prefix)
			logger.Info("announced route",
				slog.String("prefix", prefix),
				slog.String("peer", peer),
			)
			return true
		}

	case bfdv1.SessionState_SESSION_STATE_DOWN,
		bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN:
		if announced {
			fmt.Fprintf(os.Stdout, "withdraw route %s next-hop self\n", prefix)
			logger.Info("withdrew route",
				slog.String("prefix", prefix),
				slog.String("peer", peer),
			)
			return false
		}

	case bfdv1.SessionState_SESSION_STATE_UNSPECIFIED,
		bfdv1.SessionState_SESSION_STATE_INIT:
		logger.Debug("ignoring transient BFD state",
			slog.String("state", state.String()),
			slog.String("peer", peer),
		)
	}

	return announced
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
