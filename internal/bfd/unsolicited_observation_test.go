package bfd

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type unsolicitedObservationSender struct {
	lifecycleBlockingSender

	armed atomic.Bool
}

func (s *unsolicitedObservationSender) SendPacket(ctx context.Context, wire []byte, peer netip.Addr) error {
	if !s.armed.Load() {
		return nil
	}
	return s.lifecycleBlockingSender.SendPacket(ctx, wire, peer)
}

// Call before synctest.Test: the timer and its lazily created Done channel must
// remain outside the bubble. Expose only observation, never an owner context.
func realUnsolicitedWatchdog(t *testing.T, timeout time.Duration) (<-chan struct{}, func() error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	return ctx.Done(), ctx.Err
}

func awaitUnsolicitedObservation(
	done <-chan struct{}, events <-chan StateChange, ref SessionRef, state State,
) (StateChange, bool) {
	for {
		select {
		case event, open := <-events:
			if !open {
				return StateChange{}, false
			}
			if event.Ref == ref && event.NewState == state {
				return event, true
			}
		case <-done:
			return StateChange{}, false
		}
	}
}

func TestUnsolicitedObservationFiniteYieldRejectsQueuedOwner(t *testing.T) {
	done, watchdogErr := realUnsolicitedWatchdog(t, 5*time.Second)
	synctest.Test(t, func(t *testing.T) {
		sender := &unsolicitedObservationSender{
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}
		mgr, sess, pkt := newUnsolicitedTXOwner(t, sender, StateDown)
		defer mgr.Close()
		var release sync.Once
		defer release.Do(func() { close(sender.release) })
		old := unsolicitedPathFailure(t, mgr, sess)
		sender.armed.Store(true)
		pkt.State, pkt.Poll = StateDown, true
		if sess.RecvPacket(pkt) != DeliveryQueued {
			t.Fatal("restart packet was not admitted")
		}
		select {
		case <-sender.entered: // Init is published before the owner sends its Final reply.
		case <-done:
			t.Fatal("owner did not reach Final sender barrier: ", watchdogErr())
		}
		init, ok := awaitUnsolicitedObservation(done, mgr.rawNotifyCh, sess.Ref(), StateInit)
		if !ok || init.unsolicitedAttempt <= old.unsolicitedAttempt || sess.State() != StateInit {
			t.Fatal("sender barrier did not retain a committed new Init attempt")
		}
		if sess.SetPathDown() != DeliveryQueued {
			t.Fatal("blocked owner did not admit PathDown")
		}
		observed := false
		for range 1000 { // Exact historical predicate, with Down as the requested state.
			select {
			case event := <-mgr.rawNotifyCh:
				if event.Ref == sess.Ref() && event.NewState == StateDown {
					observed = true
				}
			default:
				runtime.Gosched()
			}
		}
		if observed || sess.State() != StateInit {
			t.Fatal("finite-yield counterexample lost its blocked owner")
		}
		release.Do(func() { close(sender.release) })
		synctest.Wait()
		failure, ok := awaitUnsolicitedObservation(done, mgr.rawNotifyCh, sess.Ref(), StateDown)
		if !ok || failure.Cause != CausePathDown || failure.unsolicitedAttempt != init.unsolicitedAttempt ||
			failure.ObservationVersion <= init.ObservationVersion || sess.State() != StateDown {
			t.Fatal("admitted PathDown did not commit and publish after sender release")
		}
		t.Log("historical 1000-yield predicate rejected queued PathDown; real owner committed after sender release")
	})
}

func TestUnsolicitedObservationAbsentPublicationUsesExternalDeadline(t *testing.T) {
	done, watchdogErr := realUnsolicitedWatchdog(t, 100*time.Millisecond)
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, _ := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		unsolicitedPathFailure(t, mgr, sess)
		before := time.Now()
		_, ok := awaitUnsolicitedObservation(done, mgr.rawNotifyCh, sess.Ref(), StateInit)
		if ok || !errors.Is(watchdogErr(), context.DeadlineExceeded) || !time.Now().Equal(before) ||
			sess.State() != StateDown {
			t.Fatal("absent publication did not fail at the external deadline with fake time unchanged")
		}
	})
}
