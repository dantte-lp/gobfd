package bfd

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"log/slog"
	"slices"
	"time"
)

// -------------------------------------------------------------------------
// Packet Reception — RFC 5880 Section 6.8.6 Steps 8-18
// -------------------------------------------------------------------------

// handleRecvPacket processes an incoming BFD Control packet.
// Steps 1-7 (basic validation) were done by UnmarshalControlPacket.
// This method implements steps 8-18 of RFC 5880 Section 6.8.6.
func (s *Session) handleRecvPacket(
	ctx context.Context,
	item recvItem,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	pkt := item.pkt
	if outcome := s.checkReceivedPacket(item); outcome != DeliveryAccepted {
		s.recordPacketDrop(outcome)
		s.recordDelivery(DeliveryPacketOwner, outcome)
		return
	}
	defer s.recordDelivery(DeliveryPacketOwner, DeliveryCommitted)

	s.recordValidReceivedPacket()
	s.expireUnsolicitedSetup()
	oldTxInterval, oldTxAllowed := s.calcTxIntervalHot(), s.periodicTxAllowed()

	s.mu.Lock()
	// Step 13: Set bfd.RemoteDiscr = My Discriminator.
	s.remoteDiscr = pkt.MyDiscriminator
	// Step 15: Set bfd.RemoteDemandMode = Demand bit.
	s.remoteDemandMode = pkt.Demand
	// Step 16: Set bfd.RemoteMinRxInterval.
	s.remoteMinRxInterval = durationFromMicroseconds(pkt.RequiredMinRxInterval)
	// Step 17: Set remoteDesiredMinTxInterval + remoteDetectMult.
	s.remoteDesiredMinTxInterval, s.remoteDetectMult = durationFromMicroseconds(pkt.DesiredMinTxInterval), pkt.DetectMult
	s.updateAdminDrainLocked(s.lastValidRecv)
	processFSM := s.updateUnsolicitedReceiveLocked(pkt.State)
	s.mu.Unlock()

	// Stage causal facts; publication waits for the final local FSM result.
	s.stagedRemote = Observation{RemoteState: pkt.State, RemoteDiag: pkt.Diag, RemoteC: pkt.ControlPlaneIndependent}
	s.remoteStaged = true

	// RFC 5880 Section 6.8.6: Final terminates a Poll being transmitted,
	// not one whose initial send failed or only replied to a crossed Poll.
	if pkt.Final && s.pollActive && s.pollSent {
		s.terminatePollSequence()
	}

	// If Poll bit is set, we must reply with Final.
	if pkt.Poll {
		s.pendingFinal = true
	}

	// Reset detection timer on every valid packet (RFC 5880 Section 6.8.4).
	s.resetDetectTimer(detectTimer)

	// Apply FSM event based on received state.
	event := RecvStateToEvent(pkt.State)
	if processFSM {
		s.applyFSMEvent(ctx, event, txTimer, detectTimer)
	} else {
		// Publish valid remote facts without advancing a failed attempt.
		s.executeFSMActions(ctx, FSMResult{OldState: s.cachedState, NewState: s.cachedState},
			txTimer, detectTimer, s.Observation().Cause)
	}

	// RFC 5880 Section 6.5: "the receiving system MUST transmit a BFD
	// Control packet with the Final (F) bit set as soon as practicable."
	// Send immediately if we have a pending Final response or if a state
	// change triggered ActionSendControl.
	if s.pendingFinal {
		s.sendControl(ctx)
		s.resetTxTimer(txTimer)
		return
	}

	// RFC 5880 Sections 6.8.3 and 6.8.6: apply peer-driven changes
	// immediately, measuring the new interval from the previous send.
	// Unchanged receives must not postpone the outstanding TX deadline.
	if oldTxInterval != s.calcTxIntervalHot() || oldTxAllowed != s.periodicTxAllowed() {
		s.scheduleTxTimer(txTimer, s.lastPacketSent)
	}
}

func (s *Session) checkReceivedPacket(item recvItem) DeliveryOutcome {
	pkt := item.pkt
	// RFC 5880 Section 4.1 reserves zero Desired Min TX; accepting it with
	// local Required Min RX zero would arm a zero-duration detection timer.
	// Steps 8-9 also discard authentication mismatches before any mutation.
	if pkt.DesiredMinTxInterval == 0 {
		return DeliveryZeroTX
	}
	if pkt.AuthPresent && s.auth == nil {
		return DeliveryAuthUnexpected
	}
	if !pkt.AuthPresent && s.auth != nil {
		return DeliveryAuthMissing
	}
	if s.auth == nil {
		return DeliveryAccepted
	}
	return s.verifyReceivedAuth(item)
}

func (s *Session) verifyReceivedAuth(item recvItem) DeliveryOutcome {
	// RX may win select while the detection timer is already ready.
	// Expire the old sequence window before authenticating a restarted peer.
	s.maybeResetAuthSeqKnown()
	pkt := item.pkt
	if err := s.auth.Verify(s.authState, s.authKeys, &pkt, item.wire, len(item.wire)); err != nil {
		s.logger.Debug("auth verification failed",
			slog.String("peer", s.peerAddr.String()),
			slog.String("error", err.Error()),
		)
		return DeliveryAuthFailed
	}
	return DeliveryAccepted
}

func (s *Session) recordValidReceivedPacket() {
	s.packetsReceived.Add(1)
	s.metrics.IncPacketsReceived(s.peerAddr, s.localAddr)
	s.lastValidRecv = time.Now()
	s.lastPacketRecv.Store(s.lastValidRecv.UnixNano())
}

// -------------------------------------------------------------------------
// FSM Event Application
// -------------------------------------------------------------------------

// applyFSMEvent runs the FSM and executes resulting actions.
func (s *Session) applyFSMEvent(
	ctx context.Context,
	event Event,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	result := ApplyEvent(s.cachedState, event)
	cause := s.Observation().Cause
	switch {
	case event == EventAdminDown:
		cause = CauseLocalAdmin
	case event == EventTimerExpired && s.cachedState != StateAdminDown &&
		(s.cachedState != StateDown || cause != CausePathDown):
		cause = CauseDetectionTimeout
	case event == EventRecvAdminDown && s.cachedState != StateAdminDown:
		cause = CauseRemoteAdminDown
	case result.Changed && event == EventRecvDown && result.NewState == StateDown:
		cause = CauseRemoteDown
	case result.Changed:
		cause = CauseUnknown
	}
	s.executeFSMActions(ctx, result, txTimer, detectTimer, cause)
	s.announceDown(ctx, result, txTimer)
}

// announceDown transmits a state change to Down at once.
func (s *Session) announceDown(ctx context.Context, result FSMResult, txTimer *time.Timer) {
	// RFC 5880 Section 6.8.7: a packet whose contents differ "SHOULD be
	// transmitted during the interval between periodic Control packet
	// transmissions". Detection expiry and a received Down or AdminDown
	// carry no send action, so announce the new state now. maybeSendControl
	// keeps the remote zero RX, remote Demand, Passive unknown-peer and
	// unsolicited limits; resetTxTimer keeps the Section 6.8.3 slow floor.
	// A pending Final already carries the new state and keeps its own retry.
	if result.Changed && result.NewState == StateDown && !s.pendingFinal &&
		!slices.Contains(result.Actions, ActionSendControl) && ctx.Err() == nil {
		s.maybeSendControl(ctx)
		s.resetTxTimer(txTimer)
	}
}

// executeFSMActions processes the FSMResult and performs side-effects.
func (s *Session) executeFSMActions(
	ctx context.Context,
	result FSMResult,
	txTimer *time.Timer,
	detectTimer *time.Timer,
	cause Cause,
) {
	_, diag := s.pairedStateDiag()
	for _, action := range result.Actions {
		switch action {
		case ActionSetDiagTimeExpired:
			diag = DiagControlTimeExpired
		case ActionSetDiagNeighborDown:
			diag = DiagNeighborDown
		case ActionSetDiagAdminDown:
			diag = DiagAdminDown
		case ActionSetDiagPathDown:
			diag = DiagPathDown
		case ActionSendControl, ActionNotifyUp, ActionNotifyDown:
			// These actions do not select a diagnostic.
		}
	}
	if result.Changed {
		// Timer interruption captures the OLD advertised state before commit.
		s.timerStateChange(result.OldState, result.NewState)
	}
	observation, changed := s.commitObservation(result.NewState, diag, cause)
	s.cachedState = result.NewState
	if changed {
		s.logStateChange(ctx, result, observation)
	}
	for _, action := range result.Actions {
		s.executeAction(ctx, action, txTimer, detectTimer)
	}
}

// logStateChange logs the FSM transition, updates counters, and emits a
// StateChange notification.
func (s *Session) logStateChange(ctx context.Context, result FSMResult, observation Observation) {
	s.logger.LogAttrs(ctx, slog.LevelInfo, "session state changed",
		slog.String("old_state", result.OldState.String()),
		slog.String("new_state", result.NewState.String()),
		slog.String("diag", observation.LocalDiag.String()),
	)
	if result.Changed {
		s.stateTransitions.Add(1)
		s.lastStateChange.Store(time.Now().UnixNano())
		s.metrics.RecordStateTransition(
			s.peerAddr, s.localAddr,
			result.OldState.String(), result.NewState.String(),
		)
	}
	s.emitNotification(result, observation)
}

// executeAction dispatches a single FSM action.
func (s *Session) executeAction(
	ctx context.Context,
	action Action,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	switch action {
	case ActionSendControl:
		// Immediate send + reset TX timer (RFC 5880 Section 6.8.7).
		s.sendControl(ctx)
		s.resetTxTimer(txTimer)
	case ActionNotifyUp:
		// State already set; recalculate timers for Up state.
		s.resetTxTimer(txTimer)
		s.resetDetectTimer(detectTimer)
	case ActionNotifyDown:
		s.resetTxTimer(txTimer)
		s.scheduleUpdatedDetection(detectTimer)
	case ActionSetDiagTimeExpired, ActionSetDiagNeighborDown, ActionSetDiagAdminDown, ActionSetDiagPathDown:
		// Diagnostics were committed with the local state before side effects.
	default:
		s.logger.Warn("unknown FSM action", slog.Int("action", int(action)))
	}
}

// emitNotification sends a StateChange to the notification channel if set.
func (s *Session) emitNotification(result FSMResult, observation Observation) {
	s.mu.RLock()
	attempt := s.unsolicitedAttempt
	failed := s.unsolicitedClaimWanted && s.unsolicitedCleanupAllowedLocked(attempt) &&
		(attempt != 0 || (observation.State == StateDown &&
			(observation.Cause == CauseDetectionTimeout || observation.Cause == CausePathDown)))
	s.mu.RUnlock()
	if failed {
		s.signalUnsolicitedCleanup()
	}
	if s.notifyCh == nil {
		return
	}
	sc := stateChangeOf(snapshotForObservation(s, observation), result.OldState)
	sc.unsolicitedAttempt = attempt
	select {
	case s.notifyCh <- sc:
		s.recordDelivery(DeliveryControlNotification, DeliveryQueued)
	default:
		signalPublicNotificationLoss(s.publicLossWake)
		s.recordDelivery(DeliveryControlNotification, DeliveryQueueFull)
		s.logger.Warn("notification channel full, dropping state change")
	}
}

func stateChangeOf(snap SessionSnapshot, oldState State) StateChange {
	return StateChange{
		Status: snap, LocalDiscr: snap.LocalDiscr, PeerAddr: snap.PeerAddr, LocalAddr: snap.LocalAddr,
		OldState: oldState, NewState: snap.State, Diag: snap.LocalDiag,
		RemoteKnown: snap.RemoteKnown, RemoteState: snap.RemoteState, RemoteDiag: snap.RemoteDiag,
		LocalC: snap.LocalC, RemoteC: snap.RemoteC, Cause: snap.Cause,
		ObservationVersion: snap.ObservationVersion, Ref: snap.Ref,
		Timestamp: time.Now(), Type: snap.Type, Interface: snap.Interface,
	}
}

// -------------------------------------------------------------------------
// Timer Negotiation — RFC 5880 Sections 6.8.2-6.8.4
// -------------------------------------------------------------------------

// calcTxInterval returns the negotiated TX interval.
//
// RFC 5880 Section 6.8.7: "the larger of bfd.DesiredMinTxInterval and
// bfd.RemoteMinRxInterval."
//
// RFC 5880 Section 6.8.3: "When bfd.SessionState is not Up, the system
// MUST set bfd.DesiredMinTxInterval to a value of not less than one
// second (1,000,000 microseconds).".
func (s *Session) calcTxInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	desired := s.desiredMinTxInterval
	// RFC 5880 Section 6.8.3: enforce slow rate when not Up.
	if (s.State() != StateUp || s.floorHeld) && desired < slowTxInterval {
		desired = slowTxInterval
	}
	return max(desired, s.remoteMinRxInterval)
}

// calcDetectionTime returns the detection timeout.
//
// RFC 5880 Section 6.8.4 (Asynchronous mode): "equal to the value of
// Detect Mult received from the remote system, multiplied by the agreed
// transmit interval of the remote system (the greater of
// bfd.RequiredMinRxInterval and the last received Desired Min TX Interval).".
func (s *Session) calcDetectionTime() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.remoteDetectMult == 0 {
		// Before receiving any packet, use local detect mult with slow rate.
		txInterval := s.calcTxIntervalLocked()
		return time.Duration(int64(txInterval) * int64(s.detectMult))
	}
	agreedInterval := max(s.requiredMinRxInterval, s.remoteDesiredMinTxInterval)
	return time.Duration(int64(agreedInterval) * int64(s.remoteDetectMult))
}

func (s *Session) calcTxIntervalLocked() time.Duration {
	desired := s.desiredMinTxInterval
	if (s.State() != StateUp || s.floorHeld) && desired < slowTxInterval {
		desired = slowTxInterval
	}
	return max(desired, s.remoteMinRxInterval)
}

// resetTxTimer resets the TX timer with jittered negotiated interval.
// Uses session-local PRNG and cached state for hot-path performance.
func (s *Session) resetTxTimer(txTimer *time.Timer) {
	// Normal ticks and failed Final retries wait a full interval, avoiding
	// immediate retry loops when the last successful send is far in the past.
	s.scheduleTxTimer(txTimer, time.Time{})
}

// scheduleTxTimer applies the current periodic policy and jitter. previousSend
// anchors peer-driven interval changes to the previous successful send.
func (s *Session) scheduleTxTimer(txTimer *time.Timer, previousSend time.Time) {
	txTimer.Stop()
	if !s.pendingFinal && !s.periodicTxAllowed() {
		return
	}
	var elapsed time.Duration
	if !previousSend.IsZero() {
		elapsed = time.Since(previousSend)
	}
	txTimer.Reset(max(0, s.applyJitter(s.calcTxIntervalHot())-elapsed))
}

// resetDetectTimer resets the detection timer with the calculated timeout.
// Uses cached state for hot-path performance.
func (s *Session) resetDetectTimer(detectTimer *time.Timer) {
	detectTime := s.calcDetectionTimeHot()
	if !detectTimer.Stop() {
		drainTimer(detectTimer)
	}
	detectTimer.Reset(detectTime)
}

// drainTimer non-blockingly drains the timer channel.
func drainTimer(t *time.Timer) {
	select {
	case <-t.C:
	default:
	}
}

// -------------------------------------------------------------------------
// Jitter — RFC 5880 Section 6.8.7
// -------------------------------------------------------------------------

// ApplyJitter applies random jitter to the transmission interval.
//
// RFC 5880 Section 6.8.7:
//   - The interval MUST be reduced by a random value of 0 to 25%.
//   - If bfd.DetectMult == 1: interval MUST be between 75% and 90%.
//   - Otherwise: interval MUST be between 75% and 100%.
//
// Uses a crypto-seeded PRNG for non-cryptographic jitter. Jitter is not a
// security boundary, but seeding from crypto/rand avoids predictable process
// startup state and keeps static security scanners quiet.
func ApplyJitter(interval time.Duration, detectMult uint8) time.Duration {
	if interval <= 0 {
		return interval
	}

	rng := newJitterRNG()
	return applyJitterWithRand(interval, detectMult, rng.IntN)
}

func applyJitterWithRand(interval time.Duration, detectMult uint8, intN func(int) int) time.Duration {
	// RFC 5880 Section 6.8.7:
	//   Normal: reduce by 0-25% (result 75-100%).
	//   DetectMult == 1: reduce by 10-25% (result 75-90%).
	var jitterPercent int
	if detectMult == 1 {
		// 10 + rand(0..15) = reduction of 10-25%.
		jitterPercent = 10 + intN(16)
	} else {
		// rand(0..25) = reduction of 0-25%.
		jitterPercent = intN(26)
	}

	reduction := time.Duration(int64(interval) * int64(jitterPercent) / 100)

	return interval - reduction
}

// applyJitter is the session-local variant of ApplyJitter. It uses
// s.jitterRng (goroutine-confined PRNG) instead of allocating or reading
// randomness on every timer reset.
//
// Same RFC 5880 Section 6.8.7 semantics as the exported ApplyJitter.
func (s *Session) applyJitter(interval time.Duration) time.Duration {
	if interval <= 0 {
		return interval
	}

	return applyJitterWithRand(interval, s.detectMult, s.jitterRng.IntN)
}

type jitterRNG struct {
	state uint64
}

func newJitterRNG() jitterRNG {
	var seed [8]byte
	if _, err := crand.Read(seed[:]); err == nil {
		if v := binary.LittleEndian.Uint64(seed[:]); v != 0 {
			return jitterRNG{state: v}
		}
	}

	now := time.Now()
	fallback := uint64(now.UnixNano()) ^ (uint64(now.UnixMicro()) << 17)
	if fallback == 0 {
		fallback = 0x9e3779b97f4a7c15
	}
	return jitterRNG{state: fallback}
}

func (r *jitterRNG) IntN(n int) int {
	if n <= 1 {
		return 0
	}
	v := r.next() % uint64(n)
	const maxInt = int(^uint(0) >> 1)
	if v > uint64(maxInt) {
		return n - 1
	}
	return int(v)
}

func (r *jitterRNG) next() uint64 {
	x := r.state
	if x == 0 {
		x = 0x9e3779b97f4a7c15
	}
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

// calcTxIntervalHot is the goroutine-confined variant of calcTxInterval.
// Uses s.cachedState instead of s.State() to avoid atomic load on the hot path.
// MUST only be called from the session goroutine (Run/runLoop).
func (s *Session) calcTxIntervalHot() time.Duration {
	desired := s.desiredMinTxInterval
	if (s.cachedState != StateUp || s.floorHeld) && desired < slowTxInterval {
		desired = slowTxInterval
	}
	return max(desired, s.remoteMinRxInterval)
}

// calcDetectionTimeHot is the goroutine-confined variant of calcDetectionTime.
// Uses calcTxIntervalHot for the fallback path.
// MUST only be called from the session goroutine (Run/runLoop).
func (s *Session) calcDetectionTimeHot() time.Duration {
	if s.remoteDetectMult == 0 {
		txInterval := s.calcTxIntervalHot()
		return time.Duration(int64(txInterval) * int64(s.detectMult))
	}
	agreedInterval := max(s.requiredMinRxInterval, s.remoteDesiredMinTxInterval)
	return time.Duration(int64(agreedInterval) * int64(s.remoteDetectMult))
}

// -------------------------------------------------------------------------
// Poll Sequence — RFC 5880 Section 6.5
// -------------------------------------------------------------------------

// terminatePollSequence ends the Poll Sequence and applies pending changes.
// RFC 5880 Section 6.5: "When the system sending the Poll Sequence
// receives a packet with Final, the Poll Sequence is terminated.".
func (s *Session) terminatePollSequence() {
	s.finishTimerPoll()
	s.rebuildCachedPacket()
	s.logger.Debug("poll sequence terminated")
}

// -------------------------------------------------------------------------
// Cached Packet — FRR bfdd pattern
// -------------------------------------------------------------------------

// rebuildCachedPacket pre-serializes the BFD Control packet for transmission.
// This avoids per-packet allocation on the hot path. The packet is rebuilt
// only when parameters or state change.
//
// RFC 5880 Section 6.8.7 specifies all field values for transmitted packets.
func (s *Session) rebuildCachedPacket() bool {
	// RFC 5880 Section 6.7: sign the packet if auth is configured.
	if s.auth != nil {
		pkt := s.buildControlPacket()
		return s.signCachedPacket(&pkt)
	}
	pkt := s.buildControlPacket()
	if _, err := MarshalControlPacket(&pkt, s.cachedPacket); err != nil {
		s.logger.Error("failed to marshal cached packet",
			slog.String("error", err.Error()),
		)
		return false
	}
	return true
}

// signCachedPacket applies authentication and serializes the authenticated
// packet into the cached transmit buffer.
func (s *Session) signCachedPacket(pkt *ControlPacket) bool {
	if err := s.auth.Sign(
		s.authState, s.authKeys, pkt, s.cachedPacket, 0,
	); err != nil {
		s.logger.Error("auth sign failed",
			slog.String("error", err.Error()),
		)
		return false
	}
	if _, err := MarshalControlPacket(pkt, s.cachedPacket); err != nil {
		s.logger.Error("failed to marshal authenticated cached packet",
			slog.String("error", err.Error()),
		)
		return false
	}
	return true
}

// buildControlPacket constructs a ControlPacket from current session state.
// RFC 5880 Section 6.8.7: field-by-field specification of transmitted packets.
func (s *Session) buildControlPacket() ControlPacket {
	// RFC 5880 Section 6.8.3: "When bfd.SessionState is not Up, the
	// system MUST set bfd.DesiredMinTxInterval to a value of not less
	// than one second (1,000,000 microseconds)." This applies to the
	// wire value so the remote peer calculates correct detection time.
	wire := s.advertisedTimers()
	state, diag := s.pairedStateDiag()

	pkt := ControlPacket{
		Version:                   Version,
		Diag:                      diag,
		State:                     state,
		Poll:                      s.pollActive && !s.pendingFinal,
		Final:                     s.pendingFinal,
		ControlPlaneIndependent:   false,
		AuthPresent:               false,
		Demand:                    false, // Demand mode not implemented in MVP.
		Multipoint:                false, // RFC 5880 Section 6.8.7: MUST be zero.
		DetectMult:                s.detectMult,
		MyDiscriminator:           s.localDiscr,
		YourDiscriminator:         s.remoteDiscr,
		DesiredMinTxInterval:      microsecondsFromDuration(wire.TX),
		RequiredMinRxInterval:     microsecondsFromDuration(wire.RX),
		RequiredMinEchoRxInterval: 0, // Echo not implemented in MVP.
	}

	return pkt
}

// -------------------------------------------------------------------------
// Duration <-> Microseconds conversion
// -------------------------------------------------------------------------

// durationFromMicroseconds converts a BFD wire-format microsecond value
// to time.Duration. RFC 5880: all interval fields are in microseconds.
func durationFromMicroseconds(us uint32) time.Duration {
	return time.Duration(int64(us) * int64(time.Microsecond))
}

// microsecondsFromDuration converts time.Duration to BFD wire-format
// microseconds (uint32). Values are truncated, not rounded.
func microsecondsFromDuration(d time.Duration) uint32 {
	return uint32(d / time.Microsecond) // #nosec G115 -- session and echo constructors enforce MaxWireInterval.
}
