package gobgp_test

import (
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

func TestHandlerDeletionCancelsPendingActions(t *testing.T) {
	for _, deletion := range []string{"event", "gap"} {
		for _, recovery := range []bool{false, true} {
			name := deletion + "/down"
			if recovery {
				name = deletion + "/up"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					mock := newMockClient()
					h := snapshotHandler(t, mock, func() ([]bfd.SessionSnapshot, uint64) { return nil, 10 })
					events := runHandler(t, h)
					var want []string
					if recovery {
						events <- peerTransition(bfd.StateUp, bfd.StateDown)
						synctest.Wait()
						want = append(want, methodDisablePeer)
					}
					mock.setError(status.Error(codes.Unavailable, "connection refused"))
					pending := peerTransition(bfd.StateUp, bfd.StateDown)
					if recovery {
						pending = peerTransition(bfd.StateDown, bfd.StateUp)
					}
					events <- pending
					synctest.Wait()
					if deletion == "event" {
						pending.Deleted = true
						events <- pending
					} else {
						events <- bfd.StateChange{Gap: true, Revision: 10}
					}
					synctest.Wait()
					mock.setError(nil)
					synctest.Sleep(time.Minute)
					assertMethods(t, mock, want...)
				})
			})
		}
	}
}

func TestHandlerDeletionForgetsSessionState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		snaps := []bfd.SessionSnapshot{baseSnapshot(1, "10.0.0.1", bfd.StateUp)}
		revision := uint64(1)
		events := runHandler(t, snapshotHandler(t, mock, func() ([]bfd.SessionSnapshot, uint64) {
			return snaps, revision
		}))
		events <- bfd.StateChange{Gap: true, Revision: 1}
		synctest.Wait()

		deleted := peerTransition(bfd.StateUp, bfd.StateUp)
		deleted.Deleted = true
		deleted.Revision = 2
		events <- deleted
		synctest.Wait()

		// A new session reusing the discriminator starts Down; the old Up
		// must not turn its first snapshot into a failure transition.
		snaps = []bfd.SessionSnapshot{baseSnapshot(1, "10.0.0.1", bfd.StateDown)}
		revision = 3
		events <- bfd.StateChange{Gap: true, Revision: revision}
		synctest.Wait()
		assertMethods(t, mock)
	})
}

// Removing one session must preserve a newer action for another session
// at the same peer address, and the peer ownership acquired by GoBFD.
func TestHandlerDeletionPreservesOtherSessionAction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		events := runHandler(t, newTestHandler(t, mock, gobgp.DampeningConfig{}))
		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		synctest.Wait()
		mock.setError(status.Error(codes.Unavailable, "connection refused"))
		up := peerTransition(bfd.StateDown, bfd.StateUp)
		up.LocalDiscr = 2
		events <- up
		synctest.Wait()
		deleted := peerTransition(bfd.StateDown, bfd.StateDown)
		deleted.Deleted = true
		events <- deleted
		synctest.Wait()
		mock.setError(nil)
		synctest.Sleep(time.Minute)
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}
