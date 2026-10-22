package bfd

import (
	"context"
	"fmt"
)

// sessionReplacement is a desired session whose packet demultiplexing key
// belongs to a session that the reconciling owner alone holds and no longer
// desires, such as a single-hop session without an interface re-keyed to
// multihop. Both cannot be registered (RFC 5880 Section 6.3: initial packets
// demultiplex by addresses), so the new session replaces the old one.
type sessionReplacement struct {
	key       SessionKey
	config    ReconcileConfig
	effective effectiveSessionConfig
	old       *sessionEntry

	cfg   SessionConfig
	discr uint32
	sess  *Session
	lease *SenderLease
}

// replacedEntry returns the session that a new claim for key must replace, or
// nil. The caller holds ownershipMu, which serializes every claim change.
// Only base control sessions of the config owner are replaced; the other
// sources keep their own reconciliation rules.
func (m *Manager) replacedEntry(
	owner SessionOwner,
	key SessionKey,
	cfg SessionConfig,
	desired map[SessionKey]reconcileCandidate,
	taken []sessionReplacement,
) *sessionEntry {
	if owner != configSessionOwner() || key.TransportScope.Kind != TransportScopeBase {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, exists := m.sessionsByPeer[demuxKeyFromSessionConfig(cfg)]
	if !exists || entry.key == key || entry.key.TransportScope.Kind != TransportScopeBase {
		return nil
	}
	if _, want := desired[entry.key]; want {
		return nil
	}
	if _, owned := entry.owners[owner]; !owned || len(entry.owners) != 1 {
		return nil
	}
	for _, r := range taken {
		if r.old == entry {
			return nil
		}
	}
	return entry
}

// replaceSessions prepares every replacement outside the registries and then
// swaps all of them in one Manager.mu section, so packet lookup never finds
// neither session. A pending replacement keeps its old session for the retry;
// any failure discards the prepared sessions and leaves every old one. The
// old sessions leave through the release path: AdminDown drain, then teardown.
func (m *Manager) replaceSessions(
	ctx context.Context,
	owner SessionOwner,
	replacements []sessionReplacement,
	currentClaims map[SessionKey]uint32,
	result *ReconcileResult,
) ([]retiredSession, []*SenderLease) {
	var unusedLeases []*SenderLease
	ready := make([]sessionReplacement, 0, len(replacements))
	for _, r := range replacements {
		// The replacement, not the stale-claim release, retires the old session.
		delete(currentClaims, r.old.key)
		if err := m.prepareReplacement(&r); err != nil {
			if r.lease != nil {
				unusedLeases = append(unusedLeases, r.lease)
			}
			recordClaimError(result, r.key, err)
			continue
		}
		ready = append(ready, r)
	}
	if result.Failed > 0 {
		for _, r := range ready {
			m.discriminators.Release(r.discr)
			unusedLeases = append(unusedLeases, r.lease)
		}
		return nil, unusedLeases
	}

	retired := make([]retiredSession, 0, len(ready))
	counts := make([]fanoutDelivery, 0, 2*len(ready))
	m.mu.Lock()
	for _, r := range ready {
		oldDiscr := r.old.session.LocalDiscriminator()
		counts = append(counts, m.removeSessionLocked(oldDiscr, r.old))
		counts = append(counts, m.insertSessionLocked(ctx, r.key, demuxKeyFromSessionConfig(r.cfg),
			r.effective, owner, r.discr, r.sess, r.lease))
		retired = append(retired, retiredSession{localDiscr: oldDiscr, entry: r.old})
	}
	m.mu.Unlock()
	for _, c := range counts {
		m.reportFanoutDelivery(c)
	}
	for _, r := range ready {
		m.logSessionCreated(r.cfg, r.discr)
	}
	result.Created += len(ready)
	result.Released += len(ready)
	result.wireCreated += len(ready)
	result.wireDestroyed += len(ready)
	return retired, unusedLeases
}

// prepareReplacement builds the new session as claimSession does, without
// registering it.
func (m *Manager) prepareReplacement(r *sessionReplacement) error {
	cfg, err := m.resolveSessionIfIndex(canonicalSessionConfig(r.config.SessionConfig))
	if err != nil {
		return fmt.Errorf("%s: %w", createSessionErrPrefix, err)
	}
	r.lease, err = acquireSenderLease(r.config.SenderLeaseFactory)
	if err != nil {
		return fmt.Errorf("%s: %w", createSessionErrPrefix, err)
	}
	r.cfg = cfg
	r.discr, r.sess, err = m.allocateAndBuild(cfg, r.lease.Sender())
	return err
}
