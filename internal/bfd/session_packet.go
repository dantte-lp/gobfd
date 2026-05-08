package bfd

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"log/slog"
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

	// Steps 8-9: Auth mismatch check.
	if !s.checkAuthConsistency(pkt) {
		return
	}

	// RFC 5880 Section 6.7: verify authentication if configured.
	if s.auth != nil {
		if err := s.auth.Verify(
			s.authState, s.authKeys, pkt, item.wire, len(item.wire),
		); err != nil {
			s.logger.Debug("auth verification failed",
				slog.String("peer", s.peerAddr.String()),
				slog.String("error", err.Error()),
			)
			return
		}
	}

	s.recordValidReceivedPacket()

	s.mu.Lock()
	// Step 13: Set bfd.RemoteDiscr = My Discriminator.
	s.remoteDiscr = pkt.MyDiscriminator
	// Step 15: Set bfd.RemoteDemandMode = Demand bit.
	s.remoteDemandMode = pkt.Demand
	// Step 16: Set bfd.RemoteMinRxInterval.
	s.remoteMinRxInterval = durationFromMicroseconds(pkt.RequiredMinRxInterval)
	// Step 17: Set remoteDesiredMinTxInterval + remoteDetectMult.
	s.remoteDesiredMinTxInterval = durationFromMicroseconds(pkt.DesiredMinTxInterval)
	s.remoteDetectMult = pkt.DetectMult
	s.mu.Unlock()

	// Step 14: Set bfd.RemoteState.
	s.remoteState.Store(uint32(pkt.State))

	// Poll Sequence: if Final bit set and poll is active, terminate.
	if pkt.Final && s.pollActive {
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
	s.applyFSMEvent(ctx, event, txTimer, detectTimer)

	// RFC 5880 Section 6.5: "the receiving system MUST transmit a BFD
	// Control packet with the Final (F) bit set as soon as practicable."
	// Send immediately if we have a pending Final response or if a state
	// change triggered ActionSendControl.
	if s.pendingFinal {
		s.sendControl(ctx)
		s.resetTxTimer(txTimer)
	}
}

func (s *Session) recordValidReceivedPacket() {
	s.packetsReceived.Add(1)
	s.metrics.IncPacketsReceived(s.peerAddr, s.localAddr)
	s.lastPacketRecv.Store(time.Now().UnixNano())
}

// checkAuthConsistency validates RFC 5880 Section 6.8.6 steps 8-9.
func (s *Session) checkAuthConsistency(pkt *ControlPacket) bool {
	// Step 8: A bit set but no auth configured -> discard.
	if pkt.AuthPresent && s.auth == nil {
		s.logger.Warn("discarding packet: auth present but not configured",
			slog.String("peer", s.peerAddr.String()),
		)
		return false
	}
	// Step 9: A bit clear but auth configured -> discard.
	if !pkt.AuthPresent && s.auth != nil {
		s.logger.Warn("discarding packet: auth not present but configured",
			slog.String("peer", s.peerAddr.String()),
		)
		return false
	}
	return true
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
	s.executeFSMActions(ctx, result, txTimer, detectTimer)
}

// executeFSMActions processes the FSMResult and performs side-effects.
func (s *Session) executeFSMActions(
	ctx context.Context,
	result FSMResult,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	if result.Changed {
		s.state.Store(uint32(result.NewState))
		s.cachedState = result.NewState // goroutine-confined mirror
		s.logStateChange(result)
	}
	for _, action := range result.Actions {
		s.executeAction(ctx, action, txTimer, detectTimer)
	}
}

// logStateChange logs the FSM transition, updates counters, and emits a
// StateChange notification.
func (s *Session) logStateChange(result FSMResult) {
	s.logger.Info("session state changed",
		slog.String("old_state", result.OldState.String()),
		slog.String("new_state", result.NewState.String()),
		slog.String("diag", s.LocalDiag().String()),
	)
	s.stateTransitions.Add(1)
	s.lastStateChange.Store(time.Now().UnixNano())
	s.metrics.RecordStateTransition(
		s.peerAddr, s.localAddr,
		result.OldState.String(), result.NewState.String(),
	)
	s.emitNotification(result)
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
		// RFC 5880 Section 6.8.1: reset remoteDiscr on session failure.
		s.mu.Lock()
		s.remoteDiscr = 0
		s.mu.Unlock()
		s.resetTxTimer(txTimer)
		s.resetDetectTimer(detectTimer)
	case ActionSetDiagTimeExpired:
		s.localDiag.Store(uint32(DiagControlTimeExpired))
	case ActionSetDiagNeighborDown:
		s.localDiag.Store(uint32(DiagNeighborDown))
	case ActionSetDiagAdminDown:
		s.localDiag.Store(uint32(DiagAdminDown))
	default:
		s.logger.Warn("unknown FSM action", slog.Int("action", int(action)))
	}
}

// emitNotification sends a StateChange to the notification channel if set.
func (s *Session) emitNotification(result FSMResult) {
	if s.notifyCh == nil {
		return
	}
	sc := StateChange{
		LocalDiscr: s.localDiscr,
		PeerAddr:   s.peerAddr,
		OldState:   result.OldState,
		NewState:   result.NewState,
		Diag:       s.LocalDiag(),
		Timestamp:  time.Now(),
		Type:       s.sessionType,
		Interface:  s.ifName,
	}
	select {
	case s.notifyCh <- sc:
	default:
		s.logger.Warn("notification channel full, dropping state change")
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
	if s.State() != StateUp && desired < slowTxInterval {
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
	if s.State() != StateUp && desired < slowTxInterval {
		desired = slowTxInterval
	}
	return max(desired, s.remoteMinRxInterval)
}

// resetTxTimer resets the TX timer with jittered negotiated interval.
// Uses session-local PRNG and cached state for hot-path performance.
func (s *Session) resetTxTimer(txTimer *time.Timer) {
	interval := s.calcTxIntervalHot()
	if !txTimer.Stop() {
		drainTimer(txTimer)
	}
	txTimer.Reset(s.applyJitter(interval))
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
	if s.cachedState != StateUp && desired < slowTxInterval {
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
	s.pollActive = false
	s.applyPendingParams()
	s.rebuildCachedPacket()
	s.logger.Debug("poll sequence terminated")
}

// applyPendingParams applies deferred parameter changes after poll completion.
func (s *Session) applyPendingParams() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pendingDesiredMinTx > 0 {
		s.desiredMinTxInterval = s.pendingDesiredMinTx
		s.pendingDesiredMinTx = 0
	}
	if s.pendingRequiredMinRx > 0 {
		s.requiredMinRxInterval = s.pendingRequiredMinRx
		s.pendingRequiredMinRx = 0
	}
}

// -------------------------------------------------------------------------
// Cached Packet — FRR bfdd pattern
// -------------------------------------------------------------------------

// rebuildCachedPacket pre-serializes the BFD Control packet for transmission.
// This avoids per-packet allocation on the hot path. The packet is rebuilt
// only when parameters or state change.
//
// RFC 5880 Section 6.8.7 specifies all field values for transmitted packets.
func (s *Session) rebuildCachedPacket() {
	pkt := s.buildControlPacket()
	// RFC 5880 Section 6.7: sign the packet if auth is configured.
	if s.auth != nil {
		s.signCachedPacket(&pkt)
		return
	}
	if _, err := MarshalControlPacket(&pkt, s.cachedPacket); err != nil {
		s.logger.Error("failed to marshal cached packet",
			slog.String("error", err.Error()),
		)
	}
}

// signCachedPacket applies authentication and serializes the authenticated
// packet into the cached transmit buffer.
func (s *Session) signCachedPacket(pkt *ControlPacket) {
	if err := s.auth.Sign(
		s.authState, s.authKeys, pkt, s.cachedPacket, 0,
	); err != nil {
		s.logger.Error("auth sign failed",
			slog.String("error", err.Error()),
		)
		return
	}
	if _, err := MarshalControlPacket(pkt, s.cachedPacket); err != nil {
		s.logger.Error("failed to marshal authenticated cached packet",
			slog.String("error", err.Error()),
		)
	}
}

// buildControlPacket constructs a ControlPacket from current session state.
// RFC 5880 Section 6.8.7: field-by-field specification of transmitted packets.
func (s *Session) buildControlPacket() ControlPacket {
	// RFC 5880 Section 6.8.3: "When bfd.SessionState is not Up, the
	// system MUST set bfd.DesiredMinTxInterval to a value of not less
	// than one second (1,000,000 microseconds)." This applies to the
	// wire value so the remote peer calculates correct detection time.
	wireTxInterval := s.desiredMinTxInterval
	if s.cachedState != StateUp && wireTxInterval < slowTxInterval {
		wireTxInterval = slowTxInterval
	}

	pkt := ControlPacket{
		Version:                   Version,
		Diag:                      s.LocalDiag(),
		State:                     s.cachedState,
		Poll:                      s.pollActive,
		Final:                     s.pendingFinal,
		ControlPlaneIndependent:   false,
		AuthPresent:               false,
		Demand:                    false, // Demand mode not implemented in MVP.
		Multipoint:                false, // RFC 5880 Section 6.8.7: MUST be zero.
		DetectMult:                s.detectMult,
		MyDiscriminator:           s.localDiscr,
		YourDiscriminator:         s.remoteDiscr,
		DesiredMinTxInterval:      microsecondsFromDuration(wireTxInterval),
		RequiredMinRxInterval:     microsecondsFromDuration(s.requiredMinRxInterval),
		RequiredMinEchoRxInterval: 0, // Echo not implemented in MVP.
	}

	// Clear pendingFinal after building packet (it was consumed).
	s.pendingFinal = false

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
	return uint32(d / time.Microsecond) //nolint:gosec // G115: intentional truncation for BFD wire format
}
