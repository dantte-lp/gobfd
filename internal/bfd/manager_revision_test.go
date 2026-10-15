package bfd

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
)

// A slow subscriber loses events but never silently: the last buffer slot
// carries a gap marker, which arrives even when no further event follows
// (H-29, M-08).
func TestSubscriberOverflowQueuesGapMarker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go mgr.RunDispatch(ctx)

		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		for i := range notifyChSize + 10 {
			mgr.rawNotifyCh <- StateChange{LocalDiscr: uint32(i + 1), OldState: StateDown, NewState: StateInit}
			synctest.Wait()
		}

		var last uint64
		for i := range notifyChSize - 1 {
			sc := <-sub
			if sc.Gap || sc.LocalDiscr != uint32(i+1) {
				t.Fatalf("event %d = %+v, want discriminator %d", i, sc, i+1)
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
		mgr.rawNotifyCh <- StateChange{LocalDiscr: 999, OldState: StateInit, NewState: StateUp}
		synctest.Wait()
		if sc := <-sub; sc.Gap || sc.LocalDiscr != 999 {
			t.Fatalf("event after drain = %+v, want discriminator 999", sc)
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
		go mgr.RunDispatch(ctx)

		mgr.rawNotifyCh <- StateChange{LocalDiscr: 1, OldState: StateDown, NewState: StateInit}
		synctest.Wait()

		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		_, rev := mgr.SessionsWithRevision()
		if rev != 1 {
			t.Fatalf("snapshot revision = %d, want 1", rev)
		}
		mgr.rawNotifyCh <- StateChange{LocalDiscr: 1, OldState: StateInit, NewState: StateUp}
		synctest.Wait()
		if sc := <-sub; sc.Revision != rev+1 {
			t.Fatalf("event after snapshot revision = %d, want %d", sc.Revision, rev+1)
		}
	})
}
