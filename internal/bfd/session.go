package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
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

	// SessionTypeGeneve indicates a BFD session over a Geneve tunnel (RFC 9521).
	SessionTypeGeneve
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
	case SessionTypeGeneve:
		return "Geneve"
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

	// PaddedPduSize is the padded PDU size for BFD Large Packets (RFC 9764).
	// When nonzero, transmitted BFD Control packets are padded with zeros to
	// this total length. The DF bit is set to enable path MTU verification.
	// Valid range: HeaderSize (24) to MaxPaddedPduSize (9000).
	// Zero means no padding (default behavior).
	PaddedPduSize uint16
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

	// Type is the session type (single-hop, multi-hop, micro-BFD, etc.).
	// Used by the Manager to dispatch micro-BFD events to the correct group.
	Type SessionType

	// Interface is the network interface name.
	// Used for micro-BFD dispatch: identifies the LAG member link.
	Interface string
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

	// ErrInvalidRxInterval indicates the required min RX interval is invalid.
	ErrInvalidRxInterval = errors.New("required min RX interval must be > 0")

	// ErrInvalidWireInterval indicates an interval cannot be represented in BFD wire format.
	ErrInvalidWireInterval = errors.New("wire interval must fit uint32 microseconds")

	// ErrInvalidSessionType indicates an unknown session type.
	ErrInvalidSessionType = errors.New("invalid session type")

	// ErrInvalidSessionRole indicates an unknown session role.
	ErrInvalidSessionRole = errors.New("invalid session role")

	// ErrInvalidDiscriminator indicates the local discriminator is zero.
	ErrInvalidDiscriminator = errors.New("local discriminator must be nonzero")

	// ErrInvalidPaddedPduSize indicates the padded PDU size is out of range.
	ErrInvalidPaddedPduSize = errors.New("padded PDU size must be 0 or between 24 and 9000")

	// ErrMissingAuthKeyStore indicates authentication was configured without keys.
	ErrMissingAuthKeyStore = errors.New("auth key store is required when auth is configured")
)

// -------------------------------------------------------------------------
// Session Constants
// -------------------------------------------------------------------------

const (
	// MaxPaddedPduSize is the maximum padded PDU size for RFC 9764.
	// Capped at jumbo frame Ethernet MTU minus IP+UDP headers.
	MaxPaddedPduSize = 9000

	// MaxWireInterval is the largest BFD interval encodable on the wire.
	// RFC 5880 stores intervals as uint32 microseconds.
	MaxWireInterval = time.Duration(1<<32-1) * time.Microsecond

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
	mu sync.RWMutex

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

	// paddedPduSize is the RFC 9764 padded PDU size. Zero means no padding.
	paddedPduSize uint16

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

	// --- Hot-path optimizations ---

	// jitterRng is a session-local PRNG for jitter calculations.
	// Goroutine-confined to the session goroutine — no synchronization needed.
	jitterRng jitterRNG

	// cachedState mirrors s.state for hot-path reads within the session goroutine.
	// Updated in executeFSMActions on every state change. Avoids
	// atomic.Uint32.Load() on every timer recalculation.
	cachedState State

	// --- Runtime ---

	sender   PacketSender
	metrics  MetricsReporter
	logger   *slog.Logger
	recvCh   chan recvItem
	ctrlCh   chan sessionControl
	notifyCh chan<- StateChange
	running  atomic.Bool
}

// recvItem carries a received BFD Control packet along with the raw
// wire bytes needed for authentication verification (RFC 5880 Section 6.7).
type recvItem struct {
	pkt  *ControlPacket
	wire []byte // raw wire bytes for auth digest verification
}

type sessionControlKind uint8

const (
	controlPathDown sessionControlKind = iota + 1
	controlAdminDown
)

type sessionControl struct {
	kind sessionControlKind
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

	// RFC 9764: allocate a buffer large enough for padded packets.
	pktBufSize := MaxPacketSize
	if cfg.PaddedPduSize > uint16(pktBufSize) {
		pktBufSize = int(cfg.PaddedPduSize)
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
		paddedPduSize:         cfg.PaddedPduSize,
		jitterRng:             newJitterRNG(),
		cachedState:           StateDown, // RFC 5880 Section 6.8.1: initialized to Down
		sender:                sender,
		metrics:               noopMetrics{},
		notifyCh:              notifyCh,
		recvCh:                make(chan recvItem, recvChSize),
		ctrlCh:                make(chan sessionControl, 4),
		cachedPacket:          make([]byte, pktBufSize),
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
	if cfg.RequiredMinRxInterval <= 0 {
		return fmt.Errorf("required min RX interval %v: %w", cfg.RequiredMinRxInterval, ErrInvalidRxInterval)
	}
	if cfg.DesiredMinTxInterval > MaxWireInterval {
		return fmt.Errorf(
			"desired min TX interval %v exceeds max wire interval %v: %w",
			cfg.DesiredMinTxInterval,
			MaxWireInterval,
			ErrInvalidWireInterval,
		)
	}
	if cfg.RequiredMinRxInterval > MaxWireInterval {
		return fmt.Errorf(
			"required min RX interval %v exceeds max wire interval %v: %w",
			cfg.RequiredMinRxInterval,
			MaxWireInterval,
			ErrInvalidWireInterval,
		)
	}
	if !isValidSessionType(cfg.Type) {
		return fmt.Errorf("session type %d: %w", cfg.Type, ErrInvalidSessionType)
	}
	if cfg.Role != RoleActive && cfg.Role != RolePassive {
		return fmt.Errorf("session role %d: %w", cfg.Role, ErrInvalidSessionRole)
	}
	if localDiscr == 0 {
		return fmt.Errorf("local discriminator: %w", ErrInvalidDiscriminator)
	}
	// RFC 9764: PaddedPduSize must be 0 (disabled) or within [HeaderSize, MaxPaddedPduSize].
	if cfg.PaddedPduSize != 0 && (cfg.PaddedPduSize < HeaderSize || cfg.PaddedPduSize > MaxPaddedPduSize) {
		return fmt.Errorf("padded PDU size %d: %w", cfg.PaddedPduSize, ErrInvalidPaddedPduSize)
	}
	if err := validateSessionAuthConfig(cfg); err != nil {
		return err
	}
	return nil
}

func validateSessionAuthConfig(cfg SessionConfig) error {
	if cfg.Auth == nil {
		return nil
	}
	if cfg.AuthKeys == nil {
		return ErrMissingAuthKeyStore
	}

	key := cfg.AuthKeys.CurrentKey()
	authType, ok := authenticatorType(cfg.Auth)
	if !ok {
		return fmt.Errorf("authenticator %T: %w", cfg.Auth, ErrAuthTypeMismatch)
	}
	if key.Type != authType {
		return fmt.Errorf("authenticator %s with key %s: %w",
			authType, key.Type, ErrAuthKeyTypeMismatch)
	}
	return nil
}

func isValidSessionType(t SessionType) bool {
	switch t {
	case SessionTypeSingleHop,
		SessionTypeMultiHop,
		SessionTypeMicroBFD,
		SessionTypeVXLAN,
		SessionTypeGeneve:
		return true
	default:
		return false
	}
}

// initAuth initializes the authentication state if auth is configured.
// RFC 5880 Section 6.8.1: bfd.XmitAuthSeq MUST be initialized to a
// random 32-bit value.
func (s *Session) initAuth(cfg SessionConfig) error {
	if cfg.Auth == nil {
		return nil
	}
	authType, ok := authenticatorType(cfg.Auth)
	if !ok {
		return fmt.Errorf("init auth state: authenticator %T: %w", cfg.Auth, ErrAuthTypeMismatch)
	}
	as, err := NewAuthState(authType)
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
func (s *Session) RemoteDiscriminator() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.remoteDiscr
}

// PeerAddr returns the remote system's IP address.
func (s *Session) PeerAddr() netip.Addr { return s.peerAddr }

// LocalAddr returns the local system's IP address.
func (s *Session) LocalAddr() netip.Addr { return s.localAddr }

// Interface returns the network interface name (empty for multi-hop sessions).
func (s *Session) Interface() string { return s.ifName }

// Type returns the session type (single-hop or multi-hop).
func (s *Session) Type() SessionType { return s.sessionType }

// PaddedPduSize returns the RFC 9764 padded PDU size. Zero means no padding.
func (s *Session) PaddedPduSize() uint16 { return s.paddedPduSize }

// AuthType returns the RFC 5880 authentication type configured for this session.
func (s *Session) AuthType() AuthType {
	if s.authState == nil {
		return AuthTypeNone
	}
	return s.authState.Type
}

// DesiredMinTxInterval returns the configured desired minimum TX interval.
func (s *Session) DesiredMinTxInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.desiredMinTxInterval
}

// RequiredMinRxInterval returns the configured required minimum RX interval.
func (s *Session) RequiredMinRxInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requiredMinRxInterval
}

// DetectMultiplier returns the configured detection multiplier.
func (s *Session) DetectMultiplier() uint8 { return s.detectMult }

// NegotiatedTxInterval returns the current negotiated TX interval.
// RFC 5880 Section 6.8.7: max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval).
// When state is not Up, the slow rate (1s) is enforced per RFC 5880 Section 6.8.3.
func (s *Session) NegotiatedTxInterval() time.Duration {
	return s.calcTxInterval()
}

// DetectionTime returns the current calculated detection time.
// RFC 5880 Section 6.8.4: RemoteDetectMult * max(RequiredMinRx, RemoteDesiredMinTx).
func (s *Session) DetectionTime() time.Duration {
	return s.calcDetectionTime()
}

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
// Thread-safe: when the session goroutine is running, routes the transition
// through ctrlCh so the goroutine-confined cached state stays coherent.
func (s *Session) SetAdminDown() {
	if s.running.Load() {
		select {
		case s.ctrlCh <- sessionControl{kind: controlAdminDown}:
			return
		default:
			s.logger.Warn("session control channel full, applying AdminDown directly")
		}
	}

	s.localDiag.Store(uint32(DiagAdminDown))
	s.state.Store(uint32(StateAdminDown))
	s.logger.Info("session set to AdminDown for graceful drain")
}

// SetPathDown asks the session goroutine to transition the session to Down
// with DiagPathDown. It returns false if the control channel is full.
func (s *Session) SetPathDown() bool {
	select {
	case s.ctrlCh <- sessionControl{kind: controlPathDown}:
		return true
	default:
		s.logger.Warn("session control channel full, dropping path-down event")
		return false
	}
}
