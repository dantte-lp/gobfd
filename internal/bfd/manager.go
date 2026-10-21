package bfd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
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

	// ErrSessionParameterConflict indicates matching ownership claims requested
	// different effective parameters for one canonical wire session.
	ErrSessionParameterConflict = errors.New("conflicting effective session parameters")

	// ErrSessionOwnerClaimNotFound indicates a source attempted to release a
	// session without owning a claim on it.
	ErrSessionOwnerClaimNotFound = errors.New("session owner claim not found")

	// ErrInvalidReconciliationOwner indicates reconciliation was requested for
	// an owner other than one of the declarative configuration sources.
	ErrInvalidReconciliationOwner = errors.New("invalid reconciliation owner")

	// ErrSenderLeaseFactoryNil indicates session creation lacks a sender factory.
	ErrSenderLeaseFactoryNil = errors.New("sender lease factory is nil")

	// ErrSenderLeaseNil indicates a sender factory returned no lease.
	ErrSenderLeaseNil = errors.New("sender lease factory returned nil lease")

	// ErrSenderLeaseSenderNil indicates a lease contains no packet sender.
	ErrSenderLeaseSenderNil = errors.New("sender lease has nil sender")

	// ErrManagerClosing indicates an operation was rejected because Manager
	// shutdown has begun but has not completed.
	ErrManagerClosing = errors.New("manager is closing")

	// ErrManagerClosed indicates an operation was rejected after Manager
	// shutdown completed.
	ErrManagerClosed = errors.New("manager is closed")

	// ErrDemuxNoMatch indicates no session matched the incoming packet during
	// demultiplexing (RFC 5880 Section 6.8.6).
	ErrDemuxNoMatch = errors.New("no matching session for incoming packet")

	// ErrDemuxTTLRejected indicates the demultiplexed session discarded the
	// packet because its TTL or Hop Limit is below the session hop policy.
	ErrDemuxTTLRejected = errors.New("received TTL below session hop policy")

	// ErrInterfaceUnresolved indicates that the receive interface of a new
	// single-hop session could not be resolved (RFC 5881 Section 3).
	ErrInterfaceUnresolved = errors.New("single-hop session receive interface is unresolved")

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

	// IfIndex is the receiving interface index from packet ancillary data.
	// It resolves unsolicited admission after the configured peer lookup.
	IfIndex int

	// MultiHop is true when the packet arrived on a multi-hop (RFC 5883)
	// listener.
	MultiHop bool

	// TransportScope is the exact overlay identity extracted from the wire.
	// It is empty for base and Micro-BFD transports.
	TransportScope TransportScope
}

// -------------------------------------------------------------------------
// Packet Demux Key — peer identity for initial demultiplexing
// -------------------------------------------------------------------------

// packetDemuxKey is the composite key for initial session demultiplexing when
// Your Discriminator is zero (RFC 5880 Section 6.8.6).
//
// For single-hop (RFC 5881 Section 3): match by (PeerAddr, LocalAddr, IfName).
// For multi-hop (RFC 5883): match by (PeerAddr, LocalAddr) — IfName is empty.
type packetDemuxKey struct {
	peerAddr       netip.Addr
	localAddr      netip.Addr
	ifName         string
	transportScope TransportScope
}

type managerLifecycleState uint8

const (
	managerOpen managerLifecycleState = iota
	managerClosing
	managerClosed
)

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

	RemoteKnown        bool
	RemoteDiag         Diag
	LocalC             bool
	RemoteC            bool
	Cause              Cause
	ObservationVersion uint64
	Ref                SessionRef

	// DesiredMinTx is the configured desired minimum TX interval.
	DesiredMinTx time.Duration

	// RequiredMinRx is the configured required minimum RX interval.
	RequiredMinRx time.Duration

	// RemoteMinRx is bfd.RemoteMinRxInterval from the last received packet.
	RemoteMinRx time.Duration

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
	PacketsDropped  uint64
	AuthFailures    uint64

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
	sessionsByPeer map[packetDemuxKey]*sessionEntry

	// sessionsByKey indexes wire sessions by canonical desired identity.
	sessionsByKey map[SessionKey]*sessionEntry

	// echoSessions indexed by local discriminator for echo demux.
	// RFC 9747: echo packets are demultiplexed by MyDiscriminator on return.
	echoSessions map[uint32]*echoSessionEntry

	// microGroups holds RFC 7130 micro-BFD groups indexed by LAG interface name.
	// Each group tracks the aggregate state of per-member-link BFD sessions.
	microGroups map[string]*MicroBFDGroup

	// microMembers maps each member link and address family to its owning
	// group, so dispatch has one deterministic owner (RFC 7130 Section 2).
	microMembers map[microMemberKey]*MicroBFDGroup

	// microResync queues actuator resyncs of members seeded Up from existing
	// sessions; RunDispatch drains it after microResyncWake. Guarded by mu.
	microResync     []microResync
	microResyncWake chan struct{}

	// ownershipMu serializes complete source-ownership operations. Public
	// ownership mutations acquire it before mu so multi-step reconciliation
	// cannot interleave with another claim or release.
	ownershipMu sync.Mutex

	mu sync.RWMutex

	discriminators *DiscriminatorAllocator

	// metrics is the optional metrics reporter. Never nil -- uses noopMetrics
	// when no collector is configured.
	metrics  MetricsReporter
	delivery deliveryLedger

	// unsolicited holds the RFC 9468 unsolicited BFD state.
	// nil when unsolicited BFD is not configured.
	unsolicited *unsolicitedState

	// unsolicitedSessionSender creates the sender of one unsolicited session
	// from the received packet's local address and interface.
	unsolicitedSessionSender func(PacketMeta) (*SenderLease, error)

	// resolveIfIndex returns the receive interface index of a new single-hop
	// session from its local address or interface name. Nil disables it.
	resolveIfIndex func(local netip.Addr, ifName string) (int, error)

	// microActuator receives RFC 7130 member state transitions after the
	// Manager updates its MicroBFDGroup aggregate state.
	microActuator MicroBFDActuator

	// rawNotifyCh receives state changes from all sessions.
	// The Manager's dispatch goroutine reads from this channel,
	// handles micro-BFD group updates, and forwards to publicNotifyCh.
	rawNotifyCh    chan StateChange
	cleanupWake    chan struct{}
	publicLossWake chan struct{}

	// publicNotifyCh is the legacy single-consumer channel exposed via
	// StateChanges(). New consumers should use SubscribeStateChanges.
	publicNotifyCh chan StateChange
	// One daemon consumer; coalesced notifications never carry authoritative state.
	timerUpdateCh chan struct{}

	subscribers map[chan StateChange]*subscriber
	subMu       sync.RWMutex

	// revision numbers state changes and removals, under subMu.
	revision atomic.Uint64
	// observationClock orders semantic causal observations, independently of dispatch.
	observationClock atomic.Uint64

	logger *slog.Logger

	// lifecycleMu gates new mutations and goroutine registration. Mutations
	// hold a read lock only until their registry changes are complete. Resource
	// callbacks run after the lifecycle, ownership, and registry locks are free.
	lifecycleMu    sync.RWMutex
	lifecycleState managerLifecycleState
	activeOps      sync.WaitGroup
	workers        sync.WaitGroup
	closeDone      chan struct{}

	shutdownCh chan struct{}

	dispatchStarted bool
	legacyClosed    bool // protected by subMu
}

// sessionEntry holds a session and its cancellation function.
// The cancel function is used by DestroySession to stop the session goroutine.
type sessionEntry struct {
	session        *Session
	cancel         context.CancelFunc
	senderLease    *SenderLease
	key            SessionKey
	demuxKey       packetDemuxKey
	effective      effectiveSessionConfig
	owners         map[SessionOwner]struct{}
	unsolicited    bool
	done           chan struct{}
	cleanupWake    chan struct{}
	cleanupPending unsolicitedCleanupPending
}

type echoSessionSource uint8

const (
	echoSessionSourceCompatibility echoSessionSource = iota
	echoSessionSourceDeclarative
)

type retiredSession struct {
	localDiscr uint32
	entry      *sessionEntry
}

// echoSessionEntry holds one echo session's canonical identity, source, and
// owned runtime resources.
type echoSessionEntry struct {
	session     *EchoSession
	cancel      context.CancelFunc
	senderLease *SenderLease
	key         string
	source      echoSessionSource
	done        chan struct{}
}

type retiredEchoSession struct {
	localDiscr uint32
	entry      *echoSessionEntry
}

type managerOperation struct {
	manager          *Manager
	mutationUnlocked bool
}

func (m *Manager) beginOperation() (*managerOperation, error) {
	m.lifecycleMu.RLock()
	switch m.lifecycleState {
	case managerOpen:
		m.activeOps.Add(1)
		return &managerOperation{manager: m}, nil
	case managerClosing:
		m.lifecycleMu.RUnlock()
		return nil, ErrManagerClosing
	case managerClosed:
		m.lifecycleMu.RUnlock()
		return nil, ErrManagerClosed
	default:
		m.lifecycleMu.RUnlock()
		return nil, ErrManagerClosed
	}
}

func (op *managerOperation) unlockMutation() {
	if op == nil || op.mutationUnlocked {
		return
	}
	op.mutationUnlocked = true
	op.manager.lifecycleMu.RUnlock()
}

func (op *managerOperation) finish() {
	if op == nil {
		return
	}
	op.unlockMutation()
	op.manager.activeOps.Done()
}

// ManagerOption configures optional Manager parameters.
type ManagerOption func(*Manager)

// MicroBFDActuator reacts to RFC 7130 Micro-BFD member state transitions.
//
// RunDispatch calls the actuator after it releases Manager locks, in the
// single dispatch goroutine, so member transitions keep their order. Each call
// receives a context bounded by microBFDActuatorTimeout; implementations must
// return when it is done. A slow call delays later notifications only.
type MicroBFDActuator interface {
	HandleMicroBFDMemberEvent(ctx context.Context, event MicroBFDMemberEvent) error
}

// WithSessionInterfaceResolver sets the resolver that binds each new
// single-hop session to the interface owning its local address, or to its
// configured interface (RFC 5881 Section 3).
func WithSessionInterfaceResolver(resolve func(local netip.Addr, ifName string) (int, error)) ManagerOption {
	return func(m *Manager) {
		m.resolveIfIndex = resolve
	}
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

// WithUnsolicitedSessionSender sets the per-session sender factory for
// auto-created RFC 9468 sessions. It receives the metadata of the packet that
// triggered the session, so the reply leaves from the local address and
// interface that received it. The returned lease belongs to the session.
func WithUnsolicitedSessionSender(f func(PacketMeta) (*SenderLease, error)) ManagerOption {
	return func(m *Manager) {
		m.unsolicitedSessionSender = f
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
		sessions:        make(map[uint32]*sessionEntry),
		sessionsByPeer:  make(map[packetDemuxKey]*sessionEntry),
		sessionsByKey:   make(map[SessionKey]*sessionEntry),
		echoSessions:    make(map[uint32]*echoSessionEntry),
		microGroups:     make(map[string]*MicroBFDGroup),
		microMembers:    make(map[microMemberKey]*MicroBFDGroup),
		microResyncWake: make(chan struct{}, 1),
		discriminators:  NewDiscriminatorAllocator(),
		metrics:         noopMetrics{},
		rawNotifyCh:     make(chan StateChange, notifyChSize),
		cleanupWake:     make(chan struct{}, 1),
		publicLossWake:  make(chan struct{}, 1),
		publicNotifyCh:  make(chan StateChange, notifyChSize),
		timerUpdateCh:   make(chan struct{}, 1),
		subscribers:     make(map[chan StateChange]*subscriber),
		closeDone:       make(chan struct{}),
		shutdownCh:      make(chan struct{}),
		logger:          logger.With(slog.String("component", "bfd.manager")),
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
// RunDispatch is single-run. The caller starts it, while Manager owns its
// registered lifetime for shutdown. A second call returns immediately. The
// first call blocks until ctx is cancelled or Manager.Close begins, then
// closes the legacy StateChanges channel exactly once.
func (m *Manager) RunDispatch(ctx context.Context) {
	m.lifecycleMu.Lock()
	if m.lifecycleState != managerOpen || m.dispatchStarted {
		m.lifecycleMu.Unlock()
		return
	}
	m.dispatchStarted = true
	m.workers.Add(1)
	m.lifecycleMu.Unlock()
	defer func() {
		m.closeLegacyStateChanges()
		m.workers.Done()
	}()
	if ctx.Err() != nil {
		return
	}
	m.scanUnsolicitedCleanup(ctx)

	for {
		select {
		case <-m.publicLossWake:
			m.invalidateStateChanges()
		default:
		}
		select {
		case <-m.cleanupWake:
			m.scanUnsolicitedCleanup(ctx)
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-m.shutdownCh:
			return
		case <-m.publicLossWake:
			m.invalidateStateChanges()
		case <-m.cleanupWake:
			m.scanUnsolicitedCleanup(ctx)
		case <-m.microResyncWake:
			m.dispatchMicroResync(ctx)
		case sc := <-m.rawNotifyCh:
			m.dispatchStateChange(ctx, sc)
		}
	}
}

func (m *Manager) dispatchStateChange(ctx context.Context, sc StateChange) {
	m.mu.RLock()
	var valid bool
	if sc.Type == SessionTypeEcho {
		entry, ok := m.echoSessions[sc.LocalDiscr]
		valid = ok && entry.session.Ref() == sc.Ref
	} else {
		entry, ok := m.sessions[sc.LocalDiscr]
		valid = ok && entry.session.Ref() == sc.Ref
		if valid && sc.unsolicitedFailure {
			entry.session.mu.RLock()
			valid = entry.unsolicited && entry.session.unsolicitedAttempt == sc.unsolicitedAttempt
			entry.session.mu.RUnlock()
		}
	}
	if !valid {
		m.mu.RUnlock()
		m.RecordDelivery(DeliveryNotificationDispatch, DeliveryStale)
		return
	}
	if sc.unsolicitedFailure {
		m.mu.RUnlock()
		m.RecordDelivery(DeliveryNotificationDispatch, DeliveryAccepted)
		m.scheduleUnsolicitedCleanup(ctx, sc)
		return
	}
	var memberEvent MicroBFDMemberEvent
	var actuate bool
	if sc.Type == SessionTypeMicroBFD && sc.Interface != "" {
		memberEvent, actuate = m.dispatchMicroBFD(sc)
	}
	counts := m.broadcastStateChange(sc)
	m.mu.RUnlock()
	m.RecordDelivery(DeliveryNotificationDispatch, DeliveryAccepted)
	m.reportFanoutDelivery(counts)
	if actuate {
		m.handleMicroBFDActuator(ctx, memberEvent)
	}
	if sc.NewState == StateDown {
		m.scheduleUnsolicitedCleanup(ctx, sc)
	}
}

func (m *Manager) closeLegacyStateChanges() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	if !m.legacyClosed {
		m.legacyClosed = true
		close(m.publicNotifyCh)
	}
}

// subscriber is the delivery state of one SubscribeStateChanges channel,
// protected by subMu.
type subscriber struct {
	// overflowed is set once a gap marker is queued and cleared when
	// delivery resumes.
	overflowed bool
}

// invalidateStateChanges consumes no originals: subscribers resnapshot committed owners.
func (m *Manager) invalidateStateChanges() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	gap := StateChange{Gap: true, Revision: m.revision.Add(1), Timestamp: time.Now()}
	for ch, sub := range m.subscribers {
		if ch == nil || len(ch) == cap(ch) {
			// A full channel already has an unread GAP; its later snapshot covers this loss.
			continue
		}
		ch <- gap
		sub.overflowed = true
	}
}

// broadcastStateChange numbers sc and delivers it to every subscriber. The
// last buffer slot of a subscriber is reserved for a gap marker, so a slow
// subscriber learns about lost events even when no further event follows.
// subMu serializes dispatch and synchronous registration/removal.
func (m *Manager) broadcastStateChange(sc StateChange) fanoutDelivery {
	m.subMu.Lock()
	defer m.subMu.Unlock()

	// Assigned under subMu: a subscriber registered before a revision is
	// read receives every later revision.
	sc.Revision = m.revision.Add(1)
	var counts fanoutDelivery
	for ch, sub := range m.subscribers {
		if ch == nil {
			continue
		}
		if len(ch) < cap(ch)-1 {
			sub.overflowed = false
			ch <- sc
			counts.queued++
			continue
		}
		counts.gapped++
		if !sub.overflowed {
			sub.overflowed = true
			ch <- StateChange{Gap: true, Revision: sc.Revision, Timestamp: sc.Timestamp}
			m.logger.Warn("state change subscriber is behind, queued a gap marker",
				slog.Uint64("revision", sc.Revision),
			)
		}
	}
	if !sc.Added && !m.legacyClosed && m.publicNotifyCh != nil {
		select {
		case m.publicNotifyCh <- sc:
			counts.legacy = DeliveryQueued
		default:
			counts.legacy = DeliveryQueueFull
			m.logger.Warn("public notification channel full, dropping state change",
				slog.Uint64("local_discr", uint64(sc.LocalDiscr)),
				slog.String("new_state", sc.NewState.String()),
			)
		}
	}
	return counts
}

type fanoutDelivery struct {
	queued uint64
	gapped uint64
	legacy DeliveryOutcome
}

func (m *Manager) reportFanoutDelivery(counts fanoutDelivery) {
	for range counts.queued {
		m.RecordDelivery(DeliveryWatchRecipient, DeliveryQueued)
	}
	for range counts.gapped {
		m.RecordDelivery(DeliveryWatchRecipient, DeliveryGapped)
	}
	if counts.legacy != DeliveryOutcomeUnspecified {
		m.RecordDelivery(DeliveryLegacyNotification, counts.legacy)
	}
}

func (m *Manager) startWorker(run func()) bool {
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	if m.lifecycleState != managerOpen {
		return false
	}
	m.workers.Go(func() {
		run()
	})
	return true
}

func (m *Manager) cleanupUnsolicitedSession(_ context.Context, source *Session) {
	source.mu.RLock()
	attempt := source.unsolicitedAttempt
	source.mu.RUnlock()
	m.cleanupUnsolicitedAttempt(source, attempt)
}

func (m *Manager) cleanupUnsolicitedAttempt(source *Session, attempt uint64) {
	op, err := m.beginOperation()
	if err != nil {
		return
	}
	defer op.finish()

	m.ownershipMu.Lock()
	localDiscr := source.LocalDiscriminator()
	if !m.retireUnsolicitedAttempt(source, attempt) {
		m.ownershipMu.Unlock()
		return
	}
	peer := source.PeerAddr()

	wireDestroyed, retired, err := m.detachSessionClaimByDiscriminator(
		localDiscr, unsolicitedSessionOwner(),
	)
	m.ownershipMu.Unlock()
	op.unlockMutation()
	if retired != nil {
		// RFC 9468 Section 2 failure cleanup must stop Control transmission.
		err = errors.Join(err, m.finishSessionDestroyError(retired.localDiscr, retired.entry))
	}
	if err != nil {
		m.logger.Warn("unsolicited cleanup incomplete",
			slog.Uint64("local_discr", uint64(localDiscr)),
			slog.String("peer", peer.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	m.logger.Info("unsolicited claim cleaned up",
		slog.Uint64("local_discr", uint64(localDiscr)),
		slog.String("peer", peer.String()),
		slog.Bool("wire_destroyed", wireDestroyed),
	)
}

// retireUnsolicitedAttempt requires ownershipMu. Session.mu fences the
// attempt check with claim detachment or the owner's retirement decision.
func (m *Manager) retireUnsolicitedAttempt(source *Session, attempt uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.sessions[source.LocalDiscriminator()]
	if !ok || entry.session != source || !entry.unsolicited {
		return false
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if !source.unsolicitedCleanupAllowedLocked(attempt) {
		return false
	}
	if len(entry.owners) > 1 {
		delete(entry.owners, unsolicitedSessionOwner())
		entry.unsolicited = false
		m.unsolicited.release()
		entry.publishUnsolicitedOwnershipLocked()
		return false
	}
	// The owner cannot resume after this conditional retirement decision.
	source.unsolicitedRetiring = true
	return true
}

// -------------------------------------------------------------------------
// Graceful Drain — RFC 5880 Section 6.8.16
// -------------------------------------------------------------------------

// DrainAllSessions waits for captured owners to commit AdminDown/7, write it
// successfully, and maintain the negotiated notification window. ctx bounds
// all stages; incomplete drains return DrainIncompleteError. No peer receipt is implied.
func (m *Manager) DrainAllSessions(ctx context.Context) error {
	op, err := m.beginOperation()
	if err != nil {
		return err
	}
	defer op.finish()

	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, entry := range m.sessions {
		sessions = append(sessions, entry.session)
	}
	m.mu.RUnlock()
	op.unlockMutation()

	errs := make([]error, len(sessions))
	var workers sync.WaitGroup
	for i, session := range sessions {
		workers.Go(func() {
			if err := session.drainAdminDown(ctx); err != nil {
				errs[i] = fmt.Errorf("drain session %d: %w", session.localDiscr, err)
			}
		})
	}
	workers.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	m.logger.Info("all captured sessions completed AdminDown notification window",
		slog.Int("count", len(sessions)),
	)
	return nil
}

// -------------------------------------------------------------------------
// Lifecycle
// -------------------------------------------------------------------------

// Close cancels all Manager-owned goroutines and releases resources.
// After Close returns, new mutations are rejected and StateChanges has reached
// EOF. Close is safe to call concurrently and more than once.
// Sender release callbacks run without Manager locks and may reenter Manager
// read or mutation APIs, but must not recursively call this blocking method.
func (m *Manager) Close() {
	if !m.beginClose() {
		return
	}

	retired, retiredEcho := m.detachCloseResources()
	m.stopAndWait(retired, retiredEcho)
	m.releaseCloseResources(retired, retiredEcho)
	m.completeClose()
	m.logger.Info("manager closed")
}

func (m *Manager) beginClose() bool {
	m.lifecycleMu.Lock()
	switch m.lifecycleState {
	case managerClosing, managerClosed:
		done := m.closeDone
		m.lifecycleMu.Unlock()
		<-done
		return false
	case managerOpen:
		m.lifecycleState = managerClosing
	}
	m.lifecycleMu.Unlock()
	return true
}

func (m *Manager) detachCloseResources() (
	[]retiredSession,
	[]retiredEchoSession,
) {
	m.ownershipMu.Lock()
	m.mu.Lock()
	retired := make([]retiredSession, 0, len(m.sessions))
	for discr, entry := range m.sessions {
		retired = append(retired, retiredSession{localDiscr: discr, entry: entry})
	}
	retiredEcho := make([]retiredEchoSession, 0, len(m.echoSessions))
	for discr, entry := range m.echoSessions {
		retiredEcho = append(retiredEcho, retiredEchoSession{localDiscr: discr, entry: entry})
	}
	// Clear maps to prevent use-after-close.
	m.sessions = make(map[uint32]*sessionEntry)
	m.sessionsByPeer = make(map[packetDemuxKey]*sessionEntry)
	m.sessionsByKey = make(map[SessionKey]*sessionEntry)
	m.echoSessions = make(map[uint32]*echoSessionEntry)
	m.microGroups = make(map[string]*MicroBFDGroup)
	m.microMembers = make(map[microMemberKey]*MicroBFDGroup)
	m.microResync = nil
	m.mu.Unlock()
	m.ownershipMu.Unlock()
	return retired, retiredEcho
}

func (m *Manager) stopAndWait(retired []retiredSession, retiredEcho []retiredEchoSession) {
	for _, item := range retired {
		item.entry.cancel()
	}
	for _, item := range retiredEcho {
		item.entry.cancel()
	}
	close(m.shutdownCh)

	m.activeOps.Wait()
	m.workers.Wait()
	for len(m.rawNotifyCh) > 0 {
		<-m.rawNotifyCh
		m.RecordDelivery(DeliveryNotificationDispatch, DeliveryAbandoned)
	}
	m.closeLegacyStateChanges()
}

func (m *Manager) releaseCloseResources(
	retired []retiredSession,
	retiredEcho []retiredEchoSession,
) {
	slices.SortFunc(retired, func(a, b retiredSession) int {
		return cmp.Compare(a.localDiscr, b.localDiscr)
	})
	for _, item := range retired {
		m.finishSessionDestroy(item.localDiscr, item.entry)
	}
	slices.SortFunc(retiredEcho, func(a, b retiredEchoSession) int {
		return cmp.Compare(a.localDiscr, b.localDiscr)
	})
	for _, item := range retiredEcho {
		m.finishEchoSessionDestroy(item.localDiscr, item.entry)
	}
}

func (m *Manager) completeClose() {
	m.lifecycleMu.Lock()
	m.lifecycleState = managerClosed
	close(m.closeDone)
	m.lifecycleMu.Unlock()
}
