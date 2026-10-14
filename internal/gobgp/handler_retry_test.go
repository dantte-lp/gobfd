package gobgp_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

func peerTransition(from, to bfd.State) bfd.StateChange {
	return bfd.StateChange{
		LocalDiscr: 1,
		PeerAddr:   netip.MustParseAddr("10.0.0.1"),
		OldState:   from,
		NewState:   to,
		Diag:       bfd.DiagControlTimeExpired,
		Timestamp:  time.Now(),
	}
}

// runHandler starts h in the synctest bubble and returns the event channel.
// The handler stops when the test ends.
func runHandler(t *testing.T, h *gobgp.Handler) chan<- bfd.StateChange {
	t.Helper()

	events := make(chan bfd.StateChange, 10)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.Run(ctx, events)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return events
}

func callMethods(m *mockClient) []string {
	calls := m.getCalls()
	methods := make([]string, 0, len(calls))
	for _, c := range calls {
		methods = append(methods, c.method)
	}
	return methods
}

func assertMethods(t *testing.T, m *mockClient, want ...string) {
	t.Helper()

	got := callMethods(m)
	if len(got) != len(want) {
		t.Fatalf("GoBGP calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("GoBGP calls = %v, want %v", got, want)
		}
	}
}

// A GoBGP outage must not lose the Down: the handler retries until GoBGP
// accepts it, without another BFD event.
func TestHandlerRetriesActionAfterGoBGPOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		assertMethods(t, mock)

		mock.setError(nil)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)

		// Applied actions are not repeated.
		time.Sleep(time.Minute)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)
	})
}

// RFC 5882 Section 3.1: a Down hidden by dampening must still reach BGP
// when the session stays Down past the suppression period.
func TestHandlerAppliesSuppressedDownWhenSuppressionLifts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{
			Enabled:           true,
			SuppressThreshold: 2,
			ReuseThreshold:    1,
			MaxSuppressTime:   60 * time.Second,
			HalfLife:          15 * time.Second,
		}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		events <- peerTransition(bfd.StateUp, bfd.StateDown) // penalty 2: suppressed
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)

		// The penalty decays below the reuse threshold after one half-life.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)

		time.Sleep(10 * time.Second)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer, methodDisablePeer)
	})
}

// The newest BFD state wins: an Up that arrives while a Down retry is
// pending replaces it, so GoBGP ends with the peer enabled.
func TestHandlerNewerEventReplacesPendingAction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()

		mock.setError(nil)
		time.Sleep(time.Minute)
		synctest.Wait()
		assertMethods(t, mock, methodEnablePeer)
	})
}

func TestHandlerDoesNotRetryPermanentErrors(t *testing.T) {
	for _, err := range []error{
		status.Error(codes.InvalidArgument, "failed to parse address"),
		status.Error(codes.Unimplemented, "unknown service api.GoBgpService"),
		gobgp.ErrClientClosed,
	} {
		t.Run(err.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mock := newMockClient()
				mock.setError(err)
				events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

				events <- peerTransition(bfd.StateUp, bfd.StateDown)
				synctest.Wait()

				mock.setError(nil)
				time.Sleep(time.Minute)
				synctest.Wait()
				assertMethods(t, mock)
			})
		})
	}
}

// Cancellation during a retry backoff stops the handler at once.
func TestHandlerStopsDuringRetryBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		mock.setError(errors.New("neighbor that has 10.0.0.1 doesn't exist"))
		h := newTestHandler(t, mock, gobgp.DampeningConfig{})

		events := make(chan bfd.StateChange, 1)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = h.Run(ctx, events)
		}()

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()

		start := time.Now()
		cancel()
		<-done
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("handler stopped after %v, want immediately", waited)
		}
	})
}
