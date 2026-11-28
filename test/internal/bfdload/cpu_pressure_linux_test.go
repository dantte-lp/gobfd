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
	window, err := burnCPUWindow(ctx, time.Now().Add(time.Hour), time.Second, &checksum)
	used := window.used
	if !errors.Is(err, context.Canceled) || used != 0 || checksum != 1 {
		t.Fatalf("canceled duty burned work: used=%s checksum=%d: %v", used, checksum, err)
	}
}

// Run serially: the trace fixture temporarily owns the process's single Go P.
func TestCPUWindowMeasuredBudgetWithRunnableCompanion(t *testing.T) {
	processors := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(processors) })
	ready, stop, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		close(ready)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.Gosched()
			}
		}
	}()
	<-ready
	t.Cleanup(func() {
		close(stop)
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Fatal("runnable CPU companion did not join")
		}
	})
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	budget, checksum := 20*time.Millisecond, uint64(1)
	before, err := threadCPUTime()
	if err != nil {
		t.Fatal(err)
	}
	window, err := burnCPUWindow(t.Context(), time.Now().Add(time.Second), budget, &checksum)
	used := window.used
	if err != nil || used < budget || checksum == 1 {
		t.Fatalf("real CPU budget was not consumed: used=%s checksum=%d: %v", used, checksum, err)
	}
	after, clockErr := threadCPUTime()
	if clockErr != nil || after-before < budget || used > after-before {
		t.Fatalf("CPU receipt differs from the actual thread clock: used=%s clock=%s: %v", used, after-before, clockErr)
	}
	t.Logf("real CPU budget=%s consumed=%s outer_clock=%s checksum=%d", budget, used, after-before, checksum)
	previous := checksum
	window, err = burnCPUWindow(t.Context(), time.Now().Add(-time.Nanosecond), budget, &checksum)
	used = window.used
	if err != nil || used != 0 || checksum != previous {
		t.Fatalf("expired CPU window burned work: used=%s checksum=%d: %v", used, checksum, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	window, err = burnCPUWindow(ctx, time.Now().Add(time.Second), budget, &checksum)
	used = window.used
	if !errors.Is(err, context.Canceled) || used != 0 || checksum != previous {
		t.Fatalf("canceled CPU window burned work: used=%s checksum=%d: %v", used, checksum, err)
	}
}
