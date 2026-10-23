//go:build linux

// Command bfdload reports evidence for the actual UDP-to-owner workload.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

var errCommandArguments = errors.New("expected bfdload environment")

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		slog.Error("workload observation failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 1 || args[0] != "environment" {
		return fmt.Errorf("workload command arguments: %w", errCommandArguments)
	}
	report, err := bfdload.CaptureEnvironment(ctx)
	if err != nil {
		return fmt.Errorf("capture workload environment: %w", err)
	}
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return fmt.Errorf("encode workload environment: %w", err)
	}
	return nil
}
