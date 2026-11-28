package bfd

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
)

func TestNotificationWritersAccountEachAttempt(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()
	metrics := &controlDeliveryMetrics{}
	s := newCachedPacketTestSession(MaxPacketSize)
	s.delivery, s.metrics, s.unsolicitedClaimWanted = mgr, metrics, true
	es, err := NewEchoSession(echoLeaseConfig("192.0.2.64"), 42, echoLeaseSender{}, nil,
		mgr.logger, withEchoDelivery(mgr), WithEchoMetrics(metrics))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source     DeliverySource
		emit       func()
		setChannel func(chan StateChange)
	}{
		{
			DeliveryControlNotification, func() { s.emitNotification(FSMResult{}, s.Observation()) },
			func(ch chan StateChange) { s.notifyCh = ch },
		},
		{
			DeliveryUnsolicitedNotification, s.notifyUnsolicitedFailure,
			func(ch chan StateChange) { s.notifyCh = ch },
		},
		{
			DeliveryEchoNotification, func() { es.emitNotification(StateDown, es.watchSnapshot()) },
			func(ch chan StateChange) { es.notifyCh = ch },
		},
	} {
		t.Run(tc.source.String(), func(t *testing.T) {
			ch := make(chan StateChange, 1)
			tc.setChannel(ch)
			tc.emit()
			tc.emit()
			tc.emit()
			tc.setChannel(nil)
			tc.emit()
			if deliveryCount(t, mgr, tc.source, DeliveryQueued) != 1 ||
				deliveryCount(t, mgr, tc.source, DeliveryQueueFull) != 2 || len(ch) != 1 {
				t.Fatal("raw writer did not count each queued/refused original exactly once or counted a nil sink")
			}
		})
	}
	if metrics.dropped != 0 || metrics.auth != 0 || s.counters().PacketsDropped != 0 {
		t.Fatal("notification refusals were counted as packet drops or authentication failures")
	}
}

func TestNotificationFanoutCountsEachGappedOriginal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		s := notificationTestSession(t, mgr)
		synctest.Wait()
		slow, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		fast, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		go mgr.RunDispatch(t.Context())
		const events = notifyChSize + 3
		for range events {
			s.emitNotification(FSMResult{}, s.Observation())
			synctest.Wait()
			if event := <-fast; event.Gap {
				t.Fatal("fast subscriber received a gap")
			}
		}
		if deliveryCount(t, mgr, DeliveryWatchRecipient, DeliveryQueued) != events+notifyChSize-1 ||
			deliveryCount(t, mgr, DeliveryWatchRecipient, DeliveryGapped) != events-(notifyChSize-1) ||
			deliveryCount(t, mgr, DeliveryLegacyNotification, DeliveryQueued) != notifyChSize ||
			deliveryCount(t, mgr, DeliveryLegacyNotification, DeliveryQueueFull) != events-notifyChSize {
			t.Fatal("fanout counts did not preserve recipient-specific original event outcomes")
		}
		for range notifyChSize - 1 {
			if (<-slow).Gap {
				t.Fatal("GAP marker replaced an admitted original")
			}
		}
		if !(<-slow).Gap {
			t.Fatal("overflow did not queue its single GAP marker")
		}
		s.emitNotification(FSMResult{}, s.Observation())
		synctest.Wait()
		if (<-slow).Gap || (<-fast).Gap ||
			deliveryCount(t, mgr, DeliveryWatchRecipient, DeliveryQueued) != events+notifyChSize+1 ||
			deliveryCount(t, mgr, DeliveryWatchRecipient, DeliveryGapped) != events-(notifyChSize-1) {
			t.Fatal("fanout did not resume without counting the GAP marker as original success")
		}
		before := deliveryCount(t, mgr, DeliveryLegacyNotification, DeliveryQueueFull)
		mgr.reportFanoutDelivery(mgr.broadcastStateChange(StateChange{Added: true}))
		if deliveryCount(t, mgr, DeliveryLegacyNotification, DeliveryQueueFull) != before {
			t.Fatal("skipped legacy Added was counted as an attempt")
		}
		mgr.closeLegacyStateChanges()
		mgr.reportFanoutDelivery(mgr.broadcastStateChange(StateChange{}))
		if deliveryCount(t, mgr, DeliveryLegacyNotification, DeliveryQueueFull) != before {
			t.Fatal("closed legacy sink or skipped Added was counted as an attempt")
		}
	})
}

func TestDeliveryOutcomeMaskCapacity(t *testing.T) {
	if deliveryOutcomeCount > 32 {
		t.Fatal("delivery outcomes exceed the fixed uint32 mask capacity")
	}
	for _, pair := range DeliveryCounterPairs() {
		if !validDeliveryPair(pair.Source, pair.Outcome) {
			t.Fatalf("declared delivery pair is not representable: %+v", pair)
		}
	}
}

func TestNotificationAbandonmentWaitsForProducerJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		sender := &lifecycleBlockingSender{entered: make(chan struct{}), release: make(chan struct{})}
		var once sync.Once
		release := func() { once.Do(func() { close(sender.release) }) }
		defer release()
		s, err := mgr.CreateSession(t.Context(), senderLeaseTestConfig("192.0.2.65"), NonOwningSenderLeaseFactory(sender))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetAdminDown(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-sender.entered
		queued := len(mgr.rawNotifyCh)
		if queued == 0 {
			t.Fatal("owner published no raw notification before its blocked send")
		}
		done := make(chan struct{})
		go func() { mgr.Close(); close(done) }()
		synctest.Wait()
		if len(mgr.rawNotifyCh) != queued || deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryAbandoned) != 0 {
			t.Fatal("raw notifications were abandoned before the producer joined")
		}
		release()
		<-done
		mgr.Close()
		if len(mgr.rawNotifyCh) != 0 ||
			deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryAbandoned) != uint64(queued) ||
			s.counters().PacketsDropped != 0 {
			t.Fatal("joined shutdown did not abandon each raw notification once without packet drops")
		}
	})
}

func TestEchoDelayedNotificationRejectsReusedDiscriminator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := echoLeaseConfig("192.0.2.66")
		discr, err := mgr.CreateEchoSession(t.Context(), cfg, echoLeaseSender{})
		if err != nil {
			t.Fatal(err)
		}
		mgr.mu.RLock()
		original := mgr.echoSessions[discr].session
		mgr.mu.RUnlock()
		original.emitNotification(StateDown, original.watchSnapshot())
		delayed := <-mgr.rawNotifyCh
		if closeErr := mgr.DestroyEchoSession(discr); closeErr != nil {
			t.Fatal(closeErr)
		}
		if deleted := <-mgr.publicNotifyCh; !deleted.Deleted || deleted.Ref != original.Ref() {
			t.Fatalf("Echo removal = %+v, want original owner's tombstone", deleted)
		}
		replacement, err := NewEchoSession(cfg, discr, echoLeaseSender{}, mgr.rawNotifyCh, mgr.logger, withEchoDelivery(mgr))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
		entry := &echoSessionEntry{
			session: replacement, cancel: cancel,
			senderLease: NewSenderLease(echoLeaseSender{}, nil), done: make(chan struct{}),
		}
		mgr.mu.Lock()
		mgr.echoSessions[discr] = entry
		mgr.mu.Unlock()
		mgr.workers.Go(func() { defer close(entry.done); replacement.Run(ctx) })
		mgr.dispatchStateChange(t.Context(), delayed)
		if len(mgr.publicNotifyCh) != 0 || deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryStale) != 1 ||
			deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryAccepted) != 0 {
			t.Fatal("old Echo owner notification reached fanout after discriminator reuse")
		}
	})
}
