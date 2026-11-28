package bfd

import (
	"errors"
	"log/slog"
	"math"
	"net/netip"
	"testing"
	"testing/synctest"
)

func TestNotificationPopulationBounds(t *testing.T) {
	for _, tc := range []struct {
		name        string
		populations []int
		want        int
		invalid     bool
	}{
		{name: "empty", want: 64},
		{name: "zero", populations: []int{0}, want: 64},
		{name: "cohorts", populations: []int{1, 159}, want: 544},
		{name: "last complete cohort", populations: []int{21824}, want: 65536},
		{name: "saturated", populations: []int{21825}, want: 65536},
		{name: "integer overflow", populations: []int{math.MaxInt, math.MaxInt}, want: 65536},
		{name: "negative", populations: []int{-1}, invalid: true},
		{name: "negative after saturation", populations: []int{math.MaxInt, -1}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			option, err := WithNotificationPopulation(tc.populations...)
			if tc.invalid {
				if !errors.Is(err, ErrInvalidNotificationPopulation) || option != nil {
					t.Fatalf("negative population accepted: option nil=%t, error=%v", option == nil, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			manager := NewManager(slog.New(slog.DiscardHandler), option)
			defer manager.Close()
			changes, err := manager.SubscribeStateChanges(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if cap(changes) != tc.want || cap(manager.rawNotifyCh) != tc.want || cap(manager.StateChanges()) != 64 {
				t.Fatalf("notification capacities: subscriber=%d raw=%d legacy=%d; want %d/%d/64",
					cap(changes), cap(manager.rawNotifyCh), cap(manager.StateChanges()), tc.want, tc.want)
			}
		})
	}
}

func TestNotificationPopulationPreservesEchoStartupAndExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const population = 160
		option, err := WithNotificationPopulation(population)
		if err != nil {
			t.Fatal(err)
		}
		manager := NewManager(slog.New(slog.DiscardHandler), option)
		defer manager.Close()
		changes, err := manager.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for index := range population {
			peer := netip.AddrFrom4([4]byte{192, 0, 2, byte(index + 1)})
			discr, createErr := manager.CreateEchoSession(t.Context(), echoLeaseConfig(peer.String()), echoLeaseSender{})
			if createErr != nil {
				t.Fatal(createErr)
			}
			if outcome, recvErr := manager.DemuxEcho(discr); outcome != DeliveryQueued || recvErr != nil {
				t.Fatalf("actual Echo return = %s, %v", outcome, recvErr)
			}
			synctest.Wait()
		}
		synctest.Sleep(3 * echoLeaseConfig("192.0.2.1").TxInterval)
		go manager.RunDispatch(t.Context())
		synctest.Wait()
		seen := make(map[uint32][]StateChange)
		for range 3 * population {
			select {
			case change := <-changes:
				if change.Gap || change.Type != SessionTypeEcho || change.ObservationVersion == 0 {
					t.Fatalf("Echo lifecycle lost: %+v", change)
				}
				seen[change.LocalDiscr] = append(seen[change.LocalDiscr], change)
			default:
				t.Fatal("Echo startup or expiry notification missing")
			}
		}
		if len(seen) != population {
			t.Fatalf("Echo owners observed: %d", len(seen))
		}
		for _, events := range seen {
			if len(events) != 3 || !events[0].Added || events[1].NewState != StateUp ||
				events[2].NewState != StateDown || events[2].Diag != DiagEchoFailed ||
				events[1].ObservationVersion >= events[2].ObservationVersion ||
				events[0].Ref != events[1].Ref || events[1].Ref != events[2].Ref {
				t.Fatalf("Echo incarnation history incomplete: %+v", events)
			}
		}
	})
}

func TestNotificationPopulationStillReportsOverBudgetLoss(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "subscriber", true: "raw"}[raw], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				option, err := WithNotificationPopulation(1)
				if err != nil {
					t.Fatal(err)
				}
				manager := NewManager(slog.New(slog.DiscardHandler), option)
				defer manager.Close()
				session := notificationTestSession(t, manager)
				synctest.Wait()
				changes, err := manager.SubscribeStateChanges(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if raw {
					fillRawLossQueue(t, manager, changes)
					session.emitNotification(FSMResult{}, session.Observation())
					go manager.RunDispatch(t.Context())
					synctest.Wait()
					if deliveryCount(t, manager, DeliveryControlNotification, DeliveryQueueFull) != 1 || !(<-changes).Gap {
						t.Fatal("over-budget raw loss did not produce recovery GAP")
					}
					return
				}
				go manager.RunDispatch(t.Context())
				for range 67 {
					session.emitNotification(FSMResult{}, session.Observation())
					synctest.Wait()
				}
				for range 66 {
					if (<-changes).Gap {
						t.Fatal("GAP replaced an admitted original")
					}
				}
				if !(<-changes).Gap || deliveryCount(t, manager, DeliveryWatchRecipient, DeliveryGapped) != 1 {
					t.Fatal("over-budget subscriber loss was hidden")
				}
			})
		})
	}
}
