package gobgp_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
	"github.com/dantte-lp/gobfd/internal/testutil"
)

func TestHandlerRawLossResyncRejectsQueuedOldUp(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	mgr, session := testutil.RawLossSession(ctx, t)
	sub, err := mgr.SubscribeStateChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mock := newMockClient()
	// A startup GAP commits the actual authoritative Up before the lost Down.
	snapshotted := make(chan struct{}, 3)
	handler, err := gobgp.NewHandler(gobgp.HandlerConfig{
		Client: mock, Strategy: gobgp.StrategyDisablePeer, Logger: slog.New(slog.DiscardHandler),
		Snapshot: func() ([]bfd.SessionSnapshot, uint64) {
			snaps, revision := mgr.SessionsWithRevision()
			select {
			case snapshotted <- struct{}{}:
			default:
				t.Error("unexpected extra handler resnapshot")
			}
			return snaps, revision
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan bfd.StateChange, 4)
	done := make(chan error, 1)
	go func() { done <- handler.Run(ctx, events) }()
	events <- bfd.StateChange{Gap: true, Revision: mgr.Revision()}
	select {
	case <-snapshotted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	testutil.LoseFinalDown(ctx, t, mgr, session)
	oldForwarded := make(chan struct{})
	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for {
			select {
			case event, ok := <-sub:
				if !ok {
					return
				}
				select {
				case events <- event:
					if event.Ref == session.Ref() && event.NewState == bfd.StateUp {
						close(oldForwarded)
					}
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	go mgr.RunDispatch(ctx)
	testutil.Wait(ctx, t, func() bool {
		return testutil.DeliveryCount(mgr, bfd.DeliveryNotificationDispatch, bfd.DeliveryAccepted) == 1 &&
			testutil.DeliveryCount(mgr, bfd.DeliveryNotificationDispatch, bfd.DeliveryStale) == 63
	})
	select {
	case <-oldForwarded:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Ordered after the forwarded old Up: this callback proves the handler processed that event.
	select {
	case events <- bfd.StateChange{Gap: true, Revision: mgr.Revision() + 1}:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for range 2 {
		select {
		case <-snapshotted:
		case <-ctx.Done():
			t.Fatal("handler did not reach the ordered resnapshot barrier")
		}
	}
	assertMethods(t, mock, methodDisablePeer)
	if testutil.DeliveryCount(mgr, bfd.DeliveryWatchRecipient, bfd.DeliveryGapped) != 0 {
		t.Fatal("handler converged through subscriber overflow instead of raw loss")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after cancellation")
	}
	select {
	case <-forwardDone:
	case <-time.After(time.Second):
		t.Fatal("handler event forwarder did not stop after cancellation")
	}
}
