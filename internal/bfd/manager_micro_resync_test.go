package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

var errMicroActuatorTransient = errors.New("transient LAG actuator failure")

type recordingStatusActuator struct {
	mu          sync.Mutex
	failCount   int
	events      []MicroBFDMemberEvent
	statusCalls []bool
}

func (a *recordingStatusActuator) HandleMicroBFDMemberEvent(_ context.Context, ev MicroBFDMemberEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, ev)
	if a.failCount > 0 {
		a.failCount--
		return errMicroActuatorTransient
	}
	return nil
}

func (a *recordingStatusActuator) recordStatus(healthy bool, _ error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.statusCalls = append(a.statusCalls, healthy)
}

func (a *recordingStatusActuator) snapshot() ([]MicroBFDMemberEvent, []bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	events := append([]MicroBFDMemberEvent(nil), a.events...)
	statuses := append([]bool(nil), a.statusCalls...)
	return events, statuses
}

func TestMicroBFDRawLossFinalDownConvergesWithoutNewEdge(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		act := &recordingStatusActuator{}
		mgr := NewManager(
			slog.New(slog.DiscardHandler),
			WithMicroBFDActuator(act),
			WithMicroActuatorStatusFunc(act.recordStatus),
		)
		defer mgr.Close()

		groupCfg := MicroBFDConfig{
			LAGInterface:          "bond0",
			MemberLinks:           []string{"eth0"},
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			DesiredMinTxInterval:  time.Hour,
			RequiredMinRxInterval: time.Hour,
			DetectMultiplier:      3,
			MinActiveLinks:        1,
		}
		group, err := mgr.CreateMicroBFDGroupAs(groupCfg, APISessionOwner("micro-loss-owner"))
		if err != nil {
			t.Fatalf("CreateMicroBFDGroupAs: %v", err)
		}
		sessCfg := microBFDMemberSessionConfig(groupCfg, "eth0")
		sessCfg.Role = RolePassive
		sess, err := mgr.CreateSession(t.Context(), sessCfg, NonOwningSenderLeaseFactory(senderLeaseTestSender{}))
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		sub, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatalf("SubscribeStateChanges: %v", err)
		}

		// Drive member Up and dispatch initial Up so aggregate and actuator are Up.
		pkt := sess.buildControlPacket()
		pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = StateInit, 77, sess.LocalDiscriminator()
		if sess.RecvPacket(&pkt) != DeliveryQueued {
			t.Fatal("initial Micro-BFD receive refused")
		}
		synctest.Wait()
		upEvent := dispatchRawLossInitialUp(t, mgr, sub)
		if !group.IsUp() || group.UpCount() != 1 {
			t.Fatalf("group did not reach Up after initial dispatch: up=%t count=%d", group.IsUp(), group.UpCount())
		}

		// Re-queue the older Up witness at the head of rawNotifyCh, fill the rest
		// with stale events, and lose the final Up->Down transition on overflow.
		mgr.rawNotifyCh <- upEvent
		for range cap(mgr.rawNotifyCh) - 1 {
			mgr.rawNotifyCh <- StateChange{}
		}
		if sess.SetPathDown() != DeliveryQueued {
			t.Fatal("SetPathDown refused")
		}
		synctest.Wait()
		if sess.State() != StateDown || deliveryCount(t, mgr, DeliveryControlNotification, DeliveryQueueFull) != 1 {
			t.Fatal("final Micro-BFD Down was not dropped on full rawNotifyCh")
		}

		dispatchCtx, cancelDispatch := context.WithCancel(t.Context())
		dispatchDone := make(chan struct{})
		go func() {
			defer close(dispatchDone)
			mgr.RunDispatch(dispatchCtx)
		}()
		defer func() {
			cancelDispatch()
			<-dispatchDone
		}()
		synctest.Wait()

		if group.IsUp() || group.UpCount() != 0 {
			t.Fatalf("group remained Up after raw notification loss: up=%t count=%d", group.IsUp(), group.UpCount())
		}
		events, statuses := act.snapshot()
		if len(events) != 2 {
			t.Fatalf("actuator events = %+v, want [Up, Down]", events)
		}
		last := events[len(events)-1]
		if last.OldState != StateUp || last.NewState != StateDown || last.AggregateUp {
			t.Fatalf("final actuator event = %+v, want Up->Down with AggregateUp=false", last)
		}
		if len(statuses) != 0 {
			t.Fatalf("actuator status transition calls = %v, want none when never failed", statuses)
		}
		// Verify owner claim was preserved untouched.
		if err := mgr.DestroyMicroBFDGroupAs("bond0", APISessionOwner("other")); !errors.Is(
			err, ErrMicroBFDGroupOwnerClaimNotFound,
		) {
			t.Fatalf("unexpected foreign owner release result: %v", err)
		}
	})
}

func TestMicroBFDActuatorTransientFailureRetriesAndReportsStatus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		act := &recordingStatusActuator{failCount: 2}
		var healthyFlag atomic.Bool
		healthyFlag.Store(true)
		mgr := NewManager(
			slog.New(slog.DiscardHandler),
			WithMicroBFDActuator(act),
		)
		defer mgr.Close()
		mgr.SetMicroActuatorStatusFunc(func(healthy bool, err error) {
			healthyFlag.Store(healthy)
			act.recordStatus(healthy, err)
		})

		groupCfg := MicroBFDConfig{
			LAGInterface:          "bond0",
			MemberLinks:           []string{"eth0"},
			PeerAddr:              netip.MustParseAddr("192.0.2.11"),
			LocalAddr:             netip.MustParseAddr("192.0.2.12"),
			DesiredMinTxInterval:  time.Hour,
			RequiredMinRxInterval: time.Hour,
			DetectMultiplier:      3,
			MinActiveLinks:        1,
		}
		if _, err := mgr.CreateMicroBFDGroup(groupCfg); err != nil {
			t.Fatalf("CreateMicroBFDGroup: %v", err)
		}
		sessCfg := microBFDMemberSessionConfig(groupCfg, "eth0")
		sessCfg.Role = RolePassive
		sess, err := mgr.CreateSession(t.Context(), sessCfg, NonOwningSenderLeaseFactory(senderLeaseTestSender{}))
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		dispatchCtx, cancelDispatch := context.WithCancel(t.Context())
		dispatchDone := make(chan struct{})
		go func() {
			defer close(dispatchDone)
			mgr.RunDispatch(dispatchCtx)
		}()
		defer func() {
			cancelDispatch()
			<-dispatchDone
		}()

		pkt := sess.buildControlPacket()
		pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = StateInit, 88, sess.LocalDiscriminator()
		if sess.RecvPacket(&pkt) != DeliveryQueued {
			t.Fatal("initial Micro-BFD receive refused")
		}
		synctest.Wait()

		// Initial attempt failed (failCount: 2 -> 1); status must be unhealthy.
		if healthyFlag.Load() {
			t.Fatal("actuator status remained healthy after initial actuation failure")
		}

		// First retry at 50ms fails (failCount: 1 -> 0); status remains unhealthy.
		synctest.Sleep(microBFDActuatorRetryInitial)
		if healthyFlag.Load() {
			t.Fatal("actuator status turned healthy while second attempt still failed")
		}

		// Second backoff retry at 100ms succeeds without any new BFD packet!
		synctest.Sleep(microBFDActuatorRetryInitial * microBFDActuatorRetryFactor)
		if !healthyFlag.Load() {
			t.Fatal("actuator status did not recover to healthy after bounded retry succeeded")
		}
		events, statuses := act.snapshot()
		if len(events) != 3 {
			t.Fatalf("actuator call count = %d, want 3 (1 initial + 2 retries)", len(events))
		}
		for i, ev := range events {
			if ev.OldState != StateDown || ev.NewState != StateUp || !ev.AggregateUp {
				t.Fatalf("attempt %d event = %+v, want Down->Up AggregateUp=true", i, ev)
			}
		}
		wantStatuses := []bool{false, true}
		if len(statuses) != len(wantStatuses) {
			t.Fatalf("status transitions = %v, want %v", statuses, wantStatuses)
		}
		for i, want := range wantStatuses {
			if statuses[i] != want {
				t.Fatalf("status[%d] = %t, want %t", i, statuses[i], want)
			}
		}

		// Now fail Up->Down once and verify Down actuation also retries with OldState=Up.
		act.mu.Lock()
		act.failCount = 1
		act.events = nil
		act.mu.Unlock()
		if sess.SetPathDown() != DeliveryQueued {
			t.Fatal("SetPathDown refused")
		}
		synctest.Wait()
		if healthyFlag.Load() {
			t.Fatal("actuator status remained healthy after Down actuation failure")
		}
		synctest.Sleep(microBFDActuatorRetryInitial)
		if !healthyFlag.Load() {
			t.Fatal("actuator status did not recover after Down retry succeeded")
		}
		downEvents, _ := act.snapshot()
		if len(downEvents) != 2 || downEvents[1].OldState != StateUp || downEvents[1].NewState != StateDown {
			t.Fatalf("Down retry events = %+v, want 2 Up->Down events", downEvents)
		}
	})
}

type gatedFailingActuator struct {
	mu       sync.Mutex
	gateOnce sync.Once
	entered  chan struct{}
	release  chan struct{}
	calls    []MicroBFDMemberEvent
	failLAG  map[string]bool
}

func (a *gatedFailingActuator) HandleMicroBFDMemberEvent(_ context.Context, ev MicroBFDMemberEvent) error {
	a.mu.Lock()
	a.calls = append(a.calls, ev)
	a.mu.Unlock()

	if a.entered != nil {
		a.gateOnce.Do(func() {
			close(a.entered)
			<-a.release
		})
	}
	a.mu.Lock()
	shouldFail := a.failLAG[ev.LAGInterface]
	a.mu.Unlock()
	if shouldFail {
		return errMicroActuatorTransient
	}
	return nil
}

func TestMicroBFDInFlightActuationAfterDestroyAndRecreate(t *testing.T) {
	t.Parallel()

	t.Run("in_flight_fail_after_recreate_ignores_dead_group", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			act := &gatedFailingActuator{
				entered: make(chan struct{}),
				release: make(chan struct{}),
				failLAG: map[string]bool{"bond0": true},
			}
			var releaseOnce sync.Once
			releaseGate := func() {
				releaseOnce.Do(func() { close(act.release) })
			}
			defer releaseGate()

			var healthy atomic.Bool
			healthy.Store(true)
			var statusTransitions []bool
			var statusMu sync.Mutex

			mgr := NewManager(
				slog.New(slog.DiscardHandler),
				WithMicroBFDActuator(act),
				WithMicroActuatorStatusFunc(func(ok bool, _ error) {
					healthy.Store(ok)
					statusMu.Lock()
					statusTransitions = append(statusTransitions, ok)
					statusMu.Unlock()
				}),
			)
			defer mgr.Close()

			groupCfg := MicroBFDConfig{
				LAGInterface:          "bond0",
				MemberLinks:           []string{"eth0"},
				PeerAddr:              netip.MustParseAddr("192.0.2.31"),
				LocalAddr:             netip.MustParseAddr("192.0.2.32"),
				DesiredMinTxInterval:  time.Hour,
				RequiredMinRxInterval: time.Hour,
				DetectMultiplier:      3,
				MinActiveLinks:        1,
			}
			group1, err := mgr.CreateMicroBFDGroup(groupCfg)
			if err != nil {
				t.Fatalf("CreateMicroBFDGroup group1: %v", err)
			}
			sessCfg := microBFDMemberSessionConfig(groupCfg, "eth0")
			sessCfg.Role = RolePassive
			sess, err := mgr.CreateSession(t.Context(), sessCfg, NonOwningSenderLeaseFactory(senderLeaseTestSender{}))
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}

			dispatchCtx, cancelDispatch := context.WithCancel(t.Context())
			dispatchDone := make(chan struct{})
			go func() {
				defer close(dispatchDone)
				mgr.RunDispatch(dispatchCtx)
			}()
			defer func() {
				cancelDispatch()
				<-dispatchDone
			}()

			pkt := sess.buildControlPacket()
			pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = StateInit, 91, sess.LocalDiscriminator()
			if sess.RecvPacket(&pkt) != DeliveryQueued {
				t.Fatal("RecvPacket refused")
			}
			<-act.entered

			// Drive sess Down, destroy group1, and recreate group2 under the same LAG while group1's actuation is in flight.
			if sess.SetPathDown() != DeliveryQueued {
				t.Fatal("SetPathDown refused")
			}
			synctest.Wait()
			if delErr := mgr.DestroyMicroBFDGroup("bond0"); delErr != nil {
				t.Fatalf("DestroyMicroBFDGroup: %v", delErr)
			}
			group2, err := mgr.CreateMicroBFDGroup(groupCfg)
			if err != nil {
				t.Fatalf("CreateMicroBFDGroup group2: %v", err)
			}
			if group2 == group1 {
				t.Fatal("recreated group reused dead group pointer")
			}

			// Release group1's in-flight actuation so it returns errMicroActuatorTransient after recreate.
			releaseGate()
			synctest.Wait()
			synctest.Sleep(10 * time.Second)

			if group2.hasFailedActuation() {
				t.Fatal("dead group1 in-flight failure polluted recreated group2 state")
			}
			if mgr.microActuatorFailed.Load() || !healthy.Load() {
				t.Fatal("dead group1 in-flight failure marked Manager actuator unhealthy")
			}
			statusMu.Lock()
			gotTransitions := append([]bool(nil), statusTransitions...)
			statusMu.Unlock()
			if len(gotTransitions) != 0 {
				t.Fatalf("unexpected health transitions from dead group: %v", gotTransitions)
			}
			act.mu.Lock()
			totalCalls := len(act.calls)
			act.mu.Unlock()
			if totalCalls != 1 {
				t.Fatalf("actuator calls = %d, want 1 (retry timer must stay stopped)", totalCalls)
			}
		})
	})

	t.Run("destroy_failing_group_recovers_only_after_last_failing_group", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			act := &gatedFailingActuator{
				failLAG: map[string]bool{"bond0": true, "bond1": true},
			}
			var transitions []bool
			var mu sync.Mutex
			mgr := NewManager(
				slog.New(slog.DiscardHandler),
				WithMicroBFDActuator(act),
				WithMicroActuatorStatusFunc(func(ok bool, _ error) {
					mu.Lock()
					transitions = append(transitions, ok)
					mu.Unlock()
				}),
			)
			defer mgr.Close()

			dispatchCtx, cancelDispatch := context.WithCancel(t.Context())
			dispatchDone := make(chan struct{})
			go func() {
				defer close(dispatchDone)
				mgr.RunDispatch(dispatchCtx)
			}()
			defer func() {
				cancelDispatch()
				<-dispatchDone
			}()

			for i, lag := range []string{"bond0", "bond1"} {
				member := "eth" + string(rune('0'+i))
				peer := netip.AddrFrom4([4]byte{192, 0, 2, byte(50 + i)})
				cfg := MicroBFDConfig{
					LAGInterface: lag, MemberLinks: []string{member},
					PeerAddr: peer, LocalAddr: netip.MustParseAddr("192.0.2.254"),
					DesiredMinTxInterval: time.Hour, RequiredMinRxInterval: time.Hour,
					DetectMultiplier: 3, MinActiveLinks: 1,
				}
				if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
					t.Fatal(err)
				}
				sCfg := microBFDMemberSessionConfig(cfg, member)
				sCfg.Role = RolePassive
				s, err := mgr.CreateSession(t.Context(), sCfg, NonOwningSenderLeaseFactory(senderLeaseTestSender{}))
				if err != nil {
					t.Fatal(err)
				}
				pkt := s.buildControlPacket()
				pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = StateInit, uint32(60+i), s.LocalDiscriminator()
				if s.RecvPacket(&pkt) != DeliveryQueued {
					t.Fatal("RecvPacket refused")
				}
			}
			synctest.Wait()

			// Deleting bond0 while bond1 is still failing MUST NOT prematurely report healthy=true!
			if err := mgr.DestroyMicroBFDGroup("bond0"); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			mu.Lock()
			afterFirstDelete := append([]bool(nil), transitions...)
			mu.Unlock()
			if len(afterFirstDelete) != 1 || afterFirstDelete[0] != false {
				t.Fatalf("transitions while bond1 still fails = %v, want [false]", afterFirstDelete)
			}

			// Deleting bond1 clears the last failing group: health transitions to true and retries stop!
			if err := mgr.DestroyMicroBFDGroup("bond1"); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			mu.Lock()
			afterSecondDelete := append([]bool(nil), transitions...)
			mu.Unlock()
			if len(afterSecondDelete) != 2 || afterSecondDelete[0] != false || afterSecondDelete[1] != true {
				t.Fatalf("transitions after deleting last failing group = %v, want [false, true]", afterSecondDelete)
			}
			act.mu.Lock()
			callsBeforeSleep := len(act.calls)
			act.mu.Unlock()
			synctest.Sleep(10 * time.Second)
			act.mu.Lock()
			callsAfterSleep := len(act.calls)
			act.mu.Unlock()
			if callsAfterSleep != callsBeforeSleep {
				t.Fatalf("retry timer fired after all failing groups were destroyed: %d -> %d",
					callsBeforeSleep, callsAfterSleep)
			}
		})
	})
}
