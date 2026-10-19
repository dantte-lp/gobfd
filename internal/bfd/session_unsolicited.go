package bfd

import "time"

// publishUnsolicitedOwnership requires Manager.mu; the owner consumes the
// latest intent before processing its next event, using the existing wakeup.
func (entry *sessionEntry) publishUnsolicitedOwnership() {
	s := entry.session
	s.mu.Lock()
	entry.publishUnsolicitedOwnershipLocked()
	s.mu.Unlock()
}

// publishUnsolicitedOwnershipLocked requires Manager.mu and Session.mu.
func (entry *sessionEntry) publishUnsolicitedOwnershipLocked() {
	s := entry.session
	_, unsolicited := entry.owners[unsolicitedSessionOwner()]
	s.unsolicitedClaimWanted = unsolicited
	s.unsolicitedOnlyWanted = unsolicited && len(entry.owners) == 1
	select {
	case s.updateWake <- struct{}{}:
	default:
	}
}

// processUnsolicitedLocked shares the timer-intent critical section.
func (s *Session) processUnsolicitedLocked(now time.Time) bool {
	changed := s.unsolicitedOnly != s.unsolicitedOnlyWanted
	s.unsolicitedOnly = s.unsolicitedOnlyWanted
	if !s.unsolicitedClaimWanted {
		s.unsolicitedDeadline = time.Time{}
		return false
	}
	if changed && s.unsolicitedOnly && s.unsolicitedAttempt == 0 {
		s.unsolicitedStopped = true
	}
	failed := changed && s.unsolicitedOnly && s.unsolicitedStopped && s.unsolicitedAttempt != 0
	if !s.unsolicitedDeadline.IsZero() && !now.Before(s.unsolicitedDeadline) {
		s.stopUnsolicitedLocked()
		failed = true
	}
	return failed
}

func (s *Session) stopUnsolicitedLocked() {
	s.unsolicitedStopped = true
	s.unsolicitedDeadline = time.Time{}
}

func (s *Session) unsolicitedCleanupAllowedLocked(attempt uint64) bool {
	return s.unsolicitedAttempt == attempt && s.observation.State != StateAdminDown &&
		(s.unsolicitedStopped || (!s.unsolicitedOnly && s.observation.State == StateDown))
}

func (s *Session) unsolicitedTxAllowed() bool {
	return !s.unsolicitedOnly || !s.unsolicitedStopped || s.cachedState == StateAdminDown
}

// The setup budget is independent of valid receive silence (RFC 9468 Section
// 2). Expiry preserves the RFC 5880 FSM, diagnostic and learned state.
func (s *Session) expireUnsolicitedSetup() {
	s.mu.Lock()
	expired := !s.unsolicitedDeadline.IsZero() && !time.Now().Before(s.unsolicitedDeadline)
	if expired {
		s.stopUnsolicitedLocked()
	}
	s.mu.Unlock()
	if expired {
		s.notifyUnsolicitedFailure()
	}
}

func (s *Session) notifyUnsolicitedFailure() {
	s.mu.RLock()
	if !s.unsolicitedClaimWanted {
		s.mu.RUnlock()
		return
	}
	sc := StateChange{
		LocalDiscr: s.localDiscr, Ref: s.Ref(),
		unsolicitedFailure: true, unsolicitedAttempt: s.unsolicitedAttempt,
	}
	s.mu.RUnlock()
	s.signalUnsolicitedCleanup()
	if s.notifyCh == nil {
		return
	}
	select {
	case s.notifyCh <- sc:
		s.recordDelivery(DeliveryUnsolicitedNotification, DeliveryQueued)
	default:
		s.recordDelivery(DeliveryUnsolicitedNotification, DeliveryQueueFull)
		s.logger.Warn("notification channel full, dropping unsolicited failure")
	}
}

func (s *Session) signalUnsolicitedCleanup() {
	select {
	case s.cleanupWake <- struct{}{}:
	default:
	}
}

// updateUnsolicitedReceiveLocked follows validation/authentication and learned
// timer updates. A subsequent accepted Control packet starts a local attempt;
// it does not identify a new wire epoch. The RFC 5880 FSM remains unchanged.
func (s *Session) updateUnsolicitedReceiveLocked(state State) bool {
	if s.unsolicitedRetiring {
		return false
	}
	if !s.unsolicitedClaimWanted || s.cachedState == StateAdminDown {
		return true
	}
	if state == StateAdminDown && s.unsolicitedStopped && s.unsolicitedAttempt != 0 {
		return true
	}
	if s.unsolicitedStopped || s.unsolicitedStarted.IsZero() {
		s.unsolicitedAttempt++
		s.unsolicitedStopped = false
		s.unsolicitedStarted = s.lastValidRecv
		s.unsolicitedDeadline = time.Time{}
		if s.cachedState != StateUp {
			s.unsolicitedDeadline = s.unsolicitedStarted.Add(s.calcDetectionTimeHot())
		}
	}
	if !s.unsolicitedStopped && !s.unsolicitedDeadline.IsZero() {
		// Use the greatest observed local RX Detection Time within this attempt;
		// identical repeated Down packets cannot refresh the setup budget.
		deadline := s.unsolicitedStarted.Add(s.calcDetectionTimeHot())
		if deadline.After(s.unsolicitedDeadline) {
			s.unsolicitedDeadline = deadline
		}
	}
	return true
}
