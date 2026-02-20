// GoBFD daemon -- BFD protocol implementation (RFC 5880/5881).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dantte-lp/gobfd/internal/bfd"
	appversion "github.com/dantte-lp/gobfd/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	mgr := bfd.NewManager(logger)
	defer mgr.Close()

	logger.Info("gobfd started",
		slog.String("version", appversion.Version),
	)

	<-ctx.Done()

	logger.Info("gobfd shutting down",
		slog.String("reason", ctx.Err().Error()),
	)

	return 0
}
