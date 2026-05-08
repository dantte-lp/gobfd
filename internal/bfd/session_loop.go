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

// Run starts the session event loop. It blocks until ctx is cancelled.
// The session begins in Down state and starts sending BFD Control packets
// according to the configured role and timing parameters.
//
// The event loop processes:
//  1. Incoming packets from recvCh (RFC 5880 Section 6.8.6)
//  2. Transmission timer fires (RFC 5880 Section 6.8.7)
//  3. Detection timer expires (RFC 5880 Section 6.8.4)
//  4. Context cancellation (graceful shutdown)
func (s *Session) Run(ctx context.Context) {
	// Pin the session goroutine to an OS thread for sub-millisecond timer
	// precision. BFD detection intervals can be as low as 50ms; OS thread
	// affinity reduces scheduler-induced jitter on timer wakeups.
	s.running.Store(true)
	defer s.running.Store(false)
	s.cachedState = s.State()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	txInterval := s.calcTxIntervalHot()
	txTimer := time.NewTimer(s.applyJitter(txInterval))
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
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("session stopped")
			return

		case item := <-s.recvCh:
			s.handleRecvPacket(ctx, item, txTimer, detectTimer)

		case cmd := <-s.ctrlCh:
			s.handleControlCommand(ctx, cmd, txTimer, detectTimer)

		case <-txTimer.C:
			s.handleTxTimer(ctx, txTimer)

		case <-detectTimer.C:
			s.handleDetectTimer(ctx, txTimer, detectTimer)
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
		s.handlePathDown(ctx, txTimer, detectTimer)
	case controlAdminDown:
		s.handleAdminDown(ctx, txTimer, detectTimer)
	default:
		s.logger.Warn("unknown session control command", slog.Int("kind", int(cmd.kind)))
	}
}

func (s *Session) handleAdminDown(ctx context.Context, txTimer *time.Timer, detectTimer *time.Timer) {
	result := ApplyEvent(s.cachedState, EventAdminDown)
	if !result.Changed {
		s.localDiag.Store(uint32(DiagAdminDown))
		return
	}
	result.Actions = append(result.Actions, ActionSendControl)
	s.executeFSMActions(ctx, result, txTimer, detectTimer)
}

func (s *Session) handlePathDown(ctx context.Context, txTimer *time.Timer, detectTimer *time.Timer) {
	oldState := s.cachedState
	s.localDiag.Store(uint32(DiagPathDown))
	if oldState == StateDown || oldState == StateAdminDown {
		return
	}

	s.executeFSMActions(ctx, FSMResult{
		OldState: oldState,
		NewState: StateDown,
		Changed:  true,
		Actions:  []Action{ActionNotifyDown, ActionSendControl},
	}, txTimer, detectTimer)
}

// -------------------------------------------------------------------------
// TX Timer Handling — RFC 5880 Section 6.8.7
// -------------------------------------------------------------------------

// handleTxTimer fires on each transmission interval.
func (s *Session) handleTxTimer(ctx context.Context, txTimer *time.Timer) {
	s.maybeSendControl(ctx)
	txInterval := s.calcTxIntervalHot()
	txTimer.Reset(s.applyJitter(txInterval))
}

// maybeSendControl checks transmission preconditions and sends if allowed.
func (s *Session) maybeSendControl(ctx context.Context) {
	// RFC 5880 Section 6.8.7: "A system MUST NOT transmit BFD Control
	// packets if bfd.RemoteDiscr is zero and the system is taking the
	// Passive role."
	if s.role == RolePassive && s.remoteDiscr == 0 {
		return
	}
	// RFC 5880 Section 6.8.7: "A system MUST NOT periodically transmit
	// BFD Control packets if bfd.RemoteMinRxInterval is zero."
	if s.remoteMinRxInterval == 0 {
		return
	}
	s.sendControl(ctx)
}

// sendControl serializes and sends a BFD Control packet.
// RFC 9764: when paddedPduSize is set, the packet is padded with zeros
// to the configured size. The Length field in the BFD header retains the
// actual (unpadded) protocol length; padding follows the BFD PDU.
func (s *Session) sendControl(ctx context.Context) {
	s.rebuildCachedPacket()
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

	// Use cachedState (goroutine-confined) to avoid atomic load.
	// RFC 5880 Section 6.8.4: only if bfd.SessionState is Init or Up.
	if s.cachedState != StateInit && s.cachedState != StateUp {
		// Restart detect timer even in Down state to handle re-negotiation.
		detectTimer.Reset(s.calcDetectionTimeHot())
		return
	}
	s.applyFSMEvent(ctx, EventTimerExpired, txTimer, detectTimer)
}

// maybeResetAuthSeqKnown implements RFC 5880 Section 6.8.1:
// bfd.AuthSeqKnown MUST be reset to false after no packets have been received
// for at least 2x Detection Time.
func (s *Session) maybeResetAuthSeqKnown() {
	if s.authState == nil || !s.authState.AuthSeqKnown {
		return
	}
	lastNS := s.lastPacketRecv.Load()
	if lastNS == 0 {
		return
	}

	detectTime := s.calcDetectionTimeHot()
	if time.Since(time.Unix(0, lastNS)) < 2*detectTime {
		return
	}

	s.authState.AuthSeqKnown = false
}
