package gobgp_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
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
		Type:       bfd.SessionTypeSingleHop,
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
// pending replaces it. The Down was never applied, so GoBFD did not disable
// the peer and sends nothing.
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
		assertMethods(t, mock)
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

// RFC 5882 Section 4.2: no control protocol action on AdminDown. An
// operator taking the session AdminDown cancels a Down that is still
// pending, so a late retry cannot disable a peer GoBFD stopped watching.
func TestHandlerAdminDownClearsPendingDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateAdminDown)
		synctest.Wait()

		mock.setError(nil)
		time.Sleep(time.Minute)
		synctest.Wait()
		assertMethods(t, mock)
	})
}

// A pending Up survives AdminDown: dropping it would leave a peer that
// GoBFD disabled stuck disabled.
func TestHandlerAdminDownKeepsPendingUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))
		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)

		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		events <- peerTransition(bfd.StateUp, bfd.StateAdminDown)
		synctest.Wait()

		mock.setError(nil)
		time.Sleep(time.Minute)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}

// Session deletion publishes no BFD event, so before a late Down the
// handler asks whether the peer still has a BFD session; a deleted
// session's pending Down is dropped.
func TestHandlerDropsPendingDownForUnmanagedPeer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		var managed atomic.Bool
		managed.Store(true)
		h, err := gobgp.NewHandler(gobgp.HandlerConfig{
			Client:   mock,
			Strategy: gobgp.StrategyDisablePeer,
			Actuates: func(bfd.StateChange) bool { return managed.Load() },
			Logger:   slog.Default(),
		})
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}
		events := runHandler(t, h)

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()

		managed.Store(false)
		mock.setError(nil)
		time.Sleep(time.Minute)
		synctest.Wait()
		assertMethods(t, mock)
	})
}

// GitHub #86: every GoBGP action attempt is reported, so failures reach
// metrics and readiness instead of logs only.
func TestHandlerReportsEveryActionAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type result struct {
			rpc string
			err bool
		}
		var (
			mu      sync.Mutex
			results []result
		)
		mock := newMockClient()
		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		h, err := gobgp.NewHandler(gobgp.HandlerConfig{
			Client:   mock,
			Strategy: gobgp.StrategyDisablePeer,
			Logger:   slog.New(slog.DiscardHandler),
			OnAction: func(rpc string, actionErr error) {
				mu.Lock()
				defer mu.Unlock()
				results = append(results, result{rpc, actionErr != nil})
			},
		})
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}
		events := runHandler(t, h)

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()
		mock.setError(nil)
		time.Sleep(time.Minute)
		synctest.Wait()
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		want := []result{{methodDisablePeer, true}, {methodDisablePeer, false}, {methodEnablePeer, false}}
		if len(results) != len(want) {
			t.Fatalf("reported actions = %v, want %v", results, want)
		}
		for i := range want {
			if results[i] != want[i] {
				t.Fatalf("reported actions = %v, want %v", results, want)
			}
		}
	})
}

// .8.2.4.1 (maintainer decision): GoBFD re-enables only peers it disabled.
// A peer the operator already disabled is neither disabled nor enabled.
func TestHandlerLeavesOperatorDisabledPeer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		mock.setAdminDown("10.0.0.1", true)
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()
		assertMethods(t, mock)
	})
}

// Without a disable by this process (for example after a restart) an Up
// does not enable the peer: a stale state cannot enable a peer that the
// operator may have disabled.
func TestHandlerEnablesOnlyPeersItDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()
		assertMethods(t, mock)

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}

// H-31: only base single-hop and multi-hop sessions describe the BGP
// neighbor. A Micro-BFD member link, overlay or Echo session to the same
// address must not disable the peer.
func TestHandlerIgnoresNonBaseSessionTypes(t *testing.T) {
	for _, typ := range []bfd.SessionType{
		bfd.SessionTypeEcho, bfd.SessionTypeMicroBFD,
		bfd.SessionTypeVXLAN, bfd.SessionTypeGeneve, 0,
	} {
		t.Run(typ.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mock := newMockClient()
				events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

				sc := peerTransition(bfd.StateUp, bfd.StateDown)
				sc.Type = typ
				events <- sc
				synctest.Wait()
				assertMethods(t, mock)
			})
		})
	}
}

// H-31: the daemon decides whether a session is the canonical BFD session
// for its BGP neighbor; the handler acts only on events it accepts.
func TestHandlerActsOnlyOnAcceptedSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		h, err := gobgp.NewHandler(gobgp.HandlerConfig{
			Client:   mock,
			Strategy: gobgp.StrategyDisablePeer,
			Actuates: func(sc bfd.StateChange) bool { return sc.LocalDiscr == 1 },
			Logger:   slog.Default(),
		})
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}
		events := runHandler(t, h)

		other := peerTransition(bfd.StateUp, bfd.StateDown)
		other.LocalDiscr = 2
		events <- other
		synctest.Wait()
		assertMethods(t, mock)

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)
	})
}

// An IPv4-mapped peer address names the same GoBGP neighbor as the plain
// IPv4 address.
func TestHandlerUnmapsIPv4MappedPeer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))

		sc := peerTransition(bfd.StateUp, bfd.StateDown)
		sc.PeerAddr = netip.MustParseAddr("::ffff:10.0.0.1")
		events <- sc
		synctest.Wait()

		calls := mock.getCalls()
		if len(calls) != 1 || calls[0].addr != "10.0.0.1" {
			t.Fatalf("GoBGP calls = %+v, want one call for 10.0.0.1", calls)
		}
	})
}
