package bfd

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrTimerUpdatePending means admitted intent has not completed negotiation.
	ErrTimerUpdatePending = errors.New("timer update pending")
	// ErrTimerUpdateFailed means this desired generation cannot converge.
	ErrTimerUpdateFailed = errors.New("timer update failed")
)

type timerTuple struct{ TX, RX time.Duration }

const timerUpdateBudgetMultiplier = 3

type timerProposal struct {
	Tuple                timerTuple
	Revision, Generation uint64
	Deadline             time.Time
	Present, Failed      bool
}

type timerUpdateState uint8

const (
	timerUpdateApplied timerUpdateState = iota
	timerUpdatePending
	timerUpdateFailed
)

// timerUpdateStatus is a bounded, revision-qualified internal receipt. No
// packet, peer address or caller context is retained in transaction state.
type timerUpdateStatus struct {
	Requested                        timerProposal
	Advertised, Effective, Confirmed timerTuple
	ConfirmedRevision                uint64
	State                            timerUpdateState
	Changed, Unresolved              bool
}

func (s *Session) timerUpdateSnapshot() timerUpdateStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timerUpdateSnapshotLocked()
}

func (s *Session) timerUpdateSnapshotLocked() timerUpdateStatus {
	status := s.timerUpdate
	status.Effective = timerTuple{s.desiredMinTxInterval, s.requiredMinRxInterval}
	status.Advertised = s.advertisedTimers()
	status.Unresolved = s.activeUpdate.Present || s.waitingUpdate.Present ||
		s.pollActive || !s.separateUntil.IsZero() || s.floorHeld || s.recoverOnUp
	if s.timerRevision == 0 {
		status.Confirmed = status.Effective
	}
	if s.State() != StateUp || s.floorHeld {
		status.Effective.TX = max(status.Effective.TX, slowTxInterval)
	}
	return status
}

// admitTimerUpdate is called only after Manager validates the complete set and
// ownership. It never waits for the session loop or the peer.
func (s *Session) admitTimerUpdate(
	ctx context.Context, tuple timerTuple, generation uint64,
) (timerUpdateStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return timerUpdateStatus{}, fmt.Errorf("admit timer update: %w", err)
	}
	if s.updateClosed {
		return s.timerUpdateSnapshotLocked(), ErrTimerUpdateFailed
	}
	if s.timerRevision == 0 {
		s.timerUpdate.Confirmed = timerTuple{s.desiredMinTxInterval, s.requiredMinRxInterval}
	}
	generation, repeated, err := s.resolveTimerGenerationLocked(tuple, generation)
	if err != nil || repeated {
		return s.timerUpdateSnapshotLocked(), err
	}
	now := time.Now()
	if s.failedPollSeparation ||
		(s.activeUpdate.Present && (s.activeUpdate.Failed || !now.Before(s.activeUpdate.Deadline))) {
		// The latest rejected generation is terminal independently of the
		// older Poll, which must remain retryable on the wire.
		s.timerRevision++
		s.timerUpdate.Requested = timerProposal{
			Tuple: tuple, Revision: s.timerRevision, Generation: generation, Present: true, Failed: true,
		}
		s.waitingUpdate = timerProposal{}
		s.failTimerUpdateLocked()
		return s.timerUpdateSnapshotLocked(), ErrTimerUpdateFailed
	}
	if s.activeUpdate.Present && s.activeUpdate.Tuple == tuple {
		s.activeUpdate.Generation = generation
		s.waitingUpdate = timerProposal{}
		s.timerUpdate.Requested = s.activeUpdate
		s.timerUpdate.State = timerUpdatePending
		s.timerUpdate.Changed = tuple != s.timerUpdate.Confirmed
	} else {
		s.queueTimerProposalLocked(tuple, generation, now)
	}
	select {
	case s.updateWake <- struct{}{}:
	default:
	}
	return s.timerUpdateSnapshotLocked(), nil
}

func (s *Session) resolveTimerGenerationLocked(tuple timerTuple, generation uint64) (uint64, bool, error) {
	latest := s.timerUpdate.Requested
	// Generation zero is the compatibility reconcile path: consecutive equal
	// intent observes the same receipt; a different tuple is a fresh request.
	if generation == 0 {
		generation = latest.Generation
		if !latest.Present || latest.Tuple != tuple {
			generation++
		}
	}
	if latest.Present && generation <= latest.Generation {
		if generation != latest.Generation || latest.Tuple != tuple {
			return generation, false, ErrTimerUpdateFailed
		}
		return generation, true, nil
	}
	return generation, false, nil
}

func (s *Session) queueTimerProposalLocked(tuple timerTuple, generation uint64, now time.Time) {
	s.timerRevision++
	proposal := timerProposal{Tuple: tuple, Revision: s.timerRevision, Generation: generation, Present: true}
	currentTX, proposedTX := s.desiredMinTxInterval, tuple.TX
	if s.State() != StateUp || s.floorHeld {
		currentTX, proposedTX = max(currentTX, slowTxInterval), max(proposedTX, slowTxInterval)
	}
	// Valid uint32-microsecond intervals and an uint8 multiplier bound
	// this product below 3.3e15 nanoseconds, within time.Duration.
	budget := timerUpdateBudgetMultiplier * time.Duration(s.detectMult) *
		max(currentTX, proposedTX, s.remoteMinRxInterval)
	proposal.Deadline = now.Add(budget)
	s.timerUpdate.Requested = proposal
	s.timerUpdate.State = timerUpdatePending
	s.timerUpdate.Changed = tuple != s.timerUpdate.Confirmed
	s.waitingUpdate = proposal
	if !s.activeUpdate.Present && !s.pollActive && s.separateUntil.IsZero() &&
		!s.floorHeld && !s.recoverOnUp && tuple == s.timerUpdate.Confirmed {
		s.waitingUpdate = timerProposal{}
		s.timerUpdate.State = timerUpdateApplied
		s.timerUpdate.Changed = false
	}
}

func (s *Session) notifyTimerUpdate() {
	select {
	case s.updateNotify <- struct{}{}:
	default:
	}
}

// processTimerUpdates runs solely on the event loop. Network I/O is never
// performed under mu; promotion uses the already scheduled TX deadline.
func (s *Session) processTimerUpdates(tx, detect *time.Timer) {
	oldTX, oldAllowed, oldDetection := s.calcTxIntervalHot(), s.periodicTxAllowed(), s.calcDetectionTimeHot()
	s.mu.Lock()
	now := time.Now()
	s.expireTimerUpdatesLocked(now)
	if !s.separateUntil.IsZero() && !now.Before(s.separateUntil) {
		s.separateUntil = time.Time{}
		s.failedPollSeparation = false
	}
	s.promoteTimerUpdateLocked()
	s.mu.Unlock()
	if oldTX != s.calcTxIntervalHot() || oldAllowed != s.periodicTxAllowed() {
		s.scheduleTxTimer(tx, s.lastPacketSent)
	}
	if oldDetection != s.calcDetectionTimeHot() {
		s.scheduleUpdatedDetection(detect)
	}
}

func (s *Session) promoteTimerUpdateLocked() {
	if s.pollActive || !s.separateUntil.IsZero() || s.cachedState != StateUp {
		return
	}
	if s.floorHeld || s.recoverOnUp {
		s.floorHeld, s.recoverOnUp = false, false
		s.startPollLocked()
		return
	}
	if !s.waitingUpdate.Present {
		return
	}
	if s.waitingUpdate.Tuple == s.timerUpdate.Confirmed {
		s.waitingUpdate = timerProposal{}
		s.timerUpdate.State, s.timerUpdate.Changed = timerUpdateApplied, false
		s.notifyTimerUpdate()
		return
	}
	s.activeUpdate, s.waitingUpdate = s.waitingUpdate, timerProposal{}
	s.timerUpdate.Changed = s.activeUpdate.Tuple != s.timerUpdate.Confirmed
	s.desiredMinTxInterval = min(s.desiredMinTxInterval, s.activeUpdate.Tuple.TX)
	s.requiredMinRxInterval = max(s.requiredMinRxInterval, s.activeUpdate.Tuple.RX)
	s.startPollLocked()
}

func (s *Session) expireTimerUpdatesLocked(now time.Time) {
	if s.activeUpdate.Present && !s.activeUpdate.Failed && !now.Before(s.activeUpdate.Deadline) {
		s.activeUpdate.Failed = true
		if s.timerUpdate.Requested.Revision == s.activeUpdate.Revision {
			s.failTimerUpdateLocked()
		}
	}
	if s.waitingUpdate.Present && !now.Before(s.waitingUpdate.Deadline) {
		s.waitingUpdate = timerProposal{}
		s.failTimerUpdateLocked()
	}
}

func (s *Session) failTimerUpdateLocked() {
	s.timerUpdate.State = timerUpdateFailed
	s.timerUpdate.Requested.Failed = true
	s.notifyTimerUpdate()
}

func (s *Session) resetUpdateTimer(timer *time.Timer) {
	s.mu.RLock()
	deadline := s.separateUntil
	for _, proposal := range [2]timerProposal{s.activeUpdate, s.waitingUpdate} {
		if proposal.Present && !proposal.Failed && (deadline.IsZero() || proposal.Deadline.Before(deadline)) {
			deadline = proposal.Deadline
		}
	}
	s.mu.RUnlock()
	timer.Stop()
	if !deadline.IsZero() {
		timer.Reset(max(0, time.Until(deadline)))
	}
}

func (s *Session) scheduleUpdatedDetection(timer *time.Timer) {
	elapsed := time.Duration(0)
	if !s.lastValidRecv.IsZero() {
		elapsed = time.Since(s.lastValidRecv)
	}
	timer.Reset(max(0, s.calcDetectionTimeHot()-elapsed))
}

func (s *Session) startPollLocked() {
	s.pollActive, s.pollSent = true, false
	s.pollStarted = time.Time{}
}

func (s *Session) finishTimerPoll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.expireTimerUpdatesLocked(now)
	s.pollActive, s.pollSent = false, false
	s.separateUntil = now.Add(max(time.Microsecond, now.Sub(s.pollStarted)))
	s.pollStarted = time.Time{}
	if s.activeUpdate.Present {
		proposal := s.activeUpdate
		s.failedPollSeparation = proposal.Failed
		s.desiredMinTxInterval, s.requiredMinRxInterval = proposal.Tuple.TX, proposal.Tuple.RX
		s.timerUpdate.Confirmed, s.timerUpdate.ConfirmedRevision = proposal.Tuple, proposal.Revision
		if !proposal.Failed && s.timerUpdate.Requested.Revision == proposal.Revision {
			s.timerUpdate.State = timerUpdateApplied
		}
		s.activeUpdate = timerProposal{}
	} else if !s.floorHeld && !s.recoverOnUp {
		// Startup/recovery confirmation updates protocol truth without
		// changing a failed update receipt into successful application.
		s.timerUpdate.Confirmed = timerTuple{s.desiredMinTxInterval, s.requiredMinRxInterval}
	}
	s.notifyTimerUpdate()
}

// timerStateChange preserves recovery ordering across a loss of Up. The
// mandatory non-Up floor always supersedes an interrupted Up transaction.
func (s *Session) timerStateChange(oldState, newState State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newState != StateUp && oldState == StateUp {
		s.interruptTimerUpdateLocked()
	} else if newState == StateUp && !s.pollActive && s.separateUntil.IsZero() {
		if s.desiredMinTxInterval < slowTxInterval || s.recoverOnUp {
			s.floorHeld, s.recoverOnUp = false, false
			s.startPollLocked()
		}
	}
}

func (s *Session) interruptTimerUpdateLocked() {
	wire := s.advertisedTimers()
	if s.activeUpdate.Present {
		s.desiredMinTxInterval, s.requiredMinRxInterval = s.activeUpdate.Tuple.TX, s.activeUpdate.Tuple.RX
	}
	if s.timerUpdate.State == timerUpdatePending {
		s.failTimerUpdateLocked()
	}
	s.activeUpdate, s.waitingUpdate = timerProposal{}, timerProposal{}
	s.pollActive, s.pollSent = false, false
	s.pollStarted, s.separateUntil = time.Time{}, time.Time{}
	s.failedPollSeparation = false
	s.floorHeld = s.desiredMinTxInterval < slowTxInterval
	s.recoverOnUp = true
	if wire.TX != max(s.desiredMinTxInterval, slowTxInterval) {
		s.startPollLocked()
	}
}

// advertisedTimers is loop-owned (or called under mu by snapshot readers).
func (s *Session) advertisedTimers() timerTuple {
	tuple := timerTuple{s.desiredMinTxInterval, s.requiredMinRxInterval}
	if s.activeUpdate.Present {
		tuple = s.activeUpdate.Tuple
	}
	if s.floorHeld || s.State() != StateUp {
		tuple.TX = max(tuple.TX, slowTxInterval)
	}
	return tuple
}

func (s *Session) closeTimerUpdates() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateClosed = true
	if s.timerUpdate.State == timerUpdatePending {
		s.failTimerUpdateLocked()
	}
	s.activeUpdate, s.waitingUpdate = timerProposal{}, timerProposal{}
}
