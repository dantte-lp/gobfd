package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// -------------------------------------------------------------------------
// Session CRUD — Create
// -------------------------------------------------------------------------

// CreateSession creates a new BFD session with the given configuration.
//
// CreateSession is the compatibility/API ownership path. An exact session
// already claimed by another source is shared; a second compatibility claim
// for the same session returns ErrDuplicateSession.
func (m *Manager) CreateSession(
	ctx context.Context,
	cfg SessionConfig,
	senderFactory SenderLeaseFactory,
) (*Session, error) {
	op, err := m.beginOperation()
	if err != nil {
		return nil, err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	sess, _, unusedLease, err := m.claimSession(
		ctx, canonicalSessionConfig(cfg), senderFactory, compatibilityAPISessionOwner(), false,
	)
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if closeErr := closeSenderLeaseError(unusedLease); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return sess, err
}

func (m *Manager) claimSession(
	ctx context.Context,
	cfg SessionConfig,
	senderFactory SenderLeaseFactory,
	owner SessionOwner,
	idempotent bool,
) (*Session, bool, *SenderLease, error) {
	key, err := sessionKeyFromConfig(cfg)
	if err != nil {
		return nil, false, nil, fmt.Errorf("%s: %w", createSessionErrPrefix, err)
	}
	cfg = canonicalSessionConfig(cfg)
	effective, err := normalizeEffectiveSessionConfig(cfg)
	if err != nil {
		return nil, false, nil, fmt.Errorf("%s: %w", createSessionErrPrefix, err)
	}

	if sess, found, claimErr := m.claimExisting(key, effective, owner, idempotent); found {
		return sess, false, nil, claimErr
	}

	lease, err := acquireSenderLease(senderFactory)
	if err != nil {
		return nil, false, lease, fmt.Errorf("%s: %w", createSessionErrPrefix, err)
	}

	discr, sess, err := m.allocateAndBuild(cfg, lease.Sender())
	if err != nil {
		return nil, false, lease, err
	}

	registered, created, err := m.registerAndStart(
		ctx, key, cfg, effective, owner, idempotent, discr, sess, lease,
	)
	if !created {
		m.discriminators.Release(discr)
	}
	if err != nil {
		return nil, false, lease, err
	}
	if created {
		m.logSessionCreated(cfg, discr)
		return registered, true, nil, nil
	}

	return registered, false, lease, nil
}

func acquireSenderLease(factory SenderLeaseFactory) (*SenderLease, error) {
	if factory == nil {
		return nil, ErrSenderLeaseFactoryNil
	}
	lease, err := factory()
	if err != nil {
		return lease, err
	}
	if lease == nil {
		return nil, ErrSenderLeaseNil
	}
	if lease.Sender() == nil {
		return lease, ErrSenderLeaseSenderNil
	}
	return lease, nil
}

func closeSenderLeaseError(lease *SenderLease) error {
	if err := lease.Close(); err != nil {
		return fmt.Errorf("close sender lease: %w", err)
	}
	return nil
}

func (m *Manager) claimExisting(
	key SessionKey,
	effective effectiveSessionConfig,
	owner SessionOwner,
	idempotent bool,
) (*Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.sessionsByKey[key]
	if !exists {
		return nil, false, nil
	}
	if entry.effective != effective {
		return nil, true, fmt.Errorf("claim session %+v for owner %+v: %w",
			key, owner, ErrSessionParameterConflict)
	}
	if _, owned := entry.owners[owner]; owned {
		if idempotent {
			return entry.session, true, nil
		}
		return nil, true, fmt.Errorf("claim session %+v for owner %+v: %w",
			key, owner, ErrDuplicateSession)
	}

	entry.owners[owner] = struct{}{}
	return entry.session, true, nil
}

// allocateAndBuild allocates a discriminator and constructs the session.
// On session creation failure, the discriminator is released.
func (m *Manager) allocateAndBuild(
	cfg SessionConfig,
	sender PacketSender,
) (uint32, *Session, error) {
	discr, err := m.discriminators.Allocate()
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", createSessionErrPrefix, err)
	}

	sess, err := NewSession(cfg, discr, sender, m.rawNotifyCh, m.logger,
		WithMetrics(m.metrics),
	)
	if err != nil {
		m.discriminators.Release(discr)
		return 0, nil, fmt.Errorf("%s: %w", createSessionErrPrefix, err)
	}

	return discr, sess, nil
}

// registerAndStart registers the session under write lock and starts the
// session goroutine. Re-checks for duplicates that may have appeared
// between the initial RLock check and this WLock.
func (m *Manager) registerAndStart(
	ctx context.Context,
	key SessionKey,
	cfg SessionConfig,
	effective effectiveSessionConfig,
	owner SessionOwner,
	idempotent bool,
	discr uint32,
	sess *Session,
	senderLease *SenderLease,
) (*Session, bool, error) {
	demuxKey := demuxKeyFromSessionConfig(cfg)

	m.mu.Lock()
	if entry, exists := m.sessionsByKey[key]; exists {
		if entry.effective != effective {
			m.mu.Unlock()
			return nil, false, fmt.Errorf("claim session %+v for owner %+v: %w",
				key, owner, ErrSessionParameterConflict)
		}
		if _, owned := entry.owners[owner]; owned {
			m.mu.Unlock()
			if idempotent {
				return entry.session, false, nil
			}
			return nil, false, fmt.Errorf("claim session %+v for owner %+v: %w",
				key, owner, ErrDuplicateSession)
		}
		entry.owners[owner] = struct{}{}
		m.mu.Unlock()
		return entry.session, false, nil
	}
	if _, dup := m.sessionsByPeer[demuxKey]; dup {
		m.mu.Unlock()
		return nil, false, fmt.Errorf(
			"create session for peer %s: %w",
			key.PeerAddr, ErrDuplicateSession,
		)
	}

	entry := &sessionEntry{
		session:     sess,
		senderLease: senderLease,
		key:         key,
		demuxKey:    demuxKey,
		effective:   effective,
		owners:      map[SessionOwner]struct{}{owner: {}},
		done:        make(chan struct{}),
	}
	// Decouple session lifetime from the parent context so that SIGTERM
	// does not immediately cancel sessions. Graceful shutdown first sets
	// AdminDown (DrainAllSessions), waits for packets to be sent, and
	// only then calls Manager.Close which cancels each session explicitly.
	sessCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry.cancel = cancel
	m.startSession(sessCtx, entry, sess)

	m.sessions[discr] = entry
	m.sessionsByPeer[demuxKey] = entry
	m.sessionsByKey[key] = entry
	m.mu.Unlock()

	return sess, true, nil
}

func (m *Manager) startSession(ctx context.Context, entry *sessionEntry, sess *Session) {
	m.workers.Go(func() {
		defer close(entry.done)
		sess.Run(ctx)
	})
}

func demuxKeyFromSessionConfig(cfg SessionConfig) packetDemuxKey {
	return packetDemuxKey{
		peerAddr:  cfg.PeerAddr,
		localAddr: cfg.LocalAddr,
		ifName:    cfg.Interface,
	}
}

// logSessionCreated logs the successful creation of a BFD session and
// registers it in the metrics collector.
func (m *Manager) logSessionCreated(cfg SessionConfig, discr uint32) {
	m.metrics.RegisterSession(cfg.PeerAddr, cfg.LocalAddr, cfg.Type.String())

	m.logger.Info("session created",
		slog.String("peer", cfg.PeerAddr.String()),
		slog.String("local", cfg.LocalAddr.String()),
		slog.String("interface", cfg.Interface),
		slog.String("type", cfg.Type.String()),
		slog.String("role", cfg.Role.String()),
		slog.Uint64("local_discr", uint64(discr)),
		slog.Duration("desired_min_tx", cfg.DesiredMinTxInterval),
		slog.Duration("required_min_rx", cfg.RequiredMinRxInterval),
		slog.Uint64("detect_mult", uint64(cfg.DetectMultiplier)),
	)
}

// -------------------------------------------------------------------------
// Session CRUD — Destroy
// -------------------------------------------------------------------------

// DestroySession releases the compatibility/API claim identified by
// localDiscr. The wire session is destroyed only when that was its last claim.
//
// Returns ErrSessionNotFound if no session exists with the given discriminator.
func (m *Manager) DestroySession(_ context.Context, localDiscr uint32) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()

	m.ownershipMu.Lock()
	_, retired, err := m.detachSessionClaimByDiscriminator(localDiscr, compatibilityAPISessionOwner())
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if retired != nil {
		m.finishSessionDestroy(retired.localDiscr, retired.entry)
	}
	return err
}

func (m *Manager) detachSessionClaimByDiscriminator(
	localDiscr uint32,
	owner SessionOwner,
) (bool, *retiredSession, error) {
	m.mu.Lock()
	entry, ok := m.sessions[localDiscr]
	if !ok {
		m.mu.Unlock()
		return false, nil, fmt.Errorf(
			"destroy session with discriminator %d: %w",
			localDiscr, ErrSessionNotFound,
		)
	}
	if _, owned := entry.owners[owner]; !owned {
		m.mu.Unlock()
		return false, nil, fmt.Errorf("release session %d for owner %+v: %w",
			localDiscr, owner, ErrSessionOwnerClaimNotFound)
	}
	if owner.Source == SessionOwnerSourceUnsolicited {
		entry.unsolicited = false
		if m.unsolicited != nil {
			m.unsolicited.release()
		}
	}
	if len(entry.owners) > 1 {
		delete(entry.owners, owner)
		m.mu.Unlock()
		return false, nil, nil
	}

	// Remove the wire session from every registry after its last claim.
	delete(m.sessions, localDiscr)
	delete(m.sessionsByPeer, entry.demuxKey)
	delete(m.sessionsByKey, entry.key)
	m.mu.Unlock()

	return true, &retiredSession{localDiscr: localDiscr, entry: entry}, nil
}

func (m *Manager) finishSessionDestroy(localDiscr uint32, entry *sessionEntry) {
	if err := m.finishSessionDestroyError(localDiscr, entry); err != nil {
		return
	}
}

func (m *Manager) finishSessionDestroyError(localDiscr uint32, entry *sessionEntry) error {
	// Cancel session goroutine (outside lock to avoid holding lock during
	// goroutine teardown).
	entry.cancel()
	<-entry.done
	closeErr := entry.senderLease.Close()
	if closeErr != nil {
		m.logger.Warn("failed to close session sender lease",
			slog.Uint64("local_discr", uint64(localDiscr)),
			slog.String("error", closeErr.Error()),
		)
	}

	// Release discriminator for reuse.
	m.discriminators.Release(localDiscr)

	m.metrics.UnregisterSession(
		entry.session.PeerAddr(),
		entry.session.LocalAddr(),
		entry.session.Type().String(),
	)

	m.logger.Info("session destroyed",
		slog.String("peer", entry.session.PeerAddr().String()),
		slog.Uint64("local_discr", uint64(localDiscr)),
	)
	if closeErr != nil {
		return fmt.Errorf("destroy session %d sender lease: %w", localDiscr, closeErr)
	}
	return nil
}

// HandleInterfaceEvent applies an interface state event to sessions bound to
// the interface. Link-up events are informational; link-down events transition
// matching sessions to Down with DiagPathDown before detection timer expiry.
func (m *Manager) HandleInterfaceEvent(ifName string, up bool) int {
	if ifName == "" || up {
		return 0
	}

	m.mu.RLock()
	matches := make([]*Session, 0)
	for _, entry := range m.sessions {
		if entry.key.Interface == ifName {
			matches = append(matches, entry.session)
		}
	}
	m.mu.RUnlock()

	affected := 0
	for _, sess := range matches {
		if sess.SetPathDown() {
			affected++
		}
	}
	if affected > 0 {
		m.logger.Warn("interface link down affected BFD sessions",
			slog.String("interface", ifName),
			slog.Int("sessions", affected),
		)
	}
	return affected
}

// -------------------------------------------------------------------------
// Lookup — RFC 5880 Section 6.8.6 demultiplexing
// -------------------------------------------------------------------------

// LookupByDiscriminator returns the session with the given local discriminator.
// This is the primary O(1) lookup path for packets where Your Discriminator != 0
// (RFC 5880 Section 6.8.6).
func (m *Manager) LookupByDiscriminator(discr uint32) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.sessions[discr]
	if !ok {
		return nil, false
	}

	return entry.session, true
}

// LookupByPeer returns the session matching the given peer key.
// This is the fallback lookup for initial packets where Your Discriminator == 0
// (RFC 5880 Section 6.8.6).
func (m *Manager) LookupByPeer(key packetDemuxKey) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.sessionsByPeer[key]
	if !ok {
		return nil, false
	}

	return entry.session, true
}

// -------------------------------------------------------------------------
// Demux — Two-tier packet routing
// -------------------------------------------------------------------------

// Demux routes an incoming BFD Control packet to the appropriate session.
//
// Two-tier demultiplexing per RFC 5880 Section 6.8.6:
//
//  1. If Your Discriminator != 0: look up by discriminator (O(1)).
//  2. If Your Discriminator == 0 AND State is Down or AdminDown:
//     look up by peer key (source IP, dest IP, interface).
//
// Returns ErrDemuxNoMatch if no session matches. The caller (listener loop)
// should log and discard the packet.
func (m *Manager) Demux(pkt *ControlPacket, meta PacketMeta) error {
	// Tier 1: lookup by Your Discriminator (RFC 5880 Section 6.8.6).
	if pkt.YourDiscriminator != 0 {
		sess, ok := m.LookupByDiscriminator(pkt.YourDiscriminator)
		if !ok {
			return fmt.Errorf(
				"demux: your discriminator %d not found: %w",
				pkt.YourDiscriminator, ErrDemuxNoMatch,
			)
		}
		sess.RecvPacket(pkt)
		return nil
	}

	// Tier 2: lookup by peer key when Your Discriminator == 0.
	// RFC 5880 Section 6.8.6: Your Discriminator may be zero only when
	// State is Down or AdminDown (validated by UnmarshalControlPacket step 7b).
	key := packetDemuxKey{
		peerAddr:  meta.SrcAddr.Unmap(),
		localAddr: meta.DstAddr.Unmap(),
		ifName:    meta.IfName,
	}

	sess, ok := m.LookupByPeer(key)
	if !ok {
		return fmt.Errorf(
			"demux: no session for peer %s -> %s (iface=%s): %w",
			meta.SrcAddr, meta.DstAddr, meta.IfName, ErrDemuxNoMatch,
		)
	}

	sess.RecvPacket(pkt)
	return nil
}

// DemuxWithWire routes a packet like Demux but also passes raw wire
// bytes to the session for authentication verification (RFC 5880 Section 6.7).
func (m *Manager) DemuxWithWire(
	pkt *ControlPacket,
	meta PacketMeta,
	wire []byte,
) error {
	// Tier 1: lookup by Your Discriminator (RFC 5880 Section 6.8.6).
	if pkt.YourDiscriminator != 0 {
		return m.demuxByDiscr(pkt, wire)
	}

	// Tier 2: lookup by peer key when Your Discriminator == 0.
	return m.demuxByPeer(pkt, meta, wire)
}

// demuxByDiscr routes a packet by Your Discriminator (tier 1).
func (m *Manager) demuxByDiscr(pkt *ControlPacket, wire []byte) error {
	sess, ok := m.LookupByDiscriminator(pkt.YourDiscriminator)
	if !ok {
		return fmt.Errorf(
			"demux: your discriminator %d not found: %w",
			pkt.YourDiscriminator, ErrDemuxNoMatch,
		)
	}
	sess.RecvPacket(pkt, wire)
	return nil
}

// demuxByPeer routes a packet by peer key (tier 2).
// If no matching session exists and unsolicited BFD is enabled (RFC 9468),
// attempts to auto-create a passive session for the peer.
func (m *Manager) demuxByPeer(
	pkt *ControlPacket,
	meta PacketMeta,
	wire []byte,
) error {
	key := packetDemuxKey{
		peerAddr:  meta.SrcAddr.Unmap(),
		localAddr: meta.DstAddr.Unmap(),
		ifName:    meta.IfName,
	}

	sess, ok := m.LookupByPeer(key)
	if ok {
		sess.RecvPacket(pkt, wire)
		return nil
	}

	// RFC 9468: attempt unsolicited session creation.
	if m.unsolicited != nil {
		return m.tryCreateUnsolicited(pkt, meta, wire)
	}

	return fmt.Errorf(
		"demux: no session for peer %s -> %s (iface=%s): %w",
		meta.SrcAddr, meta.DstAddr, meta.IfName, ErrDemuxNoMatch,
	)
}

// tryCreateUnsolicited validates the unsolicited policy and creates a
// passive BFD session for the incoming packet (RFC 9468 Section 2).
func (m *Manager) tryCreateUnsolicited(
	pkt *ControlPacket,
	meta PacketMeta,
	wire []byte,
) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()

	m.ownershipMu.Lock()

	// RFC 9468 Section 6.1: unsolicited BFD is single-hop only.
	// Multi-hop packets arrive on port 4784; single-hop on 3784.
	// We use the interface name as a proxy: multi-hop sessions have no interface.
	// Also validate via policy.

	if reserveErr := m.unsolicited.reserve(meta.SrcAddr, meta.IfName); reserveErr != nil {
		m.ownershipMu.Unlock()
		return fmt.Errorf(
			"unsolicited: peer %s on %s: %w",
			meta.SrcAddr, meta.IfName, reserveErr,
		)
	}

	cfg := unsolicitedSessionConfig(meta, m.unsolicited.policy.SessionDefaults)

	senderFactory := m.unsolicitedSenderFactory
	if senderFactory == nil {
		m.unsolicited.release()
		m.ownershipMu.Unlock()
		return fmt.Errorf(
			"unsolicited: no sender configured for peer %s: %w",
			meta.SrcAddr, ErrUnsolicitedDisabled,
		)
	}

	sess, _, unusedLease, err := m.claimSession(
		context.Background(), cfg, senderFactory,
		unsolicitedSessionOwner(), false,
	)
	if err != nil {
		m.unsolicited.release()
		m.ownershipMu.Unlock()
		op.unlockMutation()
		if closeErr := closeSenderLeaseError(unusedLease); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return fmt.Errorf("unsolicited: create session for peer %s: %w", meta.SrcAddr, err)
	}

	m.markSessionUnsolicited(sess.LocalDiscriminator())
	m.activateUnsolicitedSession(sess, pkt, meta, wire)
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if closeErr := closeSenderLeaseError(unusedLease); closeErr != nil {
		return fmt.Errorf("unsolicited: close unused sender for peer %s: %w", meta.SrcAddr, closeErr)
	}

	return nil
}

func unsolicitedSessionConfig(
	meta PacketMeta,
	defaults UnsolicitedSessionDefaults,
) SessionConfig {
	return SessionConfig{
		PeerAddr:              meta.SrcAddr,
		LocalAddr:             meta.DstAddr,
		Interface:             meta.IfName,
		Type:                  SessionTypeSingleHop,
		Role:                  RolePassive,
		DesiredMinTxInterval:  defaults.DesiredMinTxInterval,
		RequiredMinRxInterval: defaults.RequiredMinRxInterval,
		DetectMultiplier:      defaults.DetectMultiplier,
	}
}

func (m *Manager) activateUnsolicitedSession(
	sess *Session,
	pkt *ControlPacket,
	meta PacketMeta,
	wire []byte,
) {
	m.logger.Info("unsolicited session created (RFC 9468)",
		slog.String("peer", meta.SrcAddr.String()),
		slog.String("local", meta.DstAddr.String()),
		slog.String("interface", meta.IfName),
		slog.Uint64("local_discr", uint64(sess.LocalDiscriminator())),
	)
	sess.RecvPacket(pkt, wire)
}

func (m *Manager) markSessionUnsolicited(localDiscr uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.sessions[localDiscr]; ok {
		entry.unsolicited = true
	}
}

// -------------------------------------------------------------------------
// Snapshot — read-only session listing
// -------------------------------------------------------------------------

// Sessions returns a snapshot of all active sessions. The returned slice
// contains copies of session state; no references to mutable data are held.
//
// Used by the ListSessions RPC to provide a consistent view without
// holding locks during gRPC serialization.
func (m *Manager) Sessions() []SessionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshots := make([]SessionSnapshot, 0, len(m.sessions))

	for _, entry := range m.sessions {
		s := entry.session
		snapshots = append(snapshots, SessionSnapshot{
			LocalDiscr:           s.LocalDiscriminator(),
			RemoteDiscr:          s.RemoteDiscriminator(),
			PeerAddr:             s.PeerAddr(),
			LocalAddr:            s.LocalAddr(),
			Interface:            s.Interface(),
			Type:                 s.Type(),
			State:                s.State(),
			RemoteState:          s.RemoteState(),
			LocalDiag:            s.LocalDiag(),
			DesiredMinTx:         s.DesiredMinTxInterval(),
			RequiredMinRx:        s.RequiredMinRxInterval(),
			DetectMultiplier:     s.DetectMultiplier(),
			NegotiatedTxInterval: s.NegotiatedTxInterval(),
			DetectionTime:        s.DetectionTime(),
			LastStateChange:      s.LastStateChange(),
			LastPacketReceived:   s.LastPacketReceived(),
			PaddedPduSize:        s.PaddedPduSize(),
			AuthType:             s.AuthType(),
			Counters: SessionCounters{
				PacketsSent:      s.PacketsSent(),
				PacketsReceived:  s.PacketsReceived(),
				StateTransitions: s.StateTransitions(),
			},
		})
	}

	return snapshots
}

// -------------------------------------------------------------------------
// State Change Notifications
// -------------------------------------------------------------------------

// StateChanges returns the legacy read-only channel that receives state change
// notifications from all sessions. Prefer SubscribeStateChanges for new
// consumers; multiple StateChanges readers compete for events.
//
// The channel is buffered (64 entries). If the consumer falls behind,
// individual session goroutines will drop notifications (logged at warn level).
//
// Micro-BFD state changes are dispatched internally by the Manager's
// RunDispatch goroutine before forwarding to this channel.
func (m *Manager) StateChanges() <-chan StateChange {
	return m.publicNotifyCh
}

// SubscribeStateChanges returns a per-consumer channel that receives every
// manager state change until ctx is cancelled or Manager.Close begins. Slow
// subscribers drop their own events without affecting other subscribers or
// session goroutines. Calls made after shutdown begins fail without registering
// a subscriber.
func (m *Manager) SubscribeStateChanges(ctx context.Context) (<-chan StateChange, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("subscribe state changes with canceled context: %w", err)
	}

	m.lifecycleMu.RLock()
	switch m.lifecycleState {
	case managerClosing:
		m.lifecycleMu.RUnlock()
		return nil, ErrManagerClosing
	case managerClosed:
		m.lifecycleMu.RUnlock()
		return nil, ErrManagerClosed
	case managerOpen:
	default:
		m.lifecycleMu.RUnlock()
		return nil, ErrManagerClosed
	}

	ch := make(chan StateChange, notifyChSize)

	m.subMu.Lock()
	m.subscribers[ch] = struct{}{}
	m.subMu.Unlock()

	m.workers.Go(func() {
		select {
		case <-ctx.Done():
		case <-m.shutdownCh:
		}
		m.subMu.Lock()
		if _, ok := m.subscribers[ch]; ok {
			delete(m.subscribers, ch)
			close(ch)
		}
		m.subMu.Unlock()
	})
	m.lifecycleMu.RUnlock()

	return ch, nil
}

// -------------------------------------------------------------------------
// Session Reconciliation — SIGHUP reload
// -------------------------------------------------------------------------

// ReconcileConfig describes a desired BFD session for reconciliation.
// The Manager creates sessions that are missing and destroys sessions
// that no longer appear in the desired set.
type ReconcileConfig struct {
	// Key uniquely identifies the session for diffing purposes.
	// Typically: "peer|local|interface".
	Key string

	// SessionConfig is the BFD session configuration to create if missing.
	SessionConfig SessionConfig

	// SenderLeaseFactory lazily acquires the sender and its release operation
	// only after Manager determines that a new physical session is required.
	SenderLeaseFactory SenderLeaseFactory
}

// ReconcileSessions reconciles only claims owned by the configuration source.
// Compatibility/API claims and their wire sessions are preserved when absent
// from desired. Existing exact sessions are shared across sources.
//
// Returns the number of sessions created and destroyed, and any errors
// encountered. Partial failures are logged and accumulated; reconciliation
// continues for all sessions.
func (m *Manager) ReconcileSessions(
	ctx context.Context,
	desired []ReconcileConfig,
) (int, int, error) {
	result := m.reconcileSessionsForOwner(ctx, configSessionOwner(), desired)
	return result.wireCreated, result.wireDestroyed, result.Err()
}

// ReconcileSessionsForOwner reconciles only claims held by owner. Declarative
// adapters use distinct typed owners so one source cannot release another
// source's claims. Exact matching claims still share a wire session.
func (m *Manager) ReconcileSessionsForOwner(
	ctx context.Context,
	owner SessionOwner,
	desired []ReconcileConfig,
) (int, int, error) {
	result := m.reconcileSessionsForOwner(ctx, owner, desired)
	return result.wireCreated, result.wireDestroyed, result.Err()
}

// ReconcileSessionsForOwnerDetailed reconciles one declarative owner's
// complete desired set and returns claim-level net changes. Exact claims from
// different owners may share one physical wire session, so these counts are
// deliberately distinct from the legacy tuple returned above.
func (m *Manager) ReconcileSessionsForOwnerDetailed(
	ctx context.Context,
	owner SessionOwner,
	desired []ReconcileConfig,
) ReconcileResult {
	return m.reconcileSessionsForOwner(ctx, owner, desired)
}

func (m *Manager) reconcileSessionsForOwner(
	ctx context.Context,
	owner SessionOwner,
	desired []ReconcileConfig,
) ReconcileResult {
	op, err := m.beginOperation()
	if err != nil {
		return failedReconcileResult(ReconcileErrorLifecycle, err)
	}
	defer op.finish()

	if !isDeclarativeReconciliationOwner(owner) {
		return failedReconcileResult(
			ReconcileErrorInvalid,
			fmt.Errorf("reconcile sessions for owner source %d ID %q: %w",
				owner.Source, owner.ID, ErrInvalidReconciliationOwner),
		)
	}

	m.ownershipMu.Lock()

	desiredByKey, desiredOrder, err := compileReconcileConfigs(desired)
	if err != nil {
		m.ownershipMu.Unlock()
		return failedReconcileResult(ReconcileErrorInvalid, err)
	}

	currentClaims, err := m.ownerClaimSnapshot(owner, desiredByKey, desiredOrder)
	if err != nil {
		m.ownershipMu.Unlock()
		return failedReconcileResult(ReconcileErrorConflict, err)
	}

	result, retired := m.releaseStaleOwnerClaims(owner, desiredByKey, currentClaims)
	claimResult, rollbackRetired, unusedLeases := m.claimDesiredOwnerSessions(
		ctx, owner, desiredByKey, desiredOrder, currentClaims,
	)
	result.Created = claimResult.Created
	result.wireCreated = claimResult.wireCreated
	result.Errors = append(result.Errors, claimResult.Errors...)
	result.Failed = len(result.Errors)
	retired = append(retired, rollbackRetired...)
	m.ownershipMu.Unlock()
	op.unlockMutation()

	for _, cleanupErr := range m.finishRetiredSessions(retired) {
		addReconcileError(&result, ReconcileErrorCleanup, cleanupErr)
	}
	for _, lease := range unusedLeases {
		if closeErr := closeSenderLeaseError(lease); closeErr != nil {
			addReconcileError(&result, ReconcileErrorCleanup, closeErr)
		}
	}

	m.logSessionReconcileResult(result)

	return result
}

func (m *Manager) logSessionReconcileResult(result ReconcileResult) {
	if result.Err() != nil {
		m.logger.Warn("session reconciliation incomplete",
			slog.Int("created", result.Created),
			slog.Int("released", result.Released),
			slog.Int("failed", result.Failed),
		)
	} else {
		m.logger.Info("session reconciliation complete",
			slog.Int("created", result.Created),
			slog.Int("released", result.Released),
		)
	}
}

// ValidateReconcileConfigs validates the complete desired set without opening
// senders or mutating Manager state.
func ValidateReconcileConfigs(desired []ReconcileConfig) error {
	_, _, err := compileReconcileConfigs(desired)
	return err
}

type reconcileCandidate struct {
	config    ReconcileConfig
	effective effectiveSessionConfig
}

func compileReconcileConfigs(
	desired []ReconcileConfig,
) (map[SessionKey]reconcileCandidate, []SessionKey, error) {
	desiredByKey := make(map[SessionKey]reconcileCandidate, len(desired))
	desiredOrder := make([]SessionKey, 0, len(desired))
	for _, rc := range desired {
		rc.SessionConfig = canonicalSessionConfig(rc.SessionConfig)
		if err := ValidateSessionConfig(rc.SessionConfig); err != nil {
			return nil, nil, fmt.Errorf("reconcile config %q: %w", rc.Key, err)
		}
		key, err := sessionKeyFromConfig(rc.SessionConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("reconcile config %q: %w", rc.Key, err)
		}
		effective, err := normalizeEffectiveSessionConfig(rc.SessionConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("reconcile config %q: %w", rc.Key, err)
		}
		if previous, exists := desiredByKey[key]; exists {
			if previous.effective != effective {
				return nil, nil, fmt.Errorf("reconcile duplicate session %+v: %w",
					key, ErrSessionParameterConflict)
			}
			continue
		}
		desiredByKey[key] = reconcileCandidate{config: rc, effective: effective}
		desiredOrder = append(desiredOrder, key)
	}
	return desiredByKey, desiredOrder, nil
}

func (m *Manager) ownerClaimSnapshot(
	owner SessionOwner,
	desiredByKey map[SessionKey]reconcileCandidate,
	desiredOrder []SessionKey,
) (map[SessionKey]uint32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	currentClaims := make(map[SessionKey]uint32)
	for key, entry := range m.sessionsByKey {
		if _, owned := entry.owners[owner]; owned {
			currentClaims[key] = entry.session.LocalDiscriminator()
		}
	}
	for _, key := range desiredOrder {
		entry, exists := m.sessionsByKey[key]
		if !exists {
			continue
		}
		if entry.effective != desiredByKey[key].effective {
			return nil, fmt.Errorf("reconcile session %+v: %w",
				key, ErrSessionParameterConflict)
		}
	}
	return currentClaims, nil
}

func (m *Manager) releaseStaleOwnerClaims(
	owner SessionOwner,
	desiredByKey map[SessionKey]reconcileCandidate,
	currentClaims map[SessionKey]uint32,
) (ReconcileResult, []retiredSession) {
	var result ReconcileResult
	var retired []retiredSession
	for key, discr := range currentClaims {
		if _, want := desiredByKey[key]; want {
			continue
		}

		m.logger.Info("reconcile: releasing removed owner claim",
			slog.Any("key", key),
			slog.Any("owner", owner),
			slog.Uint64("local_discr", uint64(discr)),
		)

		wireDestroyed, detached, releaseErr := m.detachSessionClaimByDiscriminator(discr, owner)
		if releaseErr != nil {
			addReconcileError(&result, ReconcileErrorRelease,
				fmt.Errorf("reconcile release %+v: %w", key, releaseErr))
			continue
		}
		result.Released++
		if wireDestroyed {
			result.wireDestroyed++
			retired = append(retired, *detached)
		}
	}
	return result, retired
}

func (m *Manager) claimDesiredOwnerSessions(
	ctx context.Context,
	owner SessionOwner,
	desiredByKey map[SessionKey]reconcileCandidate,
	desiredOrder []SessionKey,
	currentClaims map[SessionKey]uint32,
) (ReconcileResult, []retiredSession, []*SenderLease) {
	var result ReconcileResult
	var retired []retiredSession
	var unusedLeases []*SenderLease
	newClaims := make([]uint32, 0, len(desiredOrder))
	for _, key := range desiredOrder {
		rc := desiredByKey[key].config

		m.logger.Info("reconcile: claiming desired owner session",
			slog.Any("key", key),
			slog.Any("owner", owner),
		)

		sess, wireCreated, unusedLease, claimErr := m.claimSession(
			ctx, rc.SessionConfig, rc.SenderLeaseFactory, owner, true,
		)
		if unusedLease != nil {
			unusedLeases = append(unusedLeases, unusedLease)
		}
		if claimErr != nil {
			addReconcileError(&result, ReconcileErrorCreate,
				fmt.Errorf("reconcile claim %+v: %w", key, claimErr))
			continue
		}
		if wireCreated {
			result.wireCreated++
		}
		if _, alreadyClaimed := currentClaims[key]; !alreadyClaimed {
			result.Created++
			newClaims = append(newClaims, sess.LocalDiscriminator())
		}
	}
	if result.Failed == 0 {
		return result, nil, unusedLeases
	}

	// Roll back claims added by this creation pass. This closes accepted
	// sender leases for newly created physical sessions without attempting the
	// broader stale-claim/full-reload rollback owned by a later lifecycle slice.
	for _, discr := range newClaims {
		wireDestroyed, detached, err := m.detachSessionClaimByDiscriminator(discr, owner)
		if err != nil {
			addReconcileError(&result, ReconcileErrorRollback,
				fmt.Errorf("rollback new session claim %d: %w", discr, err))
			continue
		}
		result.Created--
		if wireDestroyed {
			result.wireCreated--
			retired = append(retired, *detached)
		}
	}
	return result, retired, unusedLeases
}

func (m *Manager) finishRetiredSessions(retired []retiredSession) []error {
	var errs []error
	for _, item := range retired {
		if err := m.finishSessionDestroyError(item.localDiscr, item.entry); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
