//go:build linux

package bfdload

import (
	"fmt"
	"math"
	"time"
)

// WithClockEpoch attaches a finished witness and validates the measured window.
// Its bound remains conditional on the calibration model, not a support verdict.
func (o OwnerObservation) WithClockEpoch(epoch ClockEpochObservation) OwnerObservation {
	o.ClockEpoch = &epoch
	o.TimingUncertaintyNS = nil
	bound, err := o.receiveUncertainty(epoch)
	if err != nil {
		o.TimingUncertaintyError = err.Error()
		return o
	}
	o.TimingUncertaintyNS = &bound
	o.TimingUncertaintyError = ""
	return o
}

func (o OwnerObservation) receiveUncertainty(epoch ClockEpochObservation) (uint64, error) {
	if !epoch.valid() {
		return 0, fmt.Errorf("missing completed realtime epoch witness: %w", errClockEpochUnverifiable)
	}
	if !o.stoppedWithin(epoch) || len(o.ClockCalibrations) == 0 {
		return 0, fmt.Errorf("owner shutdown is outside the guarded window: %w", errClockEpochUnverifiable)
	}
	var maximum time.Duration
	for _, clock := range o.ClockCalibrations {
		anchor := clock.Calibration.AnchorUnixNS
		_, bound, valid := clock.Calibration.ConditionalInterval(0)
		if !valid || bound < 0 || !clock.Calibration.CoveredBy(epoch.started, epoch.finished) ||
			anchor < epoch.StartedAtUTC.UnixNano() ||
			anchor > o.StoppedAtUTC.UnixNano() {
			return 0, fmt.Errorf("listener %s has no covered calibration: %w", clock.Binding, errClockEpochUnverifiable)
		}
		maximum = max(maximum, bound)
	}
	return uint64(maximum), nil
}

func (o OwnerObservation) stoppedWithin(epoch ClockEpochObservation) bool {
	return o.Stopped && o.stoppedAt != o.stoppedAt.Round(0) &&
		!o.stoppedAt.Before(epoch.started) && !o.stoppedAt.After(epoch.finished) &&
		!o.StoppedAtUTC.Before(epoch.StartedAtUTC) && !o.StoppedAtUTC.After(epoch.FinishedAtUTC)
}

func (e ClockEpochObservation) valid() bool {
	return e.Mechanism == clockEpochMechanism && e.Unchanged && e.Error == "" && e.ElapsedNS >= 0 &&
		!e.StartedAtUTC.Before(time.Unix(0, 0)) && !e.FinishedAtUTC.After(time.Unix(0, math.MaxInt64)) &&
		e.FinishedAtUTC.After(e.StartedAtUTC)
}
