//go:build linux

package bfdload

import (
	"encoding/json"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestOwnerClockProofRequiresCoveredStoppedWindow(t *testing.T) {
	started := time.Now()
	calibration := bfd.NewReceiveClock().Calibration()
	calibration.SpanNS = 200
	calibration.RealtimeResolutionNS = 2
	calibration.MonotonicResolutionNS = 4
	calibration.ConditionalBoundNS = 206
	other := calibration
	other.SpanNS = 400
	other.ConditionalBoundNS = 406
	stopped := time.Now()
	finished := stopped.Add(time.Nanosecond)
	observation := OwnerObservation{
		stoppedAt: stopped, Stopped: true, StoppedAtUTC: stopped.UTC(),
		ClockCalibrations: []OwnerClockObservation{{Calibration: calibration}, {Calibration: other}},
	}
	witness := ClockEpochObservation{
		started: started, finished: finished,
		Mechanism: clockEpochMechanism, Unchanged: true,
		StartedAtUTC: started.UTC(), FinishedAtUTC: finished.UTC(), ElapsedNS: int64(finished.Sub(started)),
	}
	qualified := observation.WithClockEpoch(witness)
	if qualified.TimingUncertaintyNS == nil || *qualified.TimingUncertaintyNS != 406 ||
		qualified.TimingUncertaintyError != "" || qualified.ReferenceQualified {
		t.Fatalf("covered clock proof lost conditional bound or qualified reference load: %+v", qualified)
	}
	for _, tc := range []struct {
		name string
		edit func(*OwnerObservation, *ClockEpochObservation)
	}{
		{"changed", func(_ *OwnerObservation, epoch *ClockEpochObservation) { epoch.Unchanged = false }},
		{"error", func(_ *OwnerObservation, epoch *ClockEpochObservation) { epoch.Error = "unavailable" }},
		{"wrong_mechanism", func(_ *OwnerObservation, epoch *ClockEpochObservation) { epoch.Mechanism = "" }},
		{"live_owner", func(owner *OwnerObservation, _ *ClockEpochObservation) { owner.Stopped = false }},
		{"not_stopped_yet", func(owner *OwnerObservation, _ *ClockEpochObservation) {
			owner.StoppedAtUTC = finished.Add(time.Nanosecond).UTC()
		}},
		{"missing_calibration", func(owner *OwnerObservation, _ *ClockEpochObservation) {
			owner.ClockCalibrations = nil
		}},
		{"calibrated_before_guard", func(_ *OwnerObservation, epoch *ClockEpochObservation) {
			epoch.StartedAtUTC = time.Unix(0, calibration.AnchorUnixNS+1).UTC()
		}},
		{"invalid_window", func(_ *OwnerObservation, epoch *ClockEpochObservation) {
			epoch.FinishedAtUTC = epoch.StartedAtUTC
		}},
		{"overflow_window", func(_ *OwnerObservation, epoch *ClockEpochObservation) {
			epoch.FinishedAtUTC = time.Date(3000, time.January, 1, 0, 0, 0, 0, time.UTC)
		}},
		{"replayed_json", func(owner *OwnerObservation, epoch *ClockEpochObservation) {
			clockWire, err := json.Marshal(owner.ClockCalibrations)
			if err != nil {
				t.Fatal(err)
			}
			epochWire, err := json.Marshal(epoch)
			if err != nil {
				t.Fatal(err)
			}
			owner.ClockCalibrations, *epoch = nil, ClockEpochObservation{}
			if err := json.Unmarshal(clockWire, &owner.ClockCalibrations); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(epochWire, epoch); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, epoch := qualified, witness
			tc.edit(&changed, &epoch)
			got := changed.WithClockEpoch(epoch)
			if got.TimingUncertaintyNS != nil || got.TimingUncertaintyError == "" {
				t.Fatalf("invalid epoch reused a prior bound: %+v", got)
			}
		})
	}
}

func TestOwnerClockProofRejectsUncoveredLifetimes(t *testing.T) {
	for _, lateGuard := range []bool{false, true} {
		t.Run(map[bool]string{false: "early_finish", true: "late_guard"}[lateGuard], func(t *testing.T) {
			var guard *ClockEpochGuard
			startGuard := func() {
				var err error
				guard, err = NewClockEpochGuard(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if closeErr := guard.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				})
			}
			if !lateGuard {
				startGuard()
			}
			owner, err := StartOwner(t.Context(), OwnerConfig{
				Sessions: []OwnerEndpoint{{
					Local: netip.MustParseAddrPort("127.0.0.1:37997"),
					Peer:  netip.MustParseAddrPort("127.0.0.2:37998"),
				}},
				Interval: 100 * time.Millisecond, DetectMultiplier: 3,
			}, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := owner.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			if lateGuard {
				startGuard()
			}
			var witness ClockEpochObservation
			if !lateGuard {
				witness, err = guard.Finish(t.Context())
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = owner.Close(); err != nil {
				t.Fatal(err)
			}
			observation, err := owner.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if lateGuard {
				witness, err = guard.Finish(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				witness.StartedAtUTC = time.Unix(0, observation.ClockCalibrations[0].Calibration.AnchorUnixNS-1).UTC()
			} else {
				observation.StoppedAtUTC = witness.FinishedAtUTC.Add(-time.Nanosecond)
			}
			// Rewritten wall observations simulate rollback; the host clock never changes.
			got := observation.WithClockEpoch(witness)
			if got.TimingUncertaintyNS != nil || got.TimingUncertaintyError == "" {
				t.Fatalf("uncovered lifetime qualified after realtime rollback: bound=%v error=%q",
					got.TimingUncertaintyNS, got.TimingUncertaintyError)
			}
		})
	}
}
