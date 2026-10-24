//go:build linux

package bfdload

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestGCPressureDisabledCreatesNoActivity(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pressure, err := startGCPressure(ctx, Profile{}, time.Now(), cancel)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := pressure.join()
	if err != nil || observed.Enabled || observed.Workers != 0 || observed.Objects != 0 || !observed.Completed {
		t.Fatalf("disabled pressure generated activity: %+v: %v", observed, err)
	}
}

func TestGCPressureCancellationJoinsAndReleases(t *testing.T) {
	t.Parallel()
	for _, future := range []bool{false, true} {
		t.Run(map[bool]string{false: "working", true: "before_epoch"}[future], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				at := time.Now()
				if future {
					at = at.Add(time.Hour)
				}
				pressure, err := startGCPressure(ctx, Profile{GCPressure: true, Duration: time.Second}, at, cancel)
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				cancel()
				observed, err := pressure.join()
				if !errors.Is(err, context.Canceled) || observed.Completed || observed.Workers != 1 {
					t.Fatalf("canceled worker lost partial outcome: %+v: %v", observed, err)
				}
				if future {
					if observed.Objects != 0 || observed.Batches != 0 || observed.AllocatedBytes != 0 {
						t.Fatalf("worker allocated before the epoch: %+v", observed)
					}
				} else if observed.Objects != 64 || observed.AllocatedBytes != 4194304 ||
					observed.Batches != 1 || observed.ReleasedObjects != 64 {
					t.Fatalf("cancellation lost the actual batch or retained references: %+v", observed)
				}
			})
		})
	}
}

func TestGCPressureSkipsElapsedWindowsAndBoundsReferences(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pressure, err := startGCPressure(ctx, Profile{GCPressure: true, Duration: time.Second},
			time.Now().Add(-350*time.Millisecond), cancel)
		if err != nil {
			t.Fatal(err)
		}
		observed, err := pressure.join()
		if err != nil || !observed.Completed || observed.Batches != 7 || observed.SkippedBatches != 3 ||
			observed.Objects != 448 || observed.AllocatedBytes != 29360128 || observed.TruncatedBatches != 0 ||
			observed.PeakRetainedObjects != 256 || observed.ReleasedObjects != 256 {
			t.Fatalf("late workload caught up debt or lost bounded cleanup: %+v: %v", observed, err)
		}
	})
}

func TestGCPressureRejectsInvalidStart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := startGCPressure(ctx, Profile{GCPressure: true}, time.Now(), cancel); !errors.Is(err, ErrProfile) {
		t.Fatalf("invalid duration admitted: %v", err)
	}
	cancel()
	_, err := startGCPressure(ctx, Profile{GCPressure: true, Duration: time.Second}, time.Now(), cancel)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled worker admitted: %v", err)
	}
}
