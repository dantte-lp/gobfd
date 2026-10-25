package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// CreateEchoSession creates a compatibility/API-owned echo session. The raw
// sender is explicitly non-owning.
func (m *Manager) CreateEchoSession(
	ctx context.Context,
	cfg EchoSessionConfig,
	sender PacketSender,
) (uint32, error) {
	return m.CreateEchoSessionWithSenderLease(ctx, cfg, NonOwningSenderLeaseFactory(sender))
}

// CreateEchoSessionWithSenderLease lazily acquires an owning sender lease for
// one compatibility/API-owned echo session.
func (m *Manager) CreateEchoSessionWithSenderLease(
	ctx context.Context,
	cfg EchoSessionConfig,
	senderFactory SenderLeaseFactory,
) (uint32, error) {
	session, err := m.CreateEchoSessionAs(ctx, cfg, senderFactory, compatibilityAPISessionOwner())
	if err != nil {
		return 0, err
	}
	return session.LocalDiscriminator(), nil
}

// CreateEchoSessionAs idempotently claims a canonical Echo wire for one owner.
// Compatible claims share its sender; conflicting parameters never mutate it.
func (m *Manager) CreateEchoSessionAs(
	ctx context.Context,
	cfg EchoSessionConfig,
	senderFactory SenderLeaseFactory,
	owner SessionOwner,
) (*EchoSession, error) {
	op, err := m.beginOperation()
	if err != nil {
		return nil, err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	session, _, unusedLease, err := m.createEchoSession(
		ctx, cfg, senderFactory, owner,
	)
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if closeErr := closeSenderLeaseError(unusedLease); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return session, err
}

// createEchoSession runs with ownershipMu held. A returned lease was not
// accepted and must be closed after Manager locks are released.
func (m *Manager) createEchoSession(
	ctx context.Context,
	cfg EchoSessionConfig,
	senderFactory SenderLeaseFactory,
	owner SessionOwner,
) (*EchoSession, bool, *SenderLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, nil, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}
	cfg = canonicalEchoSessionConfig(cfg)
	if !cfg.PeerAddr.IsValid() {
		return nil, false, nil, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, ErrInvalidPeerAddr)
	}
	if err := validateEchoConfig(cfg, 1); err != nil {
		return nil, false, nil, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}
	key := canonicalEchoSessionKey(cfg)
	m.mu.Lock()
	if entry := m.echoSessionsByKey[key]; entry != nil {
		if !echoSessionMatchesConfig(entry.session, cfg) {
			m.mu.Unlock()
			return nil, false, nil, fmt.Errorf("claim Echo session %+v: %w", key, ErrSessionParameterConflict)
		}
		entry.owners[owner] = struct{}{}
		m.mu.Unlock()
		return entry.session, false, nil, nil
	}
	m.mu.Unlock()

	lease, err := acquireSenderLease(senderFactory)
	if err != nil {
		return nil, false, lease, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, false, lease, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, contextErr)
	}
	discr, err := m.discriminators.Allocate()
	if err != nil {
		return nil, false, lease, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}
	es, err := NewEchoSession(cfg, discr, lease.Sender(), m.rawNotifyCh, m.logger,
		WithEchoMetrics(m.metrics),
		withEchoDelivery(m),
	)
	if err != nil {
		m.discriminators.Release(discr)
		return nil, false, lease, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}
	m.registerEchoAndStart(ctx, es, lease, key, owner)

	m.logger.Info("echo session created",
		slog.String("peer", cfg.PeerAddr.String()),
		slog.String("local", cfg.LocalAddr.String()),
		slog.String("interface", cfg.Interface),
		slog.Uint64("local_discr", uint64(discr)),
		slog.Duration("tx_interval", cfg.TxInterval),
		slog.Uint64("detect_mult", uint64(cfg.DetectMultiplier)),
	)
	return es, true, nil, nil
}

// registerEchoAndStart publishes registration before starting the owner.
// The caller holds ownershipMu; Manager.mu orders insertion, version and fanout.
func (m *Manager) registerEchoAndStart(
	ctx context.Context,
	es *EchoSession,
	lease *SenderLease,
	key SessionKey,
	owner SessionOwner,
) {
	sessCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	lease.bindInterrupt(sessCtx)
	entry := &echoSessionEntry{
		session:     es,
		cancel:      cancel,
		senderLease: lease,
		key:         key,
		owners:      map[SessionOwner]struct{}{owner: {}},
		done:        make(chan struct{}),
	}
	m.mu.Lock()
	es.observationMu.Lock()
	es.observationClock = &m.observationClock
	es.observation.ObservationVersion = es.nextObservationVersionLocked()
	es.observationMu.Unlock()
	m.echoSessions[es.localDiscr] = entry
	m.echoSessionsByKey[key] = entry
	status := es.watchSnapshot()
	added := stateChangeOf(status, status.State)
	added.Added = true
	counts := m.broadcastStateChange(added)
	m.mu.Unlock()
	m.reportFanoutDelivery(counts)
	m.workers.Go(func() {
		defer close(entry.done)
		defer lease.stopInterrupt()
		es.Run(sessCtx)
	})
}

// DestroyEchoSession releases the compatibility owner's Echo claim.
func (m *Manager) DestroyEchoSession(discr uint32) error {
	return m.DestroyEchoSessionAs(context.Background(), discr, compatibilityAPISessionOwner())
}

// DestroyEchoSessionAs releases only this owner's claim, retiring the wire
// and its sender after the last claim. Cleanup runs outside Manager locks.
func (m *Manager) DestroyEchoSessionAs(ctx context.Context, discr uint32, owner SessionOwner) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("destroy echo session %d: %w", discr, contextErr)
	}

	m.ownershipMu.Lock()
	retired, err := m.detachEchoSessionClaim(discr, owner)
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if retired.entry != nil {
		err = errors.Join(err, m.finishEchoSessionDestroyError(retired.localDiscr, retired.entry))
	}
	return err
}

func (m *Manager) detachEchoSessionClaim(discr uint32, owner SessionOwner) (retiredEchoSession, error) {
	m.mu.Lock()
	entry, ok := m.echoSessions[discr]
	if !ok {
		m.mu.Unlock()
		return retiredEchoSession{}, fmt.Errorf(
			"destroy echo session with discriminator %d: %w",
			discr, ErrEchoSessionNotFound,
		)
	}
	if _, owned := entry.owners[owner]; !owned {
		m.mu.Unlock()
		return retiredEchoSession{}, fmt.Errorf("release Echo session %d for owner %+v: %w",
			discr, owner, ErrSessionOwnerClaimNotFound)
	}
	if len(entry.owners) > 1 {
		delete(entry.owners, owner)
		m.mu.Unlock()
		return retiredEchoSession{}, nil
	}
	delete(m.echoSessions, discr)
	delete(m.echoSessionsByKey, entry.key)
	es := entry.session
	es.observationMu.Lock()
	observation := es.observation
	observation.ObservationVersion = es.nextObservationVersionLocked()
	status := es.watchStatusForObservation(observation)
	es.observationMu.Unlock()
	deleted := stateChangeOf(status, observation.State)
	deleted.Deleted = true
	counts := m.broadcastStateChange(deleted)
	m.mu.Unlock()
	m.reportFanoutDelivery(counts)
	return retiredEchoSession{localDiscr: discr, entry: entry}, nil
}

func (m *Manager) finishEchoSessionDestroy(discr uint32, entry *echoSessionEntry) {
	if err := m.finishEchoSessionDestroyError(discr, entry); err != nil {
		return
	}
}

func (m *Manager) finishEchoSessionDestroyError(discr uint32, entry *echoSessionEntry) error {
	entry.cancel()
	<-entry.done
	closeErr := entry.senderLease.Close()
	if closeErr != nil {
		m.logger.Warn("failed to close echo session sender lease",
			slog.Uint64("local_discr", uint64(discr)),
			slog.String("error", closeErr.Error()),
		)
	}
	m.discriminators.Release(discr)
	m.logger.Info("echo session destroyed",
		slog.String("peer", entry.session.PeerAddr().String()),
		slog.Uint64("local_discr", uint64(discr)),
	)
	if closeErr != nil {
		return fmt.Errorf("destroy echo session %d sender lease: %w", discr, closeErr)
	}
	return nil
}

// DemuxEcho routes a returned echo packet to the matching echo session.
func (m *Manager) DemuxEcho(myDiscr uint32) (DeliveryOutcome, error) {
	m.mu.RLock()
	entry, ok := m.echoSessions[myDiscr]
	m.mu.RUnlock()
	if !ok {
		m.RecordDelivery(DeliveryEchoDemux, DeliveryUnknownSession)
		return DeliveryUnknownSession, fmt.Errorf(
			"echo demux: discriminator %d not found: %w",
			myDiscr, ErrEchoDemuxNoMatch,
		)
	}
	m.RecordDelivery(DeliveryEchoDemux, DeliveryFound)
	outcome := entry.session.RecvEcho()
	switch outcome {
	case DeliveryQueueFull:
		return outcome, fmt.Errorf("echo demux: discriminator %d: %w", myDiscr, ErrDeliveryQueueFull)
	case DeliveryClosed:
		return outcome, fmt.Errorf("echo demux: discriminator %d: %w", myDiscr, ErrDeliveryClosed)
	default:
		return outcome, nil
	}
}

// EchoSessions returns a snapshot of all active echo sessions.
func (m *Manager) EchoSessions() []EchoSessionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshots := make([]EchoSessionSnapshot, 0, len(m.echoSessions))
	for _, entry := range m.echoSessions {
		snapshots = append(snapshots, entry.session.Snapshot())
	}
	return snapshots
}

// EchoReconcileConfig describes one desired declarative echo session.
type EchoReconcileConfig struct {
	// Key is diagnostic adapter context only. Manager derives canonical
	// identity from EchoSessionConfig and never trusts this external value.
	Key               string
	EchoSessionConfig EchoSessionConfig
	// SenderLeaseFactory is called only after the complete desired set is valid
	// and this canonical session is known to be new.
	SenderLeaseFactory SenderLeaseFactory
}

// ReconcileEchoSessions reconciles only declarative echo sessions. API-owned
// sessions are outside this desired set.
func (m *Manager) ReconcileEchoSessions(
	ctx context.Context,
	desired []EchoReconcileConfig,
) (int, int, error) {
	result := m.ReconcileEchoSessionsDetailed(ctx, desired)
	return result.wireCreated, result.wireDestroyed, result.Err()
}

// ReconcileEchoSessionsDetailed reconciles the declarative Echo desired set
// and returns net source-resource changes plus bounded operation failures.
func (m *Manager) ReconcileEchoSessionsDetailed(
	ctx context.Context,
	desired []EchoReconcileConfig,
) ReconcileResult {
	op, err := m.beginOperation()
	if err != nil {
		return failedReconcileResult(ReconcileErrorLifecycle, err)
	}
	defer op.finish()

	m.ownershipMu.Lock()
	desiredByKey, desiredOrder, err := compileEchoReconcileConfigs(desired, true)
	if err != nil {
		m.ownershipMu.Unlock()
		return failedReconcileResult(ReconcileErrorInvalid, err)
	}
	current, err := m.declarativeEchoSnapshot(desiredByKey, desiredOrder)
	if err != nil {
		m.ownershipMu.Unlock()
		return failedReconcileResult(ReconcileErrorConflict, err)
	}
	result, retired, unusedLease := m.createDesiredDeclarativeEchoSessions(
		ctx, desiredByKey, desiredOrder, current,
	)
	if result.Err() == nil {
		released, staleRetired := m.detachStaleDeclarativeEchoSessions(desiredByKey, current)
		result.Released = released.Released
		result.wireDestroyed = released.wireDestroyed
		result.Errors = append(result.Errors, released.Errors...)
		result.Failed = len(result.Errors)
		retired = append(retired, staleRetired...)
	}
	m.ownershipMu.Unlock()
	op.unlockMutation()

	for _, cleanupErr := range m.finishRetiredEchoSessions(retired) {
		addReconcileError(&result, ReconcileErrorCleanup, cleanupErr)
	}
	if closeErr := closeSenderLeaseError(unusedLease); closeErr != nil {
		addReconcileError(&result, ReconcileErrorCleanup, closeErr)
	}
	m.logger.Debug("echo session reconciliation source result",
		slog.Int("created", result.Created),
		slog.Int("released", result.Released),
		slog.Int("failed", result.Failed),
		slog.Bool("converged", result.Err() == nil),
	)
	return result
}

type echoReconcileCandidate struct {
	config  EchoSessionConfig
	factory SenderLeaseFactory
}

// ValidateEchoReconcileConfigs validates a complete declarative Echo desired
// set without acquiring sender leases or mutating Manager state.
func ValidateEchoReconcileConfigs(desired []EchoReconcileConfig) error {
	_, _, err := compileEchoReconcileConfigs(desired, false)
	return err
}

func compileEchoReconcileConfigs(
	desired []EchoReconcileConfig,
	requireFactory bool,
) (map[SessionKey]echoReconcileCandidate, []SessionKey, error) {
	desiredByKey := make(map[SessionKey]echoReconcileCandidate, len(desired))
	desiredOrder := make([]SessionKey, 0, len(desired))
	for _, rc := range desired {
		cfg := canonicalEchoSessionConfig(rc.EchoSessionConfig)
		if err := validateEchoConfig(cfg, 1); err != nil {
			return nil, nil, fmt.Errorf("reconcile echo config %q: %w", rc.Key, err)
		}
		if requireFactory && rc.SenderLeaseFactory == nil {
			return nil, nil, fmt.Errorf("reconcile echo config %q: %w",
				rc.Key, ErrSenderLeaseFactoryNil)
		}
		key := canonicalEchoSessionKey(cfg)
		if previous, exists := desiredByKey[key]; exists {
			if previous.config != cfg {
				return nil, nil, fmt.Errorf("reconcile duplicate echo %+v: %w",
					key, ErrSessionParameterConflict)
			}
			continue
		}
		desiredByKey[key] = echoReconcileCandidate{config: cfg, factory: rc.SenderLeaseFactory}
		desiredOrder = append(desiredOrder, key)
	}
	return desiredByKey, desiredOrder, nil
}

func canonicalEchoSessionConfig(cfg EchoSessionConfig) EchoSessionConfig {
	if cfg.PeerAddr.IsValid() {
		cfg.PeerAddr = cfg.PeerAddr.Unmap()
	}
	if cfg.LocalAddr.IsValid() {
		cfg.LocalAddr = cfg.LocalAddr.Unmap()
	}
	return cfg
}

func canonicalEchoSessionKey(cfg EchoSessionConfig) SessionKey {
	cfg = canonicalEchoSessionConfig(cfg)
	family := AddressFamilyIPv6
	if cfg.PeerAddr.Is4() {
		family = AddressFamilyIPv4
	}
	return SessionKey{
		Type: SessionTypeEcho, AddressFamily: family,
		PeerAddr: cfg.PeerAddr, LocalAddr: cfg.LocalAddr, Interface: cfg.Interface,
	}
}

func (m *Manager) declarativeEchoSnapshot(
	desiredByKey map[SessionKey]echoReconcileCandidate,
	desiredOrder []SessionKey,
) (map[SessionKey]uint32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := make(map[SessionKey]uint32)
	for discr, entry := range m.echoSessions {
		if _, owned := entry.owners[configSessionOwner()]; owned {
			current[entry.key] = discr
		}
	}
	for _, key := range desiredOrder {
		entry := m.echoSessionsByKey[key]
		if entry == nil {
			continue
		}
		if !echoSessionMatchesConfig(entry.session, desiredByKey[key].config) {
			return nil, fmt.Errorf("reconcile echo %+v: %w", key, ErrSessionParameterConflict)
		}
	}
	return current, nil
}

func echoSessionMatchesConfig(session *EchoSession, cfg EchoSessionConfig) bool {
	return session.PeerAddr() == cfg.PeerAddr &&
		session.LocalAddr() == cfg.LocalAddr &&
		session.Interface() == cfg.Interface &&
		session.TxInterval() == cfg.TxInterval &&
		session.DetectMultiplier() == cfg.DetectMultiplier
}

func (m *Manager) detachStaleDeclarativeEchoSessions(
	desiredByKey map[SessionKey]echoReconcileCandidate,
	current map[SessionKey]uint32,
) (ReconcileResult, []retiredEchoSession) {
	var result ReconcileResult
	retired := make([]retiredEchoSession, 0)
	for key, discr := range current {
		if _, wanted := desiredByKey[key]; wanted {
			continue
		}
		m.logger.Info("reconcile: destroying removed echo session",
			slog.Any("key", key),
			slog.Uint64("local_discr", uint64(discr)),
		)
		entry, err := m.detachEchoSessionClaim(discr, configSessionOwner())
		if err != nil {
			addReconcileError(&result, ReconcileErrorRelease,
				fmt.Errorf("reconcile release echo %+v: %w", key, err))
			continue
		}
		result.Released++
		if entry.entry != nil {
			result.wireDestroyed++
			retired = append(retired, entry)
		}
	}
	return result, retired
}

func (m *Manager) createDesiredDeclarativeEchoSessions(
	ctx context.Context,
	desiredByKey map[SessionKey]echoReconcileCandidate,
	desiredOrder []SessionKey,
	current map[SessionKey]uint32,
) (ReconcileResult, []retiredEchoSession, *SenderLease) {
	var result ReconcileResult
	createdDiscriminators := make([]uint32, 0, len(desiredOrder))
	for _, key := range desiredOrder {
		if _, exists := current[key]; exists {
			continue
		}
		candidate := desiredByKey[key]
		m.logger.Info("reconcile: claiming echo session", slog.Any("key", key))
		session, created, unusedLease, err := m.createEchoSession(
			ctx, candidate.config, candidate.factory, configSessionOwner(),
		)
		if err != nil {
			err = fmt.Errorf("reconcile create echo %+v: %w", key, err)
			if _, unavailable := UnavailableResource(err); unavailable {
				// The pass rolls back as one unit, so every rolled-back session
				// waits for the resource with the failing one.
				result.ResourcePending += 1 + len(createdDiscriminators)
				result.resourceErrs = append(result.resourceErrs, err)
			} else {
				addReconcileError(&result, ReconcileErrorCreate, err)
			}
			retired := m.rollbackNewEchoClaims(createdDiscriminators, &result)
			return result, retired, unusedLease
		}
		result.Created++
		if created {
			result.wireCreated++
		}
		createdDiscriminators = append(createdDiscriminators, session.LocalDiscriminator())
	}
	return result, nil, nil
}

func (m *Manager) rollbackNewEchoClaims(discriminators []uint32, result *ReconcileResult) []retiredEchoSession {
	retired := make([]retiredEchoSession, 0, len(discriminators))
	for _, discr := range discriminators {
		entry, err := m.detachEchoSessionClaim(discr, configSessionOwner())
		if err != nil {
			addReconcileError(result, ReconcileErrorRollback,
				fmt.Errorf("rollback new echo session %d: %w", discr, err))
			continue
		}
		result.Created--
		if entry.entry != nil {
			result.wireCreated--
			retired = append(retired, entry)
		}
	}
	return retired
}

func (m *Manager) finishRetiredEchoSessions(retired []retiredEchoSession) []error {
	var errs []error
	for _, item := range retired {
		if err := m.finishEchoSessionDestroyError(item.localDiscr, item.entry); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
