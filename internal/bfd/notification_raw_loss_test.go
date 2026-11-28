package bfd

import (
	"testing"
	"testing/synctest"
)

func TestControlRawNotificationLossQueuesRecoveryGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		s := notificationTestSession(t, mgr)
		sub, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		pkt := s.buildControlPacket()
		pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = StateInit, 99, s.LocalDiscriminator()
		if s.RecvPacket(&pkt) != DeliveryQueued {
			t.Fatal("initial Control receive was refused")
		}
		synctest.Wait()
		up := dispatchRawLossInitialUp(t, mgr, sub)
		before, revision := mgr.SessionsWithRevision()
		if len(before) != 1 || before[0].State != StateUp ||
			before[0].ObservationVersion != up.ObservationVersion {
			t.Fatalf("initial consumer snapshot is not committed Up: %+v", before)
		}
		fillRawLossQueue(t, mgr, sub)
		if s.SetPathDown() != DeliveryQueued {
			t.Fatal("final PathDown command was refused")
		}
		synctest.Wait()
		after, _ := mgr.SessionsWithRevision()
		if len(after) != 1 || after[0].State != StateDown || after[0].LocalDiag != DiagPathDown ||
			after[0].ObservationVersion <= up.ObservationVersion ||
			deliveryCount(t, mgr, DeliveryControlNotification, DeliveryQueueFull) != 1 {
			t.Fatalf("final Control owner commit was not lost at the full raw queue: %+v", after)
		}
		go mgr.RunDispatch(t.Context())
		synctest.Wait()
		assertRawLossRecoveryGap(t, mgr, sub, revision)
		latest, covered := mgr.SessionsWithRevision()
		if len(latest) != 1 || latest[0].State != StateDown || latest[0].LocalDiag != DiagPathDown ||
			latest[0].ObservationVersion != after[0].ObservationVersion || covered <= revision {
			t.Fatalf("recovery snapshot did not converge without another owner edge: %+v at %d", latest, covered)
		}
	})
}

func TestEchoRawNotificationLossQueuesRecoveryGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		cfg := echoLeaseConfig("192.0.2.81")
		discr, err := mgr.CreateEchoSession(t.Context(), cfg, echoLeaseSender{})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if outcome, recvErr := mgr.DemuxEcho(discr); outcome != DeliveryQueued || recvErr != nil {
			t.Fatalf("initial Echo receive = %s, %v", outcome, recvErr)
		}
		synctest.Wait()
		up := dispatchRawLossInitialUp(t, mgr, sub)
		if before := mgr.EchoSessions(); len(before) != 1 || before[0].State != StateUp || up.Type != SessionTypeEcho {
			t.Fatalf("initial Echo consumer snapshot is not committed Up: %+v", before)
		}
		revision := mgr.Revision()
		fillRawLossQueue(t, mgr, sub)
		synctest.Sleep(cfg.TxInterval * 3)
		if after := mgr.EchoSessions(); len(after) != 1 || after[0].State != StateDown ||
			after[0].LocalDiag != DiagEchoFailed || after[0].StateTransitions != 2 ||
			deliveryCount(t, mgr, DeliveryEchoNotification, DeliveryQueueFull) != 1 {
			t.Fatalf("final Echo timeout commit was not lost at the full raw queue: %+v", after)
		}
		go mgr.RunDispatch(t.Context())
		synctest.Wait()
		assertRawLossRecoveryGap(t, mgr, sub, revision)
		if latest := mgr.EchoSessions(); len(latest) != 1 || latest[0].State != StateDown ||
			latest[0].LocalDiag != DiagEchoFailed || latest[0].StateTransitions != 2 {
			t.Fatalf("Echo recovery snapshot did not converge without another owner edge: %+v", latest)
		}
	})
}

func dispatchRawLossInitialUp(t *testing.T, mgr *Manager, sub <-chan StateChange) StateChange {
	t.Helper()
	// Dispatch the actual committed Up before starting RunDispatch: this exercises startup recovery.
	select {
	case raw := <-mgr.rawNotifyCh:
		mgr.dispatchStateChange(t.Context(), raw)
	default:
		t.Fatal("owner published no initial raw Up")
	}
	select {
	case up := <-sub:
		if up.Gap || up.NewState != StateUp {
			t.Fatalf("initial consumer event = %+v, want Up", up)
		}
		return up
	default:
		t.Fatal("initial actual owner Up did not reach the consumer")
		return StateChange{}
	}
}

func fillRawLossQueue(t *testing.T, mgr *Manager, sub <-chan StateChange) {
	t.Helper()
	if len(mgr.rawNotifyCh) != 0 || len(sub) != 0 {
		t.Fatal("initial owner event or subscriber queue was not drained")
	}
	// Unknown owners are stale on dispatch and cannot overflow the subscriber.
	for range cap(mgr.rawNotifyCh) {
		mgr.rawNotifyCh <- StateChange{}
	}
}

func assertRawLossRecoveryGap(t *testing.T, mgr *Manager, sub <-chan StateChange, revision uint64) {
	t.Helper()
	select {
	case gap := <-sub:
		if !gap.Gap || gap.Revision <= revision {
			t.Fatalf("raw loss recovery event = %+v, want GAP above %d", gap, revision)
		}
	default:
		t.Fatal("committed final owner state was lost without a recovery GAP for the empty subscriber")
	}
	if len(sub) != 0 || len(mgr.rawNotifyCh) != 0 ||
		deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryStale) != uint64(cap(mgr.rawNotifyCh)) ||
		deliveryCount(t, mgr, DeliveryWatchRecipient, DeliveryGapped) != 0 {
		t.Fatal("recovery did not drain stale raw work independently of subscriber overflow")
	}
}
