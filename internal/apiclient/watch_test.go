package apiclient

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestSessionViewGapSnapshotAndDeletion(t *testing.T) {
	t.Parallel()
	v := sessionView{sessions: make(map[uint32]*bfdv1.BfdSession), syncing: true}
	up := func(discr uint32) *bfdv1.BfdSession {
		return &bfdv1.BfdSession{
			LocalDiscriminator: discr, PeerAddress: "192.0.2.1", LocalState: bfdv1.SessionState_SESSION_STATE_UP,
		}
	}
	for i, step := range []struct {
		event            bfdv1.WatchSessionEventsResponse_EventType
		rev              uint64
		session          *bfdv1.BfdSession
		changed, syncing bool
		count            int
	}{
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, rev: 7, session: up(1), syncing: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, rev: 7, session: up(2), syncing: true, count: 2},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, rev: 7, changed: true, count: 2},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE, rev: 7, session: up(3), count: 2},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED, rev: 9, session: up(1), changed: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP, rev: 15, changed: true, syncing: true},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, rev: 16, session: up(4), syncing: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, rev: 16, changed: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP, rev: 20, changed: true, syncing: true},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, rev: 20, changed: true},
	} {
		changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
			Type: step.event, Revision: step.rev, Session: step.session,
		})
		if err != nil || changed != step.changed || v.syncing != step.syncing || len(v.sessions) != step.count {
			t.Fatalf("step %d: changed=%v syncing=%v count=%d err=%v", i, changed, v.syncing, len(v.sessions), err)
		}
	}
}

type unavailableWatch struct {
	bfdv1connect.BfdServiceClient

	calls atomic.Int32
}

func (c *unavailableWatch) WatchSessionEvents(
	context.Context, *bfdv1.WatchSessionEventsRequest,
) (bfdv1connect.BfdServiceWatchSessionEventsClientStream, error) {
	c.calls.Add(1)
	return bfdv1connect.BfdServiceWatchSessionEventsClientStream{}, syscall.ECONNREFUSED
}

func TestWatchSessionsBoundedBackoffAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client := &unavailableWatch{}
		done := make(chan error, 1)
		go func() {
			done <- WatchSessions(ctx, client, func(sessions []*bfdv1.BfdSession) error {
				if sessions != nil {
					t.Error("failed stream published a usable view")
				}
				return nil
			}, slog.New(slog.DiscardHandler))
		}()
		synctest.Wait()
		if client.calls.Load() != 1 {
			t.Fatalf("initial attempts %d", client.calls.Load())
		}
		waits := []time.Duration{
			time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
		}
		for i, wait := range waits {
			synctest.Sleep(wait)
			if client.calls.Load() != int32(i+2) {
				t.Fatalf("after %v: attempts=%d, want %d", wait, client.calls.Load(), i+2)
			}
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	})
}

func TestWatchSessionsConsumerErrorIsTerminal(t *testing.T) {
	t.Parallel()
	client := &unavailableWatch{}
	err := WatchSessions(t.Context(), client,
		func([]*bfdv1.BfdSession) error { return syscall.EPIPE }, slog.New(slog.DiscardHandler))
	if !errors.Is(err, syscall.EPIPE) || client.calls.Load() != 0 {
		t.Fatalf("consumer failure = %v, stream attempts %d", err, client.calls.Load())
	}
}
