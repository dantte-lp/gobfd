package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func startAdminDownCall(parent context.Context, t *testing.T, s *Session) (<-chan error, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	raw := make(chan error, 1)
	ready := make(chan struct{})
	var err error
	go func() {
		err = s.SetAdminDown(ctx)
		raw <- err
		close(ready)
	}()
	var join sync.Once
	wait := func() error {
		join.Do(func() {
			<-ready
		})
		return err
	}
	t.Cleanup(func() {
		cancel()
		_ = wait()
	})
	return raw, wait
}

func TestSessionAdminDownBeforeRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &lifecycleBlockingSender{entered: make(chan struct{}), release: make(chan struct{})}
		var releaseOnce sync.Once
		releaseSender := func() { releaseOnce.Do(func() { close(sender.release) }) }
		s := newAdminDownTestSession(t, sender, 41)
		requestCtx, cancelRequest := context.WithTimeout(t.Context(), time.Minute)
		defer cancelRequest()
		result, waitAdmin := startAdminDownCall(requestCtx, t, s)
		synctest.Wait()
		if s.State() != StateDown || s.LocalDiag() != DiagNone {
			t.Error("request changed state/diagnostic without an owner")
		}
		if len(result) != 0 {
			t.Error("request completed before owner commit")
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.Run(ctx)
		}()
		var stopOnce sync.Once
		stopSession := func() {
			stopOnce.Do(func() {
				cancel()
				releaseSender()
				<-done
			})
		}
		t.Cleanup(stopSession)

		<-sender.entered
		synctest.Wait()
		if len(result) != 1 {
			t.Error("commit acknowledgement blocked on SendPacket")
		} else if err := waitAdmin(); err != nil {
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
		stopSession()
	})
}

func TestSessionAdminDownQueueDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 42)
		for range cap(s.ctrlCh) {
			if s.SetPathDown() != DeliveryQueued {
				t.Fatal("fill control queue")
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		result, waitAdmin := startAdminDownCall(ctx, t, s)
		synctest.Wait()
		if len(result) != 0 || s.State() != StateDown || s.LocalDiag() != DiagNone {
			t.Error("full queue caused early completion or outside-owner mutation")
		}
		synctest.Sleep(time.Second)
		if err := waitAdmin(); !errors.Is(err, context.DeadlineExceeded) {
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
		stopFirst := startSessionWorker(t, s)
		synctest.Wait()
		secondCtx, cancelSecond := context.WithCancel(t.Context())
		secondDone := make(chan struct{})
		go func() {
			defer close(secondDone)
			s.Run(secondCtx)
		}()
		t.Cleanup(func() {
			cancelSecond()
			<-secondDone
		})
		synctest.Wait()
		select {
		case <-secondDone:
		default:
			t.Error("concurrent Run became another owner")
		}
		cancelSecond()
		<-secondDone
		stopFirst()
		restartCtx, cancelRestart := context.WithCancel(t.Context())
		restartDone := make(chan struct{})
		go func() {
			defer close(restartDone)
			s.Run(restartCtx)
		}()
		t.Cleanup(func() {
			cancelRestart()
			<-restartDone
		})
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
		{name: "passive unknown peer pending final", role: RolePassive, pendingFinal: true},
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
				_, waitAdmin := startAdminDownCall(t.Context(), t, s)
				synctest.Wait()
				stopSession := startSessionWorker(t, s)
				synctest.Wait()
				if err := waitAdmin(); err != nil {
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
				stopSession()
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
		stopSession := startSessionWorker(t, s)
		synctest.Wait()
		if s.State() != StateAdminDown || s.LocalDiag() != DiagAdminDown {
			t.Error("admitted intent was rolled back after caller timeout")
		}
		stopSession()
	})
}

func TestSessionAdminDownRepeated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &retryCachedPacketSender{}
		s := newAdminDownTestSession(t, sender, 46)
		stopSession := startSessionWorker(t, s)
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
		stopSession()
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
						if s.SetPathDown() != DeliveryQueued {
							t.Fatal("fill control queue")
						}
					}
				}
				_, waitAdmin := startAdminDownCall(t.Context(), t, s)
				synctest.Wait()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				s.Run(ctx)
				if err := waitAdmin(); !errors.Is(err, ErrSessionStopped) {
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
