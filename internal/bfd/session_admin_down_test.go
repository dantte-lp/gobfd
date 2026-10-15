package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

func TestSessionAdminDownBeforeRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &lifecycleBlockingSender{entered: make(chan struct{}), release: make(chan struct{})}
		s := newAdminDownTestSession(t, sender, 41)
		requestCtx, cancelRequest := context.WithTimeout(t.Context(), time.Minute)
		defer cancelRequest()
		result := make(chan error, 1)
		go func() { result <- s.SetAdminDown(requestCtx) }()
		synctest.Wait()
		if s.State() != StateDown || s.LocalDiag() != DiagNone {
			t.Error("request changed state/diagnostic without an owner")
		}
		if len(result) != 0 {
			t.Error("request completed before owner commit")
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		<-sender.entered
		synctest.Wait()
		if len(result) != 1 {
			t.Error("commit acknowledgement blocked on SendPacket")
		} else if err := <-result; err != nil {
			t.Errorf("AdminDown: %v", err)
		}
		if s.State() != StateAdminDown || s.LocalDiag() != DiagAdminDown || s.cachedState != StateAdminDown {
			t.Error("receipt preceded the complete AdminDown commit")
		}
		var pkt ControlPacket
		if err := UnmarshalControlPacket(s.cachedPacket[:MinPacketSizeNoAuth], &pkt); err != nil {
			t.Fatal(err)
		}
		if pkt.State != StateAdminDown || pkt.Diag != DiagAdminDown {
			t.Errorf("first packet = %s/%s, want AdminDown/7", pkt.State, pkt.Diag)
		}
		cancel()
		close(sender.release)
		<-done
	})
}

func TestSessionAdminDownQueueDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 42)
		for range cap(s.ctrlCh) {
			if !s.SetPathDown() {
				t.Fatal("fill control queue")
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- s.SetAdminDown(ctx) }()
		synctest.Wait()
		if len(result) != 0 || s.State() != StateDown || s.LocalDiag() != DiagNone {
			t.Error("full queue caused early completion or outside-owner mutation")
		}
		synctest.Sleep(time.Second)
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("full queue error = %v, want deadline", err)
		}
		if len(s.ctrlCh) != cap(s.ctrlCh) || s.State() != StateDown || s.LocalDiag() != DiagNone {
			t.Error("expired admission changed queued controls or state")
		}
	})
}

func TestSessionRunSingleOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 43)
		ctx, cancel := context.WithCancel(t.Context())
		firstDone := make(chan struct{})
		go func() { s.Run(ctx); close(firstDone) }()
		synctest.Wait()
		secondCtx, cancelSecond := context.WithCancel(t.Context())
		secondDone := make(chan struct{})
		go func() { s.Run(secondCtx); close(secondDone) }()
		synctest.Wait()
		select {
		case <-secondDone:
		default:
			t.Error("concurrent Run became another owner")
		}
		cancelSecond()
		<-secondDone
		cancel()
		<-firstDone
		restartCtx, cancelRestart := context.WithCancel(t.Context())
		restartDone := make(chan struct{})
		go func() { s.Run(restartCtx); close(restartDone) }()
		synctest.Wait()
		select {
		case <-restartDone:
		default:
			t.Error("Run restarted a terminal session")
		}
		cancelRestart()
		<-restartDone
	})
}

func TestSessionAdminDownStartupTransmission(t *testing.T) {
	for _, tc := range []struct {
		name         string
		role         SessionRole
		remoteDiscr  uint32
		remoteMinRX  time.Duration
		pendingFinal bool
		wantAttempts int
	}{
		{name: "active", role: RoleActive, remoteMinRX: time.Microsecond, wantAttempts: 1},
		{name: "passive unknown peer", role: RolePassive, remoteMinRX: time.Microsecond},
		{name: "passive known peer", role: RolePassive, remoteDiscr: 91, remoteMinRX: time.Microsecond, wantAttempts: 1},
		{name: "zero remote receive", role: RoleActive},
		{name: "pending final", role: RoleActive, pendingFinal: true, wantAttempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sender := &retryCachedPacketSender{}
				s := newAdminDownTestSession(t, sender, 44)
				s.role, s.remoteDiscr = tc.role, tc.remoteDiscr
				s.remoteMinRxInterval, s.pendingFinal = tc.remoteMinRX, tc.pendingFinal
				result := make(chan error, 1)
				go func() { result <- s.SetAdminDown(t.Context()) }()
				synctest.Wait()
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() { s.Run(ctx); close(done) }()
				synctest.Wait()
				if err := <-result; err != nil {
					t.Errorf("AdminDown: %v", err)
				}
				if sender.attempts != tc.wantAttempts {
					t.Errorf("startup attempts = %d, want %d", sender.attempts, tc.wantAttempts)
				}
				for _, wire := range sender.packets {
					var pkt ControlPacket
					if err := UnmarshalControlPacket(wire, &pkt); err != nil {
						t.Fatal(err)
					}
					if pkt.State != StateAdminDown || pkt.Diag != DiagAdminDown || pkt.Final != tc.pendingFinal {
						t.Errorf("startup packet = %+v", pkt)
					}
				}
				cancel()
				<-done
			})
		})
	}
}

func TestSessionAdminDownAdmittedTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 45)
		requestCtx, cancelRequest := context.WithTimeout(t.Context(), time.Second)
		defer cancelRequest()
		if err := s.SetAdminDown(requestCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("unstarted owner error = %v, want deadline", err)
		}
		if len(s.ctrlCh) != 1 || s.State() != StateDown {
			t.Error("admitted intent disappeared or committed without owner")
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		synctest.Wait()
		if s.State() != StateAdminDown || s.LocalDiag() != DiagAdminDown {
			t.Error("admitted intent was rolled back after caller timeout")
		}
		cancel()
		<-done
	})
}

func TestSessionAdminDownRepeated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &retryCachedPacketSender{}
		s := newAdminDownTestSession(t, sender, 46)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		if err := s.SetAdminDown(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		for range 2 {
			if err := s.SetAdminDown(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if s.stateTransitions.Load() != 1 || sender.attempts != 1 {
			t.Errorf("repeated commits: transitions=%d, TX=%d", s.stateTransitions.Load(), sender.attempts)
		}
		cancel()
		<-done
	})
}

func TestSessionAdminDownStoppedOwner(t *testing.T) {
	for _, fullQueue := range []bool{false, true} {
		t.Run(fmt.Sprintf("full queue=%t", fullQueue), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sender := &retryCachedPacketSender{}
				s := newAdminDownTestSession(t, sender, 47)
				if fullQueue {
					for range cap(s.ctrlCh) {
						if !s.SetPathDown() {
							t.Fatal("fill control queue")
						}
					}
				}
				result := make(chan error, 1)
				go func() { result <- s.SetAdminDown(t.Context()) }()
				synctest.Wait()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				s.Run(ctx)
				if err := <-result; !errors.Is(err, ErrSessionStopped) {
					t.Errorf("owner exit error = %v, want stopped", err)
				}
				queued := len(s.ctrlCh)
				if err := s.SetAdminDown(t.Context()); !errors.Is(err, ErrSessionStopped) {
					t.Errorf("terminal request error = %v, want stopped", err)
				}
				if len(s.ctrlCh) != queued || s.State() != StateDown || sender.attempts != 0 {
					t.Error("cancelled Run processed controls or transmitted")
				}
				if s.running.Load() || !s.updateClosed {
					t.Error("owner exit did not close timer updates and clear running")
				}
			})
		})
	}
}

func TestSessionAdminDownReceiptWinsCancellation(t *testing.T) {
	t.Parallel()
	s := newAdminDownTestSession(t, senderLeaseTestSender{}, 48)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.Run(ctx)
	receipt := make(chan struct{})
	close(receipt)
	if err := s.waitAdminDown(ctx, receipt); err != nil {
		t.Errorf("committed request with cancelled context and stopped owner: %v", err)
	}
	if err := s.SetAdminDown(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled admission error = %v, want cancelled", err)
	}
	if len(s.ctrlCh) != 0 {
		t.Error("cancelled caller admitted new intent")
	}
}

func newAdminDownTestSession(t *testing.T, sender PacketSender, discr uint32) *Session {
	t.Helper()
	s, err := NewSession(ownershipTestConfig("192.0.2.190"), discr, sender, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
