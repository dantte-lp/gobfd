//go:build linux

package bfdload

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestClockWitnessJSONContract(t *testing.T) {
	t.Parallel()

	guard, err := NewClockEpochGuard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := guard.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	calibration := bfd.NewReceiveClock().Calibration()
	stopped := time.Now()
	observation := OwnerObservation{
		stoppedAt: stopped, Stopped: true, StoppedAtUTC: stopped.UTC(), ReferenceQualified: true,
		ClockCalibrations: []OwnerClockObservation{{Calibration: calibration}},
	}
	witness, err := guard.Finish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		epoch   ClockEpochObservation
		covered bool
	}{
		{"covered", witness, true},
		{"rejected", ClockEpochObservation{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			//nolint:musttag // Internal snapshot retains core Observation and DeliveryCounter field names.
			encoded, err := json.Marshal(observation.WithClockEpoch(tc.epoch))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["timing_uncertainty_ns"]) != "null" || string(fields["reference_qualified"]) != "false" {
				t.Errorf("JSON promoted total uncertainty or reference qualification: %s", encoded)
			}
			var reason string
			if err := json.Unmarshal(fields["timing_uncertainty_error"], &reason); err != nil || reason == "" {
				t.Errorf("JSON lost the reason total uncertainty is unknown: error=%v reason=%q", err, reason)
			}
			conditional := fields["conditional_timing_uncertainty_ns"]
			if tc.covered {
				var bound uint64
				if err := json.Unmarshal(conditional, &bound); err != nil || bound == 0 {
					t.Errorf("JSON lost the conditional bound: error=%v bound=%d", err, bound)
				}
			} else if string(conditional) != "null" {
				t.Errorf("JSON retained a conditional bound for rejected witness: %s", conditional)
			}
			var epoch map[string]json.RawMessage
			if err := json.Unmarshal(fields["clock_epoch"], &epoch); err != nil {
				t.Fatal(err)
			}
			wantObserved := "false"
			if tc.covered {
				wantObserved = "true"
			}
			if string(epoch["no_cancellation_observed"]) != wantObserved || epoch["unchanged"] != nil {
				t.Errorf("JSON lost cancellation observation semantics: %s", fields["clock_epoch"])
			}
		})
	}
}
