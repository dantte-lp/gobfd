package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/gobgp"
)

var errGoBGPUnavailable = errors.New("connection refused")

// sequenceChecker returns results in order and repeats the last one.
func sequenceChecker(results ...error) (func(context.Context) error, *atomic.Int32) {
	calls := new(atomic.Int32)
	return func(context.Context) error {
		n := int(calls.Add(1))
		return results[min(n, len(results))-1]
	}, calls
}

func TestWatchGoBGPCompatibilityReportsLateIncompatibleDaemon(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		check, calls := sequenceChecker(
			errGoBGPUnavailable,
			errGoBGPUnavailable,
			fmt.Errorf("probe: %w", gobgp.ErrIncompatibleServer),
		)
		done := make(chan struct{})
		go func() {
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			watchGoBGPCompatibility(t.Context(), check, "127.0.0.1:50051", time.Second, logger)
			close(done)
		}()

		// Probes run at 1s, 3s and 7s of virtual time: backoff 1s, 2s, 4s.
		for _, step := range []struct {
			sleep time.Duration
			calls int
		}{{time.Second, 1}, {2 * time.Second, 2}, {3 * time.Second, 2}, {time.Second, 3}} {
			time.Sleep(step.sleep)
			synctest.Wait()
			if int(calls.Load()) != step.calls {
				t.Fatalf("probe calls = %d, want %d", calls.Load(), step.calls)
			}
		}
		select {
		case <-done:
		default:
			t.Fatal("watch did not stop after a conclusive incompatible result")
		}
		if !strings.Contains(logs.String(), "gobgp endpoint requires GoBGP v4") {
			t.Fatalf("missing incompatibility error log: %s", logs.String())
		}
	})
}

func TestWatchGoBGPCompatibilityStopsAfterSuccess(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		check, calls := sequenceChecker(errGoBGPUnavailable, nil)
		watchGoBGPCompatibility(t.Context(), check, "127.0.0.1:50051", time.Second, slog.New(slog.NewTextHandler(&logs, nil)))
		if calls.Load() != 2 {
			t.Fatalf("probe calls = %d, want 2", calls.Load())
		}
		if !strings.Contains(logs.String(), "gobgp compatibility preflight passed") {
			t.Fatalf("missing success log: %s", logs.String())
		}
	})
}

func TestWatchGoBGPCompatibilityStopsOnCancel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		check, _ := sequenceChecker(errGoBGPUnavailable)
		done := make(chan struct{})
		go func() {
			watchGoBGPCompatibility(ctx, check, "127.0.0.1:50051", time.Second, slog.New(slog.DiscardHandler))
			close(done)
		}()
		time.Sleep(time.Minute)
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("watch did not stop after cancellation")
		}
	})
}
