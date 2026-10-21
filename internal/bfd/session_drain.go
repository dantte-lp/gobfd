package bfd

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DrainIncompleteError identifies a drain stage that did not finish. Completion
// confirms local writes and a notification window, not peer receipt.
type DrainIncompleteError struct {
	Stage  string
	Reason string
	Err    error
}

func (e *DrainIncompleteError) Error() string {
	return fmt.Sprintf("AdminDown drain incomplete (%s, %s): %v", e.Stage, e.Reason, e.Err)
}
func (e *DrainIncompleteError) Unwrap() error { return e.Err }

// Drain reasons that name the peer rather than a local failure.
const (
	drainReasonPassivePeerUnknown = "passive peer discriminator unknown"
	drainReasonPeerSuppressesTX   = "peer suppresses periodic transmission"
)

// PeerAttributed reports whether the drain ran out of time only because the
// peer never supplied a discriminator or suppresses periodic transmission
// (RFC 5880 Section 6.8.7), with no local admission, commit or owner failure.
func (e *DrainIncompleteError) PeerAttributed() bool {
	waiting := e.Stage == "transmission" || e.Stage == "notification window"
	peer := e.Reason == drainReasonPassivePeerUnknown || e.Reason == drainReasonPeerSuppressesTX
	return waiting && peer && errors.Is(e.Err, context.DeadlineExceeded)
}

type adminDrainProgress struct {
	firstSent   chan struct{}
	firstSentAt time.Time
	maxWindow   time.Duration
	until       time.Time
}

func (s *Session) drainAdminDown(ctx context.Context) error {
	s.mu.Lock()
	if s.adminDrain == nil {
		s.adminDrain = &adminDrainProgress{firstSent: make(chan struct{})}
	}
	progress := s.adminDrain
	complete := s.adminDrainCompleteLocked()
	stopped := s.updateClosed
	s.mu.Unlock()
	if complete {
		return nil
	}
	if stopped {
		return s.incompleteDrain("owner stopped", ErrSessionStopped)
	}
	receipt, err := s.enqueueAdminDown(ctx)
	if err != nil {
		return s.incompleteDrain("admission", err)
	}
	if err := s.waitAdminDown(ctx, receipt); err != nil {
		return s.incompleteDrain("commit", err)
	}
	return s.waitAdminDrain(ctx, progress)
}

func (s *Session) waitAdminDrain(ctx context.Context, progress *adminDrainProgress) error {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		s.mu.RLock()
		until := progress.until
		complete := s.adminDrainCompleteLocked()
		stopped := s.updateClosed
		s.mu.RUnlock()
		if complete {
			return nil
		}
		if stopped {
			return s.incompleteDrain("notification window", ErrSessionStopped)
		}
		stage := "transmission"
		var wake <-chan time.Time
		var first <-chan struct{}
		if until.IsZero() {
			first = progress.firstSent
		} else {
			stage = "notification window"
			timer.Reset(time.Until(until))
			wake = timer.C
		}
		var waitErr error
		select {
		case <-first:
		case <-wake:
		case <-s.ownerDone:
			waitErr = ErrSessionStopped
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
		if waitErr != nil {
			return s.incompleteDrain(stage, waitErr)
		}
	}
}

func (s *Session) adminDrainCompleteLocked() bool {
	return s.adminDrain != nil && !s.adminDrain.until.IsZero() && !time.Now().Before(s.adminDrain.until)
}

func (s *Session) incompleteDrain(stage string, err error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.adminDrainCompleteLocked() {
		return nil
	}
	reason := "owner or caller stopped"
	if s.role == RolePassive && s.remoteDiscr == 0 {
		reason = drainReasonPassivePeerUnknown
	} else if s.remoteMinRxInterval == 0 {
		reason = drainReasonPeerSuppressesTX
	}
	return &DrainIncompleteError{Stage: stage, Reason: reason, Err: err}
}

// The negotiated tuple and its extended drain deadline share one lock.
func (s *Session) updateAdminDrainLocked(now time.Time) {
	progress := s.adminDrain
	if progress == nil || s.State() != StateAdminDown {
		return
	}
	remoteBound := time.Duration(s.detectMult) * max(s.advertisedTimers().TX, s.remoteMinRxInterval)
	localBound := remoteBound
	if s.remoteDetectMult != 0 {
		localBound = time.Duration(s.remoteDetectMult) * max(s.requiredMinRxInterval, s.remoteDesiredMinTxInterval)
	}
	window := max(localBound, remoteBound)
	if window <= progress.maxWindow {
		return
	}
	progress.maxWindow = window
	if !progress.firstSentAt.IsZero() {
		until := now.Add(window)
		if until.After(progress.until) {
			progress.until = until
		}
	}
}

func (s *Session) recordAdminDrainSend(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateAdminDrainLocked(now)
	progress := s.adminDrain
	if progress != nil && progress.firstSentAt.IsZero() {
		progress.firstSentAt = now
		progress.until = now.Add(progress.maxWindow)
		close(progress.firstSent)
	}
}
