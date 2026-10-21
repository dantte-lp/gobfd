package bfd

import (
	"context"
	"log/slog"
	"runtime"
	"time"
)

// -------------------------------------------------------------------------
// Main Goroutine — RFC 5880 Session Lifecycle
// -------------------------------------------------------------------------

// Run starts the session event loop once. It blocks until ctx is cancelled.
// Concurrent calls and calls after owner exit return without starting a loop.
// The session begins in Down state and starts sending BFD Control packets
// according to the configured role and timing parameters.
//
// The event loop processes:
//  1. Incoming packets from recvCh (RFC 5880 Section 6.8.6)
//  2. Transmission timer fires (RFC 5880 Section 6.8.7)
//  3. Detection timer expires (RFC 5880 Section 6.8.4)
//  4. Context cancellation (graceful shutdown)
func (s *Session) Run(ctx context.Context) {
	s.mu.Lock()
	if s.running.Load() || s.updateClosed {
		s.mu.Unlock()
		return
	}
	s.running.Store(true)
	s.mu.Unlock()
	defer func() {
		s.closeTimerUpdates()
		s.senderLease.stopInterrupt()
		s.mu.Lock()
		s.running.Store(false)
		close(s.ownerDone)
		s.mu.Unlock()
	}()
	if ctx.Err() != nil {
		return
	}
	// Pin the session goroutine to an OS thread for sub-millisecond timer
	// precision. BFD detection intervals can be as low as 50ms; OS thread
	// affinity reduces scheduler-induced jitter on timer wakeups.
	s.cachedState = s.State()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	txInterval := s.calcTxIntervalHot()
	txTimer := time.NewTimer(0)
	s.resetTxTimer(txTimer)
	defer txTimer.Stop()

	detectTime := s.calcDetectionTimeHot()
	detectTimer := time.NewTimer(detectTime)
	defer detectTimer.Stop()

	s.logger.Info("session started",
		slog.String("state", s.State().String()),
		slog.Duration("tx_interval", txInterval),
		slog.Duration("detect_time", detectTime),
	)

	s.runLoop(ctx, txTimer, detectTimer)
}

// runLoop is the core select loop, separated from Run for clarity.
func (s *Session) runLoop(
	ctx context.Context,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	updateTimer := time.NewTimer(time.Hour)
	updateTimer.Stop()
	defer updateTimer.Stop()
	// Pre-Run administrative intent must commit before the first timer TX.
	for range len(s.ctrlCh) {
		if ctx.Err() != nil {
			return
		}
		cmd := <-s.ctrlCh
		s.signalControlSpace()
		s.handleControlCommand(ctx, cmd, txTimer, detectTimer)
	}
	for {
		if ctx.Err() != nil {
			return
		}
		s.processTimerUpdates(txTimer, detectTimer)
		s.resetUpdateTimer(updateTimer)
		select {
		case <-ctx.Done():
			s.logger.Info("session stopped")
			return

		case item := <-s.recvCh:
			s.handleRecvPacket(ctx, item, txTimer, detectTimer)

		case cmd := <-s.ctrlCh:
			s.signalControlSpace()
			s.handleControlCommand(ctx, cmd, txTimer, detectTimer)

		case <-txTimer.C:
			s.handleTxTimer(ctx, txTimer)

		case <-detectTimer.C:
			s.handleDetectTimer(ctx, txTimer, detectTimer)
		case <-s.updateWake:
		case <-updateTimer.C:
		}
	}
}

func (s *Session) handleControlCommand(
	ctx context.Context,
	cmd sessionControl,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	switch cmd.kind {
	case controlPathDown:
		if s.cachedState == StateAdminDown {
			s.recordDelivery(DeliveryControlOwner, DeliveryIgnored)
			return
		}
		s.handlePathDown(ctx, txTimer, detectTimer)
	case controlAdminDown:
		s.handleAdminDown(ctx, cmd.receipt, txTimer, detectTimer)
	default:
		s.logger.Warn("unknown session control command", slog.Int("kind", int(cmd.kind)))
		s.recordDelivery(DeliveryControlOwner, DeliveryIgnored)
		return
	}
	s.recordDelivery(DeliveryControlOwner, DeliveryCommitted)
}

func (s *Session) handleAdminDown(
	ctx context.Context,
	receipt chan struct{},
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	result := ApplyEvent(s.cachedState, EventAdminDown)
	s.executeFSMActions(ctx, result, txTimer, detectTimer, CauseLocalAdmin)
	s.mu.Lock()
	s.updateAdminDrainLocked(time.Now())
	needsSend := s.adminDrain != nil && s.adminDrain.firstSentAt.IsZero()
	s.mu.Unlock()
	close(receipt)
	if (result.Changed || needsSend) && ctx.Err() == nil {
		s.maybeSendControl(ctx)
		s.resetTxTimer(txTimer)
	}
}

func (s *Session) handlePathDown(ctx context.Context, txTimer *time.Timer, detectTimer *time.Timer) {
	oldState := s.cachedState
	if oldState == StateAdminDown {
		return
	}
	s.mu.Lock()
	s.unsolicitedStarted = time.Time{}
	if s.unsolicitedClaimWanted {
		s.stopUnsolicitedLocked()
	}
	s.mu.Unlock()

	s.executeFSMActions(ctx, FSMResult{
		OldState: oldState,
		NewState: StateDown,
		Changed:  oldState != StateDown,
		Actions:  []Action{ActionSetDiagPathDown, ActionNotifyDown, ActionSendControl},
	}, txTimer, detectTimer, CausePathDown)
}

// -------------------------------------------------------------------------
// TX Timer Handling — RFC 5880 Section 6.8.7
// -------------------------------------------------------------------------

// handleTxTimer fires on each transmission interval.
func (s *Session) handleTxTimer(ctx context.Context, txTimer *time.Timer) {
	s.expireUnsolicitedSetup()
	s.maybeSendControl(ctx)
	s.resetTxTimer(txTimer)
}

// maybeSendControl checks transmission preconditions and sends if allowed.
func (s *Session) maybeSendControl(ctx context.Context) {
	if s.pendingFinal || s.periodicTxAllowed() {
		s.sendControl(ctx)
	}
}

// periodicTxAllowed applies RFC 5880 Section 6.8.7 periodic transmission
// limits. Final replies bypass periodic limits, including on retry.
func (s *Session) periodicTxAllowed() bool {
	if !s.unsolicitedTxAllowed() {
		return false
	}
	// RFC 5880 Section 6.8.7: "A system MUST NOT transmit BFD Control
	// packets if bfd.RemoteDiscr is zero and the system is taking the
	// Passive role."
	if s.role == RolePassive && s.remoteDiscr == 0 {
		return false
	}
	// RFC 5880 Section 6.8.7: "A system MUST NOT periodically transmit
	// BFD Control packets if bfd.RemoteMinRxInterval is zero."
	if s.remoteMinRxInterval == 0 {
		return false
	}
	return !s.remoteDemandMode || s.cachedState != StateUp || s.RemoteState() != StateUp || s.pollActive
}

// sendControl serializes and sends a BFD Control packet.
// RFC 9764: when paddedPduSize is set, the packet is padded with zeros
// to the configured size. The Length field in the BFD header retains the
// actual (unpadded) protocol length; padding follows the BFD PDU.
func (s *Session) sendControl(ctx context.Context) {
	if !s.unsolicitedTxAllowed() && !s.pendingFinal {
		return
	}
	// RFC 5880 Section 6.8.7 also prohibits Final and direct FSM sends.
	if s.role == RolePassive && s.remoteDiscr == 0 {
		return
	}
	final := s.pendingFinal
	if !s.rebuildCachedPacket() {
		return
	}
	pktLen := int(s.cachedPacket[3]) // Length field at byte 3

	sendLen := pktLen
	if s.paddedPduSize > 0 && int(s.paddedPduSize) > pktLen {
		sendLen = int(s.paddedPduSize)
		// RFC 9764 Section 3: padding MUST be zero.
		// clear only extends beyond the BFD PDU; the buffer was
		// zero-allocated and only BFD header bytes are written.
		clear(s.cachedPacket[pktLen:sendLen])
	}

	if err := s.sender.SendPacket(ctx, s.cachedPacket[:sendLen], s.peerAddr); err != nil {
		s.logger.Warn("failed to send control packet",
			slog.String("error", err.Error()),
		)
		return
	}
	if final {
		s.pendingFinal = false
	} else if s.pollActive {
		s.mu.Lock()
		if !s.pollSent {
			s.pollStarted = time.Now()
		}
		s.pollSent = true
		s.mu.Unlock()
	}
	s.lastPacketSent = time.Now()
	if s.cachedState == StateAdminDown && s.LocalDiag() == DiagAdminDown {
		s.recordAdminDrainSend(s.lastPacketSent)
	}
	s.packetsSent.Add(1)
	s.metrics.IncPacketsSent(s.peerAddr, s.localAddr)
}

// -------------------------------------------------------------------------
// Detection Timer — RFC 5880 Section 6.8.4
// -------------------------------------------------------------------------

// handleDetectTimer fires when the detection time expires without receiving
// a valid packet. RFC 5880 Section 6.8.4: "the local system MUST set
// bfd.SessionState to Down and bfd.LocalDiag to 1.".
func (s *Session) handleDetectTimer(
	ctx context.Context,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	s.maybeResetAuthSeqKnown()
	oldTxInterval, oldTxAllowed := s.calcTxIntervalHot(), s.periodicTxAllowed()
	// RFC 5880 Section 6.8.1 expires the wire discriminator in every state.
	// Section 6.8.18: the peer's Required Min RX binds only while the peer
	// keeps transmitting; after a Detection Time without packets the system
	// "SHOULD reset bfd.RemoteMinRxInterval to its initial value of 1" and
	// "then can transmit at its own rate". Demand returns to its initial value
	// of zero (Section 6.8.1). AdminDown resumes its Section 6.8.16
	// notification; a Passive session stays silent because bfd.RemoteDiscr is
	// now zero (Section 6.8.7).
	s.mu.Lock()
	s.remoteDiscr = 0
	s.remoteMinRxInterval, s.remoteDemandMode = initialRemoteMinRx, false
	s.mu.Unlock()
	// Section 6.8.3: measure a changed interval from the previous send, as the
	// receive path does. FSM actions below may re-arm the timer again; a
	// pending Final retry keeps its armed deadline.
	if !s.pendingFinal && (oldTxInterval != s.calcTxIntervalHot() || oldTxAllowed != s.periodicTxAllowed()) {
		s.scheduleTxTimer(txTimer, s.lastPacketSent)
	}

	// Use cachedState (goroutine-confined) to avoid atomic load.
	// Held erratum 5205 also refreshes Down diagnostics; AdminDown stays inert.
	if s.cachedState == StateAdminDown {
		// Restart detect timer even in Down state to handle re-negotiation.
		detectTimer.Reset(s.calcDetectionTimeHot())
		return
	}
	s.applyFSMEvent(ctx, EventTimerExpired, txTimer, detectTimer)
	if s.cachedState == StateDown {
		s.resetDetectTimer(detectTimer)
	}
}

// maybeResetAuthSeqKnown implements RFC 5880 Section 6.8.1:
// bfd.AuthSeqKnown MUST be reset to false after no packets have been received
// for at least 2x Detection Time.
func (s *Session) maybeResetAuthSeqKnown() {
	if s.authState == nil || !s.authState.AuthSeqKnown {
		return
	}
	if s.lastValidRecv.IsZero() {
		return
	}

	detectTime := s.calcDetectionTimeHot()
	if time.Since(s.lastValidRecv) < 2*detectTime {
		return
	}

	s.authState.AuthSeqKnown = false
}
