package bfd

import (
	"context"
	"errors"
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

	// ErrEchoSessionNotFound indicates no echo session exists for the given discriminator.
	ErrEchoSessionNotFound = errors.New("echo session not found")

	// ErrEchoDemuxNoMatch indicates no echo session matched the incoming packet's
	// MyDiscriminator during demultiplexing (RFC 9747).
	ErrEchoDemuxNoMatch = errors.New("no matching echo session for incoming packet")

	// ErrMicroBFDGroupNotFound indicates no micro-BFD group exists for the
	// given LAG interface name.
	ErrMicroBFDGroupNotFound = errors.New("micro-BFD group not found")

	// ErrMicroBFDGroupExists indicates a micro-BFD group already exists for
	// the given LAG interface name.
	ErrMicroBFDGroupExists = errors.New("micro-BFD group already exists for LAG interface")
)

// createSessionErrPrefix is the common error prefix for session creation failures.
const createSessionErrPrefix = "create session"

// createEchoSessionErrPrefix is the common error prefix for echo session creation failures.
const createEchoSessionErrPrefix = "create echo session"

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

	// PaddedPduSize is the RFC 9764 padded PDU size. Zero means no padding.
	PaddedPduSize uint16

	// AuthType is the RFC 5880 authentication type configured for the session.
	AuthType AuthType

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

	// echoSessions indexed by local discriminator for echo demux.
	// RFC 9747: echo packets are demultiplexed by MyDiscriminator on return.
	echoSessions map[uint32]*echoSessionEntry

	// microGroups holds RFC 7130 micro-BFD groups indexed by LAG interface name.
	// Each group tracks the aggregate state of per-member-link BFD sessions.
	microGroups map[string]*MicroBFDGroup

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

	// microActuator receives RFC 7130 member state transitions after the
	// Manager updates its MicroBFDGroup aggregate state.
	microActuator MicroBFDActuator

	// rawNotifyCh receives state changes from all sessions.
	// The Manager's dispatch goroutine reads from this channel,
	// handles micro-BFD group updates, and forwards to publicNotifyCh.
	rawNotifyCh chan StateChange

	// publicNotifyCh is the legacy single-consumer channel exposed via
	// StateChanges(). New consumers should use SubscribeStateChanges.
	publicNotifyCh chan StateChange

	subscribers map[chan StateChange]struct{}
	subMu       sync.RWMutex

	logger *slog.Logger
}

// sessionEntry holds a session and its cancellation function.
// The cancel function is used by DestroySession to stop the session goroutine.
type sessionEntry struct {
	session     *Session
	cancel      context.CancelFunc
	key         sessionKey
	unsolicited bool
}

// echoSessionEntry holds an echo session and its cancellation function.
// The cancel function is used by DestroyEchoSession to stop the echo session goroutine.
type echoSessionEntry struct {
	session *EchoSession
	cancel  context.CancelFunc
}

// ManagerOption configures optional Manager parameters.
type ManagerOption func(*Manager)

// MicroBFDActuator reacts to RFC 7130 Micro-BFD member state transitions.
//
// Implementations must be quick or internally asynchronous. RunDispatch calls
// the actuator synchronously to preserve ordering between member transitions.
type MicroBFDActuator interface {
	HandleMicroBFDMemberEvent(ctx context.Context, event MicroBFDMemberEvent) error
}

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

// WithMicroBFDActuator sets an optional actuator for RFC 7130 Micro-BFD
// member state transitions. A nil actuator leaves GoBFD in detect/report mode.
func WithMicroBFDActuator(actuator MicroBFDActuator) ManagerOption {
	return func(m *Manager) {
		m.microActuator = actuator
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
		echoSessions:   make(map[uint32]*echoSessionEntry),
		microGroups:    make(map[string]*MicroBFDGroup),
		discriminators: NewDiscriminatorAllocator(),
		metrics:        noopMetrics{},
		rawNotifyCh:    make(chan StateChange, notifyChSize),
		publicNotifyCh: make(chan StateChange, notifyChSize),
		subscribers:    make(map[chan StateChange]struct{}),
		logger:         logger.With(slog.String("component", "bfd.manager")),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// -------------------------------------------------------------------------
// State Change Dispatch — internal fan-out with micro-BFD routing
// -------------------------------------------------------------------------

// RunDispatch reads state change notifications from all sessions (rawNotifyCh),
// dispatches micro-BFD events to the appropriate group's UpdateMemberState,
// and forwards all notifications to the public StateChanges channel.
//
// This goroutine MUST be running for state change notifications to reach
// external consumers (GoBGP handler, gRPC streaming). Without RunDispatch,
// the rawNotifyCh will fill up and sessions will drop notifications.
//
// Blocks until ctx is cancelled.
func (m *Manager) RunDispatch(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case sc := <-m.rawNotifyCh:
			// Dispatch micro-BFD events to the group.
			if sc.Type == SessionTypeMicroBFD && sc.Interface != "" {
				m.dispatchMicroBFD(ctx, sc)
			}
			if sc.NewState == StateDown {
				m.scheduleUnsolicitedCleanup(ctx, sc)
			}
			m.broadcastStateChange(sc)

			// Forward to the legacy single-consumer channel.
			select {
			case m.publicNotifyCh <- sc:
			default:
				m.logger.Warn("public notification channel full, dropping state change",
					slog.Uint64("local_discr", uint64(sc.LocalDiscr)),
					slog.String("new_state", sc.NewState.String()),
				)
			}
		}
	}
}

func (m *Manager) broadcastStateChange(sc StateChange) {
	m.subMu.RLock()
	defer m.subMu.RUnlock()

	for ch := range m.subscribers {
		select {
		case ch <- sc:
		default:
			m.logger.Warn("state change subscriber channel full, dropping event",
				slog.Uint64("local_discr", uint64(sc.LocalDiscr)),
				slog.String("new_state", sc.NewState.String()),
			)
		}
	}
}

func (m *Manager) scheduleUnsolicitedCleanup(ctx context.Context, sc StateChange) {
	if m.unsolicited == nil {
		return
	}

	m.mu.RLock()
	entry, ok := m.sessions[sc.LocalDiscr]
	isUnsolicited := ok && entry.unsolicited
	m.mu.RUnlock()
	if !isUnsolicited {
		return
	}

	timeout := m.unsolicited.policy.CleanupTimeout
	go func() {
		if timeout > 0 {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		m.cleanupUnsolicitedSession(ctx, sc.LocalDiscr)
	}()
}

func (m *Manager) cleanupUnsolicitedSession(ctx context.Context, localDiscr uint32) {
	m.mu.RLock()
	entry, ok := m.sessions[localDiscr]
	if !ok || !entry.unsolicited || entry.session.State() != StateDown {
		m.mu.RUnlock()
		return
	}
	peer := entry.session.PeerAddr()
	m.mu.RUnlock()

	if err := m.DestroySession(ctx, localDiscr); err != nil {
		m.logger.Debug("unsolicited cleanup skipped",
			slog.Uint64("local_discr", uint64(localDiscr)),
			slog.String("peer", peer.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	m.logger.Info("unsolicited session cleaned up",
		slog.Uint64("local_discr", uint64(localDiscr)),
		slog.String("peer", peer.String()),
	)
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

	for discr, entry := range m.echoSessions {
		entry.cancel()
		m.discriminators.Release(discr)
	}

	// Clear maps to prevent use-after-close.
	m.sessions = make(map[uint32]*sessionEntry)
	m.sessionsByPeer = make(map[sessionKey]*sessionEntry)
	m.echoSessions = make(map[uint32]*echoSessionEntry)
	m.microGroups = make(map[string]*MicroBFDGroup)

	m.subMu.Lock()
	for ch := range m.subscribers {
		delete(m.subscribers, ch)
		close(ch)
	}
	m.subMu.Unlock()

	m.logger.Info("manager closed")
}
