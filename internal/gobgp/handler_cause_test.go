package gobgp_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

func TestHandlerCausalPendingAndGapFloor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		mock.setError(errors.New("temporary failure"))
		snap := baseSnapshot(1, "10.0.0.1", bfd.StateDown)
		snap.RemoteKnown, snap.RemoteState = true, bfd.StateAdminDown
		snap.Cause, snap.ObservationVersion = bfd.CauseRemoteAdminDown, 500
		events := runHandler(t, snapshotHandler(t, mock, func() ([]bfd.SessionSnapshot, uint64) {
			return []bfd.SessionSnapshot{snap}, 10
		}))
		failure := peerTransition(bfd.StateUp, bfd.StateDown)
		failure.ObservationVersion, failure.Revision = 400, 1
		events <- failure
		synctest.Wait()
		events <- bfd.StateChange{Gap: true, Revision: 5}
		synctest.Wait()
		failure.Revision = 11
		events <- failure
		mock.setError(nil)
		synctest.Sleep(time.Minute)
		assertMethods(t, mock)
		// Explicit local PathDown takes priority over the retained remote admin fact.
		mock.setError(errors.New("temporary path failure"))
		path := peerTransition(bfd.StateDown, bfd.StateDown)
		path.Cause, path.ObservationVersion, path.Revision = bfd.CausePathDown, 501, 12
		path.RemoteKnown, path.RemoteState = true, bfd.StateAdminDown
		events <- path
		synctest.Wait()
		synctest.Sleep(500 * time.Millisecond)
		timeoutPath := path
		timeoutPath.Diag, timeoutPath.ObservationVersion, timeoutPath.Revision = bfd.DiagControlTimeExpired, 502, 13
		events <- timeoutPath
		synctest.Wait()
		staleAdmin := path
		staleAdmin.Cause, staleAdmin.ObservationVersion, staleAdmin.Revision = bfd.CauseRemoteAdminDown, 500, 14
		events <- staleAdmin
		synctest.Wait()
		mock.setError(nil)
		synctest.Sleep(500 * time.Millisecond)
		assertMethods(t, mock, methodDisablePeer)
	})
}

func TestHandlerCausalReplacementAndOldRefDeletion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause bfd.Cause
	}{
		{"initial-down", bfd.CauseUnknown}, {"explicit-path-down", bfd.CausePathDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mock := newMockClient()
				if tc.cause == bfd.CausePathDown {
					mock.setError(errors.New("old path failure"))
				}
				oldRef, newRef := new(bfd.Session).Ref(), new(bfd.Session).Ref()
				snap := baseSnapshot(1, "10.0.0.1", bfd.StateDown)
				snap.Ref, snap.ObservationVersion = newRef, 501
				snap.Cause = tc.cause
				events := runHandler(t, snapshotHandler(t, mock, func() ([]bfd.SessionSnapshot, uint64) {
					return []bfd.SessionSnapshot{snap}, 10
				}))
				up := peerTransition(bfd.StateDown, bfd.StateUp)
				up.Ref, up.ObservationVersion, up.Revision = oldRef, 100, 1
				if tc.cause == bfd.CausePathDown {
					up.NewState, up.Cause = bfd.StateDown, bfd.CausePathDown
				}
				events <- up
				synctest.Wait()
				mock.setError(nil)
				events <- bfd.StateChange{Gap: true, Revision: 5}
				synctest.Wait()
				var want []string
				if tc.cause == bfd.CausePathDown {
					want = append(want, methodDisablePeer)
				}
				assertMethods(t, mock, want...) // Only explicit new PathDown actuates.
				mock.setError(errors.New("temporary failure"))
				failure := peerTransition(bfd.StateUp, bfd.StateDown)
				failure.Ref, failure.ObservationVersion, failure.Revision = newRef, 502, 11
				events <- failure
				synctest.Wait()
				deleted := up
				deleted.Deleted, deleted.ObservationVersion, deleted.Revision = true, 600, 12
				events <- deleted
				synctest.Wait()
				mock.setError(nil)
				synctest.Sleep(time.Second)
				assertMethods(t, mock, append(want, methodDisablePeer)...)
			})
		})
	}
}

type causalAdminReadClient struct {
	*mockClient

	started chan struct{}
	release chan struct{}
}

func (c *causalAdminReadClient) PeerAdminDown(context.Context, string) (bool, error) {
	close(c.started)
	<-c.release
	return false, nil
}

func TestHandlerCausalVetoAfterAdminRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &causalAdminReadClient{
			mockClient: newMockClient(), started: make(chan struct{}), release: make(chan struct{}),
		}
		var allowed atomic.Bool
		allowed.Store(true)
		h, err := gobgp.NewHandler(gobgp.HandlerConfig{
			Client: client, Strategy: gobgp.StrategyDisablePeer,
			Actuates: func(bfd.StateChange) bool { return allowed.Load() }, Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		events := runHandler(t, h)
		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		<-client.started
		allowed.Store(false)
		close(client.release)
		synctest.Wait()
		assertMethods(t, client.mockClient)
	})
}

func TestHandlerCausalAdminKeepsOwnedRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{
			Enabled:           true,
			SuppressThreshold: 1, ReuseThreshold: 0.5, HalfLife: time.Minute, MaxSuppressTime: 5 * time.Minute,
		}))
		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()
		assertMethods(t, mock, methodDisablePeer)
		admin := peerTransition(bfd.StateUp, bfd.StateAdminDown)
		admin.Cause = bfd.CauseLocalAdmin
		events <- admin
		synctest.Sleep(time.Second)
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}

func TestHandlerCausalAdminPolicy(t *testing.T) {
	for _, state := range []bfd.State{bfd.StateInit, bfd.StateUp} {
		t.Run(state.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mock := newMockClient()
				events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))
				admin := peerTransition(state, bfd.StateDown)
				admin.RemoteKnown, admin.RemoteState = true, bfd.StateAdminDown
				admin.Cause, admin.ObservationVersion = bfd.CauseRemoteAdminDown, 1
				events <- admin
				synctest.Wait()
				assertMethods(t, mock)
				timeout := admin
				timeout.OldState, timeout.Cause, timeout.Diag = bfd.StateDown, bfd.CauseDetectionTimeout, bfd.DiagControlTimeExpired
				timeout.ObservationVersion++
				events <- timeout
				synctest.Sleep(time.Minute)
				assertMethods(t, mock)
				path := timeout
				path.Cause, path.Diag = bfd.CausePathDown, bfd.DiagPathDown
				path.ObservationVersion++
				events <- path
				synctest.Wait()
				assertMethods(t, mock, methodDisablePeer)
			})
		})
	}
}
