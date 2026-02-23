package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"runtime"
	"sync/atomic"
	"time"
)

// -------------------------------------------------------------------------
// Session Type & Role — RFC 5881 / RFC 5883
// -------------------------------------------------------------------------

// SessionType distinguishes single-hop from multi-hop BFD sessions.
type SessionType uint8

const (
	// SessionTypeSingleHop indicates a single-hop BFD session (RFC 5881).
	SessionTypeSingleHop SessionType = iota + 1

	// SessionTypeMultiHop indicates a multi-hop BFD session (RFC 5883).
	SessionTypeMultiHop

	// SessionTypeEcho indicates an unaffiliated BFD echo session (RFC 9747).
	SessionTypeEcho

	// SessionTypeMicroBFD indicates a per-member-link micro-BFD session (RFC 7130).
	SessionTypeMicroBFD

	// SessionTypeVXLAN indicates a BFD session over a VXLAN tunnel (RFC 8971).
	SessionTypeVXLAN
)

// String returns the human-readable name for the session type.
func (st SessionType) String() string {
	switch st {
	case SessionTypeSingleHop:
		return "SingleHop"
	case SessionTypeMultiHop:
		return "MultiHop"
	case SessionTypeEcho:
		return "Echo"
	case SessionTypeMicroBFD:
		return "MicroBFD"
	case SessionTypeVXLAN:
		return "VXLAN"
	default:
		return unknownStr
	}
}

// SessionRole determines the initial packet transmission behavior.
type SessionRole uint8

const (
	// RoleActive indicates the system MUST begin sending BFD Control
	// packets regardless of whether any packets have been received
	// (RFC 5880 Section 6.1).
	RoleActive SessionRole = iota + 1

	// RolePassive indicates the system MUST NOT send BFD Control packets
	// until a packet has been received from the remote system
	// (RFC 5880 Section 6.8.7).
	RolePassive
)

// String returns the human-readable name for the session role.
func (sr SessionRole) String() string {
	switch sr {
	case RoleActive:
		return "Active"
	case RolePassive:
		return "Passive"
	default:
		return unknownStr
	}
}

// -------------------------------------------------------------------------
// Session Configuration & Notification
// -------------------------------------------------------------------------

// SessionConfig contains the parameters needed to create a new BFD session.
type SessionConfig struct {
	// PeerAddr is the remote system's IP address.
	PeerAddr netip.Addr

	// LocalAddr is the local system's IP address used for BFD packets.
	LocalAddr netip.Addr

	// Interface is the network interface name for SO_BINDTODEVICE (optional).
	Interface string

	// Type distinguishes single-hop (RFC 5881) from multi-hop (RFC 5883).
	Type SessionType

	// Role determines whether the session actively initiates or waits passively.
	Role SessionRole

	// DesiredMinTxInterval is the minimum desired TX interval.
	// RFC 5880 Section 6.8.1: MUST be initialized to >= 1 second.
	// Stored as time.Duration; converted to microseconds at wire boundaries.
	DesiredMinTxInterval time.Duration

	// RequiredMinRxInterval is the minimum acceptable RX interval.
	// Stored as time.Duration; converted to microseconds at wire boundaries.
	RequiredMinRxInterval time.Duration

	// DetectMultiplier is the detection time multiplier (RFC 5880 Section 6.8.1).
	// MUST be nonzero.
	DetectMultiplier uint8

	// Auth is the optional authenticator for this session.
	// nil means no authentication (RFC 5880 Section 6.7).
	Auth Authenticator

	// AuthKeys provides the key store for authentication.
	// Required if Auth is not nil.
	AuthKeys AuthKeyStore
}

// StateChange is emitted when a session FSM transitions between states.
type StateChange struct {
	// LocalDiscr is the local discriminator of the session.
	LocalDiscr uint32

	// PeerAddr is the remote system's IP address.
	PeerAddr netip.Addr

	// OldState is the session state before the transition.
	OldState State

	// NewState is the session state after the transition.
	NewState State

	// Diag is the current diagnostic code after the transition.
	Diag Diag

	// Timestamp is when the transition occurred.
	Timestamp time.Time
}

// PacketSender abstracts sending BFD Control packets over the network.
// This interface enables testing without real network I/O.
type PacketSender interface {
	SendPacket(ctx context.Context, buf []byte, addr netip.Addr) error
}

// -------------------------------------------------------------------------
// Session Options — functional options pattern
// -------------------------------------------------------------------------

// SessionOption configures optional Session parameters.
type SessionOption func(*Session)

// WithMetrics attaches a MetricsReporter to the session. If mr is nil,
// the default no-op reporter is used.
func WithMetrics(mr MetricsReporter) SessionOption {
	return func(s *Session) {
		if mr != nil {
			s.metrics = mr
		}
	}
}

// -------------------------------------------------------------------------
// Session Errors
// -------------------------------------------------------------------------

// Sentinel errors for Session configuration validation.
var (
	// ErrInvalidDetectMult indicates the detect multiplier is zero.
	ErrInvalidDetectMult = errors.New("detect multiplier must be >= 1")

	// ErrInvalidTxInterval indicates the desired min TX interval is invalid.
	ErrInvalidTxInterval = errors.New("desired min TX interval must be > 0")

	// ErrInvalidSessionType indicates an unknown session type.
	ErrInvalidSessionType = errors.New("invalid session type")

	// ErrInvalidSessionRole indicates an unknown session role.
	ErrInvalidSessionRole = errors.New("invalid session role")

	// ErrInvalidDiscriminator indicates the local discriminator is zero.
	ErrInvalidDiscriminator = errors.New("local discriminator must be nonzero")
)

// -------------------------------------------------------------------------
// Session Constants
// -------------------------------------------------------------------------

const (
	// slowTxInterval is the minimum TX interval when session is not Up.
	// RFC 5880 Section 6.8.3: "MUST set bfd.DesiredMinTxInterval to a
	// value of not less than one second (1,000,000 microseconds).".
	slowTxInterval = 1 * time.Second

	// recvChSize is the buffer size for the receive channel. Sized to
	// avoid blocking the network listener goroutine.
	recvChSize = 16

	// initialRemoteMinRx is the initial value of bfd.RemoteMinRxInterval.
	// RFC 5880 Section 6.8.1: "This variable MUST be initialized to 1."
	// The value is 1 microsecond.
	initialRemoteMinRx = 1 * time.Microsecond
)

// -------------------------------------------------------------------------
// Session — RFC 5880 Section 6.8.1
// -------------------------------------------------------------------------

// Session implements a single BFD session as described in RFC 5880.
//
// All mutable state is owned by the session goroutine started via Run().
// External reads use atomic operations (State, RemoteState, LocalDiag).
// Incoming packets are delivered via RecvPacket() through a buffered channel.
//
// The session implements:
//   - RFC 5880 Section 6.8.1: state variables
//   - RFC 5880 Section 6.8.2: timer negotiation
//   - RFC 5880 Section 6.8.3: timer manipulation (slow TX rate)
//   - RFC 5880 Section 6.8.4: detection time calculation
//   - RFC 5880 Section 6.8.6: packet reception processing
//   - RFC 5880 Section 6.8.7: packet transmission (jitter, cached packet)
//   - RFC 5880 Section 6.5: Poll Sequence
type Session struct {
	// --- RFC 5880 Section 6.8.1 state variables ---

	// state is bfd.SessionState. Atomic for lock-free external reads.
	state atomic.Uint32

	// remoteState is bfd.RemoteSessionState. Atomic for external reads.
	remoteState atomic.Uint32

	// localDiag is bfd.LocalDiag. Atomic for external reads.
	localDiag atomic.Uint32

	// localDiscr is bfd.LocalDiscr — unique nonzero discriminator.
	localDiscr uint32

	// remoteDiscr is bfd.RemoteDiscr — set from received packets.
	remoteDiscr uint32

	// desiredMinTxInterval is bfd.DesiredMinTxInterval.
	desiredMinTxInterval time.Duration

	// requiredMinRxInterval is bfd.RequiredMinRxInterval.
	requiredMinRxInterval time.Duration

	// remoteMinRxInterval is bfd.RemoteMinRxInterval (init 1us per RFC).
	remoteMinRxInterval time.Duration

	// remoteDesiredMinTxInterval from the last received packet.
	remoteDesiredMinTxInterval time.Duration

	// remoteDetectMult from the last received packet.
	remoteDetectMult uint8

	// detectMult is bfd.DetectMult.
	detectMult uint8

	// remoteDemandMode is bfd.RemoteDemandMode (init false per RFC).
	remoteDemandMode bool

	// --- Poll Sequence state (RFC 5880 Section 6.5) ---

	// pollActive is true when a Poll Sequence is in progress.
	pollActive bool

	// pendingFinal is true when we received a Poll and need to send Final.
	pendingFinal bool

	// pendingDesiredMinTx holds the new value awaiting poll completion.
	pendingDesiredMinTx time.Duration

	// pendingRequiredMinRx holds the new value awaiting poll completion.
	pendingRequiredMinRx time.Duration

	// --- Session identity ---

	sessionType SessionType
	role        SessionRole
	peerAddr    netip.Addr
	localAddr   netip.Addr
	ifName      string

	// --- Cached packet (FRR bfdd pattern) ---
	cachedPacket []byte

	// --- Authentication (RFC 5880 Section 6.7) ---

	// auth holds the authenticator (nil if no auth).
	auth Authenticator
	// authKeys provides the key store for authentication.
	authKeys AuthKeyStore
	// authState tracks per-session auth sequence numbers.
	authState *AuthState

	// --- Per-session atomic counters ---
	// These counters are updated on the hot path by the session goroutine
	// and read atomically by snapshot methods. Using sync/atomic avoids
	// contention with the session goroutine.

	packetsSent      atomic.Uint64
	packetsReceived  atomic.Uint64
	stateTransitions atomic.Uint64

	// lastStateChange stores the Unix nanosecond timestamp of the most
	// recent FSM state transition. Zero means no transition has occurred.
	lastStateChange atomic.Int64

	// lastPacketRecv stores the Unix nanosecond timestamp of the most
	// recent valid BFD Control packet received. Zero means no packet received.
	lastPacketRecv atomic.Int64

	// --- Runtime ---

	sender   PacketSender
	metrics  MetricsReporter
	logger   *slog.Logger
	recvCh   chan recvItem
	notifyCh chan<- StateChange
}

// recvItem carries a received BFD Control packet along with the raw
// wire bytes needed for authentication verification (RFC 5880 Section 6.7).
type recvItem struct {
	pkt  *ControlPacket
	wire []byte // raw wire bytes for auth digest verification
}

// -------------------------------------------------------------------------
// Constructor
// -------------------------------------------------------------------------

// NewSession creates a new BFD session with the given configuration.
// The session goroutine is NOT started until Run() is called.
//
// localDiscr must be a unique nonzero discriminator allocated externally.
// sender is the abstraction for sending BFD packets on the wire.
// notifyCh may be nil if no state change notifications are needed.
// metrics may be nil; a no-op reporter is used in that case.
//
// RFC 5880 Section 6.8.1: all state variables are initialized to their
// mandatory values.
func NewSession(
	cfg SessionConfig,
	localDiscr uint32,
	sender PacketSender,
	notifyCh chan<- StateChange,
	logger *slog.Logger,
	opts ...SessionOption,
) (*Session, error) {
	if err := validateSessionConfig(cfg, localDiscr); err != nil {
		return nil, err
	}

	s := &Session{
		localDiscr:            localDiscr,
		desiredMinTxInterval:  cfg.DesiredMinTxInterval,
		requiredMinRxInterval: cfg.RequiredMinRxInterval,
		remoteMinRxInterval:   initialRemoteMinRx,
		detectMult:            cfg.DetectMultiplier,
		sessionType:           cfg.Type,
		role:                  cfg.Role,
		peerAddr:              cfg.PeerAddr,
		localAddr:             cfg.LocalAddr,
		ifName:                cfg.Interface,
		auth:                  cfg.Auth,
		authKeys:              cfg.AuthKeys,
		sender:                sender,
		metrics:               noopMetrics{},
		notifyCh:              notifyCh,
		recvCh:                make(chan recvItem, recvChSize),
		cachedPacket:          make([]byte, MaxPacketSize),
		logger: logger.With(
			slog.String("peer", cfg.PeerAddr.String()),
			slog.Uint64("local_discr", uint64(localDiscr)),
		),
	}

	for _, opt := range opts {
		opt(s)
	}

	// RFC 5880 Section 6.8.1: bfd.SessionState MUST be initialized to Down.
	s.state.Store(uint32(StateDown))
	// RFC 5880 Section 6.8.1: bfd.RemoteSessionState MUST be initialized to Down.
	s.remoteState.Store(uint32(StateDown))
	// RFC 5880 Section 6.8.1: bfd.LocalDiag MUST be initialized to zero.
	s.localDiag.Store(uint32(DiagNone))

	// Initialize auth state if authentication is configured.
	if err := s.initAuth(cfg); err != nil {
		return nil, err
	}

	s.rebuildCachedPacket()

	return s, nil
}

// validateSessionConfig checks all config parameters.
func validateSessionConfig(cfg SessionConfig, localDiscr uint32) error {
	if cfg.DetectMultiplier < 1 {
		return fmt.Errorf("detect multiplier %d: %w", cfg.DetectMultiplier, ErrInvalidDetectMult)
	}
	if cfg.DesiredMinTxInterval <= 0 {
		return fmt.Errorf("desired min TX interval %v: %w", cfg.DesiredMinTxInterval, ErrInvalidTxInterval)
	}
	if cfg.Type != SessionTypeSingleHop && cfg.Type != SessionTypeMultiHop {
		return fmt.Errorf("session type %d: %w", cfg.Type, ErrInvalidSessionType)
	}
	if cfg.Role != RoleActive && cfg.Role != RolePassive {
		return fmt.Errorf("session role %d: %w", cfg.Role, ErrInvalidSessionRole)
	}
	if localDiscr == 0 {
		return fmt.Errorf("local discriminator: %w", ErrInvalidDiscriminator)
	}
	return nil
}

// initAuth initializes the authentication state if auth is configured.
// RFC 5880 Section 6.8.1: bfd.XmitAuthSeq MUST be initialized to a
// random 32-bit value.
func (s *Session) initAuth(cfg SessionConfig) error {
	if cfg.Auth == nil {
		return nil
	}
	as, err := NewAuthState(AuthTypeNone)
	if err != nil {
		return fmt.Errorf("init auth state: %w", err)
	}
	s.authState = as
	return nil
}

// -------------------------------------------------------------------------
// Public Accessors — Thread-safe via atomic
// -------------------------------------------------------------------------

// LocalDiscriminator returns the session's local discriminator.
func (s *Session) LocalDiscriminator() uint32 { return s.localDiscr }

// State returns the current session state (atomic read).
func (s *Session) State() State {
	return State(s.state.Load()) //nolint:gosec // G115: State is 0-3, fits uint8
}

// RemoteState returns the last reported remote session state (atomic read).
func (s *Session) RemoteState() State {
	return State(s.remoteState.Load()) //nolint:gosec // G115: State is 0-3, fits uint8
}

// LocalDiag returns the current local diagnostic code (atomic read).
func (s *Session) LocalDiag() Diag {
	return Diag(s.localDiag.Load()) //nolint:gosec // G115: Diag is 0-8, fits uint8
}

// RemoteDiscriminator returns the remote discriminator learned from the peer.
// Returns 0 if no packet has been received yet (RFC 5880 Section 6.8.1).
//
// NOTE: This value is updated by the session goroutine and is NOT atomic.
// It is intended for snapshot reads (e.g., Manager.Sessions) where the
// session goroutine may be running. Callers must tolerate slightly stale
// values; exact consistency is not required for display/monitoring purposes.
func (s *Session) RemoteDiscriminator() uint32 { return s.remoteDiscr }

// PeerAddr returns the remote system's IP address.
func (s *Session) PeerAddr() netip.Addr { return s.peerAddr }

// LocalAddr returns the local system's IP address.
func (s *Session) LocalAddr() netip.Addr { return s.localAddr }

// Interface returns the network interface name (empty for multi-hop sessions).
func (s *Session) Interface() string { return s.ifName }

// Type returns the session type (single-hop or multi-hop).
func (s *Session) Type() SessionType { return s.sessionType }

// DesiredMinTxInterval returns the configured desired minimum TX interval.
func (s *Session) DesiredMinTxInterval() time.Duration { return s.desiredMinTxInterval }

// RequiredMinRxInterval returns the configured required minimum RX interval.
func (s *Session) RequiredMinRxInterval() time.Duration { return s.requiredMinRxInterval }

// DetectMultiplier returns the configured detection multiplier.
func (s *Session) DetectMultiplier() uint8 { return s.detectMult }

// NegotiatedTxInterval returns the current negotiated TX interval.
// RFC 5880 Section 6.8.7: max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval).
// When state is not Up, the slow rate (1s) is enforced per RFC 5880 Section 6.8.3.
func (s *Session) NegotiatedTxInterval() time.Duration { return s.calcTxInterval() }

// DetectionTime returns the current calculated detection time.
// RFC 5880 Section 6.8.4: RemoteDetectMult * max(RequiredMinRx, RemoteDesiredMinTx).
func (s *Session) DetectionTime() time.Duration { return s.calcDetectionTime() }

// PacketsSent returns the total BFD Control packets transmitted (atomic read).
func (s *Session) PacketsSent() uint64 { return s.packetsSent.Load() }

// PacketsReceived returns the total BFD Control packets received (atomic read).
func (s *Session) PacketsReceived() uint64 { return s.packetsReceived.Load() }

// StateTransitions returns the total FSM state transitions (atomic read).
func (s *Session) StateTransitions() uint64 { return s.stateTransitions.Load() }

// LastStateChange returns the timestamp of the most recent FSM state
// transition. Returns zero time.Time if no transition has occurred.
func (s *Session) LastStateChange() time.Time {
	ns := s.lastStateChange.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// LastPacketReceived returns the timestamp of the most recent valid BFD
// Control packet received. Returns zero time.Time if no packet received.
func (s *Session) LastPacketReceived() time.Time {
	ns := s.lastPacketRecv.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// RecvPacket delivers a received BFD Control packet to the session for
// processing. This is safe to call from any goroutine. If the receive
// channel is full, the packet is dropped (logged at debug level).
//
// wire is the raw packet bytes needed for auth verification. It may be
// nil if no authentication is configured.
func (s *Session) RecvPacket(pkt *ControlPacket, wire ...[]byte) {
	var w []byte
	if len(wire) > 0 {
		w = wire[0]
	}
	select {
	case s.recvCh <- recvItem{pkt: pkt, wire: w}:
	default:
		s.logger.Debug("recv channel full, dropping packet")
	}
}

// SetAdminDown transitions the session to AdminDown with DiagAdminDown.
// RFC 5880 Section 6.8.16: the local system sets bfd.SessionState to
// AdminDown and bfd.LocalDiag to 7 (Administratively Down).
//
// This is used during graceful shutdown to signal the remote peer that
// the session is being administratively disabled, not failing. The session
// goroutine will rebuild the cached packet and transmit the AdminDown
// state on the next TX interval.
//
// Thread-safe: uses atomic operations on state and diag.
func (s *Session) SetAdminDown() {
	s.localDiag.Store(uint32(DiagAdminDown))
	s.state.Store(uint32(StateAdminDown))
	s.logger.Info("session set to AdminDown for graceful drain")
}

// -------------------------------------------------------------------------
// Main Goroutine — RFC 5880 Session Lifecycle
// -------------------------------------------------------------------------

// Run starts the session event loop. It blocks until ctx is cancelled.
// The session begins in Down state and starts sending BFD Control packets
// according to the configured role and timing parameters.
//
// The event loop processes:
//  1. Incoming packets from recvCh (RFC 5880 Section 6.8.6)
//  2. Transmission timer fires (RFC 5880 Section 6.8.7)
//  3. Detection timer expires (RFC 5880 Section 6.8.4)
//  4. Context cancellation (graceful shutdown)
func (s *Session) Run(ctx context.Context) {
	// Pin the session goroutine to an OS thread for sub-millisecond timer
	// precision. BFD detection intervals can be as low as 50ms; OS thread
	// affinity reduces scheduler-induced jitter on timer wakeups.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	txInterval := s.calcTxInterval()
	txTimer := time.NewTimer(ApplyJitter(txInterval, s.detectMult))
	defer txTimer.Stop()

	detectTime := s.calcDetectionTime()
	detectTimer := time.NewTimer(detectTime)
	defer detectTimer.Stop()

	s.logger.Info("session started",
		slog.String("state", s.State().String()),
		slog.Duration("tx_interval", txInterval),
		slog.Duration("detect_time", detectTime),
	)

	s.runLoop(ctx, txTimer, detectTimer)
}

// runLoop is the core select loop, separated from Run for clarity.
func (s *Session) runLoop(
	ctx context.Context,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("session stopped")
			return

		case item := <-s.recvCh:
			s.handleRecvPacket(ctx, item, txTimer, detectTimer)

		case <-txTimer.C:
			s.handleTxTimer(ctx, txTimer)

		case <-detectTimer.C:
			s.handleDetectTimer(ctx, txTimer, detectTimer)
		}
	}
}

// -------------------------------------------------------------------------
// TX Timer Handling — RFC 5880 Section 6.8.7
// -------------------------------------------------------------------------

// handleTxTimer fires on each transmission interval.
func (s *Session) handleTxTimer(ctx context.Context, txTimer *time.Timer) {
	s.maybeSendControl(ctx)
	txInterval := s.calcTxInterval()
	txTimer.Reset(ApplyJitter(txInterval, s.detectMult))
}

// maybeSendControl checks transmission preconditions and sends if allowed.
func (s *Session) maybeSendControl(ctx context.Context) {
	// RFC 5880 Section 6.8.7: "A system MUST NOT transmit BFD Control
	// packets if bfd.RemoteDiscr is zero and the system is taking the
	// Passive role."
	if s.role == RolePassive && s.remoteDiscr == 0 {
		return
	}
	// RFC 5880 Section 6.8.7: "A system MUST NOT periodically transmit
	// BFD Control packets if bfd.RemoteMinRxInterval is zero."
	if s.remoteMinRxInterval == 0 {
		return
	}
	s.sendControl(ctx)
}

// sendControl serializes and sends a BFD Control packet.
func (s *Session) sendControl(ctx context.Context) {
	s.rebuildCachedPacket()
	pktLen := int(s.cachedPacket[3]) // Length field at byte 3
	if err := s.sender.SendPacket(ctx, s.cachedPacket[:pktLen], s.peerAddr); err != nil {
		s.logger.Warn("failed to send control packet",
			slog.String("error", err.Error()),
		)
		return
	}
	s.packetsSent.Add(1)
	s.metrics.IncPacketsSent(s.peerAddr, s.localAddr)
}

// -------------------------------------------------------------------------
// Detection Timer — RFC 5880 Section 6.8.4
// -------------------------------------------------------------------------

// handleDetectTimer fires when the detection time expires without receiving
// a valid packet. RFC 5880 Section 6.8.4: "the local system MUST set
// bfd.SessionState to Down and bfd.LocalDiag to 1.".
func (s *Session) handleDetectTimer(
	ctx context.Context,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	curState := s.State()
	// RFC 5880 Section 6.8.4: only if bfd.SessionState is Init or Up.
	if curState != StateInit && curState != StateUp {
		// Restart detect timer even in Down state to handle re-negotiation.
		detectTimer.Reset(s.calcDetectionTime())
		return
	}
	s.applyFSMEvent(ctx, EventTimerExpired, txTimer, detectTimer)
}

// -------------------------------------------------------------------------
// Packet Reception — RFC 5880 Section 6.8.6 Steps 8-18
// -------------------------------------------------------------------------

// handleRecvPacket processes an incoming BFD Control packet.
// Steps 1-7 (basic validation) were done by UnmarshalControlPacket.
// This method implements steps 8-18 of RFC 5880 Section 6.8.6.
func (s *Session) handleRecvPacket(
	ctx context.Context,
	item recvItem,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	pkt := item.pkt

	// Steps 8-9: Auth mismatch check.
	if !s.checkAuthConsistency(pkt) {
		return
	}

	// Record received packet counter and timestamp.
	s.packetsReceived.Add(1)
	s.metrics.IncPacketsReceived(s.peerAddr, s.localAddr)
	s.lastPacketRecv.Store(time.Now().UnixNano())

	// RFC 5880 Section 6.7: verify authentication if configured.
	if s.auth != nil {
		if err := s.auth.Verify(
			s.authState, s.authKeys, pkt, item.wire, len(item.wire),
		); err != nil {
			s.logger.Debug("auth verification failed",
				slog.String("peer", s.peerAddr.String()),
				slog.String("error", err.Error()),
			)
			return
		}
	}

	// Step 13: Set bfd.RemoteDiscr = My Discriminator.
	s.remoteDiscr = pkt.MyDiscriminator

	// Step 14: Set bfd.RemoteState.
	s.remoteState.Store(uint32(pkt.State))

	// Step 15: Set bfd.RemoteDemandMode = Demand bit.
	s.remoteDemandMode = pkt.Demand

	// Step 16: Set bfd.RemoteMinRxInterval.
	s.remoteMinRxInterval = durationFromMicroseconds(pkt.RequiredMinRxInterval)

	// Step 17: Set remoteDesiredMinTxInterval + remoteDetectMult.
	s.remoteDesiredMinTxInterval = durationFromMicroseconds(pkt.DesiredMinTxInterval)
	s.remoteDetectMult = pkt.DetectMult

	// Poll Sequence: if Final bit set and poll is active, terminate.
	if pkt.Final && s.pollActive {
		s.terminatePollSequence()
	}

	// If Poll bit is set, we must reply with Final.
	if pkt.Poll {
		s.pendingFinal = true
	}

	// Reset detection timer on every valid packet (RFC 5880 Section 6.8.4).
	s.resetDetectTimer(detectTimer)

	// Apply FSM event based on received state.
	event := RecvStateToEvent(pkt.State)
	s.applyFSMEvent(ctx, event, txTimer, detectTimer)

	// RFC 5880 Section 6.5: "the receiving system MUST transmit a BFD
	// Control packet with the Final (F) bit set as soon as practicable."
	// Send immediately if we have a pending Final response or if a state
	// change triggered ActionSendControl.
	if s.pendingFinal {
		s.sendControl(ctx)
		s.resetTxTimer(txTimer)
	}
}

// checkAuthConsistency validates RFC 5880 Section 6.8.6 steps 8-9.
func (s *Session) checkAuthConsistency(pkt *ControlPacket) bool {
	// Step 8: A bit set but no auth configured -> discard.
	if pkt.AuthPresent && s.auth == nil {
		s.logger.Warn("discarding packet: auth present but not configured",
			slog.String("peer", s.peerAddr.String()),
		)
		return false
	}
	// Step 9: A bit clear but auth configured -> discard.
	if !pkt.AuthPresent && s.auth != nil {
		s.logger.Warn("discarding packet: auth not present but configured",
			slog.String("peer", s.peerAddr.String()),
		)
		return false
	}
	return true
}

// -------------------------------------------------------------------------
// FSM Event Application
// -------------------------------------------------------------------------

// applyFSMEvent runs the FSM and executes resulting actions.
func (s *Session) applyFSMEvent(
	ctx context.Context,
	event Event,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	result := ApplyEvent(s.State(), event)
	s.executeFSMActions(ctx, result, txTimer, detectTimer)
}

// executeFSMActions processes the FSMResult and performs side-effects.
func (s *Session) executeFSMActions(
	ctx context.Context,
	result FSMResult,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	if result.Changed {
		s.state.Store(uint32(result.NewState))
		s.logStateChange(result)
	}
	for _, action := range result.Actions {
		s.executeAction(ctx, action, txTimer, detectTimer)
	}
}

// logStateChange logs the FSM transition, updates counters, and emits a
// StateChange notification.
func (s *Session) logStateChange(result FSMResult) {
	s.logger.Info("session state changed",
		slog.String("old_state", result.OldState.String()),
		slog.String("new_state", result.NewState.String()),
		slog.String("diag", s.LocalDiag().String()),
	)
	s.stateTransitions.Add(1)
	s.lastStateChange.Store(time.Now().UnixNano())
	s.metrics.RecordStateTransition(
		s.peerAddr, s.localAddr,
		result.OldState.String(), result.NewState.String(),
	)
	s.emitNotification(result)
}

// executeAction dispatches a single FSM action.
func (s *Session) executeAction(
	ctx context.Context,
	action Action,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	switch action {
	case ActionSendControl:
		// Immediate send + reset TX timer (RFC 5880 Section 6.8.7).
		s.sendControl(ctx)
		s.resetTxTimer(txTimer)
	case ActionNotifyUp:
		// State already set; recalculate timers for Up state.
		s.resetTxTimer(txTimer)
		s.resetDetectTimer(detectTimer)
	case ActionNotifyDown:
		// RFC 5880 Section 6.8.1: reset remoteDiscr on session failure.
		s.remoteDiscr = 0
		s.resetTxTimer(txTimer)
		s.resetDetectTimer(detectTimer)
	case ActionSetDiagTimeExpired:
		s.localDiag.Store(uint32(DiagControlTimeExpired))
	case ActionSetDiagNeighborDown:
		s.localDiag.Store(uint32(DiagNeighborDown))
	case ActionSetDiagAdminDown:
		s.localDiag.Store(uint32(DiagAdminDown))
	default:
		s.logger.Warn("unknown FSM action", slog.Int("action", int(action)))
	}
}

// emitNotification sends a StateChange to the notification channel if set.
func (s *Session) emitNotification(result FSMResult) {
	if s.notifyCh == nil {
		return
	}
	sc := StateChange{
		LocalDiscr: s.localDiscr,
		PeerAddr:   s.peerAddr,
		OldState:   result.OldState,
		NewState:   result.NewState,
		Diag:       s.LocalDiag(),
		Timestamp:  time.Now(),
	}
	select {
	case s.notifyCh <- sc:
	default:
		s.logger.Warn("notification channel full, dropping state change")
	}
}

// -------------------------------------------------------------------------
// Timer Negotiation — RFC 5880 Sections 6.8.2-6.8.4
// -------------------------------------------------------------------------

// calcTxInterval returns the negotiated TX interval.
//
// RFC 5880 Section 6.8.7: "the larger of bfd.DesiredMinTxInterval and
// bfd.RemoteMinRxInterval."
//
// RFC 5880 Section 6.8.3: "When bfd.SessionState is not Up, the system
// MUST set bfd.DesiredMinTxInterval to a value of not less than one
// second (1,000,000 microseconds).".
func (s *Session) calcTxInterval() time.Duration {
	desired := s.desiredMinTxInterval
	// RFC 5880 Section 6.8.3: enforce slow rate when not Up.
	if s.State() != StateUp && desired < slowTxInterval {
		desired = slowTxInterval
	}
	return max(desired, s.remoteMinRxInterval)
}

// calcDetectionTime returns the detection timeout.
//
// RFC 5880 Section 6.8.4 (Asynchronous mode): "equal to the value of
// Detect Mult received from the remote system, multiplied by the agreed
// transmit interval of the remote system (the greater of
// bfd.RequiredMinRxInterval and the last received Desired Min TX Interval).".
func (s *Session) calcDetectionTime() time.Duration {
	if s.remoteDetectMult == 0 {
		// Before receiving any packet, use local detect mult with slow rate.
		txInterval := s.calcTxInterval()
		return time.Duration(int64(txInterval) * int64(s.detectMult))
	}
	agreedInterval := max(s.requiredMinRxInterval, s.remoteDesiredMinTxInterval)
	return time.Duration(int64(agreedInterval) * int64(s.remoteDetectMult))
}

// resetTxTimer resets the TX timer with jittered negotiated interval.
func (s *Session) resetTxTimer(txTimer *time.Timer) {
	interval := s.calcTxInterval()
	if !txTimer.Stop() {
		drainTimer(txTimer)
	}
	txTimer.Reset(ApplyJitter(interval, s.detectMult))
}

// resetDetectTimer resets the detection timer with the calculated timeout.
func (s *Session) resetDetectTimer(detectTimer *time.Timer) {
	detectTime := s.calcDetectionTime()
	if !detectTimer.Stop() {
		drainTimer(detectTimer)
	}
	detectTimer.Reset(detectTime)
}

// drainTimer non-blockingly drains the timer channel.
func drainTimer(t *time.Timer) {
	select {
	case <-t.C:
	default:
	}
}

// -------------------------------------------------------------------------
// Jitter — RFC 5880 Section 6.8.7
// -------------------------------------------------------------------------

// ApplyJitter applies random jitter to the transmission interval.
//
// RFC 5880 Section 6.8.7:
//   - The interval MUST be reduced by a random value of 0 to 25%.
//   - If bfd.DetectMult == 1: interval MUST be between 75% and 90%.
//   - Otherwise: interval MUST be between 75% and 100%.
//
// Uses math/rand/v2 for non-cryptographic randomness (jitter is not
// security-sensitive; using crypto/rand would add unnecessary overhead
// on the hot path).
func ApplyJitter(interval time.Duration, detectMult uint8) time.Duration {
	if interval <= 0 {
		return interval
	}

	// RFC 5880 Section 6.8.7:
	//   Normal: reduce by 0-25% (result 75-100%).
	//   DetectMult == 1: reduce by 10-25% (result 75-90%).
	var jitterPercent int
	if detectMult == 1 {
		// 10 + rand(0..15) = reduction of 10-25%.
		jitterPercent = 10 + rand.IntN(16) //nolint:gosec // G404: jitter does not require cryptographic randomness
	} else {
		// rand(0..25) = reduction of 0-25%.
		jitterPercent = rand.IntN(26) //nolint:gosec // G404: jitter does not require cryptographic randomness
	}

	reduction := time.Duration(int64(interval) * int64(jitterPercent) / 100)

	return interval - reduction
}

// -------------------------------------------------------------------------
// Poll Sequence — RFC 5880 Section 6.5
// -------------------------------------------------------------------------

// terminatePollSequence ends the Poll Sequence and applies pending changes.
// RFC 5880 Section 6.5: "When the system sending the Poll Sequence
// receives a packet with Final, the Poll Sequence is terminated.".
func (s *Session) terminatePollSequence() {
	s.pollActive = false
	s.applyPendingParams()
	s.rebuildCachedPacket()
	s.logger.Debug("poll sequence terminated")
}

// applyPendingParams applies deferred parameter changes after poll completion.
func (s *Session) applyPendingParams() {
	if s.pendingDesiredMinTx > 0 {
		s.desiredMinTxInterval = s.pendingDesiredMinTx
		s.pendingDesiredMinTx = 0
	}
	if s.pendingRequiredMinRx > 0 {
		s.requiredMinRxInterval = s.pendingRequiredMinRx
		s.pendingRequiredMinRx = 0
	}
}

// -------------------------------------------------------------------------
// Cached Packet — FRR bfdd pattern
// -------------------------------------------------------------------------

// rebuildCachedPacket pre-serializes the BFD Control packet for transmission.
// This avoids per-packet allocation on the hot path. The packet is rebuilt
// only when parameters or state change.
//
// RFC 5880 Section 6.8.7 specifies all field values for transmitted packets.
func (s *Session) rebuildCachedPacket() {
	pkt := s.buildControlPacket()
	n, err := MarshalControlPacket(&pkt, s.cachedPacket)
	if err != nil {
		s.logger.Error("failed to marshal cached packet",
			slog.String("error", err.Error()),
		)
		return
	}
	// RFC 5880 Section 6.7: sign the packet if auth is configured.
	if s.auth != nil {
		s.signCachedPacket(&pkt, n)
	}
}

// signCachedPacket applies authentication to the cached packet.
// Sign modifies both the packet struct and the buffer in-place.
func (s *Session) signCachedPacket(pkt *ControlPacket, n int) {
	if err := s.auth.Sign(
		s.authState, s.authKeys, pkt, s.cachedPacket, n,
	); err != nil {
		s.logger.Error("auth sign failed",
			slog.String("error", err.Error()),
		)
	}
}

// buildControlPacket constructs a ControlPacket from current session state.
// RFC 5880 Section 6.8.7: field-by-field specification of transmitted packets.
func (s *Session) buildControlPacket() ControlPacket {
	// RFC 5880 Section 6.8.3: "When bfd.SessionState is not Up, the
	// system MUST set bfd.DesiredMinTxInterval to a value of not less
	// than one second (1,000,000 microseconds)." This applies to the
	// wire value so the remote peer calculates correct detection time.
	wireTxInterval := s.desiredMinTxInterval
	if s.State() != StateUp && wireTxInterval < slowTxInterval {
		wireTxInterval = slowTxInterval
	}

	pkt := ControlPacket{
		Version:                   Version,
		Diag:                      s.LocalDiag(),
		State:                     s.State(),
		Poll:                      s.pollActive,
		Final:                     s.pendingFinal,
		ControlPlaneIndependent:   false,
		AuthPresent:               false,
		Demand:                    false, // Demand mode not implemented in MVP.
		Multipoint:                false, // RFC 5880 Section 6.8.7: MUST be zero.
		DetectMult:                s.detectMult,
		MyDiscriminator:           s.localDiscr,
		YourDiscriminator:         s.remoteDiscr,
		DesiredMinTxInterval:      microsecondsFromDuration(wireTxInterval),
		RequiredMinRxInterval:     microsecondsFromDuration(s.requiredMinRxInterval),
		RequiredMinEchoRxInterval: 0, // Echo not implemented in MVP.
	}

	// Clear pendingFinal after building packet (it was consumed).
	s.pendingFinal = false

	return pkt
}

// -------------------------------------------------------------------------
// Duration <-> Microseconds conversion
// -------------------------------------------------------------------------

// durationFromMicroseconds converts a BFD wire-format microsecond value
// to time.Duration. RFC 5880: all interval fields are in microseconds.
func durationFromMicroseconds(us uint32) time.Duration {
	return time.Duration(int64(us) * int64(time.Microsecond))
}

// microsecondsFromDuration converts time.Duration to BFD wire-format
// microseconds (uint32). Values are truncated, not rounded.
func microsecondsFromDuration(d time.Duration) uint32 {
	return uint32(d / time.Microsecond) //nolint:gosec // G115: intentional truncation for BFD wire format
}
