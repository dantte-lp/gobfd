package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"time"
)

// microBFDActuatorTimeout bounds one RFC 7130 Section 5 actuator call.
// ponytail: fixed bound; make it configurable when a backend needs longer.
const microBFDActuatorTimeout = 5 * time.Second

// microMemberKey identifies a member link session: RFC 7130 Section 2 runs
// one micro-BFD session per member link and address family.
type microMemberKey struct {
	ifName string
	family AddressFamily
}

func microMemberKeyOf(ifName string, peer netip.Addr) microMemberKey {
	family := AddressFamilyIPv6
	if peer.Unmap().Is4() {
		family = AddressFamilyIPv4
	}
	return microMemberKey{ifName: ifName, family: family}
}

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
// The configuration must satisfy ValidateMicroBFDConfig, including the
// RFC 5880 session contract of its member sessions. Returns
// ErrMicroBFDGroupExists if a group already exists for the LAG and
// ErrMicroBFDMemberOwned if another group owns one of its member links.
func (m *Manager) CreateMicroBFDGroup(cfg MicroBFDConfig) (*MicroBFDGroup, error) {
	if err := ValidateMicroBFDConfig(cfg); err != nil {
		return nil, fmt.Errorf("create micro-BFD group for %q: %w", cfg.LAGInterface, err)
	}
	op, err := m.beginOperation()
	if err != nil {
		return nil, err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	group, err := m.createMicroBFDGroup(cfg)
	m.ownershipMu.Unlock()
	return group, err
}

func (m *Manager) createMicroBFDGroup(cfg MicroBFDConfig) (*MicroBFDGroup, error) {
	group, err := NewMicroBFDGroup(cfg, m.logger)
	if err != nil {
		return nil, fmt.Errorf("create micro-BFD group for %q: %w",
			cfg.LAGInterface, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.microGroups[cfg.LAGInterface]; exists {
		return nil, fmt.Errorf(
			"create micro-BFD group for %q: %w",
			cfg.LAGInterface, ErrMicroBFDGroupExists,
		)
	}
	for _, member := range cfg.MemberLinks {
		if owner, owned := m.microMemberOwnerLocked(member); owned {
			return nil, fmt.Errorf("create micro-BFD group for %q: member %q of %q: %w",
				cfg.LAGInterface, member, owner.LAGInterface(), ErrMicroBFDMemberOwned)
		}
	}

	m.registerMicroBFDGroupLocked(group)
	m.logMicroBFDGroupCreated(cfg)
	return group, nil
}

func (m *Manager) logMicroBFDGroupCreated(cfg MicroBFDConfig) {
	m.logger.Info("micro-BFD group created",
		slog.String("lag", cfg.LAGInterface),
		slog.Int("members", len(cfg.MemberLinks)),
		slog.Int("min_active", cfg.MinActiveLinks),
	)
}

// DestroyMicroBFDGroup removes the micro-BFD group for the given LAG interface.
// The caller is responsible for destroying the per-member BFD sessions
// associated with the group beforehand.
//
// Returns ErrMicroBFDGroupNotFound if no group exists for the LAG.
func (m *Manager) DestroyMicroBFDGroup(lagInterface string) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	err = m.destroyMicroBFDGroup(lagInterface)
	m.ownershipMu.Unlock()
	return err
}

func (m *Manager) destroyMicroBFDGroup(lagInterface string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	group, exists := m.microGroups[lagInterface]
	if !exists {
		return fmt.Errorf(
			"destroy micro-BFD group %q: %w",
			lagInterface, ErrMicroBFDGroupNotFound,
		)
	}

	m.unregisterMicroBFDGroupLocked(group)

	m.logger.Info("micro-BFD group destroyed",
		slog.String("lag", lagInterface),
	)

	return nil
}

// registerMicroBFDGroupLocked adds group and makes it the owner of its
// member links. A transaction that moves a member link replaces the entry of
// the group it removes later. The caller holds Manager.mu for writing.
func (m *Manager) registerMicroBFDGroupLocked(group *MicroBFDGroup) {
	m.microGroups[group.LAGInterface()] = group
	m.indexMicroBFDGroupLocked(group, true)
}

func (m *Manager) indexMicroBFDGroupLocked(group *MicroBFDGroup, replace bool) {
	for _, member := range group.MemberNames() {
		key := microMemberKeyOf(member, group.PeerAddr())
		if _, taken := m.microMembers[key]; replace || !taken {
			m.microMembers[key] = group
		}
	}
}

// unregisterMicroBFDGroupLocked removes group and the member entries it still
// owns. The caller holds Manager.mu for writing.
func (m *Manager) unregisterMicroBFDGroupLocked(group *MicroBFDGroup) {
	delete(m.microGroups, group.LAGInterface())
	for _, member := range group.MemberNames() {
		key := microMemberKeyOf(member, group.PeerAddr())
		if m.microMembers[key] == group {
			delete(m.microMembers, key)
		}
	}
}

// microMemberOwnerLocked returns the group owning member in either address
// family. The caller holds Manager.mu.
func (m *Manager) microMemberOwnerLocked(member string) (*MicroBFDGroup, bool) {
	for _, family := range [...]AddressFamily{AddressFamilyIPv4, AddressFamilyIPv6} {
		if group, owned := m.microMembers[microMemberKey{ifName: member, family: family}]; owned {
			return group, true
		}
	}
	return nil, false
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

// dispatchMicroBFD applies a member session transition to the group that
// owns the member link and address family, and returns the actuator event.
// The caller holds Manager.mu for reading and runs the actuator after it
// releases the lock.
func (m *Manager) dispatchMicroBFD(sc StateChange) (MicroBFDMemberEvent, bool) {
	group, ok := m.microMembers[microMemberKeyOf(sc.Interface, sc.PeerAddr)]
	if !ok {
		return MicroBFDMemberEvent{}, false
	}
	changed, err := group.UpdateMemberState(sc.Interface, sc.NewState, sc.LocalDiscr)
	if err != nil {
		m.logger.Warn("micro-BFD dispatch error",
			slog.String("lag", group.LAGInterface()),
			slog.String("member", sc.Interface),
			slog.String("error", err.Error()),
		)
		return MicroBFDMemberEvent{}, false
	}

	aggregateUp := group.IsUp()
	if changed {
		m.logger.Info("micro-BFD aggregate state changed",
			slog.String("lag", group.LAGInterface()),
			slog.Bool("aggregate_up", aggregateUp),
			slog.Int("up_count", group.UpCount()),
		)
	}
	return MicroBFDMemberEvent{
		LAGInterface:     group.LAGInterface(),
		MemberInterface:  sc.Interface,
		OldState:         sc.OldState,
		NewState:         sc.NewState,
		LocalDiscr:       sc.LocalDiscr,
		AggregateUp:      aggregateUp,
		AggregateChanged: changed,
	}, m.microActuator != nil
}

// handleMicroBFDActuator runs one bounded actuator call without Manager locks.
func (m *Manager) handleMicroBFDActuator(ctx context.Context, ev MicroBFDMemberEvent) {
	ctx, cancel := context.WithTimeout(ctx, microBFDActuatorTimeout)
	defer cancel()
	if err := m.microActuator.HandleMicroBFDMemberEvent(ctx, ev); err != nil {
		m.logger.Warn("micro-BFD actuator failed",
			slog.String("lag", ev.LAGInterface),
			slog.String("member", ev.MemberInterface),
			slog.String("new_state", ev.NewState.String()),
			slog.Bool("deadline_exceeded", errors.Is(ctx.Err(), context.DeadlineExceeded)),
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

	// SenderLeaseFactory returns the sender factory of one member session.
	// Manager calls the returned factory only for a session it must create.
	SenderLeaseFactory func(member string) SenderLeaseFactory
}

// ReconcileMicroBFDGroups reconciles the complete desired group set with
// the member sessions of every group; see ReconcileMicroBFDGroupsDetailed.
//
// Returns the number of groups created and destroyed, and any errors.
func (m *Manager) ReconcileMicroBFDGroups(
	ctx context.Context,
	desired []MicroBFDReconcileConfig,
) (int, int, error) {
	result := m.ReconcileMicroBFDGroupsDetailed(ctx, desired)
	return result.wireCreated, result.wireDestroyed, result.Err()
}

// ReconcileMicroBFDGroupsDetailed reconciles the complete declarative
// Micro-BFD set. Each group commits together with its member sessions,
// claimed by MicroBFDReconciliationOwner. An invalid, ambiguous or
// conflicting candidate changes nothing. A failed member claim rolls back the
// groups and claims of this pass before any committed group or session is
// released. Created and Released count groups.
func (m *Manager) ReconcileMicroBFDGroupsDetailed(
	ctx context.Context,
	desired []MicroBFDReconcileConfig,
) ReconcileResult {
	op, err := m.beginOperation()
	if err != nil {
		return failedReconcileResult(ReconcileErrorLifecycle, err)
	}
	defer op.finish()
	if contextErr := ctx.Err(); contextErr != nil {
		return failedReconcileResult(ReconcileErrorLifecycle, contextErr)
	}

	m.ownershipMu.Lock()
	result, retired, unusedLeases := m.reconcileMicroBFDGroups(ctx, desired)
	m.ownershipMu.Unlock()
	op.unlockMutation()

	m.finishReconcileCleanup(ctx, &result, retired, unusedLeases)
	m.logMicroBFDReconcileResult(result)
	return result
}

func (m *Manager) reconcileMicroBFDGroups(
	ctx context.Context,
	desired []MicroBFDReconcileConfig,
) (ReconcileResult, []retiredSession, []*SenderLease) {
	compiled, err := compileMicroBFDReconcileConfigs(desired)
	if err != nil {
		return failedReconcileResult(ReconcileErrorInvalid, err), nil, nil
	}
	currentGroups := m.microBFDGroupsByKey()
	if conflict := m.microBFDReconcileConflict(compiled.groups, currentGroups); conflict != nil {
		return failedReconcileResult(ReconcileErrorConflict, conflict), nil, nil
	}
	owner := MicroBFDReconciliationOwner()
	currentClaims, err := m.ownerClaimSnapshot(owner, compiled.members, compiled.memberOrder)
	if err != nil {
		return failedReconcileResult(ReconcileErrorConflict, err), nil, nil
	}
	newGroups, err := m.newMicroBFDGroups(desired, currentGroups)
	if err != nil {
		return failedReconcileResult(ReconcileErrorInvalid, err), nil, nil
	}

	// Groups precede their member sessions, so the first member transition
	// finds its aggregate. Claims precede releases, so a failed claim leaves
	// every committed group and session in place.
	m.commitMicroBFDGroups(newGroups)
	result, retired, unusedLeases := m.claimDesiredOwnerSessions(
		ctx, owner, compiled.members, compiled.memberOrder, currentClaims,
	)
	if result.Failed > 0 {
		m.rollbackMicroBFDGroups(newGroups)
		return microBFDGroupResult(result, 0, 0), retired, unusedLeases
	}

	released, staleRetired := m.releaseStaleOwnerClaims(owner, compiled.members, currentClaims)
	result.Errors = append(result.Errors, released.Errors...)
	retired = append(retired, staleRetired...)
	destroyed := m.destroyRemovedMicroBFDGroups(compiled.groups, currentGroups)
	return microBFDGroupResult(result, len(newGroups), destroyed), retired, unusedLeases
}

// microBFDGroupResult reports group-level changes with the member errors.
func microBFDGroupResult(result ReconcileResult, created, destroyed int) ReconcileResult {
	result.Created, result.Released, result.Updated = created, destroyed, 0
	result.wireCreated, result.wireDestroyed = created, destroyed
	result.Failed = len(result.Errors)
	return result
}

func (m *Manager) newMicroBFDGroups(
	desired []MicroBFDReconcileConfig,
	current map[string]*MicroBFDGroup,
) ([]*MicroBFDGroup, error) {
	var groups []*MicroBFDGroup
	for _, rc := range desired {
		if _, exists := current[rc.Key]; exists {
			continue
		}
		group, err := NewMicroBFDGroup(rc.Config, m.logger)
		if err != nil {
			return nil, fmt.Errorf("reconcile create micro-BFD %s: %w", rc.Key, err)
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (m *Manager) commitMicroBFDGroups(groups []*MicroBFDGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, group := range groups {
		m.logger.Info("reconcile: creating new micro-BFD group",
			slog.String("lag", group.LAGInterface()),
		)
		m.registerMicroBFDGroupLocked(group)
	}
}

// rollbackMicroBFDGroups removes groups of a failed pass and restores member
// entries that they replaced.
func (m *Manager) rollbackMicroBFDGroups(groups []*MicroBFDGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, group := range groups {
		m.logger.Info("reconcile: rolling back micro-BFD group",
			slog.String("lag", group.LAGInterface()),
		)
		m.unregisterMicroBFDGroupLocked(group)
	}
	for _, group := range m.microGroups {
		m.indexMicroBFDGroupLocked(group, false)
	}
}

func (m *Manager) destroyRemovedMicroBFDGroups(
	desired map[string]MicroBFDReconcileConfig,
	current map[string]*MicroBFDGroup,
) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var destroyed int
	for key, group := range current {
		if _, want := desired[key]; want || m.microGroups[key] != group {
			continue
		}
		m.logger.Info("reconcile: destroying removed micro-BFD group",
			slog.String("lag", key),
		)
		m.unregisterMicroBFDGroupLocked(group)
		destroyed++
	}
	return destroyed
}

func (m *Manager) logMicroBFDReconcileResult(result ReconcileResult) {
	m.logger.Debug("micro-BFD group reconciliation source result",
		slog.Int("created", result.Created),
		slog.Int("released", result.Released),
		slog.Int("failed", result.Failed),
		slog.Bool("converged", result.Err() == nil),
	)
}

// ValidateMicroBFDReconcileConfigs validates a complete desired Micro-BFD
// set without opening senders or mutating state: group shape, one owning
// LAG per member link and the session contract of every member session.
func ValidateMicroBFDReconcileConfigs(desired []MicroBFDReconcileConfig) error {
	_, err := compileMicroBFDReconcileConfigs(desired)
	return err
}

// compiledMicroBFD is a validated desired set: groups by LAG and their member
// sessions in member order.
type compiledMicroBFD struct {
	groups      map[string]MicroBFDReconcileConfig
	members     map[SessionKey]reconcileCandidate
	memberOrder []SessionKey
}

func compileMicroBFDReconcileConfigs(desired []MicroBFDReconcileConfig) (compiledMicroBFD, error) {
	desiredByKey := make(map[string]MicroBFDReconcileConfig, len(desired))
	memberOwners := make(map[string]string)
	var members []ReconcileConfig
	for _, rc := range desired {
		if rc.Key != rc.Config.LAGInterface {
			return compiledMicroBFD{}, fmt.Errorf(
				"micro-BFD reconcile key %q does not match config LAG %q: %w",
				rc.Key, rc.Config.LAGInterface, ErrMicroBFDConfigConflict,
			)
		}
		if err := validateMicroBFDConfig(rc.Config); err != nil {
			return compiledMicroBFD{}, fmt.Errorf("validate micro-BFD group %q: %w", rc.Key, err)
		}
		if _, exists := desiredByKey[rc.Key]; exists {
			return compiledMicroBFD{}, fmt.Errorf(
				"duplicate desired micro-BFD group %q: %w",
				rc.Key, ErrMicroBFDGroupExists,
			)
		}
		for _, member := range rc.Config.MemberLinks {
			if lag, owned := memberOwners[member]; owned {
				return compiledMicroBFD{}, fmt.Errorf("micro-BFD member %q in groups %q and %q: %w",
					member, lag, rc.Key, ErrMicroBFDMemberOwned)
			}
			memberOwners[member] = rc.Key
		}
		desiredByKey[rc.Key] = rc
		members = append(members, microBFDMemberReconcileConfigs(rc.Config, rc.SenderLeaseFactory)...)
	}
	sessions, order, err := compileReconcileConfigs(members)
	if err != nil {
		return compiledMicroBFD{}, fmt.Errorf("validate micro-BFD member sessions: %w", err)
	}
	return compiledMicroBFD{groups: desiredByKey, members: sessions, memberOrder: order}, nil
}

// microBFDReconcileConflict rejects all same-key configuration conflicts
// before reconciliation changes any group. Replacing a live group is a
// separate operation with different rollback requirements.
func (m *Manager) microBFDReconcileConflict(
	desired map[string]MicroBFDReconcileConfig,
	current map[string]*MicroBFDGroup,
) error {
	for key, rc := range desired {
		group, exists := current[key]
		if !exists || group.matchesReconcileConfig(rc.Config) {
			continue
		}

		conflict := &MicroBFDConfigConflictError{LAGInterface: key}
		m.logger.Debug("micro-BFD group reconciliation source conflict",
			slog.String("lag", key),
			slog.String("error_code", ReconcileErrorConflict.String()),
		)
		return conflict
	}
	return nil
}

// microBFDGroupsByKey returns a snapshot of active groups keyed by LAG name.
func (m *Manager) microBFDGroupsByKey() map[string]*MicroBFDGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return maps.Clone(m.microGroups)
}
