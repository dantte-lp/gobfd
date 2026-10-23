package bfd

import (
	"errors"
	"fmt"
)

// ErrAuthenticationReloadUnsupported rejects an unqualified same-identity
// authentication transition without disclosing configuration or key material.
var ErrAuthenticationReloadUnsupported = errors.New("authentication reload is unsupported")

// AdmitAuthenticationReload compares accepted intent and live authentication,
// then calls accept while ownership changes remain serialized. The callback
// must be short, must not call Manager or resource owners, and must not do I/O.
// Registry locks are released before acceptance; later source application still
// validates claims made after acceptance through the normal reconciliation path.
func (m *Manager) AdmitAuthenticationReload(previous, desired []ReconcileConfig, accept func()) error {
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
	for _, key := range desiredOrder {
		candidate := desiredByKey[key].effective
		prior, retained := previousByKey[key]
		entry, live := m.sessionsByKey[key]
		if (retained &&
			(prior.effective.AuthType != candidate.AuthType || prior.effective.AuthKeys != candidate.AuthKeys)) ||
			(live &&
				(entry.effective.AuthType != candidate.AuthType || entry.effective.AuthKeys != candidate.AuthKeys)) {
			err = ErrAuthenticationReloadUnsupported
			break
		}
	}
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	accept()
	return nil
}
