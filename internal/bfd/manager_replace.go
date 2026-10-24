package bfd

import (
	"context"
	"fmt"
)

// sessionReplacement retains a prepared identity until the complete source
// resource pass succeeds. It never starts or changes the old session.
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

// replacementEntryLocked checks a receive-key collision before any factory.
// Caller holds Manager.mu and ownershipMu. Only a sole config-owned, unbound
// base SingleHop <-> MultiHop type change may reuse an occupied receive key.
func (m *Manager) replacementEntryLocked(
	owner SessionOwner, key SessionKey, desired map[SessionKey]reconcileCandidate,
) (*sessionEntry, bool, error) {
	entry, exists := m.sessionsByPeer[demuxKeyFromSessionConfig(desired[key].config.SessionConfig)]
	if !exists || entry.key == key {
		return nil, false, nil
	}
	_, oldStillDesired := desired[entry.key]
	_, owned := entry.owners[owner]
	if owner != configSessionOwner() || !owned || len(entry.owners) != 1 || oldStillDesired ||
		!qualifiedBaseTypeReplacement(entry.key, key) {
		return nil, false, fmt.Errorf("reconcile receive identity: %w", ErrDuplicateSession)
	}
	return entry, true, nil
}

func qualifiedBaseTypeReplacement(previous, desired SessionKey) bool {
	if previous.TransportScope.Kind != TransportScopeBase || desired.TransportScope.Kind != TransportScopeBase ||
		previous.Interface != "" || desired.Interface != "" {
		return false
	}
	switch previous.Type {
	case SessionTypeSingleHop:
		if desired.Type != SessionTypeMultiHop {
			return false
		}
	case SessionTypeMultiHop:
		if desired.Type != SessionTypeSingleHop {
			return false
		}
	default:
		return false
	}
	previous.Type = desired.Type
	return previous == desired
}

// replaceSessions prepares outside registry locks and commits only after all
// claims are available. Old senders and discriminators remain owned until the
// normal retirement drain finishes outside the mutation locks.
func (m *Manager) replaceSessions(
	ctx context.Context, owner SessionOwner, replacements []sessionReplacement,
	currentClaims map[SessionKey]uint32, result *ReconcileResult,
) ([]retiredSession, []*SenderLease) {
	var unusedLeases []*SenderLease
	ready := make([]sessionReplacement, 0, len(replacements))
	for _, replacement := range replacements {
		if err := m.prepareReplacement(ctx, &replacement); err != nil {
			if replacement.lease != nil {
				unusedLeases = append(unusedLeases, replacement.lease)
			}
			recordClaimError(result, replacement.key, err)
			continue
		}
		ready = append(ready, replacement)
	}
	if result.Failed > 0 || result.ResourcePending > 0 {
		return nil, append(unusedLeases, m.discardPreparedReplacements(ready)...)
	}
	retired, counts, err := m.commitReplacements(ctx, owner, ready, currentClaims)
	if err != nil {
		addReconcileError(result, ReconcileErrorCreate, err)
		return nil, append(unusedLeases, m.discardPreparedReplacements(ready)...)
	}
	for _, count := range counts {
		m.reportFanoutDelivery(count)
	}
	for _, replacement := range ready {
		m.logSessionCreated(replacement.cfg, replacement.discr)
	}
	result.Created += len(ready)
	result.Released += len(ready)
	result.wireCreated += len(ready)
	result.wireDestroyed += len(ready)
	return retired, unusedLeases
}

func (m *Manager) discardPreparedReplacements(ready []sessionReplacement) []*SenderLease {
	leases := make([]*SenderLease, 0, len(ready))
	for _, replacement := range ready {
		m.discriminators.Release(replacement.discr)
		leases = append(leases, replacement.lease)
	}
	return leases
}

func (m *Manager) commitReplacements(
	ctx context.Context, owner SessionOwner, ready []sessionReplacement, currentClaims map[SessionKey]uint32,
) ([]retiredSession, []fanoutDelivery, error) {
	retired := make([]retiredSession, 0, len(ready))
	counts := make([]fanoutDelivery, 0, 2*len(ready))
	m.mu.Lock()
	// Cancellation before this boundary leaves every old identity registered.
	// After it the complete swap commits; retirement reports its own outcome.
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("commit replacement sessions: %w", err)
	}
	for _, replacement := range ready {
		oldDiscr := replacement.old.session.LocalDiscriminator()
		counts = append(counts, m.removeSessionLocked(oldDiscr, replacement.old))
		counts = append(counts, m.insertSessionLocked(ctx, replacement.key, demuxKeyFromSessionConfig(replacement.cfg),
			replacement.effective, owner, replacement.discr, replacement.sess, replacement.lease))
		delete(currentClaims, replacement.old.key)
		retired = append(retired, retiredSession{localDiscr: oldDiscr, entry: replacement.old})
	}
	m.mu.Unlock()
	return retired, counts, nil
}

func (m *Manager) prepareReplacement(ctx context.Context, replacement *sessionReplacement) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare replacement: %w", err)
	}
	cfg, err := m.resolveSessionIfIndex(canonicalSessionConfig(replacement.config.SessionConfig))
	if err != nil {
		return fmt.Errorf("prepare replacement: %w", err)
	}
	replacement.lease, err = acquireSenderLease(replacement.config.SenderLeaseFactory)
	if err != nil {
		return fmt.Errorf("prepare replacement sender: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return fmt.Errorf("prepare replacement sender: %w", err)
	}
	replacement.cfg = cfg
	replacement.discr, replacement.sess, err = m.allocateAndBuild(cfg, replacement.lease.Sender())
	if err != nil {
		return fmt.Errorf("prepare replacement session: %w", err)
	}
	return nil
}
