package bfd

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type ownedBlockingSender struct {
	entered          chan struct{}
	unblock          chan struct{}
	interruptEntered chan struct{}
	interruptReturn  chan struct{}
	interruptOnce    sync.Once
}

func newOwnedBlockingSender() *ownedBlockingSender {
	return &ownedBlockingSender{
		entered: make(chan struct{}, 1), unblock: make(chan struct{}),
		interruptEntered: make(chan struct{}), interruptReturn: make(chan struct{}),
	}
}

func (s *ownedBlockingSender) SendPacket(context.Context, []byte, netip.Addr) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.unblock
	return context.Canceled
}

func (s *ownedBlockingSender) InterruptWrite() error {
	s.interruptOnce.Do(func() { close(s.unblock); close(s.interruptEntered) })
	<-s.interruptReturn
	return nil
}

func TestManagerOwnedWriteInterruptJoinsBeforeOwnerDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := newOwnedBlockingSender()
		released := false
		mgr := NewManager(newAdminDownTestSession(t, sender, 62).logger)
		s, err := mgr.CreateSession(t.Context(), ownershipTestConfig("192.0.2.191"), func() (*SenderLease, error) {
			return NewSenderLease(sender, func() error { released = true; return nil }), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		<-sender.entered
		budget, stop := context.WithTimeout(t.Context(), time.Second)
		defer stop()
		result := make(chan error, 1)
		go func() { result <- mgr.DestroySession(budget, s.LocalDiscriminator()) }()
		synctest.Sleep(time.Second)
		select {
		case <-sender.interruptEntered:
		default:
			t.Error("owned cancellation did not interrupt blocked write")
			sender.interruptOnce.Do(func() { close(sender.unblock); close(sender.interruptEntered) })
		}
		synctest.Wait()
		select {
		case <-s.ownerDone:
			t.Error("ownerDone closed before interrupt callback joined")
		default:
		}
		if released || len(result) != 0 {
			t.Error("sender released before interrupt callback joined")
		}
		close(sender.interruptReturn)
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("blocked drain error=%v, want deadline", err)
		}
		if !released {
			t.Error("owned lease was not released")
		}
		mgr.Close()
	})
}

var errInterruptTest = errors.New("interrupt failure")

func TestManagerEchoOwnedWriteInterruptJoinsBeforeDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := newOwnedBlockingSender()
		released := false
		mgr := newEchoLeaseManager()
		factory := func() (*SenderLease, error) {
			return NewSenderLease(sender, func() error { released = true; return nil }), nil
		}
		discr, err := mgr.CreateEchoSessionWithSenderLease(t.Context(), echoLeaseConfig("192.0.2.192"), factory)
		if err != nil {
			t.Fatal(err)
		}
		entry := mgr.echoSessions[discr]
		<-sender.entered
		result := make(chan error, 1)
		go func() { result <- mgr.DestroyEchoSession(discr) }()
		synctest.Wait()
		select {
		case <-sender.interruptEntered:
		default:
			t.Error("owned Echo cancellation did not interrupt blocked write")
			sender.interruptOnce.Do(func() { close(sender.unblock); close(sender.interruptEntered) })
		}
		synctest.Wait()
		select {
		case <-entry.done:
			t.Error("Echo done closed before interrupt callback joined")
		default:
		}
		if released || len(result) != 0 {
			t.Error("Echo lease released before interrupt callback joined")
		}
		close(sender.interruptReturn)
		if err := <-result; err != nil {
			t.Error(err)
		}
		if !released {
			t.Error("owned Echo lease not released")
		}
		mgr.Close()
	})
}

type errorInterruptSender struct{ calls int }

func (*errorInterruptSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }
func (s *errorInterruptSender) InterruptWrite() error {
	s.calls++
	return errInterruptTest
}

func TestManagerOwnedInterruptErrorPreserved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &errorInterruptSender{}
		releaseErr := errors.New("release failure")
		mgr := newEchoLeaseManager()
		var lease *SenderLease
		s, err := mgr.CreateSession(t.Context(), ownershipTestConfig("192.0.2.193"), func() (*SenderLease, error) {
			lease = NewSenderLease(sender, func() error { return releaseErr })
			return lease, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		err = mgr.DestroySession(t.Context(), s.LocalDiscriminator())
		if !errors.Is(err, errInterruptTest) || !errors.Is(err, releaseErr) {
			t.Errorf("destroy lost owned release errors=%v", err)
		}
		err = lease.Close()
		if !errors.Is(err, errInterruptTest) || !errors.Is(err, releaseErr) {
			t.Errorf("joined owned release errors=%v", err)
		}
		if sender.calls != 1 {
			t.Errorf("interrupt calls=%d, want 1", sender.calls)
		}
		mgr.Close()
	})
}

func TestManagerNonOwningSenderNeverInterrupted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &errorInterruptSender{}
		mgr := newEchoLeaseManager()
		s, err := mgr.CreateSession(t.Context(), ownershipTestConfig("192.0.2.194"), NonOwningSenderLeaseFactory(sender))
		if err != nil {
			t.Fatal(err)
		}
		echo, err := mgr.CreateEchoSession(t.Context(), echoLeaseConfig("192.0.2.195"), sender)
		if err != nil {
			t.Fatal(err)
		}
		if err := mgr.DestroySession(t.Context(), s.LocalDiscriminator()); err != nil {
			t.Error(err)
		}
		if err := mgr.DestroyEchoSession(echo); err != nil {
			t.Error(err)
		}
		if sender.calls != 0 {
			t.Errorf("shared non-owning sender interrupted %d times", sender.calls)
		}
		mgr.Close()
	})
}

func TestManagerCloseInterruptsOwnedControlAndEcho(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		senders := make([]*ownedBlockingSender, 2)
		releases := 0
		for i := range senders {
			sender := newOwnedBlockingSender()
			senders[i] = sender
			factory := func() (*SenderLease, error) {
				return NewSenderLease(sender, func() error { releases++; return nil }), nil
			}
			if i == 0 {
				if _, err := mgr.CreateSession(t.Context(), ownershipTestConfig("192.0.2.196"), factory); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := mgr.CreateEchoSessionWithSenderLease(t.Context(), echoLeaseConfig("192.0.2.197"), factory)
				if err != nil {
					t.Fatal(err)
				}
			}
			<-sender.entered
		}
		done := make(chan struct{})
		go func() { mgr.Close(); close(done) }()
		for _, sender := range senders {
			<-sender.interruptEntered
		}
		synctest.Wait()
		if releases != 0 {
			t.Error("Close released senders before interrupt callbacks joined")
		}
		for _, sender := range senders {
			close(sender.interruptReturn)
		}
		<-done
		if releases != 2 {
			t.Errorf("Close releases=%d, want 2", releases)
		}
		mgr.Close()
	})
}
