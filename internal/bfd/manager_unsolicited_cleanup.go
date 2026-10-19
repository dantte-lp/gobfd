package bfd

import (
	"context"
	"time"
)

type unsolicitedCleanupPending struct {
	present  bool
	attempt  uint64
	deadline time.Time
}

// scanUnsolicitedCleanup runs only at dispatcher startup or a coalesced failure hint.
func (m *Manager) scanUnsolicitedCleanup(ctx context.Context) {
	if m.unsolicited == nil {
		return
	}
	var failures []StateChange
	m.mu.RLock()
	for discr, entry := range m.sessions {
		if !entry.unsolicited {
			continue
		}
		s := entry.session
		s.mu.RLock()
		attempt, observation := s.unsolicitedAttempt, s.observation
		failed := s.unsolicitedCleanupAllowedLocked(attempt) &&
			(attempt != 0 || (observation.State == StateDown &&
				(observation.Cause == CauseDetectionTimeout || observation.Cause == CausePathDown)))
		s.mu.RUnlock()
		if failed {
			failures = append(failures, StateChange{LocalDiscr: discr, Ref: s.Ref(), unsolicitedAttempt: attempt})
		}
	}
	m.mu.RUnlock()
	for _, failure := range failures {
		m.scheduleUnsolicitedCleanup(ctx, failure)
	}
}

func (m *Manager) scheduleUnsolicitedCleanup(ctx context.Context, sc StateChange) {
	if m.unsolicited == nil {
		return
	}
	m.mu.Lock()
	entry, ok := m.sessions[sc.LocalDiscr]
	eligible := ok && entry.session == sc.Ref.session && entry.unsolicited
	if eligible {
		entry.session.mu.RLock()
		eligible = entry.session.unsolicitedCleanupAllowedLocked(sc.unsolicitedAttempt)
		entry.session.mu.RUnlock()
	}
	if !eligible || (entry.cleanupPending.present && entry.cleanupPending.attempt == sc.unsolicitedAttempt) {
		m.mu.Unlock()
		return
	}
	entry.cleanupPending = unsolicitedCleanupPending{
		present: true, attempt: sc.unsolicitedAttempt,
		deadline: time.Now().Add(m.unsolicited.policy.CleanupTimeout),
	}
	start := entry.cleanupWake == nil
	if start {
		entry.cleanupWake = make(chan struct{}, 1)
	}
	wake := entry.cleanupWake
	m.mu.Unlock()
	if start {
		m.startWorker(func() { m.runUnsolicitedCleanup(ctx, entry) })
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (m *Manager) runUnsolicitedCleanup(ctx context.Context, entry *sessionEntry) {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-entry.done:
			return
		case <-m.shutdownCh:
			return
		default:
		}
		m.mu.RLock()
		pending := entry.cleanupPending
		m.mu.RUnlock()
		timer.Stop()
		var due <-chan time.Time
		if pending.present {
			if !time.Now().Before(pending.deadline) {
				m.cleanupUnsolicitedAttempt(entry.session, pending.attempt)
				m.mu.Lock()
				if entry.cleanupPending.attempt == pending.attempt && entry.cleanupPending.deadline.Equal(pending.deadline) {
					entry.cleanupPending.present = false
				}
				m.mu.Unlock()
				continue
			}
			timer.Reset(time.Until(pending.deadline))
			due = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-m.shutdownCh:
			return
		case <-entry.done:
			return
		case <-entry.cleanupWake:
		case <-due:
		}
	}
}
