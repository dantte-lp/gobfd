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
// Fixed bound; make it configurable when a backend needs longer.
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

// CreateMicroBFDGroup claims a micro-BFD group for the compatibility API
// owner; see CreateMicroBFDGroupAs.
func (m *Manager) CreateMicroBFDGroup(cfg MicroBFDConfig) (*MicroBFDGroup, error) {
	return m.CreateMicroBFDGroupAs(cfg, compatibilityAPISessionOwner())
}

// CreateMicroBFDGroupAs claims a micro-BFD group for owner, keyed by LAG
// interface name. Claims follow the session ownership model: an identical
// group of another owner is shared, a divergent one is a
// MicroBFDConfigConflictError, and a second claim by the same owner returns
// ErrMicroBFDGroupExists. A new group starts each member from its existing
// member session and returns ErrMicroBFDMemberOwned if another group owns
// one of its member links.
//
// RFC 7130 Section 2: one micro-BFD session per member link. The API caller
// creates per-member sessions of SessionTypeMicroBFD separately. The
// configuration must satisfy ValidateMicroBFDConfig.
func (m *Manager) CreateMicroBFDGroupAs(cfg MicroBFDConfig, owner SessionOwner) (*MicroBFDGroup, error) {
	if err := ValidateMicroBFDConfig(cfg); err != nil {
		return nil, fmt.Errorf("create micro-BFD group for %q: %w", cfg.LAGInterface, err)
	}
	op, err := m.beginOperation()
	if err != nil {
		return nil, err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	group, err := m.createMicroBFDGroup(cfg, owner)
	m.ownershipMu.Unlock()
	return group, err
}

func (m *Manager) createMicroBFDGroup(cfg MicroBFDConfig, owner SessionOwner) (*MicroBFDGroup, error) {
	group, err := NewMicroBFDGroup(cfg, m.logger)
	if err != nil {
		return nil, fmt.Errorf("create micro-BFD group for %q: %w",
			cfg.LAGInterface, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, exists := m.microGroups[cfg.LAGInterface]; exists {
		if err := existing.claim(cfg, owner); err != nil {
			return nil, err
		}
		return existing, nil
	}
	for _, member := range cfg.MemberLinks {
		if other, owned := m.microMemberOwnerLocked(member); owned {
			return nil, fmt.Errorf("create micro-BFD group for %q: member %q of %q: %w",
				cfg.LAGInterface, member, other.LAGInterface(), ErrMicroBFDMemberOwned)
		}
	}

	group.owners[owner] = struct{}{}
	m.registerMicroBFDGroupLocked(group)
	m.logMicroBFDGroupCreated(cfg)
	return group, nil
}

// claim adds owner to an existing group with the same effective
// configuration. The caller holds Manager.ownershipMu.
func (g *MicroBFDGroup) claim(cfg MicroBFDConfig, owner SessionOwner) error {
	if !g.matchesReconcileConfig(cfg) {
		return &MicroBFDConfigConflictError{LAGInterface: cfg.LAGInterface}
	}
	if _, owned := g.owners[owner]; owned {
		return fmt.Errorf("create micro-BFD group for %q: %w", cfg.LAGInterface, ErrMicroBFDGroupExists)
	}
	g.owners[owner] = struct{}{}
	return nil
}

func (m *Manager) logMicroBFDGroupCreated(cfg MicroBFDConfig) {
	m.logger.Info("micro-BFD group created",
		slog.String("lag", cfg.LAGInterface),
		slog.Int("members", len(cfg.MemberLinks)),
		slog.Int("min_active", cfg.MinActiveLinks),
	)
}

// DestroyMicroBFDGroup releases the compatibility API claim on a group; see
// DestroyMicroBFDGroupAs.
func (m *Manager) DestroyMicroBFDGroup(lagInterface string) error {
	return m.DestroyMicroBFDGroupAs(lagInterface, compatibilityAPISessionOwner())
}

// DestroyMicroBFDGroupAs releases the claim of owner on the group of the LAG
// interface. The group is removed with its last claim. The API caller
// destroys its own per-member sessions.
//
// Returns ErrMicroBFDGroupNotFound if no group exists for the LAG and
// ErrMicroBFDGroupOwnerClaimNotFound if owner holds no claim on it, so the
// API cannot remove a group owned by configuration reload.
func (m *Manager) DestroyMicroBFDGroupAs(lagInterface string, owner SessionOwner) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	err = m.destroyMicroBFDGroup(lagInterface, owner)
	m.ownershipMu.Unlock()
	return err
}

func (m *Manager) destroyMicroBFDGroup(lagInterface string, owner SessionOwner) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	group, exists := m.microGroups[lagInterface]
	if !exists {
		return fmt.Errorf(
			"destroy micro-BFD group %q: %w",
			lagInterface, ErrMicroBFDGroupNotFound,
		)
	}
	if _, owned := group.owners[owner]; !owned {
		return fmt.Errorf("destroy micro-BFD group %q for owner %+v: %w",
			lagInterface, owner, ErrMicroBFDGroupOwnerClaimNotFound)
	}
	delete(group.owners, owner)
	if len(group.owners) > 0 {
		return nil
	}

	m.unregisterMicroBFDGroupLocked(group)

	m.logger.Info("micro-BFD group destroyed",
		slog.String("lag", lagInterface),
	)

	return nil
}

// registerMicroBFDGroupLocked adds group, makes it the owner of its member
// links and seeds each member from its existing session. A transaction that
// moves a member link replaces the entry of the group it removes later. The
// caller holds Manager.mu for writing.
func (m *Manager) registerMicroBFDGroupLocked(group *MicroBFDGroup) {
	m.microGroups[group.LAGInterface()] = group
	m.indexMicroBFDGroupLocked(group, true)
	m.seedMicroBFDGroupLocked(group)
}

// seedMicroBFDGroupLocked starts each member from the state of its existing
// member session and queues an actuator resync for Up members, so a member
// whose session stays Up enters the LAG (RFC 7130 Section 3). The caller
// holds Manager.mu for writing.
func (m *Manager) seedMicroBFDGroupLocked(group *MicroBFDGroup) {
	for _, member := range group.MemberNames() {
		key, err := sessionKeyFromConfig(SessionConfig{
			PeerAddr: group.PeerAddr(), LocalAddr: group.LocalAddr(),
			Interface: member, Type: SessionTypeMicroBFD,
		})
		if err != nil {
			continue
		}
		entry, exists := m.sessionsByKey[key]
		if !exists {
			continue
		}
		state := entry.session.State()
		if _, err := group.UpdateMemberState(member, state, entry.session.LocalDiscriminator()); err != nil ||
			state != StateUp || m.microActuator == nil {
			continue
		}
		m.microResync = append(m.microResync, microResync{group: group, member: member})
		select {
		case m.microResyncWake <- struct{}{}:
		default:
		}
	}
}

// microResync asks RunDispatch to report a member seeded Up to the actuator.
type microResync struct {
	group  *MicroBFDGroup
	member string
}

// dispatchMicroResync reports seeded members that are still registered and
// Up. It runs in the dispatch goroutine, so it keeps the actuator order of
// member transitions; a later transition reports the newer state.
func (m *Manager) dispatchMicroResync(ctx context.Context) {
	m.mu.Lock()
	pending := m.microResync
	m.microResync = nil
	events := make([]MicroBFDMemberEvent, 0, len(pending))
	for _, p := range pending {
		state, discr, ok := p.group.memberState(p.member)
		if !ok || state != StateUp || m.microGroups[p.group.LAGInterface()] != p.group {
			continue
		}
		events = append(events, MicroBFDMemberEvent{
			LAGInterface: p.group.LAGInterface(), MemberInterface: p.member,
			OldState: StateDown, NewState: StateUp, LocalDiscr: discr,
			AggregateUp: p.group.IsUp(),
		})
	}
	m.mu.Unlock()
	for _, ev := range events {
		m.handleMicroBFDActuator(ctx, ev)
	}
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
//
//nolint:contextcheck,nolintlint // Background matches Info/Warn; contextcheck consumes the directive before nolintlint.
func (m *Manager) dispatchMicroBFD(sc StateChange) (MicroBFDMemberEvent, bool) {
	group, ok := m.microMembers[microMemberKeyOf(sc.Interface, sc.PeerAddr)]
	if !ok {
		return MicroBFDMemberEvent{}, false
	}
	changed, err := group.UpdateMemberState(sc.Interface, sc.NewState, sc.LocalDiscr)
	if err != nil {
		m.logger.LogAttrs(context.Background(), slog.LevelWarn, "micro-BFD dispatch error",
			slog.String("lag", group.LAGInterface()),
			slog.String("member", sc.Interface),
			slog.String("error", err.Error()),
		)
		return MicroBFDMemberEvent{}, false
	}

	aggregateUp := group.IsUp()
	if changed {
		m.logger.LogAttrs(context.Background(), slog.LevelInfo, "micro-BFD aggregate state changed",
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
		//nolint:contextcheck // Failure logging retains Warn's background context after the actuator context is canceled.
		m.logger.LogAttrs(context.Background(), slog.LevelWarn, "micro-BFD actuator failed",
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
// session claims of this pass before any group of the pass is published and
// before any committed group or session is released. Reload claims a group
// like a session: it shares an identical API group, releases only its own
// claim and removes a group with its last claim. Created and Released count
// group claims; the legacy tuple counts registered and removed groups.
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
	owner := MicroBFDReconciliationOwner()
	currentGroups := m.microBFDGroupsByKey()
	if conflict := m.microBFDReconcileConflict(compiled.groups, currentGroups); conflict != nil {
		return failedReconcileResult(ReconcileErrorConflict, conflict), nil, nil
	}
	if conflict := m.microBFDSurvivorConflict(compiled, currentGroups, owner); conflict != nil {
		return failedReconcileResult(ReconcileErrorConflict, conflict), nil, nil
	}
	currentClaims, err := m.ownerClaimSnapshot(owner, compiled.members, compiled.memberOrder)
	if err != nil {
		return failedReconcileResult(ReconcileErrorConflict, err), nil, nil
	}
	newGroups, err := m.newMicroBFDGroups(desired, currentGroups)
	if err != nil {
		return failedReconcileResult(ReconcileErrorInvalid, err), nil, nil
	}

	// Member claims precede every group change: a failed claim rolls back only
	// its own session claims, and no dispatch or actuator event ever sees a
	// group of the failed pass. Publishing a group seeds its members from the
	// sessions, which covers transitions made before publication. Claims
	// precede releases, so a failed claim leaves committed state in place.
	result, retired, unusedLeases := m.claimDesiredOwnerSessions(
		ctx, owner, compiled.members, compiled.memberOrder, currentClaims,
	)
	if result.Failed > 0 {
		return microBFDGroupResult(result, microBFDGroupCounts{}), retired, unusedLeases
	}
	claimed := m.commitMicroBFDGroups(compiled.groups, currentGroups, newGroups, owner)

	released, staleRetired := m.releaseStaleOwnerClaims(owner, compiled.members, currentClaims)
	result.Errors = append(result.Errors, released.Errors...)
	retired = append(retired, staleRetired...)
	counts := m.releaseRemovedMicroBFDGroups(compiled.groups, currentGroups, owner)
	counts.claimed = len(newGroups) + len(claimed)
	counts.created = len(newGroups)
	return microBFDGroupResult(result, counts), retired, unusedLeases
}

// microBFDGroupCounts are group claim and registry changes of one pass.
type microBFDGroupCounts struct {
	claimed, released, created, destroyed int
}

// microBFDGroupResult reports group claims and group registry changes with
// the member errors, as session results report claims and wire sessions.
func microBFDGroupResult(result ReconcileResult, counts microBFDGroupCounts) ReconcileResult {
	result.Created, result.Released, result.Updated = counts.claimed, counts.released, 0
	result.wireCreated, result.wireDestroyed = counts.created, counts.destroyed
	result.Failed = len(result.Errors)
	return result
}

// microBFDReleasable reports whether this pass removes group: reload
// owns it alone and no longer desires it. The caller holds ownershipMu.
func microBFDReleasable(
	key string, group *MicroBFDGroup, desired map[string]MicroBFDReconcileConfig, owner SessionOwner,
) bool {
	_, want := desired[key]
	_, owned := group.owners[owner]
	return owned && !want && len(group.owners) == 1
}

// microBFDSurvivorConflict rejects a new group whose member link belongs to
// a group that survives this pass, such as an API-owned group.
func (m *Manager) microBFDSurvivorConflict(
	compiled compiledMicroBFD,
	current map[string]*MicroBFDGroup,
	owner SessionOwner,
) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key, rc := range compiled.groups {
		if _, exists := current[key]; exists {
			continue
		}
		for _, member := range rc.Config.MemberLinks {
			other, owned := m.microMemberOwnerLocked(member)
			if owned && !microBFDReleasable(other.LAGInterface(), other, compiled.groups, owner) {
				return fmt.Errorf("micro-BFD group %q member %q of %q: %w",
					key, member, other.LAGInterface(), ErrMicroBFDMemberOwned)
			}
		}
	}
	return nil
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

// commitMicroBFDGroups registers the new groups and adds the reload claim to
// desired groups that another owner created. It returns the claimed groups.
func (m *Manager) commitMicroBFDGroups(
	desired map[string]MicroBFDReconcileConfig,
	current map[string]*MicroBFDGroup,
	groups []*MicroBFDGroup,
	owner SessionOwner,
) []*MicroBFDGroup {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, group := range groups {
		m.logger.Info("reconcile: creating new micro-BFD group",
			slog.String("lag", group.LAGInterface()),
		)
		group.owners[owner] = struct{}{}
		m.registerMicroBFDGroupLocked(group)
	}
	var claimed []*MicroBFDGroup
	for key := range desired {
		group, exists := current[key]
		if !exists {
			continue
		}
		if _, owned := group.owners[owner]; !owned {
			group.owners[owner] = struct{}{}
			claimed = append(claimed, group)
		}
	}
	return claimed
}

// releaseRemovedMicroBFDGroups releases the reload claim of groups absent
// from desired and removes each group with its last claim.
func (m *Manager) releaseRemovedMicroBFDGroups(
	desired map[string]MicroBFDReconcileConfig,
	current map[string]*MicroBFDGroup,
	owner SessionOwner,
) microBFDGroupCounts {
	m.mu.Lock()
	defer m.mu.Unlock()
	var counts microBFDGroupCounts
	for key, group := range current {
		_, want := desired[key]
		_, owned := group.owners[owner]
		if want || !owned || m.microGroups[key] != group {
			continue
		}
		delete(group.owners, owner)
		counts.released++
		if len(group.owners) > 0 {
			continue
		}
		m.logger.Info("reconcile: destroying removed micro-BFD group",
			slog.String("lag", key),
		)
		m.unregisterMicroBFDGroupLocked(group)
		counts.destroyed++
	}
	return counts
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
