package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// -------------------------------------------------------------------------
// Echo Session CRUD — RFC 9747
// -------------------------------------------------------------------------

// CreateEchoSession creates a new RFC 9747 echo session with the given
// configuration. The session is registered in the echo session map and
// its Run goroutine is started. Returns the allocated discriminator.
//
// RFC 9747 Section 3: echo sessions do not negotiate timers and do not
// participate in the BFD three-way handshake.
func (m *Manager) CreateEchoSession(
	ctx context.Context,
	cfg EchoSessionConfig,
	sender PacketSender,
) (uint32, error) {
	if !cfg.PeerAddr.IsValid() {
		return 0, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, ErrInvalidPeerAddr)
	}

	discr, err := m.discriminators.Allocate()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}

	es, err := NewEchoSession(cfg, discr, sender, m.rawNotifyCh, m.logger,
		WithEchoMetrics(m.metrics),
	)
	if err != nil {
		m.discriminators.Release(discr)
		return 0, fmt.Errorf("%s: %w", createEchoSessionErrPrefix, err)
	}

	// Start echo session goroutine with a decoupled context (same pattern
	// as control sessions — graceful shutdown calls DrainAllSessions first).
	sessCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &echoSessionEntry{session: es, cancel: cancel}

	m.mu.Lock()
	m.echoSessions[discr] = entry
	m.mu.Unlock()

	go es.Run(sessCtx)

	m.logger.Info("echo session created",
		slog.String("peer", cfg.PeerAddr.String()),
		slog.String("local", cfg.LocalAddr.String()),
		slog.String("interface", cfg.Interface),
		slog.Uint64("local_discr", uint64(discr)),
		slog.Duration("tx_interval", cfg.TxInterval),
		slog.Uint64("detect_mult", uint64(cfg.DetectMultiplier)),
	)

	return discr, nil
}

// DestroyEchoSession stops and removes the echo session identified by discr.
// The session goroutine is cancelled and the discriminator is released.
//
// Returns ErrEchoSessionNotFound if no echo session exists with the given
// discriminator.
func (m *Manager) DestroyEchoSession(discr uint32) error {
	m.mu.Lock()
	entry, ok := m.echoSessions[discr]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf(
			"destroy echo session with discriminator %d: %w",
			discr, ErrEchoSessionNotFound,
		)
	}
	delete(m.echoSessions, discr)
	m.mu.Unlock()

	entry.cancel()
	m.discriminators.Release(discr)

	m.logger.Info("echo session destroyed",
		slog.String("peer", entry.session.PeerAddr().String()),
		slog.Uint64("local_discr", uint64(discr)),
	)

	return nil
}

// DemuxEcho routes a returned echo packet to the appropriate echo session.
//
// RFC 9747: echo packets are self-originated and bounced back by the remote.
// Demultiplexing is by MyDiscriminator in the returned packet, which identifies
// the local echo session that originated the packet.
//
// Returns ErrEchoDemuxNoMatch if no echo session matches the discriminator.
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
// The returned slice contains copies; no references to mutable data are held.
func (m *Manager) EchoSessions() []EchoSessionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshots := make([]EchoSessionSnapshot, 0, len(m.echoSessions))
	for _, entry := range m.echoSessions {
		snapshots = append(snapshots, entry.session.Snapshot())
	}
	return snapshots
}

// -------------------------------------------------------------------------
// Echo Session Reconciliation — SIGHUP reload
// -------------------------------------------------------------------------

// EchoReconcileConfig describes a desired echo session for reconciliation.
type EchoReconcileConfig struct {
	// Key uniquely identifies the echo session for diffing purposes.
	// Typically: "echo|peer|local|interface".
	Key string

	// EchoSessionConfig is the echo session configuration to create if missing.
	EchoSessionConfig EchoSessionConfig

	// Sender provides the packet sending capability for the echo session.
	Sender PacketSender
}

// ReconcileEchoSessions diffs the desired echo session set against current
// echo sessions. Sessions present in desired but absent are created. Sessions
// present in current but absent from desired are destroyed.
//
// Returns the number of sessions created and destroyed, and any errors.
func (m *Manager) ReconcileEchoSessions(
	ctx context.Context,
	desired []EchoReconcileConfig,
) (int, int, error) {
	desiredKeys := make(map[string]EchoReconcileConfig, len(desired))
	for _, rc := range desired {
		desiredKeys[rc.Key] = rc
	}

	currentKeys := m.echoSessionKeySet()

	var created, destroyed int
	var errs []error

	// Destroy echo sessions not in desired set.
	for key, discr := range currentKeys {
		if _, want := desiredKeys[key]; want {
			continue
		}

		m.logger.Info("reconcile: destroying removed echo session",
			slog.String("key", key),
			slog.Uint64("local_discr", uint64(discr)),
		)

		if dErr := m.DestroyEchoSession(discr); dErr != nil {
			errs = append(errs, fmt.Errorf("reconcile destroy echo %s: %w", key, dErr))
			continue
		}
		destroyed++
	}

	// Create echo sessions in desired but not in current.
	for key, rc := range desiredKeys {
		if _, exists := currentKeys[key]; exists {
			continue
		}

		m.logger.Info("reconcile: creating new echo session",
			slog.String("key", key),
		)

		if _, cErr := m.CreateEchoSession(ctx, rc.EchoSessionConfig, rc.Sender); cErr != nil {
			errs = append(errs, fmt.Errorf("reconcile create echo %s: %w", key, cErr))
			continue
		}
		created++
	}

	var err error
	if len(errs) > 0 {
		err = errors.Join(errs...)
	}

	m.logger.Info("echo session reconciliation complete",
		slog.Int("created", created),
		slog.Int("destroyed", destroyed),
	)

	return created, destroyed, err
}

// echoSessionKeySet returns a map of echo session key -> discriminator
// for all active echo sessions.
func (m *Manager) echoSessionKeySet() map[string]uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make(map[string]uint32, len(m.echoSessions))
	for _, entry := range m.echoSessions {
		es := entry.session
		key := "echo|" + es.PeerAddr().String() + "|" + es.LocalAddr().String() + "|" + es.Interface()
		keys[key] = es.LocalDiscriminator()
	}
	return keys
}
