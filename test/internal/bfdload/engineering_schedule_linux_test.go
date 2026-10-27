//go:build linux

package bfdload

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestEngineeringScheduleHasMeasuredBlackholeAndRecovery(t *testing.T) {
	t.Parallel()

	profile, err := DefaultProfile("engineering")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	var omitted uint64
	var last time.Duration
	for elapsed := time.Duration(0); elapsed < profile.Duration; elapsed += profile.Interval {
		decision, nextErr := schedule.next(elapsed)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if decision.disposition == packetBlackhole {
			omitted++
			last = elapsed
		}
	}
	if omitted == 0 {
		t.Fatal("engineering schedule has no blackhole inside its measured phase")
	}
	if last+sessionChurnBudget >= profile.Duration {
		t.Fatalf("last omission %s leaves no recovery interval before cutoff %s", last, profile.Duration)
	}
}

func TestEngineeringDecisionLimitsLossToMeasuredPhase(t *testing.T) {
	profile, err := DefaultProfile("engineering")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	control, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Now()
	started := epoch.Add(-time.Minute)
	peer := peerRuntime{phase: new(atomic.Pointer[phaseStart]), duration: profile.Duration}
	if decision, decisionErr := peer.decision(schedule, epoch, started); decisionErr != nil ||
		decision != (packetDecision{disposition: packetTransmit}) {
		t.Fatalf("unarmed phase consumed loss input: decision=%+v err=%v", decision, decisionErr)
	}
	peer.phase.Store(&phaseStart{goStart: epoch})
	for _, tc := range []struct {
		name      string
		at        time.Duration
		measured  bool
		blackhole bool
	}{
		{"before_epoch", -time.Nanosecond, false, false},
		{"epoch", 0, true, false},
		{"before_first_window", 10*time.Minute - time.Nanosecond, true, false},
		{"first_window", 10 * time.Minute, true, true},
		{"first_window_end", 10*time.Minute + time.Second, true, false},
		{"second_window", 20 * time.Minute, true, true},
		{"second_window_end", 20*time.Minute + time.Second, true, false},
		{"before_cutoff", 30*time.Minute - time.Nanosecond, true, false},
		{"cutoff", 30 * time.Minute, false, false},
		{"drain", 30*time.Minute + time.Second, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, decisionErr := peer.decision(schedule, epoch.Add(tc.at), started)
			if decisionErr != nil {
				t.Fatal(decisionErr)
			}
			if (decision.disposition == packetBlackhole) != tc.blackhole {
				t.Fatalf("phase offset %s: decision=%+v want blackhole=%t", tc.at, decision, tc.blackhole)
			}
			want := packetDecision{disposition: packetTransmit}
			if tc.measured {
				want, err = control.next(tc.at)
				if err != nil {
					t.Fatal(err)
				}
			}
			if decision != want {
				t.Fatalf("phase decision diverged from the measured-only control: got=%+v want=%+v", decision, want)
			}
		})
	}
	for range 8 {
		if schedule.random.Uint64N(1000) != control.random.Uint64N(1000) {
			t.Fatal("setup or drain consumed the measured PCG stream")
		}
	}
}
