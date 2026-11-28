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

func TestSchedulerPressureDisabledCreatesNoActivity(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pressure, err := startSchedulerPressure(ctx, Profile{}, time.Now(), cancel)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := pressure.join()
	if err != nil || observed.Enabled || observed.Workers != 0 || observed.Yields != 0 || !observed.Completed {
		t.Fatalf("disabled scheduler pressure did work: %+v: %v", observed, err)
	}
}

func TestSchedulerPressureFutureCancellationJoins(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pressure, err := startSchedulerPressure(ctx, Profile{SchedulerPressure: true, Duration: time.Second},
			time.Now().Add(time.Hour), cancel)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		cancel()
		observed, err := pressure.join()
		if !errors.Is(err, context.Canceled) || observed.Completed || observed.Yields != 0 || observed.Rounds != 0 ||
			observed.JoinedWorkers != 4*runtime.GOMAXPROCS(0) || observed.JoinedWorkers != observed.Workers {
			t.Fatalf("future cancellation lost joined inactive workers: %+v: %v", observed, err)
		}
	})
}

func TestSchedulerPressureBoundsActualRoundsAndJoins(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pressure, err := startSchedulerPressure(ctx, Profile{SchedulerPressure: true, Duration: 200 * time.Millisecond},
			time.Now(), cancel)
		if err != nil {
			t.Fatal(err)
		}
		observed, err := pressure.join()
		want := uint64(runtime.GOMAXPROCS(0)) * 4 * 128
		if err != nil || !observed.Completed || observed.JoinedWorkers != 4*runtime.GOMAXPROCS(0) ||
			observed.Rounds != want || observed.Yields != want || observed.SkippedWindows != 0 ||
			observed.IncompleteWindows != 0 {
			t.Fatalf("bounded worker rounds or joining lost: %+v: %v", observed, err)
		}
	})
}

func TestSchedulerWorkerSkipsDebt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		observed := runSchedulerWorker(t.Context(), time.Now().Add(-350*time.Millisecond), 500*time.Millisecond, 1)
		if observed.err != nil || !observed.Completed || observed.SkippedWindows != 3 || observed.Windows != 2 ||
			observed.Rounds != 128 || observed.Yields != 128 || observed.WorkChecksum == 1 {
			t.Fatalf("worker caught up debt or skipped actual work: %+v: %v", observed, observed.err)
		}
	})
}

func TestSchedulerWindowStopsBeforeCanceledOrExpiredWork(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	observed := SchedulerPressureObservation{WorkChecksum: 1}
	if err := runSchedulerWindow(ctx, time.Now().Add(time.Hour), &observed); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled round admitted: %v", err)
	}
	if err := runSchedulerWindow(t.Context(), time.Now().Add(-time.Second), &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Rounds != 0 || observed.Yields != 0 || observed.WorkChecksum != 1 {
		t.Fatalf("inactive window burned or yielded: %+v", observed)
	}
}

func TestSchedulerPressureRejectsInvalidStart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := startSchedulerPressure(ctx, Profile{SchedulerPressure: true}, time.Now(), cancel)
	if !errors.Is(err, ErrProfile) {
		t.Fatalf("invalid duration admitted: %v", err)
	}
	cancel()
	_, err = startSchedulerPressure(ctx, Profile{SchedulerPressure: true, Duration: time.Second}, time.Now(), cancel)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled workers admitted: %v", err)
	}
}
