package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// -------------------------------------------------------------------------
// Micro-BFD Group CRUD — RFC 7130
// -------------------------------------------------------------------------

// CreateMicroBFDGroup creates a new micro-BFD group for the given configuration.
// The group is registered in the microGroups map keyed by LAG interface name.
//
// RFC 7130 Section 2: one micro-BFD session per member link. The caller
// (daemon wiring) is responsible for creating per-member BFD sessions
// with SessionTypeMicroBFD and appropriate SO_BINDTODEVICE binding.
//
// Returns ErrMicroBFDGroupExists if a group already exists for the LAG.
func (m *Manager) CreateMicroBFDGroup(cfg MicroBFDConfig) (*MicroBFDGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.microGroups[cfg.LAGInterface]; exists {
		return nil, fmt.Errorf(
			"create micro-BFD group for %q: %w",
			cfg.LAGInterface, ErrMicroBFDGroupExists,
		)
	}

	group, err := NewMicroBFDGroup(cfg, m.logger)
	if err != nil {
		return nil, fmt.Errorf("create micro-BFD group for %q: %w",
			cfg.LAGInterface, err)
	}

	m.microGroups[cfg.LAGInterface] = group

	m.logger.Info("micro-BFD group created",
		slog.String("lag", cfg.LAGInterface),
		slog.Int("members", len(cfg.MemberLinks)),
		slog.Int("min_active", cfg.MinActiveLinks),
	)

	return group, nil
}

// DestroyMicroBFDGroup removes the micro-BFD group for the given LAG interface.
// The caller is responsible for destroying the per-member BFD sessions
// associated with the group beforehand.
//
// Returns ErrMicroBFDGroupNotFound if no group exists for the LAG.
func (m *Manager) DestroyMicroBFDGroup(lagInterface string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.microGroups[lagInterface]; !exists {
		return fmt.Errorf(
			"destroy micro-BFD group %q: %w",
			lagInterface, ErrMicroBFDGroupNotFound,
		)
	}

	delete(m.microGroups, lagInterface)

	m.logger.Info("micro-BFD group destroyed",
		slog.String("lag", lagInterface),
	)

	return nil
}

// MicroBFDGroups returns a snapshot of all active micro-BFD groups.
// The returned slice contains copies; no references to mutable data are held.
func (m *Manager) MicroBFDGroups() []MicroBFDGroupSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshots := make([]MicroBFDGroupSnapshot, 0, len(m.microGroups))
	for _, group := range m.microGroups {
		snapshots = append(snapshots, group.Snapshot())
	}
	return snapshots
}

// LookupMicroBFDGroup returns the micro-BFD group for the given LAG interface.
func (m *Manager) LookupMicroBFDGroup(lagInterface string) (*MicroBFDGroup, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	group, ok := m.microGroups[lagInterface]
	return group, ok
}

// dispatchMicroBFD routes a micro-BFD session state change to the
// appropriate MicroBFDGroup by finding which group contains the session's
// interface as a member link.
func (m *Manager) dispatchMicroBFD(ctx context.Context, sc StateChange) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, group := range m.microGroups {
		changed, err := group.UpdateMemberState(sc.Interface, sc.NewState, sc.LocalDiscr)
		if err != nil {
			// This interface is not a member of this group — try the next one.
			if errors.Is(err, ErrMicroBFDMemberNotFound) {
				continue
			}
			m.logger.Warn("micro-BFD dispatch error",
				slog.String("lag", group.LAGInterface()),
				slog.String("member", sc.Interface),
				slog.String("error", err.Error()),
			)
			continue
		}

		if changed {
			m.logger.Info("micro-BFD aggregate state changed",
				slog.String("lag", group.LAGInterface()),
				slog.Bool("aggregate_up", group.IsUp()),
				slog.Int("up_count", group.UpCount()),
			)
		}
		m.handleMicroBFDActuator(ctx, group, sc, changed)
		return
	}
}

func (m *Manager) handleMicroBFDActuator(
	ctx context.Context,
	group *MicroBFDGroup,
	sc StateChange,
	aggregateChanged bool,
) {
	if m.microActuator == nil {
		return
	}
	ev := MicroBFDMemberEvent{
		LAGInterface:     group.LAGInterface(),
		MemberInterface:  sc.Interface,
		OldState:         sc.OldState,
		NewState:         sc.NewState,
		LocalDiscr:       sc.LocalDiscr,
		AggregateUp:      group.IsUp(),
		AggregateChanged: aggregateChanged,
	}
	if err := m.microActuator.HandleMicroBFDMemberEvent(ctx, ev); err != nil {
		m.logger.Warn("micro-BFD actuator failed",
			slog.String("lag", ev.LAGInterface),
			slog.String("member", ev.MemberInterface),
			slog.String("new_state", ev.NewState.String()),
			slog.String("error", err.Error()),
		)
	}
}

// -------------------------------------------------------------------------
// Micro-BFD Group Reconciliation — SIGHUP reload
// -------------------------------------------------------------------------

// MicroBFDReconcileConfig describes a desired micro-BFD group for reconciliation.
type MicroBFDReconcileConfig struct {
	// Key uniquely identifies the group (LAG interface name).
	Key string

	// Config is the micro-BFD group configuration.
	Config MicroBFDConfig
}

// ReconcileMicroBFDGroups diffs the desired micro-BFD groups against the
// current groups. Groups present in desired but absent are created. Groups
// present in current but absent from desired are destroyed.
//
// Returns the number of groups created and destroyed, and any errors.
// The caller is responsible for creating/destroying per-member sessions.
func (m *Manager) ReconcileMicroBFDGroups(
	desired []MicroBFDReconcileConfig,
) (int, int, error) {
	desiredKeys := make(map[string]MicroBFDReconcileConfig, len(desired))
	for _, rc := range desired {
		desiredKeys[rc.Key] = rc
	}

	currentKeys := m.microBFDGroupKeySet()

	var created, destroyed int
	var errs []error

	// Destroy groups not in desired set.
	for key := range currentKeys {
		if _, want := desiredKeys[key]; want {
			continue
		}

		m.logger.Info("reconcile: destroying removed micro-BFD group",
			slog.String("lag", key),
		)

		if dErr := m.DestroyMicroBFDGroup(key); dErr != nil {
			errs = append(errs, fmt.Errorf("reconcile destroy micro-BFD %s: %w", key, dErr))
			continue
		}
		destroyed++
	}

	// Create groups in desired but not in current.
	for key, rc := range desiredKeys {
		if _, exists := currentKeys[key]; exists {
			continue
		}

		m.logger.Info("reconcile: creating new micro-BFD group",
			slog.String("lag", key),
		)

		if _, cErr := m.CreateMicroBFDGroup(rc.Config); cErr != nil {
			errs = append(errs, fmt.Errorf("reconcile create micro-BFD %s: %w", key, cErr))
			continue
		}
		created++
	}

	var err error
	if len(errs) > 0 {
		err = errors.Join(errs...)
	}

	m.logger.Info("micro-BFD group reconciliation complete",
		slog.Int("created", created),
		slog.Int("destroyed", destroyed),
	)

	return created, destroyed, err
}

// microBFDGroupKeySet returns a set of LAG interface names for all active groups.
func (m *Manager) microBFDGroupKeySet() map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make(map[string]struct{}, len(m.microGroups))
	for lagName := range m.microGroups {
		keys[lagName] = struct{}{}
	}
	return keys
}
