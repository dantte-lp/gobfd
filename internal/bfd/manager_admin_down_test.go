package bfd

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

func TestManagerAdminDownFanoutAndIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		sender := &lifecycleBlockingSender{entered: make(chan struct{}), release: make(chan struct{})}
		blocked := newAdminDownTestSession(t, sender, 51)
		ready := newAdminDownTestSession(t, senderLeaseTestSender{}, 52)
		mgr.sessions[51] = &sessionEntry{session: blocked}
		mgr.sessions[52] = &sessionEntry{session: ready}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		blockedDone, readyDone := make(chan struct{}), make(chan struct{})
		go func() { blocked.Run(ctx); close(blockedDone) }()
		go func() { ready.Run(ctx); close(readyDone) }()
		<-sender.entered
		for range cap(blocked.ctrlCh) {
			if !blocked.SetPathDown() {
				t.Fatal("fill blocked owner queue")
			}
		}
		drainCtx, cancelDrain := context.WithTimeout(t.Context(), time.Minute)
		defer cancelDrain()
		result := make(chan error, 1)
		go func() { result <- mgr.DrainAllSessions(drainCtx) }()
		synctest.Wait()
		if ready.State() != StateAdminDown || ready.LocalDiag() != DiagAdminDown {
			t.Error("full queue delayed the unaffected session commit")
		}
		if blocked.State() != StateDown || len(result) != 0 {
			t.Error("drain declared blocked owner committed without its acknowledgement")
		}
		replacement := newAdminDownTestSession(t, senderLeaseTestSender{}, 51)
		if !mgr.mu.TryLock() {
			t.Error("drain held manager registry lock while waiting")
		} else {
			mgr.sessions[51] = &sessionEntry{session: replacement}
			mgr.mu.Unlock()
		}
		if !mgr.ownershipMu.TryLock() {
			t.Error("drain held ownership lock while waiting")
		} else {
			mgr.ownershipMu.Unlock()
		}
		if !mgr.lifecycleMu.TryLock() {
			t.Error("drain held lifecycle lock while waiting")
		} else {
			mgr.lifecycleMu.Unlock()
		}
		close(sender.release)
		if err := <-result; err != nil {
			t.Errorf("DrainAllSessions: %v", err)
		}
		if blocked.State() != StateAdminDown || blocked.LocalDiag() != DiagAdminDown {
			t.Error("captured Session did not commit AdminDown")
		}
		if replacement.State() != StateDown || len(replacement.ctrlCh) != 0 {
			t.Error("drain followed discriminator reuse into the replacement Session")
		}
		cancel()
		<-blockedDone
		<-readyDone
	})
}

func TestManagerAdminDownRejectsClosedLifecycle(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	if !mgr.beginClose() {
		t.Fatal("begin close")
	}
	if err := mgr.DrainAllSessions(t.Context()); !errors.Is(err, ErrManagerClosing) {
		t.Errorf("closing drain error = %v, want closing", err)
	}
	mgr.completeClose()
	if err := mgr.DrainAllSessions(t.Context()); !errors.Is(err, ErrManagerClosed) {
		t.Errorf("closed drain error = %v, want closed", err)
	}
}

func TestManagerAdminDownDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 53)
		mgr.sessions[53] = &sessionEntry{session: s}
		stopped := newAdminDownTestSession(t, senderLeaseTestSender{}, 54)
		stoppedCtx, stop := context.WithCancel(t.Context())
		stop()
		stopped.Run(stoppedCtx)
		mgr.sessions[54] = &sessionEntry{session: stopped}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err := mgr.DrainAllSessions(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("DrainAllSessions without owner = %v, want deadline", err)
		}
		if !errors.Is(err, ErrSessionStopped) {
			t.Errorf("DrainAllSessions joined error = %v, want stopped", err)
		}
		if s.State() != StateDown || s.LocalDiag() != DiagNone {
			t.Error("timed-out drain mutated owner state")
		}
	})
}
