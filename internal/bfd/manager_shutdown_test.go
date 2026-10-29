package bfd

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type shutdownTransitionBarrier struct {
	MetricsReporter

	entered chan struct{}
	release chan struct{}
}

func (b *shutdownTransitionBarrier) RecordStateTransition(peer, local netip.Addr, from, to string) {
	b.MetricsReporter.RecordStateTransition(peer, local, from, to)
	if from == "Up" && to == "Down" {
		close(b.entered)
		<-b.release
	}
}

func TestShutdownNotificationDrainJoinsProducerBeforeSubscriberEOF(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped_dispatch", true: "running_dispatch"}[running], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				barrier := &shutdownTransitionBarrier{
					MetricsReporter: noopMetrics{}, entered: make(chan struct{}), release: make(chan struct{}),
				}
				mgr := NewManager(slog.New(slog.DiscardHandler), WithShutdownNotificationDrain(), WithManagerMetrics(barrier))
				defer mgr.Close()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				go mgr.RunDispatch(ctx)
				changes, err := mgr.SubscribeStateChanges(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				released := make(chan struct{})
				es, err := mgr.CreateEchoSessionAs(t.Context(), echoLeaseConfig("192.0.2.67"),
					func() (*SenderLease, error) {
						return NewSenderLease(echoLeaseSender{}, func() error { close(released); return nil }), nil
					}, APISessionOwner("shutdown-journal"))
				if err != nil {
					t.Fatal(err)
				}
				es.RecvEcho()
				synctest.Wait()
				for len(changes) > 0 {
					<-changes
				}
				var release sync.Once
				unblock := func() { release.Do(func() { close(barrier.release) }) }
				defer unblock()
				synctest.Sleep(es.DetectionTime())
				<-barrier.entered
				committed := es.Observation()
				if committed.State != StateDown || committed.LocalDiag != DiagEchoFailed {
					t.Fatal("fixture did not commit a real Echo expiry")
				}
				if !running {
					cancel()
					synctest.Wait()
				}
				closed := make(chan struct{}, 2)
				for range 2 {
					go func() { mgr.Close(); closed <- struct{}{} }()
				}
				synctest.Wait()
				select {
				case <-closed:
					t.Fatal("concurrent Close returned before producer join")
				case <-released:
					t.Fatal("sender released before producer join")
				default:
				}
				select {
				case _, open := <-changes:
					t.Fatalf("subscriber reached an unexpected event/EOF before producer publication: open=%t", open)
				default:
				}
				unblock()
				<-closed
				<-closed
				count := 0
				for change := range changes {
					count++
					if change.Gap || change.Ref != es.Ref() || change.ObservationVersion != committed.ObservationVersion ||
						change.Type != SessionTypeEcho || change.OldState != StateUp || change.NewState != StateDown ||
						change.Cause != CauseDetectionTimeout || change.Diag != DiagEchoFailed ||
						change.TransitionTiming.Version != committed.ObservationVersion || change.TransitionTiming.DetectionDue.IsZero() {
						t.Fatalf("shutdown lost the physical source witness: %+v", change)
					}
				}
				if count != 1 || len(mgr.rawNotifyCh) != 0 ||
					deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryStale) != 0 ||
					deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryAbandoned) != 0 {
					t.Fatalf("shutdown lost or duplicated the committed transition: delivered=%d", count)
				}
				mgr.Close()
				select {
				case <-released:
				default:
					t.Fatal("joined Close did not release its sender")
				}
			})
		})
	}
}

func TestShutdownNotificationDrainPreservesLossAndPhysicalFences(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler), WithShutdownNotificationDrain())
		defer mgr.Close()
		es, err := mgr.CreateEchoSessionAs(t.Context(), echoLeaseConfig("192.0.2.68"),
			NonOwningSenderLeaseFactory(echoLeaseSender{}), APISessionOwner("shutdown-fence"))
		if err != nil {
			t.Fatal(err)
		}
		changes, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		stale := stateChangeOf(es.watchSnapshot(), StateUp)
		stale.Ref = SessionRef{}
		mgr.rawNotifyCh <- stale
		mgr.rawNotifyCh <- StateChange{unsolicitedFailure: true}
		signalPublicNotificationLoss(mgr.publicLossWake)
		mgr.Close()
		count := 0
		for change := range changes {
			count++
			if !change.Gap {
				t.Fatalf("stale/internal event escaped as a source witness: %+v", change)
			}
		}
		if count != 1 || deliveryCount(t, mgr, DeliveryNotificationDispatch, DeliveryStale) != 2 {
			t.Fatalf("shutdown lost GAP or physical identity fence: gap=%d", count)
		}
	})
}

func TestShutdownNotificationDrainDoesNotActuateMicroBFD(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		mgr := NewManager(slog.New(slog.DiscardHandler), WithShutdownNotificationDrain(),
			WithMicroBFDActuator(microActuatorFunc(func(context.Context, MicroBFDMemberEvent) error { calls++; return nil })))
		defer mgr.Close()
		cfg := senderLeaseTestConfig("192.0.2.69")
		cfg.Type, cfg.Interface = SessionTypeMicroBFD, "eth0"
		session, err := mgr.CreateSession(t.Context(), cfg, NonOwningSenderLeaseFactory(echoLeaseSender{}))
		if err != nil {
			t.Fatal(err)
		}
		if _, groupErr := mgr.CreateMicroBFDGroup(MicroBFDConfig{
			LAGInterface: "bond0", MemberLinks: []string{"eth0"}, MinActiveLinks: 1,
			LocalAddr: cfg.LocalAddr, PeerAddr: cfg.PeerAddr,
			DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
		}); groupErr != nil {
			t.Fatal(groupErr)
		}
		changes, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		packet := ControlPacket{
			Version: 1, State: StateInit, DetectMult: 3, MyDiscriminator: 7,
			YourDiscriminator: session.LocalDiscriminator(), DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 1000000,
		}
		if session.RecvPacket(&packet) != DeliveryQueued {
			t.Fatal("real Micro-BFD owner refused Init")
		}
		synctest.Wait()
		if session.State() != StateUp || len(mgr.rawNotifyCh) != 1 {
			t.Fatal("fixture did not enqueue a real Micro-BFD Up")
		}
		mgr.Close()
		count := 0
		for change := range changes {
			count++
			if change.Gap || change.Ref != session.Ref() || change.NewState != StateUp {
				t.Fatalf("shutdown lost the actual member notification: %+v", change)
			}
		}
		if calls != 0 || count != 1 {
			t.Fatalf("observational shutdown ran an actuator or lost delivery: callbacks=%d events=%d", calls, count)
		}
	})
}
