package integrationrunner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

var errQueryFailed = errors.New("gobgp query failed")

func TestWaitForRouteReturnsWhenStateMatches(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		query := func(context.Context) (bool, error) {
			switch calls.Add(1) {
			case 1:
				return false, errQueryFailed
			case 2:
				return false, nil
			default:
				return true, nil
			}
		}
		start := time.Now()
		if err := waitForRoute(t.Context(), query, true, time.Minute); err != nil {
			t.Fatalf("waitForRoute() = %v, want nil", err)
		}
		if got := time.Since(start); got != 2*time.Second || calls.Load() != 3 {
			t.Fatalf("elapsed=%s calls=%d, want 2s and 3", got, calls.Load())
		}
	})
}

func TestWaitForRouteFailsAtDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		query := func(context.Context) (bool, error) { return true, nil }
		err := waitForRoute(t.Context(), query, false, 30*time.Second)
		if !errors.Is(err, errRouteState) {
			t.Fatalf("waitForRoute() = %v, want errRouteState", err)
		}
	})
}

func TestWaitForRouteKeepsLastQueryError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		query := func(context.Context) (bool, error) { return false, errQueryFailed }
		err := waitForRoute(t.Context(), query, true, 5*time.Second)
		if !errors.Is(err, errRouteState) || !errors.Is(err, errQueryFailed) {
			t.Fatalf("waitForRoute() = %v, want errRouteState wrapping the query error", err)
		}
	})
}
