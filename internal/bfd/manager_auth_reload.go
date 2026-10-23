package bfd

import (
	"errors"
	"fmt"
)

// ErrAuthenticationReloadUnsupported rejects an unqualified same-identity
// authentication transition without disclosing configuration or key material.
var ErrAuthenticationReloadUnsupported = errors.New("authentication reload is unsupported")

// AdmitSessionReload checks accepted authentication and live parameter policy,
// then calls accept while ownership changes remain serialized. The callback
// must be short, must not call Manager or resource owners, and must not do I/O.
// Registry locks are released before acceptance; later source application still
// validates claims made after acceptance through the normal reconciliation path.
func (m *Manager) AdmitSessionReload(previous, desired []ReconcileConfig, accept func()) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()

	previousByKey, _, err := compileReconcileConfigs(previous)
	if err != nil {
		return fmt.Errorf("validate current authentication set: %w", ErrAuthenticationReloadUnsupported)
	}
	desiredByKey, desiredOrder, err := compileReconcileConfigs(desired)
	if err != nil {
		return fmt.Errorf("validate desired authentication set: %w", ErrAuthenticationReloadUnsupported)
	}

	m.ownershipMu.Lock()
	defer m.ownershipMu.Unlock()
	m.mu.RLock()
	err = m.validateSessionReloadLocked(previousByKey, desiredByKey, desiredOrder)
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	accept()
	return nil
}

func (m *Manager) validateSessionReloadLocked(
	previousByKey, desiredByKey map[SessionKey]reconcileCandidate,
	desiredOrder []SessionKey,
) error {
	for _, key := range desiredOrder {
		candidate := desiredByKey[key].effective
		prior, retained := previousByKey[key]
		entry, live := m.sessionsByKey[key]
		if (retained &&
			(prior.effective.AuthType != candidate.AuthType || prior.effective.AuthKeys != candidate.AuthKeys)) ||
			(live &&
				(entry.effective.AuthType != candidate.AuthType || entry.effective.AuthKeys != candidate.AuthKeys)) {
			return ErrAuthenticationReloadUnsupported
		}
		if !live {
			continue
		}
		if policyErr := entry.session.validatePacketPolicyUpdate(candidate.updateTuple()); policyErr != nil {
			return policyErr
		}
		_, owned := entry.owners[configSessionOwner()]
		if !entry.canUpdateTimers(configSessionOwner(), candidate) &&
			(entry.effective != candidate || (!owned && entry.session.timerUpdateSnapshot().Unresolved)) {
			return ErrSessionParameterConflict
		}
	}
	return nil
}
