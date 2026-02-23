// RFC 9747 — Unaffiliated BFD for Sessionless Applications.
//
// The unaffiliated BFD echo function provides forwarding-path liveness
// detection without requiring the remote system to run BFD. The local
// system sends BFD Control packets (echo packets) to the remote, which
// forwards them back via normal IP routing. If echoes stop returning,
// the local system declares the path down.
//
// Key differences from BFD control sessions (RFC 5880):
//   - No three-way handshake (no Init state)
//   - No timer negotiation (locally provisioned)
//   - DiagEchoFailed (2) on timeout instead of DiagControlTimeExpired (1)
//   - UDP port 3785 (RFC 5881 Section 4)
//   - TTL 255 send, TTL >= 254 receive (GTSM)
//   - Demux on return by MyDiscriminator (self-originated packet)

package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"runtime"
	"sync/atomic"
	"time"
)

// -------------------------------------------------------------------------
// Echo Session Configuration — RFC 9747 Section 3
// -------------------------------------------------------------------------

// EchoSessionConfig contains the parameters for an RFC 9747 echo session.
// All timers are locally provisioned — no negotiation with the remote.
type EchoSessionConfig struct {
	// PeerAddr is the remote system's IP address (echo target).
	// The remote forwards echo packets back via normal IP routing.
	PeerAddr netip.Addr

	// LocalAddr is the local system's IP address.
	LocalAddr netip.Addr

	// Interface is the network interface name for SO_BINDTODEVICE (optional).
	Interface string

	// TxInterval is the echo transmit interval.
	// RFC 9747 Section 3.3: locally provisioned, not negotiated.
	TxInterval time.Duration

	// DetectMultiplier is the detection time multiplier.
	// DetectionTime = DetectMult * TxInterval.
	DetectMultiplier uint8
}

// -------------------------------------------------------------------------
// Echo Session Errors
// -------------------------------------------------------------------------

// Sentinel errors for EchoSession configuration validation.
var (
	// ErrInvalidEchoPeerAddr indicates the echo peer address is not valid.
	ErrInvalidEchoPeerAddr = errors.New("echo session: peer address must be valid")

	// ErrInvalidEchoTxInterval indicates the echo TX interval is not positive.
	ErrInvalidEchoTxInterval = errors.New("echo session: TX interval must be > 0")
)

// -------------------------------------------------------------------------
// Echo Session Constants
// -------------------------------------------------------------------------

const (
	// echoRecvChSize is the buffer size for echo receive notifications.
	echoRecvChSize = 16
)

// -------------------------------------------------------------------------
// EchoSession — RFC 9747 Section 3
// -------------------------------------------------------------------------

// EchoSession implements an RFC 9747 unaffiliated BFD echo session.
//
// The echo function sends BFD Control packets to the remote system,
// which forwards them back via normal IP routing. If echoes stop
// returning within DetectionTime, the session transitions to Down
// with DiagEchoFailed.
//
// State machine (RFC 9747 Section 3.3):
//   - Down → Up: when an echo packet is received back
//   - Up → Down: when detection time expires (DiagEchoFailed)
//   - No Init state, no AdminDown (simpler than control FSM)
type EchoSession struct {
	// --- State ---
	state     atomic.Uint32
	localDiag atomic.Uint32

	// --- Identity ---
	localDiscr uint32
	peerAddr   netip.Addr
	localAddr  netip.Addr
	ifName     string

	// --- Timers (locally provisioned) ---
	txInterval time.Duration
	detectMult uint8

	// --- Runtime ---
	sender       PacketSender
	metrics      MetricsReporter
	logger       *slog.Logger
	recvCh       chan struct{}
	notifyCh     chan<- StateChange
	cachedPacket []byte

	// --- Counters ---
	echosSent        atomic.Uint64
	echosReceived    atomic.Uint64
	stateTransitions atomic.Uint64
	lastStateChange  atomic.Int64
	lastEchoRecv     atomic.Int64
}

// EchoSessionOption configures optional EchoSession parameters.
type EchoSessionOption func(*EchoSession)

// WithEchoMetrics attaches a MetricsReporter to the echo session.
func WithEchoMetrics(mr MetricsReporter) EchoSessionOption {
	return func(es *EchoSession) {
		if mr != nil {
			es.metrics = mr
		}
	}
}

// NewEchoSession creates a new RFC 9747 unaffiliated BFD echo session.
// The session goroutine is NOT started until Run() is called.
//
// localDiscr must be a unique nonzero discriminator allocated externally.
// sender provides the packet sending capability for echo packets.
// notifyCh may be nil if no state change notifications are needed.
func NewEchoSession(
	cfg EchoSessionConfig,
	localDiscr uint32,
	sender PacketSender,
	notifyCh chan<- StateChange,
	logger *slog.Logger,
	opts ...EchoSessionOption,
) (*EchoSession, error) {
	if err := validateEchoConfig(cfg, localDiscr); err != nil {
		return nil, err
	}

	es := &EchoSession{
		localDiscr:   localDiscr,
		peerAddr:     cfg.PeerAddr,
		localAddr:    cfg.LocalAddr,
		ifName:       cfg.Interface,
		txInterval:   cfg.TxInterval,
		detectMult:   cfg.DetectMultiplier,
		sender:       sender,
		metrics:      noopMetrics{},
		notifyCh:     notifyCh,
		recvCh:       make(chan struct{}, echoRecvChSize),
		cachedPacket: make([]byte, MaxPacketSize),
		logger: logger.With(
			slog.String("peer", cfg.PeerAddr.String()),
			slog.Uint64("local_discr", uint64(localDiscr)),
			slog.String("mode", "echo"),
		),
	}

	for _, opt := range opts {
		opt(es)
	}

	// RFC 9747 Section 3.3: echo session starts in Down state.
	es.state.Store(uint32(StateDown))
	es.localDiag.Store(uint32(DiagNone))
	es.rebuildCachedPacket()

	return es, nil
}

// validateEchoConfig checks all echo session configuration parameters.
func validateEchoConfig(cfg EchoSessionConfig, localDiscr uint32) error {
	if localDiscr == 0 {
		return fmt.Errorf("echo session: %w", ErrInvalidDiscriminator)
	}
	if cfg.DetectMultiplier < 1 {
		return fmt.Errorf("echo detect multiplier %d: %w", cfg.DetectMultiplier, ErrInvalidDetectMult)
	}
	if cfg.TxInterval <= 0 {
		return ErrInvalidEchoTxInterval
	}
	if !cfg.PeerAddr.IsValid() {
		return ErrInvalidEchoPeerAddr
	}
	return nil
}

// -------------------------------------------------------------------------
// Public Accessors — Thread-safe via atomic
// -------------------------------------------------------------------------

// LocalDiscriminator returns the echo session's local discriminator.
func (es *EchoSession) LocalDiscriminator() uint32 { return es.localDiscr }

// State returns the current echo session state (atomic read).
func (es *EchoSession) State() State {
	return State(es.state.Load()) //nolint:gosec // G115: State is 0-3, fits uint8
}

// LocalDiag returns the current local diagnostic code (atomic read).
func (es *EchoSession) LocalDiag() Diag {
	return Diag(es.localDiag.Load()) //nolint:gosec // G115: Diag is 0-8, fits uint8
}

// PeerAddr returns the remote system's IP address (echo target).
func (es *EchoSession) PeerAddr() netip.Addr { return es.peerAddr }

// LocalAddr returns the local system's IP address.
func (es *EchoSession) LocalAddr() netip.Addr { return es.localAddr }

// Interface returns the network interface name.
func (es *EchoSession) Interface() string { return es.ifName }

// TxInterval returns the configured echo transmit interval.
func (es *EchoSession) TxInterval() time.Duration { return es.txInterval }

// DetectMultiplier returns the configured detection multiplier.
func (es *EchoSession) DetectMultiplier() uint8 { return es.detectMult }

// DetectionTime returns the echo detection timeout.
// RFC 9747 Section 3.3: DetectMult * TxInterval.
func (es *EchoSession) DetectionTime() time.Duration {
	return time.Duration(int64(es.txInterval) * int64(es.detectMult))
}

// EchosSent returns the total echo packets transmitted (atomic read).
func (es *EchoSession) EchosSent() uint64 { return es.echosSent.Load() }

// EchosReceived returns the total echo packets received back (atomic read).
func (es *EchoSession) EchosReceived() uint64 { return es.echosReceived.Load() }

// StateTransitions returns the total FSM state transitions (atomic read).
func (es *EchoSession) StateTransitions() uint64 { return es.stateTransitions.Load() }

// LastStateChange returns the timestamp of the most recent state transition.
func (es *EchoSession) LastStateChange() time.Time {
	ns := es.lastStateChange.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// LastEchoReceived returns the timestamp of the most recent echo received.
func (es *EchoSession) LastEchoReceived() time.Time {
	ns := es.lastEchoRecv.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// -------------------------------------------------------------------------
// Receive Path — echo return notification
// -------------------------------------------------------------------------

// RecvEcho signals that an echo packet was received back (matched by
// discriminator). The echo listener should call this when a returned
// echo packet's MyDiscriminator matches this session. If the receive
// channel is full, the signal is dropped (logged at debug level).
func (es *EchoSession) RecvEcho() {
	select {
	case es.recvCh <- struct{}{}:
	default:
		es.logger.Debug("echo recv channel full, dropping signal")
	}
}

// -------------------------------------------------------------------------
// Main Goroutine — RFC 9747 Echo Session Lifecycle
// -------------------------------------------------------------------------

// Run starts the echo session event loop. It blocks until ctx is cancelled.
// The session begins in Down state and starts sending echo packets
// according to the configured TX interval.
//
// The event loop processes:
//  1. Echo return signals from recvCh
//  2. Transmission timer fires (send next echo)
//  3. Detection timer expires (path down)
//  4. Context cancellation (shutdown)
func (es *EchoSession) Run(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	txTimer := time.NewTimer(ApplyJitter(es.txInterval, es.detectMult))
	defer txTimer.Stop()

	detectTime := es.DetectionTime()
	detectTimer := time.NewTimer(detectTime)
	defer detectTimer.Stop()

	es.logger.Info("echo session started",
		slog.Duration("tx_interval", es.txInterval),
		slog.Duration("detect_time", detectTime),
	)

	es.echoRunLoop(ctx, txTimer, detectTimer)
}

// echoRunLoop is the core select loop for the echo session.
func (es *EchoSession) echoRunLoop(
	ctx context.Context,
	txTimer *time.Timer,
	detectTimer *time.Timer,
) {
	for {
		select {
		case <-ctx.Done():
			es.logger.Info("echo session stopped")
			return

		case <-es.recvCh:
			es.handleEchoRecv(detectTimer)

		case <-txTimer.C:
			es.sendEcho(ctx)
			txTimer.Reset(ApplyJitter(es.txInterval, es.detectMult))

		case <-detectTimer.C:
			es.handleDetectTimeout(detectTimer)
		}
	}
}

// -------------------------------------------------------------------------
// Echo Receive Handling
// -------------------------------------------------------------------------

// handleEchoRecv processes a returned echo packet.
// Resets the detection timer and transitions Down → Up if needed.
func (es *EchoSession) handleEchoRecv(detectTimer *time.Timer) {
	es.echosReceived.Add(1)
	es.lastEchoRecv.Store(time.Now().UnixNano())
	es.metrics.IncPacketsReceived(es.peerAddr, es.localAddr)

	// Reset detection timer on every echo return.
	if !detectTimer.Stop() {
		drainTimer(detectTimer)
	}
	detectTimer.Reset(es.DetectionTime())

	// RFC 9747 Section 3.3: Down → Up when echo is received.
	if es.State() == StateDown {
		es.transitionTo(StateUp, DiagNone)
	}
}

// -------------------------------------------------------------------------
// Detection Timeout
// -------------------------------------------------------------------------

// handleDetectTimeout fires when no echo returns within detection time.
// RFC 9747 Section 3.3: transition to Down with DiagEchoFailed.
func (es *EchoSession) handleDetectTimeout(detectTimer *time.Timer) {
	if es.State() == StateUp {
		es.transitionTo(StateDown, DiagEchoFailed)
	}
	// Restart detection timer regardless of state.
	detectTimer.Reset(es.DetectionTime())
}

// -------------------------------------------------------------------------
// State Transitions
// -------------------------------------------------------------------------

// transitionTo performs an echo session state transition.
func (es *EchoSession) transitionTo(newState State, diag Diag) {
	oldState := es.State()
	if oldState == newState {
		return
	}

	es.state.Store(uint32(newState))
	es.localDiag.Store(uint32(diag))
	es.stateTransitions.Add(1)
	es.lastStateChange.Store(time.Now().UnixNano())
	es.metrics.RecordStateTransition(
		es.peerAddr, es.localAddr,
		oldState.String(), newState.String(),
	)

	es.logger.Info("echo session state changed",
		slog.String("old_state", oldState.String()),
		slog.String("new_state", newState.String()),
		slog.String("diag", diag.String()),
	)

	es.emitNotification(oldState, newState, diag)
}

// -------------------------------------------------------------------------
// Echo Packet Transmission
// -------------------------------------------------------------------------

// sendEcho serializes and sends a BFD echo packet.
// The echo packet uses the standard BFD Control format but is sent
// to the remote on port 3785. The remote forwards it back.
func (es *EchoSession) sendEcho(ctx context.Context) {
	es.rebuildCachedPacket()
	pktLen := int(es.cachedPacket[3]) // Length field at byte 3
	if err := es.sender.SendPacket(ctx, es.cachedPacket[:pktLen], es.peerAddr); err != nil {
		es.logger.Warn("failed to send echo packet",
			slog.String("error", err.Error()),
		)
		return
	}
	es.echosSent.Add(1)
	es.metrics.IncPacketsSent(es.peerAddr, es.localAddr)
}

// rebuildCachedPacket pre-serializes the BFD echo packet.
// RFC 9747: uses standard BFD Control packet format.
// MyDiscriminator is set for demux on return. YourDiscriminator is zero.
func (es *EchoSession) rebuildCachedPacket() {
	pkt := ControlPacket{
		Version:                   Version,
		Diag:                      es.LocalDiag(),
		State:                     es.State(),
		DetectMult:                es.detectMult,
		MyDiscriminator:           es.localDiscr,
		YourDiscriminator:         0,
		DesiredMinTxInterval:      microsecondsFromDuration(es.txInterval),
		RequiredMinRxInterval:     0, // Echo does not negotiate.
		RequiredMinEchoRxInterval: 0,
	}

	if _, err := MarshalControlPacket(&pkt, es.cachedPacket); err != nil {
		es.logger.Error("failed to marshal echo packet",
			slog.String("error", err.Error()),
		)
	}
}

// -------------------------------------------------------------------------
// Notifications
// -------------------------------------------------------------------------

// emitNotification sends a StateChange to the notification channel.
func (es *EchoSession) emitNotification(oldState, newState State, diag Diag) {
	if es.notifyCh == nil {
		return
	}
	sc := StateChange{
		LocalDiscr: es.localDiscr,
		PeerAddr:   es.peerAddr,
		OldState:   oldState,
		NewState:   newState,
		Diag:       diag,
		Timestamp:  time.Now(),
	}
	select {
	case es.notifyCh <- sc:
	default:
		es.logger.Warn("notification channel full, dropping echo state change")
	}
}

// -------------------------------------------------------------------------
// Snapshot — read-only view for external consumers
// -------------------------------------------------------------------------

// EchoSessionSnapshot is a read-only view of an echo session's state.
type EchoSessionSnapshot struct {
	// LocalDiscr is the local discriminator.
	LocalDiscr uint32

	// PeerAddr is the remote system's IP address (echo target).
	PeerAddr netip.Addr

	// LocalAddr is the local system's IP address.
	LocalAddr netip.Addr

	// Interface is the network interface name.
	Interface string

	// State is the current echo session state.
	State State

	// LocalDiag is the current diagnostic code.
	LocalDiag Diag

	// TxInterval is the configured echo transmit interval.
	TxInterval time.Duration

	// DetectMultiplier is the configured detection multiplier.
	DetectMultiplier uint8

	// DetectionTime is the calculated detection time.
	DetectionTime time.Duration

	// LastStateChange is the timestamp of the most recent state transition.
	LastStateChange time.Time

	// LastEchoReceived is the timestamp of the most recent echo received.
	LastEchoReceived time.Time

	// EchosSent is the total echo packets transmitted.
	EchosSent uint64

	// EchosReceived is the total echo packets received back.
	EchosReceived uint64

	// StateTransitions is the total state transitions.
	StateTransitions uint64
}

// Snapshot returns a read-only view of the echo session's current state.
func (es *EchoSession) Snapshot() EchoSessionSnapshot {
	return EchoSessionSnapshot{
		LocalDiscr:       es.localDiscr,
		PeerAddr:         es.peerAddr,
		LocalAddr:        es.localAddr,
		Interface:        es.ifName,
		State:            es.State(),
		LocalDiag:        es.LocalDiag(),
		TxInterval:       es.txInterval,
		DetectMultiplier: es.detectMult,
		DetectionTime:    es.DetectionTime(),
		LastStateChange:  es.LastStateChange(),
		LastEchoReceived: es.LastEchoReceived(),
		EchosSent:        es.echosSent.Load(),
		EchosReceived:    es.echosReceived.Load(),
		StateTransitions: es.stateTransitions.Load(),
	}
}
