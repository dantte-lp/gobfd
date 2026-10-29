//go:build linux

package bfdload

import (
	"context"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

type cpuWindowDelayedContext struct {
	context.Context

	delay time.Duration
}

func (c cpuWindowDelayedContext) Err() error {
	// Delay between the wall precheck and CPU credit without replacing the CPU clock.
	time.Sleep(c.delay)
	return c.Context.Err()
}

func TestCPUWindowCreditsOnlySamplesBeforeDeadline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		delay time.Duration
	}{
		{name: "before", delay: 0},
		{name: "at", delay: time.Second},
		{name: "after", delay: 2 * time.Second},
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
				if err != nil || clockErr != nil || after <= before || checksum == 1 || time.Since(at) != tc.delay {
					t.Fatalf("invalid delayed-credit fixture: used=%s outer=%s checksum=%d elapsed=%s: %v, %v",
						used, after-before, checksum, time.Since(at), err, clockErr)
				}
				if tc.delay < time.Second && used < time.Nanosecond {
					t.Fatalf("on-time CPU sample lost credit: used=%s", used)
				}
				if tc.delay >= time.Second && used != 0 {
					t.Fatalf("CPU sample at or after deadline received credit: used=%s delay=%s", used, tc.delay)
				}
			})
		})
	}
}
