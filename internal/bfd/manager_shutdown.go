package bfd

import "context"

// validStateChangeLocked requires mu and preserves the physical owner fence.
func (m *Manager) validStateChangeLocked(sc StateChange) bool {
	if sc.Type == SessionTypeEcho {
		entry, ok := m.echoSessions[sc.LocalDiscr]
		return ok && entry.session.Ref() == sc.Ref
	}
	entry, ok := m.sessions[sc.LocalDiscr]
	if !ok || entry.session.Ref() != sc.Ref {
		return false
	}
	if sc.unsolicitedFailure {
		entry.session.mu.RLock()
		defer entry.session.mu.RUnlock()
		return entry.unsolicited && entry.session.unsolicitedAttempt == sc.unsolicitedAttempt
	}
	return true
}

func (m *Manager) stopAndDrainNotifications() {
	m.mu.RLock()
	cancels := make([]context.CancelFunc, 0, len(m.sessions)+len(m.echoSessions))
	for _, entry := range m.sessions {
		cancels = append(cancels, entry.cancel)
	}
	for _, entry := range m.echoSessions {
		cancels = append(cancels, entry.cancel)
	}
	m.mu.RUnlock()
	for _, cancel := range cancels {
		cancel()
	}
	close(m.shutdownCh)
	m.activeOps.Wait()
	m.workers.Wait()
	m.closeLegacyStateChanges()
	select {
	case <-m.publicLossWake:
		m.invalidateStateChanges()
	default:
	}
	// Producers and dispatcher have joined; identities remain valid until EOF.
	for len(m.rawNotifyCh) > 0 {
		sc := <-m.rawNotifyCh
		m.mu.RLock()
		valid := m.validStateChangeLocked(sc)
		m.mu.RUnlock()
		if !valid {
			m.RecordDelivery(DeliveryNotificationDispatch, DeliveryStale)
			continue
		}
		if sc.unsolicitedFailure {
			m.RecordDelivery(DeliveryNotificationDispatch, DeliveryAbandoned)
			continue
		}
		counts := m.broadcastStateChange(sc)
		m.RecordDelivery(DeliveryNotificationDispatch, DeliveryAccepted)
		m.reportFanoutDelivery(counts)
	}
	close(m.subscriberShutdownCh)
	m.subscriberWorkers.Wait()
}
