package bfd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestEchoWatchRejectsDelayedOwnersAcrossRegistries(t *testing.T) {
	for _, kinds := range [][2]SessionType{
		{SessionTypeEcho, SessionTypeEcho},
		{SessionTypeEcho, SessionTypeSingleHop},
		{SessionTypeSingleHop, SessionTypeEcho},
	} {
		t.Run(kinds[0].String()+"-to-"+kinds[1].String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := newEchoLeaseManager()
				defer mgr.Close()
				cfg := echoLeaseConfig("192.0.2.73")
				var discr uint32
				var control *Session
				var err error
				if kinds[0] == SessionTypeEcho {
					discr, err = mgr.CreateEchoSession(t.Context(), cfg, echoLeaseSender{})
				} else {
					control, err = mgr.CreateSession(t.Context(), senderLeaseTestConfig("192.0.2.73"), ownershipSenderLeaseFactory())
					if err == nil {
						discr = control.LocalDiscriminator()
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				sub, err := mgr.SubscribeStateChanges(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if control != nil {
					control.SetPathDown()
				} else if _, recvErr := mgr.DemuxEcho(discr); recvErr != nil {
					t.Fatal(recvErr)
				}
				synctest.Wait()
				delayed := <-mgr.rawNotifyCh
				mgr.dispatchStateChange(t.Context(), delayed)
				oldSubscriber := <-sub
				if control != nil {
					err = mgr.DestroySession(t.Context(), discr)
				} else {
					err = mgr.DestroyEchoSession(discr)
				}
				if err != nil {
					t.Fatal(err)
				}
				deleted := <-sub
				staleEvents := []StateChange{delayed}
				for len(mgr.rawNotifyCh) > 0 {
					retired := <-mgr.rawNotifyCh
					if retired.Ref != deleted.Ref {
						t.Fatalf("unexpected raw owner before replacement: %+v", retired)
					}
					staleEvents = append(staleEvents, retired)
				}
				if !deleted.Deleted || deleted.Ref != oldSubscriber.Ref ||
					deleted.ObservationVersion <= oldSubscriber.ObservationVersion {
					t.Fatalf("old owner deletion = %+v, subscriber=%+v", deleted, oldSubscriber)
				}
				// Force allocator reuse through the real registration paths, without relying on random collisions.
				if kinds[1] == SessionTypeEcho {
					es, createErr := NewEchoSession(cfg, discr, echoLeaseSender{}, mgr.rawNotifyCh, mgr.logger, withEchoDelivery(mgr))
					if createErr != nil {
						t.Fatal(createErr)
					}
					mgr.ownershipMu.Lock()
					mgr.registerEchoAndStart(t.Context(), es, NewSenderLease(echoLeaseSender{}, nil),
						canonicalEchoSessionKey(cfg), echoSessionSourceCompatibility)
					mgr.ownershipMu.Unlock()
					if es.RecvEcho() != DeliveryQueued {
						t.Fatal("replacement Echo receive was refused")
					}
				} else {
					ccfg := senderLeaseTestConfig("192.0.2.73")
					lease := NewSenderLease(echoLeaseSender{}, nil)
					replacement, createErr := NewSession(ccfg, discr, lease.Sender(), mgr.rawNotifyCh, mgr.logger)
					if createErr != nil {
						t.Fatal(createErr)
					}
					key, keyErr := sessionKeyFromConfig(ccfg)
					if keyErr != nil {
						t.Fatal(keyErr)
					}
					effective, configErr := normalizeEffectiveSessionConfig(ccfg)
					if configErr != nil {
						t.Fatal(configErr)
					}
					if _, _, registerErr := mgr.registerAndStart(t.Context(), key, ccfg, effective,
						compatibilityAPISessionOwner(), false, discr, replacement, lease); registerErr != nil {
						t.Fatal(registerErr)
					}
					replacement.SetPathDown()
				}
				added := <-sub
				if !added.Added || added.Ref == deleted.Ref || added.ObservationVersion <= deleted.ObservationVersion {
					t.Fatalf("replacement addition = %+v, old deletion=%+v", added, deleted)
				}
				synctest.Wait()
				current := <-mgr.rawNotifyCh
				if current.Ref != added.Ref {
					t.Fatalf("next raw event belongs to a retired owner: %+v", current)
				}
				for _, stale := range staleEvents {
					mgr.dispatchStateChange(t.Context(), stale)
				}
				invalid := current
				if invalid.Ref.echo != nil {
					invalid.Ref.session = new(Session)
				} else {
					invalid.Ref.echo = new(EchoSession)
				}
				mgr.dispatchStateChange(t.Context(), invalid)
				if len(sub) != 0 || mgr.Revision() != added.Revision ||
					deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryStale) != uint64(len(staleEvents)+1) {
					t.Fatal("retired raw owner changed the replacement's subscriber view")
				}
				mgr.dispatchStateChange(t.Context(), current)
				if event := <-sub; event.Ref != added.Ref || event.ObservationVersion <= added.ObservationVersion ||
					event.Status.Ref != event.Ref || event.Revision != added.Revision+1 {
					t.Fatalf("live replacement event = %+v, addition=%+v", event, added)
				}
			})
		})
	}
}

func TestEchoWatchReconcileRemovalAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove", true: "rollback"}[rollback], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := newEchoLeaseManager()
				defer mgr.Close()
				api, err := mgr.CreateEchoSession(t.Context(), echoLeaseConfig("192.0.2.74"), echoLeaseSender{})
				if err != nil {
					t.Fatal(err)
				}
				sub, err := mgr.SubscribeStateChanges(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				counter := &echoLeaseCounter{}
				desired := []EchoReconcileConfig{{
					EchoSessionConfig: echoLeaseConfig("192.0.2.75"), SenderLeaseFactory: counter.factory(nil),
				}}
				factoryErr := errors.New("Echo sender unavailable")
				if rollback {
					desired = append(desired, EchoReconcileConfig{
						EchoSessionConfig: echoLeaseConfig("192.0.2.76"), SenderLeaseFactory: counter.factory(factoryErr),
					})
				}
				_, _, reconcileErr := mgr.ReconcileEchoSessions(t.Context(), desired)
				if errors.Is(reconcileErr, factoryErr) != rollback || (!rollback && reconcileErr != nil) {
					t.Fatalf("reconcile = %v, rollback=%t", reconcileErr, rollback)
				}
				if !rollback {
					if _, _, removeErr := mgr.ReconcileEchoSessions(t.Context(), nil); removeErr != nil {
						t.Fatal(removeErr)
					}
				}
				synctest.Wait()
				if len(sub) != 2 {
					t.Fatalf("reconcile lifecycle events=%d, want Added then Deleted", len(sub))
				}
				added, deleted := <-sub, <-sub
				if !added.Added || !deleted.Deleted || added.Ref != deleted.Ref ||
					deleted.ObservationVersion <= added.ObservationVersion || deleted.Revision <= added.Revision ||
					deleted.Status.LocalDiscr != added.LocalDiscr || added.LocalDiscr == api ||
					len(mgr.EchoSessions()) != 1 || mgr.EchoSessions()[0].LocalDiscr != api ||
					counter.closes.Load() != counter.opens.Load() {
					t.Fatalf("reconcile/rollback lifecycle mismatch: added=%+v deleted=%+v", added, deleted)
				}
			})
		})
	}
}

func TestEchoWatchCoherentObservation(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()
	es, err := NewEchoSession(echoLeaseConfig("192.0.2.77"), 42, echoLeaseSender{}, nil, mgr.logger)
	if err != nil {
		t.Fatal(err)
	}
	observed := 0
	es.metrics = causalMetrics{observe: func() {
		observed++
		snapshot := es.watchSnapshot()
		if snapshot.State != es.State() || snapshot.LocalDiag != es.LocalDiag() ||
			snapshot.ObservationVersion != uint64(observed+1) ||
			snapshot.State == StateUp && snapshot.Cause != CauseUnknown ||
			snapshot.State == StateDown && snapshot.Cause != CauseDetectionTimeout {
			t.Errorf("Echo callback ran before commit or under an observation lock: %+v", snapshot)
		}
	}}
	var owners sync.WaitGroup
	ctx := t.Context()
	owners.Go(func() {
		for range 1000 {
			es.transitionTo(ctx, StateUp, DiagNone)
			es.transitionTo(ctx, StateDown, DiagEchoFailed)
		}
	})
	for range 1000 {
		snapshot := es.watchSnapshot()
		if snapshot.State == StateUp && (snapshot.LocalDiag != DiagNone || snapshot.Cause != CauseUnknown) ||
			snapshot.State == StateDown && snapshot.ObservationVersion > 1 &&
				(snapshot.LocalDiag != DiagEchoFailed || snapshot.Cause != CauseDetectionTimeout) {
			t.Errorf("torn Echo causal observation: %+v", snapshot)
			break
		}
	}
	owners.Wait()
	if observed != 2000 {
		t.Fatalf("Echo committed callbacks=%d, want 2000", observed)
	}
}

func TestEchoWatchCloseClosesSubscriberBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		if _, err := mgr.CreateEchoSession(t.Context(), echoLeaseConfig("192.0.2.78"), echoLeaseSender{}); err != nil {
			t.Fatal(err)
		}
		sub, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		mgr.Close()
		if event, open := <-sub; open {
			t.Fatalf("Close synthesized an Echo terminal event instead of closing the stream: %+v", event)
		}
		if _, subscribeErr := mgr.SubscribeStateChanges(t.Context()); !errors.Is(subscribeErr, ErrManagerClosed) {
			t.Fatalf("late Echo Watch subscription = %v, want ErrManagerClosed", subscribeErr)
		}
	})
}

func TestEchoWatchOwnerStatusIsFrozen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		cfg := echoLeaseConfig("192.0.2.71")
		cfg.TxInterval = time.Second
		discr, err := mgr.CreateEchoSession(t.Context(), cfg, echoLeaseSender{})
		if err != nil {
			t.Fatal(err)
		}
		mgr.mu.RLock()
		es := mgr.echoSessions[discr].session
		mgr.mu.RUnlock()
		synctest.Wait()
		synctest.Sleep(time.Second)
		if outcome := es.RecvEcho(); outcome != DeliveryQueued {
			t.Fatalf("RecvEcho = %v, want queued", outcome)
		}
		synctest.Wait()
		var up StateChange
		select {
		case up = <-mgr.rawNotifyCh:
		default:
			t.Fatal("actual Echo return emitted no owner state change")
		}
		before := es.Snapshot()
		frozen := up.Status
		clock := mgr.observationClock.Load()
		if outcome := es.RecvEcho(); outcome != DeliveryQueued {
			t.Fatalf("second RecvEcho = %v, want queued", outcome)
		}
		synctest.Wait()
		if mgr.observationClock.Load() != clock || len(mgr.rawNotifyCh) != 0 || es.EchosReceived() != 2 {
			t.Fatal("counter-only Echo return changed observation version or emitted a state event")
		}
		synctest.Sleep(3 * time.Second)
		var down StateChange
		select {
		case down = <-mgr.rawNotifyCh:
		default:
			t.Fatal("actual Echo detection timeout emitted no owner state change")
		}
		if up.Status != frozen || frozen.LocalDiscr != discr || frozen.Type != SessionTypeEcho ||
			frozen.PeerAddr != cfg.PeerAddr || frozen.LocalAddr != cfg.LocalAddr || frozen.Interface != cfg.Interface ||
			frozen.State != StateUp || frozen.LocalDiag != DiagNone || frozen.Cause != CauseUnknown ||
			frozen.DesiredMinTx != cfg.TxInterval || frozen.DetectMultiplier != cfg.DetectMultiplier ||
			frozen.DetectionTime != before.DetectionTime || frozen.Counters.PacketsSent != before.EchosSent ||
			frozen.Counters.PacketsReceived != 1 || frozen.Counters.StateTransitions != 1 ||
			frozen.LastStateChange != before.LastStateChange || frozen.LastStateChange.IsZero() ||
			frozen.LastPacketReceived != before.LastEchoReceived || frozen.LastPacketReceived.IsZero() ||
			up.Ref == (SessionRef{}) || frozen.Ref != up.Ref || down.Ref != up.Ref ||
			up.ObservationVersion == 0 || frozen.ObservationVersion != up.ObservationVersion ||
			down.ObservationVersion <= up.ObservationVersion || down.NewState != StateDown ||
			down.Diag != DiagEchoFailed || down.Cause != CauseDetectionTimeout ||
			down.Status.State != StateDown || down.Status.LocalDiag != DiagEchoFailed ||
			down.Status.ObservationVersion != down.ObservationVersion || down.Status.Counters.PacketsReceived != 2 ||
			down.Status.Counters.StateTransitions != 2 || frozen.RemoteKnown || frozen.LocalC || frozen.RemoteC ||
			frozen.RemoteDiscr != 0 || frozen.RequiredMinRx != 0 || frozen.RemoteMinRx != 0 ||
			frozen.NegotiatedTxInterval != 0 || frozen.Counters.PacketsDropped != 0 || frozen.Counters.AuthFailures != 0 {
			t.Fatalf("actual owner Echo status is missing, incoherent or mutable: up=%+v down=%+v want=%+v", up, down, before)
		}
	})
}

func TestEchoWatchDestroyPublishesVersionedDeletionWithoutDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cfg := echoLeaseConfig("192.0.2.72")
		discr, err := mgr.CreateEchoSession(ctx, cfg, echoLeaseSender{})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		var added StateChange
		select {
		case added = <-sub:
			if !added.Added || added.LocalDiscr != discr {
				t.Fatalf("registration event = %+v, want Echo addition", added)
			}
		default:
		}
		revision := mgr.Revision()
		if destroyErr := mgr.DestroyEchoSession(discr); destroyErr != nil {
			t.Fatal(destroyErr)
		}
		synctest.Wait()
		select {
		case deleted := <-sub:
			if !deleted.Deleted || deleted.Added || deleted.Gap || deleted.LocalDiscr != discr ||
				deleted.Type != SessionTypeEcho || deleted.PeerAddr != cfg.PeerAddr ||
				deleted.LocalAddr != cfg.LocalAddr || deleted.Interface != cfg.Interface ||
				deleted.Ref == (SessionRef{}) || deleted.Ref != added.Ref || deleted.Status.Ref != deleted.Ref ||
				deleted.ObservationVersion <= added.ObservationVersion || deleted.ObservationVersion == 0 ||
				deleted.Revision <= revision || deleted.Status.ObservationVersion != deleted.ObservationVersion ||
				deleted.Status.LocalDiscr != discr || deleted.Status.State != StateDown ||
				deleted.Status.LocalDiag != DiagNone || deleted.Status.DesiredMinTx != cfg.TxInterval ||
				len(mgr.EchoSessions()) != 0 {
				t.Fatalf("Echo deletion lacks final frozen status, identity or fresh version: %+v added=%+v", deleted, added)
			}
		default:
			t.Error("DestroyEchoSession emitted no synchronous SESSION_DELETED without RunDispatch")
		}
	})
}
