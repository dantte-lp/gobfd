package bfd

import "time"

// TransitionTiming brackets one Up-to-Down owner commit using monotonic values.
// A zero Version denotes unavailable evidence, never a zero-duration observation.
// Echo ValidatedAt is owner acceptance in the preview receiver, not RFC authentication.
type TransitionTiming struct {
	Version                      uint64
	ValidatedAt                  time.Time
	CommitStartedAt              time.Time
	CommitEndedAt                time.Time
	DetectionDue                 time.Time
	DetectionTime                time.Duration
	ValidatedRemoteDiscriminator uint32
}

func (s *Session) commitTransitionObservation(result FSMResult, diag Diag, cause Cause) (Observation, bool) {
	down := result.OldState == StateUp && result.NewState == StateDown
	var timing TransitionTiming
	if down {
		timing.ValidatedAt, timing.DetectionTime = s.lastValidRecv, s.calcDetectionTimeHot()
		if s.timerEvent.active && s.timerEvent.kind == TimerControlDetection {
			timing.DetectionDue = s.timerEvent.due
			timing.ValidatedRemoteDiscriminator = s.timerEvent.validatedRemoteDiscriminator
		}
	}
	// Interruption can apply pending RX; retain the expired inputs before it changes them.
	s.timerStateChange(result.OldState, result.NewState)
	if !down {
		return s.commitObservation(result.NewState, diag, cause)
	}
	timing.CommitStartedAt = time.Now()
	observation, changed := s.commitObservation(StateDown, diag, cause)
	timing.CommitEndedAt = time.Now()
	if changed {
		timing.Version = observation.ObservationVersion
		s.mu.Lock()
		s.transitionTiming = timing
		s.mu.Unlock()
	}
	return observation, changed
}
