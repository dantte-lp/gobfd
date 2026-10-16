package bfd

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

type adminDrainSender struct {
	attempts, successful chan time.Time
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
	return nil
}

func TestManagerDrainWaitsAfterSuccessfulRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64), fail: true}
		s := newAdminDownTestSession(t, sender, 61)
		mgr := NewManager(s.logger)
		mgr.sessions[61] = &sessionEntry{session: s}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		drainCtx, cancelDrain := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancelDrain()
		result := make(chan error, 1)
		go func() { result <- mgr.DrainAllSessions(drainCtx) }()
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
		if err := <-result; err != nil {
			t.Error(err)
		}
		if time.Since(first) < 3*time.Second {
			t.Error("notification window shorter than DT")
		}
		cancel()
		<-done
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
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan struct{})
				go func() { s.Run(ctx); close(done) }()
				drainCtx, stop := context.WithTimeout(t.Context(), 15*time.Second)
				defer stop()
				result := make(chan error, 1)
				go func() { result <- mgr.DrainAllSessions(drainCtx) }()
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
				s.recvCh <- recvItem{pkt: &pkt}
				synctest.Wait()
				synctest.Sleep(time.Second)
				if len(result) != 0 {
					t.Error("drain completed at superseded deadline")
				}
				pkt2 := pkt
				pkt2.DesiredMinTxInterval = 100000
				pkt2.RequiredMinRxInterval = 100000
				pkt2.DetectMult = 1
				s.recvCh <- recvItem{pkt: &pkt2}
				synctest.Wait()
				synctest.Sleep(window - time.Second - time.Nanosecond)
				if len(result) != 0 {
					t.Error("lower negotiation shortened drain window")
				}
				s.recvCh <- recvItem{pkt: &pkt2}
				synctest.Wait()
				synctest.Sleep(time.Nanosecond)
				if err := <-result; err != nil {
					t.Error(err)
				}
				cancel()
				<-done
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
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		if err := s.SetAdminDown(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-sender.successful
		results := make(chan error, 2)
		for range 2 {
			go func() { results <- mgr.DrainAllSessions(t.Context()) }()
		}
		synctest.Wait()
		if len(sender.successful) == 0 {
			t.Error("already AdminDown drain did not attempt confirmation write")
		}
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if len(results) != 0 {
			t.Error("concurrent drain completed early")
		}
		synctest.Sleep(time.Nanosecond)
		for range 2 {
			if err := <-results; err != nil {
				t.Error(err)
			}
		}
		canceled, stop := context.WithCancel(t.Context())
		stop()
		if err := mgr.DrainAllSessions(canceled); err != nil {
			t.Errorf("completed drain lost precedence: %v", err)
		}
		cancel()
		<-done
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
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan struct{})
				switch kind {
				case "admission":
					for range cap(s.ctrlCh) {
						s.SetPathDown()
					}
				case "commit":
				case "passive":
					s.role = RolePassive
				case "remote-zero":
					s.remoteMinRxInterval = 0
				case "stopped":
					cancel()
					s.Run(ctx)
				}
				running := kind == "passive" || kind == "remote-zero" || kind == "window"
				if running {
					go func() { s.Run(ctx); close(done) }()
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
				cancel()
				if running {
					<-done
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
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		budget, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		result := make(chan error, 1)
		go func() { result <- mgr.DrainAllSessions(budget) }()
		synctest.Sleep(time.Second / 2)
		if len(sender.attempts) != 0 {
			t.Error("unknown passive drain attempted write")
		}
		pkt := ControlPacket{
			Version: 1, State: StateDown, Poll: true, DetectMult: 3,
			MyDiscriminator: 7, DesiredMinTxInterval: 1000000,
		}
		s.recvCh <- recvItem{pkt: &pkt}
		<-sender.successful
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if len(result) != 0 {
			t.Error("Final confirmation drain ended early")
		}
		synctest.Sleep(time.Nanosecond)
		if err := <-result; err != nil {
			t.Error(err)
		}
		if len(sender.attempts) != 1 {
			t.Errorf("zero RX allowed periodic write: attempts=%d", len(sender.attempts))
		}
		cancel()
		<-done
	})
}

func TestManagerDrainStoppedWindowCannotBecomeComplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &adminDrainSender{attempts: make(chan time.Time, 64), successful: make(chan time.Time, 64)}
		s := newAdminDownTestSession(t, sender, 67)
		mgr := NewManager(s.logger)
		mgr.sessions[67] = &sessionEntry{session: s}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		result := make(chan error, 1)
		go func() { result <- mgr.DrainAllSessions(t.Context()) }()
		<-sender.successful
		synctest.Sleep(time.Second)
		cancel()
		<-done
		if err := <-result; !errors.Is(err, ErrSessionStopped) {
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
				s := newAdminDownTestSession(t, sender, 68)
				mgr := NewManager(s.logger)
				mgr.sessions[68] = &sessionEntry{session: s}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() { s.Run(ctx); close(done) }()
				first := make(chan error, 1)
				go func() { first <- mgr.DrainAllSessions(t.Context()) }()
				<-sender.blocked
				if full {
					for range cap(s.ctrlCh) {
						s.SetPathDown()
					}
				}
				budget, stop := context.WithTimeout(t.Context(), 4*time.Second)
				defer stop()
				second := make(chan error, 1)
				go func() { second <- mgr.DrainAllSessions(budget) }()
				if err := <-first; err != nil {
					t.Error(err)
				}
				if err := <-second; err != nil {
					t.Errorf("shared completed window lost to queued request deadline: %v", err)
				}
				cancel()
				close(sender.release)
				<-done
			})
		})
	}
}
