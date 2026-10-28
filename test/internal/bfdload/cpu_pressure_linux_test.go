//go:build linux

package bfdload

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestCPUPressureDisabledCreatesNoWorkers(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pressure, err := startCPUPressure(ctx, profile, time.Now(), cancel)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := pressure.join()
	if err != nil || pressure.results != nil || observed.Workers != 0 || observed.CPUTimeNS != 0 || !observed.Completed {
		t.Fatalf("disabled CPU pressure spawned work: %+v: %v", observed, err)
	}
}

func TestCPUPressureCanceledBeforeFutureEpochJoins(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.CPULoadPercent = 25
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pressure, err := startCPUPressure(ctx, profile, time.Now().Add(time.Hour), cancel)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	joined := make(chan cpuWorkerResult, 1)
	go func() {
		observed, joinErr := pressure.join()
		joined <- cpuWorkerResult{observed, joinErr}
	}()
	select {
	case result := <-joined:
		if !errors.Is(result.err, context.Canceled) || result.Completed || result.CPUTimeNS != 0 || result.Windows != 0 {
			t.Fatalf("canceled workers reported applied CPU load: %+v: %v", result.CPUPressureObservation, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("CPU workers did not join after cancellation")
	}
}

func TestCPUPressureRejectsInvalidDuty(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.CPULoadPercent = 101
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := startCPUPressure(ctx, profile, time.Now(), cancel); !errors.Is(err, ErrProfile) {
		t.Fatalf("invalid CPU duty admitted: %v", err)
	}
}

func TestCPUWindowRejectsCancellationBeforeBurn(t *testing.T) {
	t.Parallel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checksum := uint64(1)
	used, err := burnCPUWindow(ctx, time.Now().Add(time.Hour), time.Second, &checksum)
	if !errors.Is(err, context.Canceled) || used != 0 || checksum != 1 {
		t.Fatalf("canceled duty burned work: used=%s checksum=%d: %v", used, checksum, err)
	}
}
