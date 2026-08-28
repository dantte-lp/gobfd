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
	return m.createEchoSessionWithSenderLease(ctx, cfg, NonOwningSenderLeaseFactory(sender))
}

// CreateEchoSessionWithSenderLease lazily acquires an owning sender lease for
// one compatibility/API-owned echo session.
func (m *Manager) CreateEchoSessionWithSenderLease(
	ctx context.Context,
	cfg EchoSessionConfig,
	senderFactory SenderLeaseFactory,
) (uint32, error) {
	return m.createEchoSessionWithSenderLease(ctx, cfg, senderFactory)
}

func (m *Manager) createEchoSessionWithSenderLease(
	ctx context.Context,
	cfg EchoSessionConfig,
	senderFactory SenderLeaseFactory,
) (uint32, error) {
	op, err := m.beginOperation()
	if err != nil {
		return 0, err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	discr, unusedLease, err := m.createEchoSession(
		ctx, cfg, senderFactory, echoSessionSourceCompatibility,
	)
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if closeErr := closeSenderLeaseError(unusedLease); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return discr, err
}

// createEchoSession runs with ownershipMu held. A returned lease was not
// accepted and must be closed after Manager locks are released.
func (m *Manager) createEchoSession(
	ctx context.Context,
	cfg EchoSessionConfig,
	senderFactory SenderLeaseFactory,
	source echoSessionSource,
) (uint32, *SenderLease, error) {
	cfg = canonicalEchoSessionConfig(cfg)
	if !cfg.PeerAddr.IsValid() {
		return 0, nil, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, ErrInvalidPeerAddr)
	}
	if err := validateEchoConfig(cfg, 1); err != nil {
		return 0, nil, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}

	lease, err := acquireSenderLease(senderFactory)
	if err != nil {
		return 0, lease, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}
	discr, err := m.discriminators.Allocate()
	if err != nil {
		return 0, lease, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}
	es, err := NewEchoSession(cfg, discr, lease.Sender(), m.rawNotifyCh, m.logger,
		WithEchoMetrics(m.metrics),
	)
	if err != nil {
		m.discriminators.Release(discr)
		return 0, lease, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}

	sessCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &echoSessionEntry{
		session:     es,
		cancel:      cancel,
		senderLease: lease,
		key:         canonicalEchoSessionKey(cfg),
		source:      source,
		done:        make(chan struct{}),
	}
	m.mu.Lock()
	m.echoSessions[discr] = entry
	m.mu.Unlock()
	m.workers.Go(func() {
		defer close(entry.done)
		es.Run(sessCtx)
	})

	m.logger.Info("echo session created",
		slog.String("peer", cfg.PeerAddr.String()),
		slog.String("local", cfg.LocalAddr.String()),
		slog.String("interface", cfg.Interface),
		slog.Uint64("local_discr", uint64(discr)),
		slog.Duration("tx_interval", cfg.TxInterval),
		slog.Uint64("detect_mult", uint64(cfg.DetectMultiplier)),
	)
	return discr, nil, nil
}

// DestroyEchoSession stops and removes the echo session identified by discr.
func (m *Manager) DestroyEchoSession(discr uint32) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	retired, err := m.detachEchoSession(discr)
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if retired != nil {
		m.finishEchoSessionDestroy(retired.localDiscr, retired.entry)
	}
	return err
}

func (m *Manager) detachEchoSession(discr uint32) (*retiredEchoSession, error) {
	m.mu.Lock()
	entry, ok := m.echoSessions[discr]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf(
			"destroy echo session with discriminator %d: %w",
			discr, ErrEchoSessionNotFound,
		)
	}
	delete(m.echoSessions, discr)
	m.mu.Unlock()
	return &retiredEchoSession{localDiscr: discr, entry: entry}, nil
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
func (m *Manager) DemuxEcho(myDiscr uint32) error {
	m.mu.RLock()
	entry, ok := m.echoSessions[myDiscr]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf(
			"echo demux: discriminator %d not found: %w",
			myDiscr, ErrEchoDemuxNoMatch,
		)
	}
	entry.session.RecvEcho()
	return nil
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
	result, retired := m.detachStaleDeclarativeEchoSessions(desiredByKey, current)
	createResult, rollbackRetired, unusedLease := m.createDesiredDeclarativeEchoSessions(
		ctx, desiredByKey, desiredOrder, current,
	)
	result.Created = createResult.Created
	result.wireCreated = createResult.wireCreated
	result.Errors = append(result.Errors, createResult.Errors...)
	result.Failed = len(result.Errors)
	retired = append(retired, rollbackRetired...)
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
) (map[string]echoReconcileCandidate, []string, error) {
	desiredByKey := make(map[string]echoReconcileCandidate, len(desired))
	desiredOrder := make([]string, 0, len(desired))
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
				return nil, nil, fmt.Errorf("reconcile duplicate echo %s: %w",
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

func canonicalEchoSessionKey(cfg EchoSessionConfig) string {
	return "echo|" + cfg.PeerAddr.String() + "|" + cfg.LocalAddr.String() + "|" + cfg.Interface
}

func (m *Manager) declarativeEchoSnapshot(
	desiredByKey map[string]echoReconcileCandidate,
	desiredOrder []string,
) (map[string]uint32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := make(map[string]uint32)
	for discr, entry := range m.echoSessions {
		if entry.source == echoSessionSourceDeclarative {
			current[entry.key] = discr
		}
	}
	for _, key := range desiredOrder {
		discr, exists := current[key]
		if !exists {
			continue
		}
		entry := m.echoSessions[discr]
		if !echoSessionMatchesConfig(entry.session, desiredByKey[key].config) {
			return nil, fmt.Errorf("reconcile echo %s: %w", key, ErrSessionParameterConflict)
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
	desiredByKey map[string]echoReconcileCandidate,
	current map[string]uint32,
) (ReconcileResult, []retiredEchoSession) {
	var result ReconcileResult
	retired := make([]retiredEchoSession, 0)
	for key, discr := range current {
		if _, wanted := desiredByKey[key]; wanted {
			continue
		}
		m.logger.Info("reconcile: destroying removed echo session",
			slog.String("key", key),
			slog.Uint64("local_discr", uint64(discr)),
		)
		entry, err := m.detachEchoSession(discr)
		if err != nil {
			addReconcileError(&result, ReconcileErrorRelease,
				fmt.Errorf("reconcile release echo %s: %w", key, err))
			continue
		}
		result.Released++
		result.wireDestroyed++
		retired = append(retired, *entry)
	}
	return result, retired
}

func (m *Manager) createDesiredDeclarativeEchoSessions(
	ctx context.Context,
	desiredByKey map[string]echoReconcileCandidate,
	desiredOrder []string,
	current map[string]uint32,
) (ReconcileResult, []retiredEchoSession, *SenderLease) {
	var result ReconcileResult
	createdDiscriminators := make([]uint32, 0, len(desiredOrder))
	for _, key := range desiredOrder {
		if _, exists := current[key]; exists {
			continue
		}
		candidate := desiredByKey[key]
		m.logger.Info("reconcile: creating new echo session", slog.String("key", key))
		discr, unusedLease, err := m.createEchoSession(
			ctx, candidate.config, candidate.factory, echoSessionSourceDeclarative,
		)
		if err != nil {
			addReconcileError(&result, ReconcileErrorCreate,
				fmt.Errorf("reconcile create echo %s: %w", key, err))
			retired := make([]retiredEchoSession, 0, len(createdDiscriminators))
			for _, createdDiscr := range createdDiscriminators {
				entry, rollbackErr := m.detachEchoSession(createdDiscr)
				if rollbackErr != nil {
					addReconcileError(&result, ReconcileErrorRollback,
						fmt.Errorf("rollback new echo session %d: %w", createdDiscr, rollbackErr))
					continue
				}
				result.Created--
				result.wireCreated--
				retired = append(retired, *entry)
			}
			return result, retired, unusedLease
		}
		result.Created++
		result.wireCreated++
		createdDiscriminators = append(createdDiscriminators, discr)
	}
	return result, nil, nil
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
