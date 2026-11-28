package gobgp

import "time"

// DampeningSnapshot evaluates one peer's recovery suppression at observation time.
type DampeningSnapshot struct {
	Penalty             float64
	Suppressed          bool
	SuppressionDeadline time.Time
}

// PeerStatus combines effective dampening with the last completed handler step.
type PeerStatus struct {
	DampeningSnapshot

	Action      string
	NextAttempt time.Time
}

// Snapshot observes the effective penalty and suppression without recording an event.
func (d *Dampener) Snapshot(peer string) DampeningSnapshot {
	if !d.cfg.Enabled {
		return DampeningSnapshot{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pp, exists := d.peers[peer]
	if !exists {
		return DampeningSnapshot{}
	}
	observed := *pp
	now := d.now()
	d.decayPenalty(&observed, now)
	if observed.suppressed && d.suppressionExpired(&observed, now) {
		return DampeningSnapshot{}
	}
	snapshot := DampeningSnapshot{Penalty: observed.penalty, Suppressed: observed.suppressed}
	if observed.suppressed {
		snapshot.SuppressionDeadline = observed.suppressedSince.Add(d.cfg.MaxSuppressTime)
	}
	return snapshot
}

// PeerStatus reads one canonical peer without accessing Run-owned mutable state.
func (h *Handler) PeerStatus(peer string) PeerStatus {
	h.statusMu.RLock()
	snapshot := h.pendingStatus[peer]
	h.statusMu.RUnlock()
	snapshot.DampeningSnapshot = h.dampener.Snapshot(peer)
	return snapshot
}

// publishPendingStatus copies only completed handler steps; scrapes never wait for RPCs.
func (h *Handler) publishPendingStatus(pending map[string]*pendingAction) {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	clear(h.pendingStatus)
	for peer, action := range pending {
		desired := "up"
		if action.down {
			desired = "down"
		}
		h.pendingStatus[peer] = PeerStatus{Action: desired, NextAttempt: action.due}
	}
}
