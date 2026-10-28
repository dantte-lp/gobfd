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

type timerTuple struct {
	TX, RX             time.Duration
	DetectMult, MinTTL uint8
	PaddedPduSize      uint16
}

func (c effectiveSessionConfig) updateTuple() timerTuple {
	return timerTuple{
		c.DesiredMinTxInterval, c.RequiredMinRxInterval, c.DetectMultiplier, c.MultiHopMinTTL, c.PaddedPduSize,
	}
}

// paddingPreparedSender proves constructor-time DF preparation without I/O.
// Socket liveness remains the receive/runtime owner's responsibility.
type paddingPreparedSender interface{ PaddingPrepared() bool }

func (s *Session) validatePacketPolicyUpdate(tuple timerTuple) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.validatePacketPolicyUpdateLocked(tuple)
}

func (s *Session) validatePacketPolicyUpdateLocked(tuple timerTuple) error {
	if tuple.MinTTL != s.minTTL &&
		(s.sessionType != SessionTypeMultiHop || tuple.MinTTL == 0 || tuple.MinTTL == singleHopTTL) {
		return ErrSessionParameterConflict
	}
	if tuple.PaddedPduSize == s.paddedPduSize {
		return nil
	}
	if ValidatePaddedPduSize(tuple.PaddedPduSize, s.auth != nil) != nil ||
		s.paddedPduSize == 0 || tuple.PaddedPduSize == 0 || s.remoteDemandMode {
		return ErrSessionParameterConflict
	}
	prepared, ok := s.sender.(paddingPreparedSender)
	if !ok || !prepared.PaddingPrepared() {
		return ErrSessionParameterConflict
	}
	return nil
}

func (s *Session) applyPacketPolicyUpdateLocked(tuple timerTuple) {
	s.detectMult, s.minTTL, s.paddedPduSize = tuple.DetectMult, tuple.MinTTL, tuple.PaddedPduSize
}

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
	status.Effective = s.effectiveTimersLocked()
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
		s.timerUpdate.Confirmed = s.effectiveTimersLocked()
	}
	generation, repeated, err := s.resolveTimerGenerationLocked(tuple, generation)
	if err != nil || repeated {
		return s.timerUpdateSnapshotLocked(), err
	}
	if err := s.validatePacketPolicyUpdateLocked(tuple); err != nil {
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
		s.waitingPacket = nil
		s.failTimerUpdateLocked()
		return s.timerUpdateSnapshotLocked(), ErrTimerUpdateFailed
	}
	if s.activeUpdate.Present && s.activeUpdate.Tuple == tuple {
		s.activeUpdate.Generation = generation
		s.waitingUpdate = timerProposal{}
		s.waitingPacket = nil
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
	budget := timerUpdateBudgetMultiplier * time.Duration(max(s.detectMult, tuple.DetectMult)) *
		max(currentTX, proposedTX, s.remoteMinRxInterval)
	proposal.Deadline = now.Add(budget)
	s.timerUpdate.Requested = proposal
	s.timerUpdate.State = timerUpdatePending
	s.timerUpdate.Changed = tuple != s.timerUpdate.Confirmed
	s.waitingUpdate = proposal
	s.waitingPacket = nil
	if int(tuple.PaddedPduSize) > len(s.cachedPacket) {
		s.waitingPacket = make([]byte, max(MaxPacketSize, int(tuple.PaddedPduSize)))
	}
	if !s.activeUpdate.Present && !s.pollActive && s.separateUntil.IsZero() &&
		!s.floorHeld && !s.recoverOnUp && tuple == s.timerUpdate.Confirmed && tuple == s.effectiveTimersLocked() {
		s.waitingUpdate = timerProposal{}
		s.waitingPacket = nil
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
	oldDetectMult := s.detectMult
	s.mu.Lock()
	now := time.Now()
	unsolicitedFailed := s.processUnsolicitedLocked(now)
	s.expireTimerUpdatesLocked(now)
	if !s.separateUntil.IsZero() && !now.Before(s.separateUntil) {
		s.separateUntil = time.Time{}
		s.failedPollSeparation = false
	}
	s.promoteTimerUpdateLocked()
	s.updateAdminDrainLocked(now)
	s.mu.Unlock()
	if unsolicitedFailed {
		s.notifyUnsolicitedFailure()
	}
	jitterChanged := (oldDetectMult == 1) != (s.detectMult == 1)
	if oldTX != s.calcTxIntervalHot() || oldAllowed != s.periodicTxAllowed() || jitterChanged {
		s.scheduleTxTimer(tx, s.lastPacketSent)
	}
	if oldDetection != s.calcDetectionTimeHot() {
		s.scheduleUpdatedDetection(detect)
	}
}

func (s *Session) promoteTimerUpdateLocked() {
	if s.pollActive || !s.separateUntil.IsZero() {
		return
	}
	if s.waitingUpdate.Present && s.validatePacketPolicyUpdateLocked(s.waitingUpdate.Tuple) != nil {
		// Demand may have become active while this proposal waited behind a Poll.
		s.waitingUpdate = timerProposal{}
		s.waitingPacket = nil
		s.failTimerUpdateLocked()
		return
	}
	up := s.cachedState == StateUp
	if up && (s.floorHeld || s.recoverOnUp) {
		s.floorHeld, s.recoverOnUp = false, false
		s.startPollLocked()
		return
	}
	if !s.waitingUpdate.Present {
		return
	}
	// Applied requires the wire to carry the confirmed tuple: an interrupted
	// Poll can leave an unconfirmed tuple effective outside Up.
	if s.waitingUpdate.Tuple == s.timerUpdate.Confirmed && s.waitingUpdate.Tuple == s.effectiveTimersLocked() {
		s.waitingUpdate = timerProposal{}
		s.waitingPacket = nil
		s.timerUpdate.State, s.timerUpdate.Changed = timerUpdateApplied, false
		s.notifyTimerUpdate()
		return
	}
	s.activeUpdate, s.waitingUpdate = s.waitingUpdate, timerProposal{}
	s.timerUpdate.Changed = s.activeUpdate.Tuple != s.timerUpdate.Confirmed
	current := s.effectiveTimersLocked()
	if s.waitingPacket != nil {
		s.cachedPacket, s.waitingPacket = s.waitingPacket, nil
	}
	s.applyPacketPolicyUpdateLocked(s.activeUpdate.Tuple)
	if !s.needsTimerPollLocked(current) {
		// TTL is local; qualified padding and Async DetectMult need no Poll.
		s.timerUpdate.Confirmed = s.activeUpdate.Tuple
		s.timerUpdate.ConfirmedRevision = s.activeUpdate.Revision
		s.timerUpdate.State = timerUpdateApplied
		s.activeUpdate = timerProposal{}
		s.notifyTimerUpdate()
		return
	}
	if up {
		s.desiredMinTxInterval = min(s.desiredMinTxInterval, s.activeUpdate.Tuple.TX)
		s.requiredMinRxInterval = max(s.requiredMinRxInterval, s.activeUpdate.Tuple.RX)
	} else {
		// RFC 5880 Section 6.8.3 defers TX increases and RX decreases only
		// "when bfd.SessionState is Up"; otherwise "timing parameter changes
		// MUST be effected immediately". The Poll still confirms the receipt.
		s.desiredMinTxInterval, s.requiredMinRxInterval = s.activeUpdate.Tuple.TX, s.activeUpdate.Tuple.RX
	}
	s.startPollLocked()
}

func (s *Session) needsTimerPollLocked(current timerTuple) bool {
	next := s.activeUpdate.Tuple
	demand := s.remoteDemandMode && s.cachedState == StateUp && s.observation.RemoteState == StateUp
	return current.TX != next.TX || current.RX != next.RX || (demand && current.DetectMult != next.DetectMult)
}

func (s *Session) expireTimerUpdatesLocked(now time.Time) {
	if s.activeUpdate.Present && !s.activeUpdate.Failed && !now.Before(s.activeUpdate.Deadline) {
		s.activeUpdate.Failed = true
		if s.timerUpdate.Requested.Revision == s.activeUpdate.Revision {
			s.failTimerUpdateLocked()
		}
		if s.cachedState != StateUp {
			// Outside Up the tuple is already effective. Keep its Poll on the wire
			// without the failed proposal so later generations queue behind it;
			// recovery confirms the effective tuple once the session is Up.
			s.activeUpdate = timerProposal{}
			s.recoverOnUp = true
		}
	}
	if s.waitingUpdate.Present && !now.Before(s.waitingUpdate.Deadline) {
		s.waitingUpdate = timerProposal{}
		s.waitingPacket = nil
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
	if !s.unsolicitedDeadline.IsZero() && (deadline.IsZero() || s.unsolicitedDeadline.Before(deadline)) {
		deadline = s.unsolicitedDeadline
	}
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
		s.applyPacketPolicyUpdateLocked(proposal.Tuple)
		s.timerUpdate.Confirmed, s.timerUpdate.ConfirmedRevision = proposal.Tuple, proposal.Revision
		if !proposal.Failed && s.timerUpdate.Requested.Revision == proposal.Revision {
			s.timerUpdate.State = timerUpdateApplied
		}
		s.activeUpdate = timerProposal{}
	} else if !s.floorHeld && !s.recoverOnUp {
		// Startup/recovery confirmation updates protocol truth without
		// changing a failed update receipt into successful application.
		s.timerUpdate.Confirmed = s.effectiveTimersLocked()
	}
	s.updateAdminDrainLocked(now)
	s.notifyTimerUpdate()
}

// timerStateChange preserves recovery ordering across a loss of Up. The
// mandatory non-Up floor always supersedes an interrupted Up transaction.
func (s *Session) timerStateChange(oldState, newState State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch newState {
	case StateDown:
		s.unsolicitedStarted = time.Time{}
		if s.unsolicitedClaimWanted && oldState != StateAdminDown {
			s.stopUnsolicitedLocked()
		}
	case StateUp, StateAdminDown:
		s.unsolicitedDeadline = time.Time{}
	case StateInit:
		// Preserve the attempt budget while establishment is in progress.
	}
	switch {
	case newState != StateUp && oldState == StateUp:
		s.interruptTimerUpdateLocked()
	case newState == StateUp && !s.pollActive && s.separateUntil.IsZero():
		if s.desiredMinTxInterval < slowTxInterval || s.recoverOnUp {
			s.floorHeld, s.recoverOnUp = false, false
			s.startPollLocked()
		}
	case newState == StateUp && s.desiredMinTxInterval < slowTxInterval:
		// A Final in the packet that enters Up is handled before the FSM, so a
		// Poll or its separation is still pending. Hold the one-second floor
		// until promotion starts the slow-to-fast Poll (Section 6.8.3).
		s.floorHeld, s.recoverOnUp = true, true
	}
}

func (s *Session) interruptTimerUpdateLocked() {
	wire := s.advertisedTimers()
	if s.activeUpdate.Present {
		s.desiredMinTxInterval, s.requiredMinRxInterval = s.activeUpdate.Tuple.TX, s.activeUpdate.Tuple.RX
		s.applyPacketPolicyUpdateLocked(s.activeUpdate.Tuple)
	}
	if s.timerUpdate.State == timerUpdatePending {
		s.failTimerUpdateLocked()
	}
	s.activeUpdate, s.waitingUpdate = timerProposal{}, timerProposal{}
	s.waitingPacket = nil
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
	tuple := s.effectiveTimersLocked()
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
	s.updateClosed = true
	if s.adminDrain != nil && time.Now().Before(s.adminDrain.until) {
		s.adminDrain.until = time.Time{}
	}
	if s.timerUpdate.State == timerUpdatePending {
		s.failTimerUpdateLocked()
	}
	s.activeUpdate, s.waitingUpdate = timerProposal{}, timerProposal{}
	s.waitingPacket = nil
	packets, controls := len(s.recvCh), len(s.ctrlCh)
	for range packets {
		<-s.recvCh
	}
	for range controls {
		<-s.ctrlCh
	}
	s.mu.Unlock()
	// Callbacks run outside the admission lock; abandoned receipts stay open.
	for range packets {
		s.recordPacketDrop(DeliveryAbandoned)
		s.recordDelivery(DeliveryPacketOwner, DeliveryAbandoned)
	}
	for range controls {
		s.recordDelivery(DeliveryControlOwner, DeliveryAbandoned)
	}
	s.signalControlSpace()
}

func (s *Session) effectiveTimersLocked() timerTuple {
	return timerTuple{
		s.desiredMinTxInterval, s.requiredMinRxInterval, s.detectMult, s.minTTL, s.paddedPduSize,
	}
}
