package bfd

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type adminDrainSender struct {
	attempts, successful chan time.Time
	packets              chan []byte
	fail                 bool
}

func (s *adminDrainSender) SendPacket(_ context.Context, buf []byte, _ netip.Addr) error {
	if State(buf[1]>>6) != StateAdminDown {
		return nil
	}
	s.attempts <- time.Now()
	if s.fail {
		s.fail = false
		return errors.New("injected AdminDown write failure")
	}
	s.successful <- time.Now()
	if s.packets != nil {
		s.packets <- append([]byte(nil), buf...)
	}
	return nil
}

func startSessionWorker(t *testing.T, s *Session) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

func startDrainWorker(parent context.Context, t *testing.T, mgr *Manager) (<-chan error, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	raw := make(chan error, 1)
	ready := make(chan struct{})
	var err error
	go func() {
		err = mgr.DrainAllSessions(ctx)
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

func TestManagerDrainWaitsAfterSuccessfulRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64), fail: true}
		s := newAdminDownTestSession(t, sender, 61)
		mgr := NewManager(s.logger)
		mgr.sessions[61] = &sessionEntry{session: s}
		stopSession := startSessionWorker(t, s)
		drainCtx, cancelDrain := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancelDrain()
		result, waitDrain := startDrainWorker(drainCtx, t, mgr)
		synctest.Wait()
		if len(result) != 0 {
			t.Error("drain completed after commit without successful write and DT window")
		}
		first := <-sender.successful
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if len(result) != 0 {
			t.Error("drain ended before three-second AdminDown floor from first successful write")
		}
		synctest.Sleep(time.Nanosecond)
		if err := waitDrain(); err != nil {
			t.Error(err)
		}
		if time.Since(first) < 3*time.Second {
			t.Error("notification window shorter than DT")
		}
		stopSession()
	})
}

func TestManagerDrainBoundOnlyExtends(t *testing.T) {
	for _, localRX := range []bool{false, true} {
		t.Run(map[bool]string{false: "peer-rx", true: "local-rx"}[localRX], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
				s := newAdminDownTestSession(t, sender, 63)
				mgr := NewManager(s.logger)
				mgr.sessions[63] = &sessionEntry{session: s}
				stopSession := startSessionWorker(t, s)
				drainCtx, stop := context.WithTimeout(t.Context(), 15*time.Second)
				defer stop()
				result, waitDrain := startDrainWorker(drainCtx, t, mgr)
				<-sender.successful
				synctest.Sleep(2 * time.Second)
				pkt := ControlPacket{
					Version: 1, State: StateDown, DetectMult: 3, MyDiscriminator: 7,
					DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 2000000,
				}
				window := 6 * time.Second
				if localRX {
					pkt.DesiredMinTxInterval = 2000000
					pkt.RequiredMinRxInterval = 1000000
					pkt.DetectMult = 4
					window = 8 * time.Second
				}
				s.recvCh <- recvItem{pkt: pkt}
				synctest.Wait()
				synctest.Sleep(time.Second)
				if len(result) != 0 {
					t.Error("drain completed at superseded deadline")
				}
				pkt2 := pkt
				pkt2.DesiredMinTxInterval = 100000
				pkt2.RequiredMinRxInterval = 100000
				pkt2.DetectMult = 1
				s.recvCh <- recvItem{pkt: pkt2}
				synctest.Wait()
				synctest.Sleep(window - time.Second - time.Nanosecond)
				if len(result) != 0 {
					t.Error("lower negotiation shortened drain window")
				}
				s.recvCh <- recvItem{pkt: pkt2}
				synctest.Wait()
				synctest.Sleep(time.Nanosecond)
				if err := waitDrain(); err != nil {
					t.Error(err)
				}
				stopSession()
			})
		})
	}
}

func TestManagerDrainConcurrentAlreadyAdminDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
		s := newAdminDownTestSession(t, sender, 64)
		mgr := NewManager(s.logger)
		mgr.sessions[64] = &sessionEntry{session: s}
		stopSession := startSessionWorker(t, s)
		if err := s.SetAdminDown(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-sender.successful
		synctest.Wait() // Finish the initial write before starting a new drain.
		firstRaw, waitFirst := startDrainWorker(t.Context(), t, mgr)
		secondRaw, waitSecond := startDrainWorker(t.Context(), t, mgr)
		synctest.Wait()
		if len(sender.successful) == 0 {
			t.Error("already AdminDown drain did not attempt confirmation write")
		}
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if len(firstRaw) != 0 || len(secondRaw) != 0 {
			t.Error("concurrent drain completed early")
		}
		synctest.Sleep(time.Nanosecond)
		for _, wait := range []func() error{waitFirst, waitSecond} {
			if err := wait(); err != nil {
				t.Error(err)
			}
		}
		canceled, stop := context.WithCancel(t.Context())
		stop()
		if err := mgr.DrainAllSessions(canceled); err != nil {
			t.Errorf("completed drain lost precedence: %v", err)
		}
		stopSession()
	})
}

func TestManagerDrainIncompleteStages(t *testing.T) {
	for _, kind := range []string{"admission", "commit", "passive", "remote-zero", "window", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
				s := newAdminDownTestSession(t, sender, 65)
				mgr := NewManager(s.logger)
				mgr.sessions[65] = &sessionEntry{session: s}
				switch kind {
				case "admission":
					for range cap(s.ctrlCh) {
						s.SetPathDown()
					}
				case "commit":
				case "passive":
					s.role = RolePassive
				case "remote-zero":
					// RFC 5880 Section 6.8.7: "A system MUST NOT periodically transmit BFD
					// Control packets if bfd.RemoteMinRxInterval is zero." The 1s budget
					// ends before the Section 6.8.18 reset after one Detection Time.
					s.remoteMinRxInterval = 0
				case "stopped":
					stoppedCtx, cancelStopped := context.WithCancel(t.Context())
					cancelStopped()
					s.Run(stoppedCtx)
				}
				var stopSession func()
				running := kind == "passive" || kind == "remote-zero" || kind == "window"
				if running {
					stopSession = startSessionWorker(t, s)
				}
				budget, stop := context.WithTimeout(t.Context(), time.Second)
				defer stop()
				err := mgr.DrainAllSessions(budget)
				expected := context.DeadlineExceeded
				if kind == "stopped" {
					expected = ErrSessionStopped
				}
				if !errors.Is(err, expected) {
					t.Errorf("drain error=%v, want %v", err, expected)
				}
				if _, ok := errors.AsType[*DrainIncompleteError](err); !ok {
					t.Errorf("drain did not return typed incomplete: %v", err)
				}
				if (kind == "passive" || kind == "remote-zero") && len(sender.attempts) != 0 {
					t.Error("drain bypassed RFC transmission suppression")
				}
				if running {
					stopSession()
				}
			})
		})
	}
}

func TestManagerDrainKnownPassiveFinalWithZeroRX(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
		s := newAdminDownTestSession(t, sender, 66)
		s.role = RolePassive
		mgr := NewManager(s.logger)
		mgr.sessions[66] = &sessionEntry{session: s}
		stopSession := startSessionWorker(t, s)
		budget, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		result, waitDrain := startDrainWorker(budget, t, mgr)
		synctest.Sleep(time.Second / 2)
		if len(sender.attempts) != 0 {
			t.Error("unknown passive drain attempted write")
		}
		pkt := ControlPacket{
			Version: 1, State: StateDown, Poll: true, DetectMult: 3,
			MyDiscriminator: 7, DesiredMinTxInterval: 1000000,
		}
		s.recvCh <- recvItem{pkt: pkt}
		<-sender.successful
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if len(result) != 0 {
			t.Error("Final confirmation drain ended early")
		}
		synctest.Sleep(time.Nanosecond)
		if err := waitDrain(); err != nil {
			t.Error(err)
		}
		if len(sender.attempts) != 1 {
			t.Errorf("zero RX allowed periodic write: attempts=%d", len(sender.attempts))
		}
		stopSession()
	})
}

func TestManagerDrainStoppedWindowCannotBecomeComplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
		s := newAdminDownTestSession(t, sender, 67)
		mgr := NewManager(s.logger)
		mgr.sessions[67] = &sessionEntry{session: s}
		stopSession := startSessionWorker(t, s)
		_, waitDrain := startDrainWorker(t.Context(), t, mgr)
		<-sender.successful
		synctest.Sleep(time.Second)
		stopSession()
		if err := waitDrain(); !errors.Is(err, ErrSessionStopped) {
			t.Errorf("early owner exit=%v", err)
		}
		synctest.Sleep(3 * time.Second)
		if err := mgr.DrainAllSessions(t.Context()); !errors.Is(err, ErrSessionStopped) {
			t.Errorf("elapsed wall time converted stopped drain into success: %v", err)
		}
	})
}

type laterBlockedAdminSender struct {
	writes  int
	blocked chan struct{}
	release chan struct{}
}

func (s *laterBlockedAdminSender) SendPacket(_ context.Context, buf []byte, _ netip.Addr) error {
	if State(buf[1]>>6) != StateAdminDown {
		return nil
	}
	s.writes++
	if s.writes > 1 {
		select {
		case s.blocked <- struct{}{}:
		default:
		}
		<-s.release
	}
	return nil
}

func TestManagerDrainSharedCompletionWinsQueuedRequestDeadline(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "admission"}[full], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sender := &laterBlockedAdminSender{blocked: make(chan struct{}, 1), release: make(chan struct{})}
				var unblockSender sync.Once
				releaseSender := func() { unblockSender.Do(func() { close(sender.release) }) }
				s := newAdminDownTestSession(t, sender, 68)
				mgr := NewManager(s.logger)
				mgr.sessions[68] = &sessionEntry{session: s}
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

				_, waitFirst := startDrainWorker(t.Context(), t, mgr)
				<-sender.blocked
				if full {
					for range cap(s.ctrlCh) {
						s.SetPathDown()
					}
				}
				budget, stop := context.WithTimeout(t.Context(), 4*time.Second)
				defer stop()
				_, waitSecond := startDrainWorker(budget, t, mgr)
				if err := waitFirst(); err != nil {
					t.Error(err)
				}
				if err := waitSecond(); err != nil {
					t.Errorf("shared completed window lost to queued request deadline: %v", err)
				}
				stopSession()
			})
		})
	}
}

func TestDrainIncompletePeerAttributed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stage string
		cause string
		err   error
		want  bool
	}{
		{"unknown passive peer", "transmission", drainReasonPassivePeerUnknown, context.DeadlineExceeded, true},
		{"suppressing peer", "notification window", drainReasonPeerSuppressesTX, context.DeadlineExceeded, true},
		{"local owner", "transmission", "owner or caller stopped", context.DeadlineExceeded, false},
		{"commit timeout", "commit", drainReasonPassivePeerUnknown, context.DeadlineExceeded, false},
		{"admission", "admission", drainReasonPeerSuppressesTX, context.DeadlineExceeded, false},
		{"stopped owner", "transmission", drainReasonPassivePeerUnknown, ErrSessionStopped, false},
		{"canceled", "transmission", drainReasonPassivePeerUnknown, context.Canceled, false},
	}
	for _, tt := range tests {
		err := &DrainIncompleteError{Stage: tt.stage, Reason: tt.cause, Err: tt.err}
		if got := err.PeerAttributed(); got != tt.want {
			t.Errorf("%s: PeerAttributed() = %v, want %v", tt.name, got, tt.want)
		}
	}
	writeFailed := &DrainIncompleteError{
		Stage: "transmission", Reason: drainReasonPassivePeerUnknown,
		Err: context.DeadlineExceeded, SendErr: errors.New("sendmsg: operation not permitted"),
	}
	if writeFailed.PeerAttributed() {
		t.Error("a failed AdminDown write was attributed to the peer")
	}
}

// failingDrainSender refuses every AdminDown write, like a sandbox EPERM or a
// closed socket.
type failingDrainSender struct{ attempts chan struct{} }

func (s failingDrainSender) SendPacket(_ context.Context, buf []byte, _ netip.Addr) error {
	if State(buf[1]>>6) != StateAdminDown {
		return nil
	}
	select {
	case s.attempts <- struct{}{}:
	default:
	}
	return errors.New("injected local AdminDown write failure")
}

// A failed AdminDown write stays a local drain failure even when the peer
// also suppresses periodic transmission (RFC 5880 Section 6.8.7).
func TestManagerDrainSendFailureIsNotPeerAttributed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := failingDrainSender{attempts: make(chan struct{}, 1)}
		s := newAdminDownTestSession(t, sender, 68)
		s.role = RolePassive
		mgr := NewManager(s.logger)
		mgr.sessions[68] = &sessionEntry{session: s}
		stopSession := startSessionWorker(t, s)
		budget, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		_, waitDrain := startDrainWorker(budget, t, mgr)
		synctest.Sleep(time.Second / 2)
		s.recvCh <- recvItem{pkt: ControlPacket{
			Version: 1, State: StateDown, Poll: true, DetectMult: 3,
			MyDiscriminator: 7, DesiredMinTxInterval: 1000000,
		}}
		<-sender.attempts
		err := waitDrain()
		incomplete, ok := errors.AsType[*DrainIncompleteError](err)
		if !ok {
			t.Fatalf("drain error = %v, want DrainIncompleteError", err)
		}
		if incomplete.PeerAttributed() {
			t.Errorf("failed AdminDown write was attributed to the peer: %v", err)
		}
		if !strings.Contains(err.Error(), "injected local AdminDown write failure") {
			t.Errorf("drain error hides the send failure: %v", err)
		}
		stopSession()
	})
}
