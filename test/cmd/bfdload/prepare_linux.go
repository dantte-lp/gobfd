//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func runPrepare(ctx context.Context, args []string, output io.Writer) error {
	if len(args) <= 1 {
		return fmt.Errorf("expected prepare HOST_NAMESPACE PROFILE [flags]: %w", bfdload.ErrTopology)
	}
	hostID, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("parse host network namespace inode: %w: %w", bfdload.ErrTopology, err)
	}
	cfg, err := parseWorkloadConfig(args[1:])
	if err != nil {
		return err
	}
	const setupTimeout = 30 * time.Second
	setupCtx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	runCtx, stop := signal.NotifyContext(setupCtx, os.Interrupt, syscall.SIGTERM)
	report, setupErr := bfdload.PrepareLoopback(runCtx, cfg, hostID)
	stop()
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return errors.Join(setupErr, fmt.Errorf("encode topology setup observation: %w", err))
	}
	if setupErr != nil {
		return fmt.Errorf("prepare isolated workload topology: %w", setupErr)
	}
	return nil
}
