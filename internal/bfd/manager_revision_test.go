package bfd

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"
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

func TestCausalManagerSnapshotAheadOfDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		sess := notificationTestSession(t, mgr)
		synctest.Wait()
		initial := sess.Observation().ObservationVersion
		if initial == 0 || !sess.SetPathDown() {
			t.Fatal("missing managed registration version or PathDown admission")
		}
		synctest.Wait()
		snaps, revision := mgr.SessionsWithRevision()
		if revision != 0 || len(snaps) != 1 || snaps[0].Cause != CausePathDown ||
			snaps[0].ObservationVersion <= initial || snaps[0].State != StateDown || snaps[0].LocalDiag != DiagPathDown {
			t.Fatalf("snapshot not ahead of dispatch: snapshots=%+v revision=%d", snaps, revision)
		}
	})
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
			sess.emitNotification(FSMResult{OldState: StateDown, NewState: StateInit}, sess.Observation())
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
		sess.emitNotification(FSMResult{OldState: StateInit, NewState: StateUp}, sess.Observation())
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

		sess.emitNotification(FSMResult{OldState: StateDown, NewState: StateInit}, sess.Observation())
		synctest.Wait()

		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}
		_, rev := mgr.SessionsWithRevision()
		if rev != 1 {
			t.Fatalf("snapshot revision = %d, want 1", rev)
		}
		sess.emitNotification(FSMResult{OldState: StateInit, NewState: StateUp}, sess.Observation())
		synctest.Wait()
		if sc := <-sub; sc.Revision != rev+1 {
			t.Fatalf("event after snapshot revision = %d, want %d", sc.Revision, rev+1)
		}
	})
}

// A snapshot waiting for the registry must cover a deletion that replaces
// the session while that lock is held, or its queued tombstone erases the replacement.
func TestSnapshotRevisionCoversReplacementWhileWaitingForRegistry(t *testing.T) {
	for _, method := range []string{"SessionsWithRevision", "SessionWithRevision"} {
		t.Run(method, func(t *testing.T) {
			mgr := NewManager(slog.New(slog.DiscardHandler))
			defer mgr.Close()
			session := notificationTestSession(t, mgr)
			discr := session.LocalDiscriminator()
			replacement, err := NewSession(senderLeaseTestConfig("192.0.2.1"), discr,
				ownershipNoopSender{}, mgr.rawNotifyCh, mgr.logger)
			if err != nil {
				t.Fatalf("NewSession replacement: %v", err)
			}
			type result struct {
				snapshots []SessionSnapshot
				revision  uint64
			}
			done := make(chan result, 1)
			func() {
				mgr.mu.Lock()
				defer mgr.mu.Unlock()
				go func() {
					if method == "SessionsWithRevision" {
						snapshots, revision := mgr.SessionsWithRevision()
						done <- result{snapshots, revision}
					} else {
						snapshot, revision, _ := mgr.SessionWithRevision(discr)
						done <- result{[]SessionSnapshot{snapshot}, revision}
					}
				}()
				waitForSnapshotRegistryLock(t, method)
				mgr.broadcastStateChange(StateChange{LocalDiscr: discr, Deleted: true})
				mgr.sessions[discr].session = replacement
			}()
			got := <-done
			if len(got.snapshots) != 1 || got.snapshots[0].LocalDiscr != discr || got.revision != 1 {
				t.Fatalf("replacement snapshot = %+v revision %d, want discriminator %d covered by 1",
					got.snapshots, got.revision, discr)
			}
		})
	}
}

func waitForSnapshotRegistryLock(t *testing.T, method string) {
	t.Helper()
	stack := make([]byte, 64*1024)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n := runtime.Stack(stack, true)
		if n == len(stack) {
			stack = make([]byte, 2*len(stack))
			continue
		}
		for goroutine := range strings.SplitSeq(string(stack[:n]), "\n\n") {
			if strings.Contains(goroutine, "(*Manager)."+method+"(") &&
				strings.Contains(goroutine, "sync.(*RWMutex).RLock") {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatalf("%s did not block on the registry read lock", method)
}
