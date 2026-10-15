package bfd

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
)

func notificationTestSession(t *testing.T, mgr *Manager) *Session {
	t.Helper()
	sess, err := mgr.CreateSession(context.Background(), senderLeaseTestConfig("192.0.2.1"),
		ownershipSenderLeaseFactory())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

// A slow subscriber loses events but never silently: the last buffer slot
// carries a gap marker, which arrives even when no further event follows
// (H-29, M-08).
func TestSubscriberOverflowQueuesGapMarker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sess := notificationTestSession(t, mgr)
		go mgr.RunDispatch(ctx)

		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		for range notifyChSize + 10 {
			sess.emitNotification(FSMResult{OldState: StateDown, NewState: StateInit})
			synctest.Wait()
		}

		var last uint64
		for i := range notifyChSize - 1 {
			sc := <-sub
			if sc.Gap || sc.LocalDiscr != sess.LocalDiscriminator() {
				t.Fatalf("event %d = %+v, want discriminator %d", i, sc, sess.LocalDiscriminator())
			}
			if sc.Revision <= last {
				t.Fatalf("event %d revision %d not above %d", i, sc.Revision, last)
			}
			last = sc.Revision
		}
		gap := <-sub
		if !gap.Gap || gap.Revision <= last {
			t.Fatalf("last buffered event = %+v, want a gap marker above revision %d", gap, last)
		}
		select {
		case sc := <-sub:
			t.Fatalf("event after the gap marker without new traffic: %+v", sc)
		default:
		}

		// Delivery resumes once the subscriber has drained its buffer.
		sess.emitNotification(FSMResult{OldState: StateInit, NewState: StateUp})
		synctest.Wait()
		if sc := <-sub; sc.Gap || sc.LocalDiscr != sess.LocalDiscriminator() {
			t.Fatalf("event after drain = %+v, want discriminator %d", sc, sess.LocalDiscriminator())
		}
	})
}

// A snapshot taken after subscribing reports the dispatch revision it
// covers: every later event carries a higher revision (M-08 handoff).
func TestSessionsWithRevisionCoversDispatchedEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sess := notificationTestSession(t, mgr)
		go mgr.RunDispatch(ctx)

		sess.emitNotification(FSMResult{OldState: StateDown, NewState: StateInit})
		synctest.Wait()

		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		_, rev := mgr.SessionsWithRevision()
		if rev != 1 {
			t.Fatalf("snapshot revision = %d, want 1", rev)
		}
		sess.emitNotification(FSMResult{OldState: StateInit, NewState: StateUp})
		synctest.Wait()
		if sc := <-sub; sc.Revision != rev+1 {
			t.Fatalf("event after snapshot revision = %d, want %d", sc.Revision, rev+1)
		}
	})
}
