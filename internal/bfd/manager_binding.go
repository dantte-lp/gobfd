package bfd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"time"
)

const interfaceRebindTimeout = 5 * time.Second

type interfaceBindingKey struct {
	local netip.Addr
	name  string
}

type resolvedInterfaceBinding struct {
	index int
	err   error
}

// RefreshInterfaceBindings restores existing single-hop transports in place.
// onPending runs outside Manager locks before an affected binding is suspended.
// beforeCommit verifies receive listeners after sender recovery, outside Manager locks.
// Failed work stays pending for the next inventory check.
func (m *Manager) RefreshInterfaceBindings(
	ctx context.Context, onPending func(), beforeCommit func() error,
) (int, error) {
	op, err := m.beginOperation()
	if err != nil {
		return 0, err
	}
	defer op.finish()
	op.unlockMutation()
	ctx, cancel := context.WithTimeout(ctx, interfaceRebindTimeout)
	defer cancel()
	select {
	case m.interfaceRefresh <- struct{}{}:
		defer func() { <-m.interfaceRefresh }()
	case <-m.shutdownCh:
		return 0, ErrManagerClosing
	case <-ctx.Done():
		return 0, fmt.Errorf("admit interface refresh: %w", ctx.Err())
	}
	if m.resolveIfIndex == nil {
		return 0, nil
	}
	policyPending, policyErr := m.refreshUnsolicitedInterfaces(onPending)
	pending, sessionErr := m.refreshSessionBindings(ctx, onPending, beforeCommit)
	return pending + policyPending, errors.Join(sessionErr, policyErr)
}

func (m *Manager) interfaceEntries() []*sessionEntry {
	m.mu.RLock()
	entries := make([]*sessionEntry, 0, len(m.sessions))
	for _, entry := range m.sessions {
		if entry.session.Type() == SessionTypeSingleHop {
			entries = append(entries, entry)
		}
	}
	m.mu.RUnlock()
	slices.SortFunc(entries, func(a, b *sessionEntry) int {
		return cmp.Compare(a.session.LocalDiscriminator(), b.session.LocalDiscriminator())
	})
	return entries
}

func (m *Manager) refreshSessionBindings(
	ctx context.Context, onPending func(), beforeCommit func() error,
) (int, error) {
	var pending int
	var failures []error
	resolved := make(map[interfaceBindingKey]resolvedInterfaceBinding)
	for _, entry := range m.interfaceEntries() {
		if err := ctx.Err(); err != nil {
			return pending, errors.Join(append(failures, err)...)
		}
		s := entry.session
		binding := s.receiveBinding.Load()
		if binding == nil || binding.ifIndex == 0 {
			continue
		}
		key := interfaceBindingKey{local: s.LocalAddr(), name: s.Interface()}
		lookup, known := resolved[key]
		if !known {
			lookup.index, lookup.err = m.resolveIfIndex(key.local, key.name)
			resolved[key] = lookup
		}
		resolveErr := m.restoreInterfaceBinding(ctx, entry, binding, lookup, onPending, beforeCommit)
		if resolveErr != nil && m.interfaceEntryLive(entry) {
			pending++
			failures = append(failures, fmt.Errorf("restore session %d interface: %w", s.LocalDiscriminator(), resolveErr))
		}
	}
	return pending, errors.Join(failures...)
}

func (m *Manager) restoreInterfaceBinding(
	ctx context.Context, entry *sessionEntry, binding *receiveBinding,
	lookup resolvedInterfaceBinding, onPending func(), beforeCommit func() error,
) error {
	s := entry.session
	if lookup.err == nil && lookup.index == binding.ifIndex && !binding.pending {
		return nil
	}
	if onPending != nil {
		onPending()
	}
	if lookup.err == nil && lookup.index > 0 {
		return s.rebindInterface(ctx, lookup.index, func(commitCtx context.Context, prior, next *receiveBinding) error {
			if beforeCommit != nil {
				if err := beforeCommit(); err != nil {
					return fmt.Errorf("verify receive listener before interface commit: %w", err)
				}
			}
			current, checkErr := m.resolveIfIndex(s.LocalAddr(), s.Interface())
			if checkErr != nil || current != lookup.index {
				return errors.Join(ErrInterfaceUnresolved, checkErr)
			}
			return m.commitInterfaceBinding(commitCtx, entry, prior, next)
		})
	}
	return errors.Join(ErrInterfaceUnresolved, lookup.err, s.suspendInterfacePath())
}

func (m *Manager) refreshUnsolicitedInterfaces(onPending func()) (int, error) {
	if m.unsolicited == nil || m.unsolicited.policy == nil {
		return 0, nil
	}
	names := make(map[int]string)
	var failures []error
	for name, config := range m.unsolicited.policy.Interfaces {
		if !config.Enabled || name == "" {
			continue
		}
		index, err := m.resolveIfIndex(netip.Addr{}, name)
		if err != nil || index <= 0 {
			failures = append(failures, errors.Join(ErrInterfaceUnresolved, err))
			continue
		}
		names[index] = name
	}
	prior := m.unsolicited.interfaceNames.Load()
	if prior == nil || !maps.Equal(*prior, names) {
		if onPending != nil {
			onPending()
		}
		m.mu.Lock()
		m.unsolicited.interfaceNames.Store(&names)
		m.mu.Unlock()
	}
	if len(failures) != 0 && onPending != nil {
		onPending()
	}
	return len(failures), errors.Join(failures...)
}

// Manager.mu fences qualified admission against policy-index publication.
func (m *Manager) unsolicitedIngressCurrent(name string, index int) bool {
	if m.resolveIfIndex == nil {
		return true
	}
	if m.unsolicited == nil || index <= 0 {
		return false
	}
	names := m.unsolicited.interfaceNames.Load()
	return names != nil && (*names)[index] == name
}

func (m *Manager) interfaceEntryLive(entry *sessionEntry) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[entry.session.LocalDiscriminator()] == entry
}

func (m *Manager) commitInterfaceBinding(ctx context.Context, entry *sessionEntry, prior, next *receiveBinding) error {
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	if m.lifecycleState != managerOpen {
		return ErrManagerClosing
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.sessions[entry.session.LocalDiscriminator()] != entry {
		return ErrSessionStopped
	}
	return entry.session.commitReceiveBinding(ctx, prior, next)
}

// InterfaceBindingPending also covers API-only sessions outside desired YAML.
func (m *Manager) InterfaceBindingPending() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, entry := range m.sessions {
		if binding := entry.session.receiveBinding.Load(); binding != nil && binding.pending {
			return true
		}
	}
	return false
}
