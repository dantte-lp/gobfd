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

func TestFullWatchAddedPreservesPendingAndDampening(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recovery bool
	}{{"pending-down", false}, {"held-recovery", true}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recovery := tc.recovery
				mock := newMockClient()
				h := newTestHandler(t, mock, gobgp.DampeningConfig{
					Enabled: true, SuppressThreshold: 1, ReuseThreshold: 0.5,
					MaxSuppressTime: time.Minute, HalfLife: time.Second,
				})
				events := runHandler(t, h)
				if !recovery {
					mock.setError(errors.New("temporary outage"))
				}
				events <- peerTransition(bfd.StateUp, bfd.StateDown)
				synctest.Wait()
				if recovery {
					events <- peerTransition(bfd.StateDown, bfd.StateUp)
					synctest.Wait()
				}
				before := h.PeerStatus("10.0.0.1")
				if before.Action == "" || !before.Suppressed || before.Penalty == 0 {
					t.Fatalf("missing pending suppressed action: %+v", before)
				}
				added := peerTransition(bfd.StateDown, bfd.StateDown)
				added.Added, added.Diag = true, bfd.DiagNone
				added.LocalDiscr, added.Ref, added.ObservationVersion = 2, new(bfd.Session).Ref(), 3
				events <- added
				synctest.Wait()
				if after := h.PeerStatus("10.0.0.1"); after != before {
					t.Fatalf("registration changed pending action or dampening: before=%+v after=%+v", before, after)
				}
				if recovery {
					assertMethods(t, mock, methodDisablePeer)
				} else {
					assertMethods(t, mock)
				}
				mock.setError(nil)
				synctest.Sleep(3 * time.Second)
				if recovery {
					assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
				} else {
					assertMethods(t, mock, methodDisablePeer)
				}
			})
		})
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

// v1 dampening contract (H-27): dampening never holds a qualifying Down.
// Once suppressed it holds only the recovery Up and redundant Downs while
// the peer stays disabled; a new Down cancels the held recovery, and the
// recovery is applied when the penalty decays below the reuse threshold.
func TestHandlerDampeningHoldsOnlyRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{
			Enabled:           true,
			SuppressThreshold: 2,
			ReuseThreshold:    1,
			MaxSuppressTime:   5 * time.Minute,
			HalfLife:          15 * time.Second,
		}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		events <- peerTransition(bfd.StateUp, bfd.StateDown) // penalty 2: suppressed, still applied
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer, methodDisablePeer)

		events <- peerTransition(bfd.StateDown, bfd.StateUp) // recovery held
		events <- peerTransition(bfd.StateUp, bfd.StateDown) // redundant: cancels the held recovery
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer, methodDisablePeer)

		events <- peerTransition(bfd.StateDown, bfd.StateUp) // recovery held again
		time.Sleep(20 * time.Second)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer, methodDisablePeer)

		// Penalty 3 decays below the reuse threshold of 1 after ~24 s.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer, methodDisablePeer, methodEnablePeer)
	})
}

// A Down whose GoBGP call failed is retried even while the peer is
// suppressed: suppression never delays the disable.
func TestHandlerRetriesDownWhileSuppressed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{
			Enabled:           true,
			SuppressThreshold: 2,
			ReuseThreshold:    1,
			MaxSuppressTime:   5 * time.Minute,
			HalfLife:          15 * time.Second,
		}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()
		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		events <- peerTransition(bfd.StateUp, bfd.StateDown) // suppressed, GoBGP down
		synctest.Wait()
		mock.setError(nil)
		time.Sleep(5 * time.Second)
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

// Before a late Down the handler rechecks ownership, even when the
// event removing the session has not reached the handler yet.
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

type echoWatchActuator struct{ calls atomic.Int32 }

func (a *echoWatchActuator) HandleMicroBFDMemberEvent(context.Context, bfd.MicroBFDMemberEvent) error {
	a.calls.Add(1)
	return nil
}

func TestEchoWatchLifecycleDoesNotActuateGoBGPOrMicro(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))
		actuator := &echoWatchActuator{}
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithMicroBFDActuator(actuator))
		defer mgr.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		go mgr.RunDispatch(ctx)
		cfg := bfd.EchoSessionConfig{
			PeerAddr: netip.MustParseAddr("10.0.0.1"), LocalAddr: netip.MustParseAddr("10.0.0.2"),
			Interface: "eth0", TxInterval: time.Second, DetectMultiplier: 3,
		}
		discr, err := mgr.CreateEchoSession(ctx, cfg, echoWatchSender{})
		if err != nil {
			t.Fatal(err)
		}
		forward := func() {
			t.Helper()
			synctest.Wait()
			if len(sub) != 1 {
				t.Fatalf("Echo lifecycle events=%d, want one", len(sub))
			}
			event := <-sub
			if event.Type != bfd.SessionTypeEcho || event.ObservationVersion == 0 || event.Status.Type != bfd.SessionTypeEcho {
				t.Fatalf("actual Echo lifecycle event = %+v", event)
			}
			events <- event
			synctest.Wait()
		}
		forward()
		if _, recvErr := mgr.DemuxEcho(discr); recvErr != nil {
			t.Fatal(recvErr)
		}
		forward()
		synctest.Sleep(3 * time.Second)
		forward()
		if destroyErr := mgr.DestroyEchoSession(discr); destroyErr != nil {
			t.Fatal(destroyErr)
		}
		forward()
		snapshots, _ := mgr.SessionsWithRevision()
		if len(snapshots) != 0 || actuator.calls.Load() != 0 {
			t.Fatalf("Echo entered Control snapshot or Micro actuator: snapshots=%+v calls=%d", snapshots, actuator.calls.Load())
		}
		assertMethods(t, mock)
	})
}

type echoWatchSender struct{}

func (echoWatchSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

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

// A peer GoBFD disabled is re-enabled on BFD Up even while its session no
// longer actuates the neighbor: an ambiguity that later clears produces no
// new event, so a rejected Up would leave the peer disabled.
func TestHandlerEnablesDisabledPeerWhenSessionStopsActuating(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		var actuating atomic.Bool
		actuating.Store(true)
		h, err := gobgp.NewHandler(gobgp.HandlerConfig{
			Client:   mock,
			Strategy: gobgp.StrategyDisablePeer,
			Actuates: func(bfd.StateChange) bool { return actuating.Load() },
			Logger:   slog.Default(),
		})
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}
		events := runHandler(t, h)

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()
		actuating.Store(false)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}

func snapshotHandler(t *testing.T, mock *mockClient, snaps func() ([]bfd.SessionSnapshot, uint64)) *gobgp.Handler {
	t.Helper()
	h, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client:   mock,
		Strategy: gobgp.StrategyDisablePeer,
		Snapshot: snaps,
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func baseSnapshot(discr uint32, peer string, state bfd.State) bfd.SessionSnapshot {
	return bfd.SessionSnapshot{
		LocalDiscr: discr,
		PeerAddr:   netip.MustParseAddr(peer),
		Type:       bfd.SessionTypeSingleHop,
		State:      state,
	}
}

// .8.2.4.1: a Down lost in a subscriber gap still disables the peer. The
// handler rebuilds the edge from its last-known state and the snapshot,
// then skips buffered events the snapshot covers.
func TestHandlerResyncAppliesDownLostInGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, snapshotHandler(t, mock, func() ([]bfd.SessionSnapshot, uint64) {
			return []bfd.SessionSnapshot{baseSnapshot(1, "10.0.0.1", bfd.StateDown)}, 10
		}))

		up := peerTransition(bfd.StateDown, bfd.StateUp)
		up.Revision = 1
		events <- up
		synctest.Wait()
		assertMethods(t, mock)

		events <- bfd.StateChange{Gap: true, Revision: 5}
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)

		// A stale buffered event the snapshot already covers.
		stale := peerTransition(bfd.StateDown, bfd.StateUp)
		stale.Revision = 9
		events <- stale
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)

		// A later event that repeats the state the resync applied.
		repeat := peerTransition(bfd.StateUp, bfd.StateDown)
		repeat.Revision = 11
		events <- repeat
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)

		recovered := peerTransition(bfd.StateDown, bfd.StateUp)
		recovered.Revision = 12
		events <- recovered
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}

// .8.2.4.1: an Up lost in a gap re-enables a peer GoBFD disabled; a
// session seen only in the snapshot, Down since startup, causes no action.
func TestHandlerResyncEnablesPeerAndIgnoresUnknownDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, snapshotHandler(t, mock, func() ([]bfd.SessionSnapshot, uint64) {
			return []bfd.SessionSnapshot{
				baseSnapshot(1, "10.0.0.1", bfd.StateUp),
				baseSnapshot(2, "10.0.0.2", bfd.StateDown),
			}, 3
		}))

		down := peerTransition(bfd.StateUp, bfd.StateDown)
		down.Revision = 1
		events <- down
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)

		events <- bfd.StateChange{Gap: true, Revision: 3}
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}

// A Down while the recovery is held cancels it: the peer stays disabled
// after the penalty decays, because BFD is Down.
func TestHandlerDampenedDownCancelsHeldRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{
			Enabled:           true,
			SuppressThreshold: 2,
			ReuseThreshold:    1,
			MaxSuppressTime:   5 * time.Minute,
			HalfLife:          15 * time.Second,
		}))

		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp) // recovery held
		events <- peerTransition(bfd.StateUp, bfd.StateDown) // cancels it
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer, methodDisablePeer)
	})
}
