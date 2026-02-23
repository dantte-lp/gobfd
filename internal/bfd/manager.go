package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// -------------------------------------------------------------------------
// Manager Errors
// -------------------------------------------------------------------------

// Sentinel errors for Manager operations.
var (
	// ErrSessionNotFound indicates no session exists for the given discriminator.
	ErrSessionNotFound = errors.New("session not found")

	// ErrDuplicateSession indicates a session already exists for the given peer key.
	ErrDuplicateSession = errors.New("duplicate session for peer key")

	// ErrDemuxNoMatch indicates no session matched the incoming packet during
	// demultiplexing (RFC 5880 Section 6.8.6).
	ErrDemuxNoMatch = errors.New("no matching session for incoming packet")

	// ErrInvalidPeerAddr indicates the peer address is not valid.
	ErrInvalidPeerAddr = errors.New("peer address must be valid")
)

// createSessionErrPrefix is the common error prefix for session creation failures.
const createSessionErrPrefix = "create session"

// -------------------------------------------------------------------------
// PacketMeta — transport metadata for demultiplexing
// -------------------------------------------------------------------------

// PacketMeta contains the transport-layer metadata needed for BFD session
// demultiplexing. This is a BFD-package-local type to avoid import cycles
// between bfd and netio. The listener layer converts netio.PacketMeta to
// bfd.PacketMeta before calling Manager.Demux.
type PacketMeta struct {
	// SrcAddr is the source IP address from the received packet.
	SrcAddr netip.Addr

	// DstAddr is the destination IP address from the received packet.
	DstAddr netip.Addr

	// TTL is the Time-to-Live / Hop Limit from the IP header.
	TTL uint8

	// IfName is the interface name on which the packet was received.
	IfName string
}

// -------------------------------------------------------------------------
// Session Key — peer identity for initial demultiplexing
// -------------------------------------------------------------------------

// sessionKey is the composite key for initial session demultiplexing when
// Your Discriminator is zero (RFC 5880 Section 6.8.6).
//
// For single-hop (RFC 5881 Section 3): match by (PeerAddr, LocalAddr, IfName).
// For multi-hop (RFC 5883): match by (PeerAddr, LocalAddr) — IfName is empty.
type sessionKey struct {
	peerAddr  netip.Addr
	localAddr netip.Addr
	ifName    string
}

// -------------------------------------------------------------------------
// Session Snapshot — read-only view for external consumers
// -------------------------------------------------------------------------

// SessionSnapshot is a read-only view of a session's state at a point in time.
// Used by the ListSessions RPC and monitoring interfaces. All fields are
// copied from the session; no references to mutable state are held.
type SessionSnapshot struct {
	// LocalDiscr is the local discriminator (RFC 5880 Section 6.8.1).
	LocalDiscr uint32

	// RemoteDiscr is the remote discriminator learned from the peer.
	RemoteDiscr uint32

	// PeerAddr is the remote system's IP address.
	PeerAddr netip.Addr

	// LocalAddr is the local system's IP address.
	LocalAddr netip.Addr

	// Interface is the network interface name (empty for multi-hop).
	Interface string

	// Type is the session type (single-hop or multi-hop).
	Type SessionType

	// State is the current session FSM state (atomic snapshot).
	State State

	// RemoteState is the last reported remote session state (atomic snapshot).
	RemoteState State

	// LocalDiag is the current local diagnostic code (atomic snapshot).
	LocalDiag Diag

	// DesiredMinTx is the configured desired minimum TX interval.
	DesiredMinTx time.Duration

	// RequiredMinRx is the configured required minimum RX interval.
	RequiredMinRx time.Duration

	// DetectMultiplier is the configured detection multiplier.
	DetectMultiplier uint8

	// NegotiatedTxInterval is the actual TX interval after negotiation.
	// RFC 5880 Section 6.8.7: max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval).
	NegotiatedTxInterval time.Duration

	// DetectionTime is the calculated detection time.
	// RFC 5880 Section 6.8.4: RemoteDetectMult * max(RequiredMinRx, RemoteDesiredMinTx).
	DetectionTime time.Duration

	// LastStateChange is the timestamp of the most recent FSM state transition.
	// Zero value means no transition has occurred since session creation.
	LastStateChange time.Time

	// LastPacketReceived is the timestamp of the most recent valid BFD
	// Control packet received from the peer. Zero value means no packet
	// has been received yet.
	LastPacketReceived time.Time

	// Unsolicited indicates the session was auto-created via RFC 9468.
	Unsolicited bool

	// Counters contains per-session packet and state transition counters.
	Counters SessionCounters
}

// SessionCounters holds per-session atomic counter snapshots.
// These are monotonically increasing counters for the lifetime of the session.
type SessionCounters struct {
	// PacketsSent is the total BFD Control packets transmitted.
	PacketsSent uint64

	// PacketsReceived is the total BFD Control packets received.
	PacketsReceived uint64

	// StateTransitions is the total FSM state transitions.
	StateTransitions uint64
}

// -------------------------------------------------------------------------
// Notify Channel Size
// -------------------------------------------------------------------------

const (
	// notifyChSize is the buffer size for the aggregated state change channel.
	// Sized to handle bursts of state transitions across multiple sessions
	// without blocking session goroutines. 64 is sufficient for typical
	// deployments (hundreds of sessions with rare simultaneous transitions).
	notifyChSize = 64
)

// -------------------------------------------------------------------------
// Manager — BFD Session Manager
// -------------------------------------------------------------------------

// Manager owns all BFD sessions, handles demultiplexing of incoming packets,
// and provides the CRUD API for session lifecycle.
//
// Demultiplexing strategy (RFC 5880 Section 6.8.6, Section 6.3):
//
//  1. If Your Discriminator != 0:
//     Look up session by Your Discriminator (O(1) map lookup).
//     If no session found, discard.
//
//  2. If Your Discriminator == 0 AND State is Down or AdminDown:
//     Match by (source IP, dest IP, interface) for single-hop (RFC 5881 Section 3).
//     Match by (source IP, dest IP) for multi-hop (RFC 5883).
//     If no match found AND unsolicited BFD is enabled (RFC 9468):
//     auto-create a passive session for the peer.
//     If no match and unsolicited disabled: discard.
//
// This two-tier lookup is the standard BFD demux pattern (FRR, GoBGP, Junos).
type Manager struct {
	// sessions indexed by local discriminator (primary lookup).
	sessions map[uint32]*sessionEntry

	// sessionsByPeer indexed by peer key for initial demux
	// when Your Discriminator is zero.
	sessionsByPeer map[sessionKey]*sessionEntry

	mu sync.RWMutex

	discriminators *DiscriminatorAllocator

	// metrics is the optional metrics reporter. Never nil -- uses noopMetrics
	// when no collector is configured.
	metrics MetricsReporter

	// unsolicited holds the RFC 9468 unsolicited BFD state.
	// nil when unsolicited BFD is not configured.
	unsolicited *unsolicitedState

	// unsolicitedSender provides packet sending for auto-created sessions.
	// Set via WithUnsolicitedSender option.
	unsolicitedSender PacketSender

	// notifyCh aggregates state changes from all sessions.
	notifyCh chan StateChange

	logger *slog.Logger
}

// sessionEntry holds a session and its cancellation function.
// The cancel function is used by DestroySession to stop the session goroutine.
type sessionEntry struct {
	session *Session
	cancel  context.CancelFunc
	key     sessionKey
}

// ManagerOption configures optional Manager parameters.
type ManagerOption func(*Manager)

// WithManagerMetrics sets the MetricsReporter for the manager and all
// sessions it creates. If mr is nil, a no-op reporter is used.
func WithManagerMetrics(mr MetricsReporter) ManagerOption {
	return func(m *Manager) {
		if mr != nil {
			m.metrics = mr
		}
	}
}

// WithUnsolicitedPolicy enables RFC 9468 unsolicited BFD with the given policy.
// When set, the Manager auto-creates passive sessions for incoming packets
// from unknown peers, subject to the policy's interface and prefix restrictions.
func WithUnsolicitedPolicy(policy *UnsolicitedPolicy) ManagerOption {
	return func(m *Manager) {
		if policy != nil && policy.Enabled {
			m.unsolicited = newUnsolicitedState(policy)
		}
	}
}

// WithUnsolicitedSender sets the PacketSender used for auto-created
// unsolicited sessions. Required when unsolicited BFD is enabled.
func WithUnsolicitedSender(sender PacketSender) ManagerOption {
	return func(m *Manager) {
		m.unsolicitedSender = sender
	}
}

// NewManager creates a new BFD session manager.
//
// The manager allocates local discriminators (RFC 5880 Section 6.8.1),
// manages session lifecycle, and provides demultiplexing for incoming
// BFD Control packets.
func NewManager(logger *slog.Logger, opts ...ManagerOption) *Manager {
	m := &Manager{
		sessions:       make(map[uint32]*sessionEntry),
		sessionsByPeer: make(map[sessionKey]*sessionEntry),
		discriminators: NewDiscriminatorAllocator(),
		metrics:        noopMetrics{},
		notifyCh:       make(chan StateChange, notifyChSize),
		logger:         logger.With(slog.String("component", "bfd.manager")),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// -------------------------------------------------------------------------
// Session CRUD — Create
// -------------------------------------------------------------------------

// CreateSession creates a new BFD session with the given configuration.
//
// The session is registered in both lookup maps (by discriminator and by
// peer key) and its Run goroutine is started. The session begins in Down
// state per RFC 5880 Section 6.8.1.
//
// Returns ErrDuplicateSession if a session already exists for the same
// peer key (peerAddr, localAddr, interface).
func (m *Manager) CreateSession(
	ctx context.Context,
	cfg SessionConfig,
	sender PacketSender,
) (*Session, error) {
	if !cfg.PeerAddr.IsValid() {
		return nil, fmt.Errorf("%s: %w", createSessionErrPrefix, ErrInvalidPeerAddr)
	}

	key := sessionKey{
		peerAddr:  cfg.PeerAddr,
		localAddr: cfg.LocalAddr,
		ifName:    cfg.Interface,
	}

	if err := m.checkDuplicate(key, cfg.PeerAddr); err != nil {
		return nil, err
	}

	discr, sess, err := m.allocateAndBuild(cfg, sender)
	if err != nil {
		return nil, err
	}

	if err := m.registerAndStart(ctx, key, discr, sess); err != nil {
		m.discriminators.Release(discr)
		return nil, err
	}

	m.logSessionCreated(cfg, discr)

	return sess, nil
}

// checkDuplicate verifies no session exists for the given peer key.
func (m *Manager) checkDuplicate(key sessionKey, peerAddr netip.Addr) error {
	m.mu.RLock()
	_, exists := m.sessionsByPeer[key]
	m.mu.RUnlock()

	if exists {
		return fmt.Errorf(
			"create session for peer %s: %w",
			peerAddr, ErrDuplicateSession,
		)
	}
	return nil
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

	sess, err := NewSession(cfg, discr, sender, m.notifyCh, m.logger,
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
	key sessionKey,
	discr uint32,
	sess *Session,
) error {
	m.mu.Lock()
	if _, dup := m.sessionsByPeer[key]; dup {
		m.mu.Unlock()
		return fmt.Errorf(
			"create session for peer %s: %w",
			key.peerAddr, ErrDuplicateSession,
		)
	}

	entry := &sessionEntry{session: sess, key: key}
	// Decouple session lifetime from the parent context so that SIGTERM
	// does not immediately cancel sessions. Graceful shutdown first sets
	// AdminDown (DrainAllSessions), waits for packets to be sent, and
	// only then calls Manager.Close which cancels each session explicitly.
	sessCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry.cancel = cancel
	go sess.Run(sessCtx)

	m.sessions[discr] = entry
	m.sessionsByPeer[key] = entry
	m.mu.Unlock()

	return nil
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

// DestroySession stops and removes the session identified by localDiscr.
//
// The session goroutine is cancelled, the session is removed from both
// lookup maps, and the discriminator is released for reuse.
//
// Returns ErrSessionNotFound if no session exists with the given discriminator.
func (m *Manager) DestroySession(_ context.Context, localDiscr uint32) error {
	m.mu.Lock()
	entry, ok := m.sessions[localDiscr]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf(
			"destroy session with discriminator %d: %w",
			localDiscr, ErrSessionNotFound,
		)
	}

	// Remove from both maps.
	delete(m.sessions, localDiscr)
	delete(m.sessionsByPeer, entry.key)
	m.mu.Unlock()

	// Cancel session goroutine (outside lock to avoid holding lock during
	// goroutine teardown).
	entry.cancel()

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

	return nil
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
func (m *Manager) LookupByPeer(key sessionKey) (*Session, bool) {
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
	key := sessionKey{
		peerAddr:  meta.SrcAddr,
		localAddr: meta.DstAddr,
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
	key := sessionKey{
		peerAddr:  meta.SrcAddr,
		localAddr: meta.DstAddr,
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
	// RFC 9468 Section 6.1: unsolicited BFD is single-hop only.
	// Multi-hop packets arrive on port 4784; single-hop on 3784.
	// We use the interface name as a proxy: multi-hop sessions have no interface.
	// Also validate via policy.

	if err := m.unsolicited.checkPolicy(meta.SrcAddr, meta.IfName); err != nil {
		return fmt.Errorf(
			"unsolicited: peer %s on %s: %w",
			meta.SrcAddr, meta.IfName, err,
		)
	}

	defaults := m.unsolicited.policy.SessionDefaults
	cfg := SessionConfig{
		PeerAddr:              meta.SrcAddr,
		LocalAddr:             meta.DstAddr,
		Interface:             meta.IfName,
		Type:                  SessionTypeSingleHop,
		Role:                  RolePassive,
		DesiredMinTxInterval:  defaults.DesiredMinTxInterval,
		RequiredMinRxInterval: defaults.RequiredMinRxInterval,
		DetectMultiplier:      defaults.DetectMultiplier,
	}

	sender := m.unsolicitedSender
	if sender == nil {
		return fmt.Errorf(
			"unsolicited: no sender configured for peer %s: %w",
			meta.SrcAddr, ErrUnsolicitedDisabled,
		)
	}

	sess, err := m.CreateSession(context.Background(), cfg, sender)
	if err != nil {
		return fmt.Errorf("unsolicited: create session for peer %s: %w", meta.SrcAddr, err)
	}

	m.unsolicited.incrementCount()

	m.logger.Info("unsolicited session created (RFC 9468)",
		slog.String("peer", meta.SrcAddr.String()),
		slog.String("local", meta.DstAddr.String()),
		slog.String("interface", meta.IfName),
		slog.Uint64("local_discr", uint64(sess.LocalDiscriminator())),
	)

	// Deliver the initial packet that triggered session creation.
	sess.RecvPacket(pkt, wire)

	return nil
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

// StateChanges returns a read-only channel that receives state change
// notifications from all sessions. This channel is intended for the gRPC
// streaming API (MonitorSessions).
//
// The channel is buffered (64 entries). If the consumer falls behind,
// individual session goroutines will drop notifications (logged at warn level).
func (m *Manager) StateChanges() <-chan StateChange {
	return m.notifyCh
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

	// Sender provides the packet sending capability for new sessions.
	Sender PacketSender
}

// ReconcileSessions diffs the desired session set against the current sessions.
// Sessions present in desired but absent are created. Sessions present in
// current but absent from desired are destroyed. Existing sessions are left
// untouched (parameter changes require a separate Poll Sequence mechanism).
//
// Returns the number of sessions created and destroyed, and any errors
// encountered. Partial failures are logged and accumulated; reconciliation
// continues for all sessions.
func (m *Manager) ReconcileSessions(
	ctx context.Context,
	desired []ReconcileConfig,
) (int, int, error) {
	// Build desired key set.
	desiredKeys := make(map[string]ReconcileConfig, len(desired))
	for _, rc := range desired {
		desiredKeys[rc.Key] = rc
	}

	// Build current key set.
	currentKeys := m.sessionKeySet()

	// Destroy sessions not in desired set.
	var created, destroyed int
	var errs []error
	for key, discr := range currentKeys {
		if _, want := desiredKeys[key]; want {
			continue
		}

		m.logger.Info("reconcile: destroying removed session",
			slog.String("key", key),
			slog.Uint64("local_discr", uint64(discr)),
		)

		if dErr := m.DestroySession(ctx, discr); dErr != nil {
			errs = append(errs, fmt.Errorf("reconcile destroy %s: %w", key, dErr))
			continue
		}

		destroyed++
	}

	// Create sessions in desired but not in current.
	for key, rc := range desiredKeys {
		if _, exists := currentKeys[key]; exists {
			continue
		}

		m.logger.Info("reconcile: creating new session",
			slog.String("key", key),
		)

		if _, cErr := m.CreateSession(ctx, rc.SessionConfig, rc.Sender); cErr != nil {
			errs = append(errs, fmt.Errorf("reconcile create %s: %w", key, cErr))
			continue
		}

		created++
	}

	var err error
	if len(errs) > 0 {
		err = errors.Join(errs...)
	}

	m.logger.Info("session reconciliation complete",
		slog.Int("created", created),
		slog.Int("destroyed", destroyed),
	)

	return created, destroyed, err
}

// sessionKeySet returns a map of session key -> local discriminator for all
// currently active sessions.
func (m *Manager) sessionKeySet() map[string]uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make(map[string]uint32, len(m.sessionsByPeer))
	for sk, entry := range m.sessionsByPeer {
		key := sk.peerAddr.String() + "|" + sk.localAddr.String() + "|" + sk.ifName
		keys[key] = entry.session.LocalDiscriminator()
	}

	return keys
}

// -------------------------------------------------------------------------
// Graceful Drain — RFC 5880 Section 6.8.16
// -------------------------------------------------------------------------

// DrainAllSessions transitions all sessions to AdminDown with
// DiagAdminDown (RFC 5880 Section 6.8.16). This signals peers that the
// shutdown is intentional, not a failure. The caller should wait briefly
// for the final AdminDown packets to be transmitted before closing.
func (m *Manager) DrainAllSessions() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, entry := range m.sessions {
		entry.session.SetAdminDown()
	}

	m.logger.Info("all sessions set to AdminDown for graceful drain",
		slog.Int("count", len(m.sessions)),
	)
}

// -------------------------------------------------------------------------
// Lifecycle
// -------------------------------------------------------------------------

// Close cancels all session goroutines and releases resources.
// After Close returns, no new sessions can be created and the StateChanges
// channel should no longer be read.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for discr, entry := range m.sessions {
		entry.cancel()
		m.discriminators.Release(discr)
	}

	// Clear maps to prevent use-after-close.
	m.sessions = make(map[uint32]*sessionEntry)
	m.sessionsByPeer = make(map[sessionKey]*sessionEntry)

	m.logger.Info("manager closed")
}
