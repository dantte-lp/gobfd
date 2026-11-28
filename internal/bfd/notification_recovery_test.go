package bfd

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

func TestRawLossRecoveryCoalescesAndRearmsConsumedGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		sub, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for range 100 {
			signalPublicNotificationLoss(mgr.publicLossWake)
		}
		if len(mgr.publicLossWake) != 1 {
			t.Fatal("pre-dispatch loss hints did not coalesce")
		}
		go mgr.RunDispatch(t.Context())
		synctest.Wait()
		first := takeRecoveryGap(t, sub)
		// The consumer drained its GAP, but no original has reset overflowed yet.
		signalPublicNotificationLoss(mgr.publicLossWake)
		synctest.Wait()
		second := takeRecoveryGap(t, sub)
		if second.Revision <= first.Revision || len(sub) != 0 {
			t.Fatal("a consumed GAP suppressed the later loss or coalesced hints made extra markers")
		}
		for _, row := range mgr.DeliveryCounters() {
			if row.Count != 0 {
				t.Fatalf("recovery marker counted as an original: %+v", row)
			}
		}
		if len(mgr.StateChanges()) != 0 {
			t.Fatal("recovery marker reached the legacy sink")
		}
	})
}

func TestRawLossRecoveryFullSubscriberRetainsUnreadGap(t *testing.T) {
	for _, originalGap := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovery-gap", true: "subscriber-gap"}[originalGap], func(t *testing.T) {
			mgr := newEchoLeaseManager()
			defer mgr.Close()
			sub, err := mgr.SubscribeStateChanges(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for range notifyChSize - 1 {
				mgr.broadcastStateChange(StateChange{})
			}
			if originalGap {
				mgr.broadcastStateChange(StateChange{Timestamp: time.Now()})
			} else {
				mgr.invalidateStateChanges()
			}
			mgr.subMu.RLock()
			for _, state := range mgr.subscribers {
				if !state.overflowed {
					t.Error("last-slot GAP did not protect normal fanout from a second send")
				}
			}
			mgr.subMu.RUnlock()
			// Normal fanout on the actually full channel must not enqueue another GAP.
			mgr.broadcastStateChange(StateChange{})
			before := mgr.Revision()
			mgr.invalidateStateChanges()
			if len(sub) != notifyChSize || mgr.Revision() != before+1 {
				t.Fatal("full recovery drained admitted originals or omitted its snapshot boundary")
			}
			for range notifyChSize - 1 {
				if (<-sub).Gap {
					t.Fatal("an admitted original was replaced")
				}
			}
			gap := takeRecoveryGap(t, sub)
			_, covered := mgr.SessionsWithRevision()
			if covered <= gap.Revision || covered != before+1 {
				t.Fatal("unread GAP's later snapshot does not cover the already committed loss")
			}
		})
	}
}

func TestRawLossDuringRecoveryKeepsNextWake(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cfg := senderLeaseTestConfig("192.0.2.83")
	cfg.DesiredMinTxInterval, cfg.RequiredMinRxInterval = time.Hour, time.Hour
	s, err := mgr.CreateSession(ctx, cfg, ownershipSenderLeaseFactory())
	if err != nil {
		t.Fatal(err)
	}
	sub, err := mgr.SubscribeStateChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for range cap(mgr.rawNotifyCh) {
		mgr.rawNotifyCh <- StateChange{}
	}
	pkt := s.buildControlPacket()
	pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = StateInit, 99, s.LocalDiscriminator()
	if s.RecvPacket(&pkt) != DeliveryQueued {
		t.Fatal("initial owner receive refused")
	}
	waitRawLossCondition(ctx, t, func() bool {
		return deliveryCount(t, mgr, DeliveryControlNotification, DeliveryQueueFull) == 1
	})
	func() {
		// Mutex waits are not synctest-durable: use real context-bounded channel-state barriers.
		mgr.subMu.Lock()
		defer mgr.subMu.Unlock()
		go mgr.RunDispatch(ctx)
		waitRawLossCondition(ctx, t, func() bool { return len(mgr.publicLossWake) == 0 })
		if s.SetPathDown() != DeliveryQueued {
			t.Fatal("concurrent owner PathDown refused")
		}
		waitRawLossCondition(ctx, t, func() bool {
			return deliveryCount(t, mgr, DeliveryControlNotification, DeliveryQueueFull) == 2
		})
		if len(mgr.publicLossWake) != 1 {
			t.Fatal("later committed loss was cleared while recovery waited for subMu")
		}
	}()
	waitRawLossCondition(ctx, t, func() bool { return len(sub) == 2 })
	first, second := takeRecoveryGap(t, sub), takeRecoveryGap(t, sub)
	if second.Revision <= first.Revision || s.State() != StateDown {
		t.Fatal("running dispatch did not preserve the later loss boundary")
	}
}

func TestRawLossCountersAndPrivateFailuresDoNotInvalidate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		s, err := mgr.CreateSession(t.Context(), senderLeaseTestConfig("192.0.2.84"), ownershipSenderLeaseFactory())
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		sub, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []DeliverySource{
			DeliveryControlNotification, DeliveryEchoNotification, DeliveryUnsolicitedNotification,
		} {
			mgr.RecordDelivery(source, DeliveryQueueFull)
		}
		for range cap(mgr.rawNotifyCh) {
			mgr.rawNotifyCh <- StateChange{}
		}
		s.mu.Lock()
		s.unsolicitedClaimWanted = true
		s.mu.Unlock()
		s.notifyUnsolicitedFailure()
		if len(mgr.cleanupWake) != 1 || len(mgr.publicLossWake) != 0 {
			t.Fatal("private failure or direct counter write acquired public recovery side effects")
		}
		go mgr.RunDispatch(t.Context())
		synctest.Wait()
		if len(sub) != 0 || len(mgr.StateChanges()) != 0 {
			t.Fatal("private cleanup or accounting emitted a synthetic public event")
		}
	})
}

func TestRawLossWakeAfterCancelAndCloseIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		signalPublicNotificationLoss(mgr.publicLossWake)
		mgr.RunDispatch(ctx)
		mgr.RunDispatch(t.Context())
		mgr.Close()
		mgr.Close()
		for range 100 {
			signalPublicNotificationLoss(mgr.publicLossWake)
		}
		if len(mgr.publicLossWake) != 1 {
			t.Fatal("late loss hints blocked or exceeded the bounded mailbox")
		}
	})
}

type rawLossOrderMetrics struct {
	noopMetrics

	t    *testing.T
	wake <-chan struct{}
}

func (m rawLossOrderMetrics) RecordDelivery(source DeliverySource, outcome DeliveryOutcome) {
	if outcome == DeliveryQueueFull &&
		(source == DeliveryControlNotification || source == DeliveryEchoNotification) && len(m.wake) != 1 {
		m.t.Error("public loss hint was not visible before the external delivery callback")
	}
}

func TestRawLossWakePrecedesExternalCallback(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()
	mgr.metrics = rawLossOrderMetrics{t: t, wake: mgr.publicLossWake}
	s := newCachedPacketTestSession(MaxPacketSize)
	withManagerDelivery(mgr)(s)
	s.notifyCh = make(chan StateChange)
	s.emitNotification(FSMResult{}, s.Observation())
	<-mgr.publicLossWake
	es, err := NewEchoSession(echoLeaseConfig("192.0.2.85"), 42, echoLeaseSender{}, make(chan StateChange),
		slog.New(slog.DiscardHandler), withEchoDelivery(mgr))
	if err != nil {
		t.Fatal(err)
	}
	es.emitNotification(StateDown, es.watchSnapshot())
	if len(mgr.publicLossWake) != 1 {
		t.Fatal("Echo owner did not publish its loss hint")
	}
}

func takeRecoveryGap(t *testing.T, sub <-chan StateChange) StateChange {
	t.Helper()
	select {
	case gap := <-sub:
		if !gap.Gap || gap.Revision == 0 || gap.Timestamp.IsZero() {
			t.Fatalf("recovery event = %+v, want revisioned timestamped GAP", gap)
		}
		return gap
	default:
		t.Fatal("missing recovery GAP")
		return StateChange{}
	}
}

func waitRawLossCondition(ctx context.Context, t *testing.T, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("raw loss barrier: ", ctx.Err())
		case <-ticker.C:
		}
	}
}
