package bfd

import "time"

const unknownTimerName = "unknown"

// TimerKind identifies a fixed owner-loop timer, never a peer.
type TimerKind uint8

// These timer kinds have distinct dispatch and effect measurements.
const (
	TimerControlTX TimerKind = iota
	TimerControlDetection
	TimerEchoTX
	TimerEchoDetection
	TimerKindCount
)

func (k TimerKind) String() string {
	switch k {
	case TimerControlTX:
		return "control_tx"
	case TimerControlDetection:
		return "control_detection"
	case TimerEchoTX:
		return "echo_tx"
	case TimerEchoDetection:
		return "echo_detection"
	default:
		return unknownTimerName
	}
}

// TimerEvent describes one timer observation, not a protocol decision.
type TimerEvent uint8

// A dispatched timer can complete an effect or return without one.
const (
	TimerDispatched TimerEvent = iota
	TimerEffect
	TimerNoEffect
	TimerInvalidDispatch
	TimerInvalidEffect
	TimerEventCount
)

func (e TimerEvent) String() string {
	switch e {
	case TimerDispatched:
		return "dispatch"
	case TimerEffect:
		return "effect"
	case TimerNoEffect:
		return "no_effect"
	case TimerInvalidDispatch:
		return "invalid_dispatch"
	case TimerInvalidEffect:
		return "invalid_effect"
	default:
		return unknownTimerName
	}
}

// timerObservation is confined to one owner loop and never changes deadlines.
type timerObservation struct {
	due                          time.Time
	validatedRemoteDiscriminator uint32
	kind                         TimerKind
	active                       bool
	effected                     bool
}

func (o *timerObservation) begin(metrics MetricsReporter, kind TimerKind, due time.Time) {
	*o = timerObservation{due: due, kind: kind, active: true}
	o.observe(metrics, TimerDispatched, time.Now())
}

func (o *timerObservation) effect(metrics MetricsReporter, kind TimerKind, at time.Time) {
	if !o.active || o.kind != kind || o.effected {
		return
	}
	o.effected = true
	o.observe(metrics, TimerEffect, at)
}

func (o *timerObservation) finish(metrics MetricsReporter) {
	if o.active && !o.effected {
		metrics.ObserveTimerEvent(o.kind, TimerNoEffect, 0)
	}
	*o = timerObservation{}
}

func (o *timerObservation) observe(metrics MetricsReporter, event TimerEvent, at time.Time) {
	delay := at.Sub(o.due)
	if o.due.IsZero() || delay < 0 {
		if event == TimerDispatched {
			event = TimerInvalidDispatch
		} else {
			event = TimerInvalidEffect
		}
		delay = 0
	}
	metrics.ObserveTimerEvent(o.kind, event, delay)
}
