//go:build linux

package bfdload

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

type cpuWindowDelayedContext struct {
	context.Context

	delay  time.Duration
	cancel context.CancelFunc
}

func (c cpuWindowDelayedContext) Err() error {
	err := c.Context.Err()
	if c.cancel != nil {
		c.cancel()
	}
	// Delay between the wall precheck and CPU credit without replacing the CPU clock.
	time.Sleep(c.delay)
	return err
}

func TestCPUWindowChecksCancellationBetweenPolls(t *testing.T) {
	t.Parallel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checksum := uint64(1)
	used, err := burnCPUWindow(cpuWindowDelayedContext{Context: ctx, cancel: cancel},
		time.Now().Add(time.Hour), time.Second, &checksum)
	if !errors.Is(err, context.Canceled) || used >= time.Second || checksum != 0xcec9aff28372a1ac {
		t.Fatalf("cancellation between chunks burned additional work: used=%s checksum=%#x: %v", used, checksum, err)
	}
}

func TestCPUWindowCreditsOnlySamplesBeforeDeadline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		delay   time.Duration
		elapsed time.Duration
	}{
		{name: "before", delay: 0},
		{name: "at", delay: time.Second, elapsed: time.Second},
		{name: "after", delay: 2 * time.Second, elapsed: 2 * time.Second},
		{name: "poll_at", delay: 250 * time.Millisecond, elapsed: time.Second},
		{name: "poll_after", delay: 300 * time.Millisecond, elapsed: 1200 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				at := time.Now()
				checksum := uint64(1)
				before, err := threadCPUTime()
				if err != nil {
					t.Fatal(err)
				}
				ctx := cpuWindowDelayedContext{Context: context.Background(), delay: tc.delay}
				used, err := burnCPUWindow(ctx, at.Add(time.Second), time.Nanosecond, &checksum)
				after, clockErr := threadCPUTime()
				if err != nil || clockErr != nil || after <= before || checksum == 1 || time.Since(at) != tc.elapsed {
					t.Fatalf("invalid delayed-credit fixture: used=%s outer=%s checksum=%d elapsed=%s: %v, %v",
						used, after-before, checksum, time.Since(at), err, clockErr)
				}
				if tc.elapsed < time.Second && used < time.Nanosecond {
					t.Fatalf("on-time CPU sample lost credit: used=%s", used)
				}
				if tc.elapsed >= time.Second && used != 0 {
					t.Fatalf("CPU sample at or after deadline received credit: used=%s delay=%s", used, tc.delay)
				}
			})
		})
	}
}
