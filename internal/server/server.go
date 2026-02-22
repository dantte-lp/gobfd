package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// Sentinel errors for the server package.
var (
	// ErrMissingIdentifier indicates no identifier was provided in a GetSession request.
	ErrMissingIdentifier = errors.New("identifier must be local_discriminator or peer_address")

	// ErrInvalidSessionType indicates an unrecognized session type in the request.
	ErrInvalidSessionType = errors.New("invalid session type")

	// ErrDetectMultZero indicates a zero detect multiplier in the request.
	ErrDetectMultZero = errors.New("detect multiplier must be >= 1")

	// ErrDetectMultOverflow indicates the detect multiplier exceeds uint8 range.
	ErrDetectMultOverflow = errors.New("detect multiplier exceeds maximum 255")
)

// SenderFactory creates a PacketSender for a BFD session. The factory
// abstracts socket creation so that the server can be tested without
// real network sockets.
//
// On session creation, the factory is called with the session's local
// address and session type. On session destruction, CloseSender is
// called to release resources (close socket, release port).
type SenderFactory interface {
	// CreateSender allocates a source port and creates a PacketSender
	// bound to localAddr. Returns the sender and allocated port.
	CreateSender(
		localAddr netip.Addr,
		multiHop bool,
		logger *slog.Logger,
	) (bfd.PacketSender, uint16, error)

	// CloseSender releases resources for the sender identified by
	// the allocated source port (close socket, release port).
	CloseSender(srcPort uint16) error
}

// noopSender is a PacketSender that discards all packets.
// Used as a fallback when no SenderFactory is configured.
type noopSender struct{}

func (noopSender) SendPacket(_ context.Context, _ []byte, _ netip.Addr) error {
	return nil
}

// noopSenderFactory returns noopSender instances (for tests).
type noopSenderFactory struct{}

func (noopSenderFactory) CreateSender(
	_ netip.Addr,
	_ bool,
	_ *slog.Logger,
) (bfd.PacketSender, uint16, error) {
	return noopSender{}, 0, nil
}

func (noopSenderFactory) CloseSender(_ uint16) error { return nil }

// BFDServer implements bfdv1connect.BfdServiceHandler.
//
// Each RPC delegates to the session Manager for actual BFD operations.
// The server is a thin adapter between gRPC API and internal domain.
type BFDServer struct {
	manager       *bfd.Manager
	senderFactory SenderFactory
	logger        *slog.Logger

	// senderPorts tracks allocated source ports per session discriminator
	// for cleanup on DestroySession.
	senderPorts   map[uint32]uint16
	senderPortsMu sync.Mutex
}

// verify interface compliance at compile time.
var _ bfdv1connect.BfdServiceHandler = (*BFDServer)(nil)

// New creates a new BFDServer and returns the HTTP handler and path.
// If sf is nil, a noopSenderFactory is used (for testing without sockets).
func New(
	mgr *bfd.Manager,
	sf SenderFactory,
	logger *slog.Logger,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if sf == nil {
		sf = noopSenderFactory{}
	}
	srv := &BFDServer{
		manager:       mgr,
		senderFactory: sf,
		senderPorts:   make(map[uint32]uint16),
		logger:        logger.With(slog.String("component", "server")),
	}
	return bfdv1connect.NewBfdServiceHandler(srv, opts...)
}

// AddSession creates a new BFD session with the given parameters.
func (s *BFDServer) AddSession(
	ctx context.Context,
	req *bfdv1.AddSessionRequest,
) (*bfdv1.AddSessionResponse, error) {
	s.logger.InfoContext(ctx, "AddSession called",
		slog.String("peer", req.GetPeerAddress()),
		slog.String("local", req.GetLocalAddress()),
	)

	cfg, err := sessionConfigFromProto(req)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	sender, srcPort, sess, err := s.createSessionWithSender(ctx, cfg)
	if err != nil {
		return nil, err
	}

	_ = sender // sender is owned by session now
	s.trackSenderPort(sess.LocalDiscriminator(), srcPort)

	snap := snapshotFromSession(sess, cfg)

	return &bfdv1.AddSessionResponse{
		Session: snapshotToProto(snap),
	}, nil
}

// createSessionWithSender creates the sender and session together.
// On session creation failure, the sender is cleaned up.
func (s *BFDServer) createSessionWithSender(
	ctx context.Context,
	cfg bfd.SessionConfig,
) (bfd.PacketSender, uint16, *bfd.Session, error) {
	multiHop := cfg.Type == bfd.SessionTypeMultiHop

	sender, srcPort, err := s.senderFactory.CreateSender(
		cfg.LocalAddr, multiHop, s.logger,
	)
	if err != nil {
		return nil, 0, nil, mapManagerError(
			fmt.Errorf("create sender: %w", err), "add session",
		)
	}

	sess, err := s.manager.CreateSession(ctx, cfg, sender)
	if err != nil {
		if closeErr := s.senderFactory.CloseSender(srcPort); closeErr != nil {
			s.logger.Warn("failed to close sender after session creation failure",
				slog.String("error", closeErr.Error()),
			)
		}
		return nil, 0, nil, mapManagerError(err, "add session")
	}

	return sender, srcPort, sess, nil
}

// trackSenderPort records the source port for cleanup on DestroySession.
func (s *BFDServer) trackSenderPort(discr uint32, srcPort uint16) {
	s.senderPortsMu.Lock()
	s.senderPorts[discr] = srcPort
	s.senderPortsMu.Unlock()
}

// DeleteSession removes a BFD session by its local discriminator.
func (s *BFDServer) DeleteSession(
	ctx context.Context,
	req *bfdv1.DeleteSessionRequest,
) (*bfdv1.DeleteSessionResponse, error) {
	discr := req.GetLocalDiscriminator()
	s.logger.InfoContext(ctx, "DeleteSession called",
		slog.Uint64("discriminator", uint64(discr)),
	)

	if err := s.manager.DestroySession(ctx, discr); err != nil {
		return nil, mapManagerError(err, "delete session")
	}

	s.cleanupSender(discr)

	return &bfdv1.DeleteSessionResponse{}, nil
}

// cleanupSender closes the sender and releases the source port for a
// destroyed session.
func (s *BFDServer) cleanupSender(discr uint32) {
	s.senderPortsMu.Lock()
	srcPort, ok := s.senderPorts[discr]
	if ok {
		delete(s.senderPorts, discr)
	}
	s.senderPortsMu.Unlock()

	if ok {
		if err := s.senderFactory.CloseSender(srcPort); err != nil {
			s.logger.Warn("failed to close sender",
				slog.Uint64("discriminator", uint64(discr)),
				slog.String("error", err.Error()),
			)
		}
	}
}

// ListSessions returns all active BFD sessions.
func (s *BFDServer) ListSessions(
	ctx context.Context,
	_ *bfdv1.ListSessionsRequest,
) (*bfdv1.ListSessionsResponse, error) {
	s.logger.InfoContext(ctx, "ListSessions called")

	snapshots := s.manager.Sessions()
	sessions := make([]*bfdv1.BfdSession, 0, len(snapshots))
	for _, snap := range snapshots {
		sessions = append(sessions, snapshotToProto(snap))
	}

	return &bfdv1.ListSessionsResponse{
		Sessions: sessions,
	}, nil
}

// GetSession returns a single session by discriminator or peer address.
func (s *BFDServer) GetSession(ctx context.Context, req *bfdv1.GetSessionRequest) (*bfdv1.GetSessionResponse, error) {
	s.logger.InfoContext(ctx, "GetSession called")

	switch id := req.GetIdentifier().(type) {
	case *bfdv1.GetSessionRequest_LocalDiscriminator:
		return s.getSessionByDiscriminator(id.LocalDiscriminator)
	case *bfdv1.GetSessionRequest_PeerAddress:
		return s.getSessionByPeerAddress(id.PeerAddress)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrMissingIdentifier)
	}
}

// WatchSessionEvents streams BFD session state changes (server-side streaming).
func (s *BFDServer) WatchSessionEvents(
	ctx context.Context,
	req *bfdv1.WatchSessionEventsRequest,
	stream *connect.ServerStream[bfdv1.WatchSessionEventsResponse],
) error {
	s.logger.InfoContext(ctx, "WatchSessionEvents called",
		slog.Bool("include_current", req.GetIncludeCurrent()),
	)

	// If requested, send current sessions as SESSION_ADDED events first.
	if req.GetIncludeCurrent() {
		snapshots := s.manager.Sessions()
		for _, snap := range snapshots {
			resp := &bfdv1.WatchSessionEventsResponse{
				Type:      bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED,
				Session:   snapshotToProto(snap),
				Timestamp: timestamppb.Now(),
			}
			if err := stream.Send(resp); err != nil {
				return fmt.Errorf("send current session event: %w", err)
			}
		}
	}

	// Stream state changes from the manager's aggregated channel.
	ch := s.manager.StateChanges()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch session events: %w", ctx.Err())
		case sc, ok := <-ch:
			if !ok {
				return nil
			}
			resp := stateChangeToProto(sc)
			if err := stream.Send(resp); err != nil {
				return fmt.Errorf("send state change event: %w", err)
			}
		}
	}
}

// -------------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------------

// getSessionByDiscriminator looks up a session by its local discriminator
// and returns it as a GetSessionResponse.
func (s *BFDServer) getSessionByDiscriminator(discr uint32) (*bfdv1.GetSessionResponse, error) {
	sess, ok := s.manager.LookupByDiscriminator(discr)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("session with discriminator %d: %w", discr, bfd.ErrSessionNotFound))
	}

	snap := bfd.SessionSnapshot{
		LocalDiscr:           sess.LocalDiscriminator(),
		RemoteDiscr:          sess.RemoteDiscriminator(),
		PeerAddr:             sess.PeerAddr(),
		LocalAddr:            sess.LocalAddr(),
		Interface:            sess.Interface(),
		Type:                 sess.Type(),
		State:                sess.State(),
		RemoteState:          sess.RemoteState(),
		LocalDiag:            sess.LocalDiag(),
		DesiredMinTx:         sess.DesiredMinTxInterval(),
		RequiredMinRx:        sess.RequiredMinRxInterval(),
		DetectMultiplier:     sess.DetectMultiplier(),
		NegotiatedTxInterval: sess.NegotiatedTxInterval(),
		DetectionTime:        sess.DetectionTime(),
		LastStateChange:      sess.LastStateChange(),
		LastPacketReceived:   sess.LastPacketReceived(),
		Counters: bfd.SessionCounters{
			PacketsSent:      sess.PacketsSent(),
			PacketsReceived:  sess.PacketsReceived(),
			StateTransitions: sess.StateTransitions(),
		},
	}

	return &bfdv1.GetSessionResponse{
		Session: snapshotToProto(snap),
	}, nil
}

// getSessionByPeerAddress iterates all sessions to find one matching the
// given peer address string.
func (s *BFDServer) getSessionByPeerAddress(peerAddrStr string) (*bfdv1.GetSessionResponse, error) {
	addr, err := netip.ParseAddr(peerAddrStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("parse peer address %q: %w", peerAddrStr, err))
	}

	snapshots := s.manager.Sessions()
	for _, snap := range snapshots {
		if snap.PeerAddr == addr {
			return &bfdv1.GetSessionResponse{
				Session: snapshotToProto(snap),
			}, nil
		}
	}

	return nil, connect.NewError(connect.CodeNotFound,
		fmt.Errorf("session with peer address %s: %w", addr, bfd.ErrSessionNotFound))
}

// sessionConfigFromProto converts an AddSessionRequest into a bfd.SessionConfig.
// Returns an error with details for any invalid field.
func sessionConfigFromProto(req *bfdv1.AddSessionRequest) (bfd.SessionConfig, error) {
	peerAddr, err := netip.ParseAddr(req.GetPeerAddress())
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("parse peer address %q: %w", req.GetPeerAddress(), err)
	}

	var localAddr netip.Addr
	if la := req.GetLocalAddress(); la != "" {
		localAddr, err = netip.ParseAddr(la)
		if err != nil {
			return bfd.SessionConfig{}, fmt.Errorf("parse local address %q: %w", la, err)
		}
	}

	sessType, err := sessionTypeFromProto(req.GetType())
	if err != nil {
		return bfd.SessionConfig{}, err
	}

	desiredMinTx := durationFromProto(req.GetDesiredMinTxInterval())
	requiredMinRx := durationFromProto(req.GetRequiredMinRxInterval())

	detectMult := req.GetDetectMultiplier()
	if detectMult == 0 {
		return bfd.SessionConfig{}, ErrDetectMultZero
	}
	if detectMult > 255 {
		return bfd.SessionConfig{}, fmt.Errorf("value %d: %w", detectMult, ErrDetectMultOverflow)
	}

	return bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Interface:             req.GetInterfaceName(),
		Type:                  sessType,
		Role:                  bfd.RoleActive, // Default to active; passive requires explicit config.
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
	}, nil
}

// sessionTypeFromProto converts a proto SessionType to bfd.SessionType.
func sessionTypeFromProto(pt bfdv1.SessionType) (bfd.SessionType, error) {
	switch pt {
	case bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP:
		return bfd.SessionTypeSingleHop, nil
	case bfdv1.SessionType_SESSION_TYPE_MULTI_HOP:
		return bfd.SessionTypeMultiHop, nil
	default:
		return 0, fmt.Errorf("%s: %w", pt, ErrInvalidSessionType)
	}
}

// durationFromProto converts a protobuf Duration to time.Duration.
// Returns 1 second as default if the proto duration is nil (RFC 5880 Section 6.8.1).
func durationFromProto(d *durationpb.Duration) time.Duration {
	if d == nil {
		return time.Second
	}
	return d.AsDuration()
}

// snapshotFromSession creates a SessionSnapshot from a live Session and its config.
// Used immediately after CreateSession when we have both the session pointer and config.
func snapshotFromSession(sess *bfd.Session, cfg bfd.SessionConfig) bfd.SessionSnapshot {
	return bfd.SessionSnapshot{
		LocalDiscr:           sess.LocalDiscriminator(),
		RemoteDiscr:          sess.RemoteDiscriminator(),
		PeerAddr:             sess.PeerAddr(),
		LocalAddr:            sess.LocalAddr(),
		Interface:            sess.Interface(),
		Type:                 cfg.Type,
		State:                sess.State(),
		RemoteState:          sess.RemoteState(),
		LocalDiag:            sess.LocalDiag(),
		DesiredMinTx:         cfg.DesiredMinTxInterval,
		RequiredMinRx:        cfg.RequiredMinRxInterval,
		DetectMultiplier:     cfg.DetectMultiplier,
		NegotiatedTxInterval: sess.NegotiatedTxInterval(),
		DetectionTime:        sess.DetectionTime(),
		LastStateChange:      sess.LastStateChange(),
		LastPacketReceived:   sess.LastPacketReceived(),
		Counters: bfd.SessionCounters{
			PacketsSent:      sess.PacketsSent(),
			PacketsReceived:  sess.PacketsReceived(),
			StateTransitions: sess.StateTransitions(),
		},
	}
}

// snapshotToProto converts an internal SessionSnapshot to a proto BfdSession message.
func snapshotToProto(snap bfd.SessionSnapshot) *bfdv1.BfdSession {
	pb := &bfdv1.BfdSession{
		PeerAddress:           snap.PeerAddr.String(),
		LocalAddress:          snap.LocalAddr.String(),
		InterfaceName:         snap.Interface,
		Type:                  sessionTypeToProto(snap.Type),
		LocalState:            stateToProto(snap.State),
		RemoteState:           stateToProto(snap.RemoteState),
		LocalDiagnostic:       diagToProto(snap.LocalDiag),
		LocalDiscriminator:    snap.LocalDiscr,
		RemoteDiscriminator:   snap.RemoteDiscr,
		DesiredMinTxInterval:  durationpb.New(snap.DesiredMinTx),
		RequiredMinRxInterval: durationpb.New(snap.RequiredMinRx),
		DetectMultiplier:      uint32(snap.DetectMultiplier),
		NegotiatedTxInterval:  durationpb.New(snap.NegotiatedTxInterval),
		DetectionTime:         durationpb.New(snap.DetectionTime),
		Counters: &bfdv1.SessionCounters{
			PacketsSent:      snap.Counters.PacketsSent,
			PacketsReceived:  snap.Counters.PacketsReceived,
			StateTransitions: snap.Counters.StateTransitions,
		},
	}

	if !snap.LastStateChange.IsZero() {
		pb.LastStateChange = timestamppb.New(snap.LastStateChange)
	}
	if !snap.LastPacketReceived.IsZero() {
		pb.LastPacketReceived = timestamppb.New(snap.LastPacketReceived)
	}

	return pb
}

// stateChangeToProto converts an internal StateChange to a WatchSessionEventsResponse.
func stateChangeToProto(sc bfd.StateChange) *bfdv1.WatchSessionEventsResponse {
	return &bfdv1.WatchSessionEventsResponse{
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE,
		Session: &bfdv1.BfdSession{
			PeerAddress:        sc.PeerAddr.String(),
			LocalDiscriminator: sc.LocalDiscr,
			LocalState:         stateToProto(sc.NewState),
			LocalDiagnostic:    diagToProto(sc.Diag),
		},
		PreviousState: stateToProto(sc.OldState),
		Timestamp:     timestamppb.New(sc.Timestamp),
	}
}

// stateToProto maps internal bfd.State to proto SessionState.
// RFC 5880 Section 4.1: AdminDown=0, Down=1, Init=2, Up=3 (wire format).
// Proto enum: ADMIN_DOWN=1, DOWN=2, INIT=3, UP=4 (shifted by 1 to reserve 0 for UNSPECIFIED).
func stateToProto(s bfd.State) bfdv1.SessionState {
	switch s {
	case bfd.StateAdminDown:
		return bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN
	case bfd.StateDown:
		return bfdv1.SessionState_SESSION_STATE_DOWN
	case bfd.StateInit:
		return bfdv1.SessionState_SESSION_STATE_INIT
	case bfd.StateUp:
		return bfdv1.SessionState_SESSION_STATE_UP
	default:
		return bfdv1.SessionState_SESSION_STATE_UNSPECIFIED
	}
}

// diagToProto maps internal bfd.Diag to proto DiagnosticCode.
// RFC 5880 Section 4.1 diag codes are shifted by 1 in proto to reserve 0 for UNSPECIFIED.
func diagToProto(d bfd.Diag) bfdv1.DiagnosticCode {
	switch d {
	case bfd.DiagNone:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE
	case bfd.DiagControlTimeExpired:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_CONTROL_TIME_EXPIRED
	case bfd.DiagEchoFailed:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ECHO_FAILED
	case bfd.DiagNeighborDown:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NEIGHBOR_DOWN
	case bfd.DiagForwardingPlaneReset:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_FORWARDING_RESET
	case bfd.DiagPathDown:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_PATH_DOWN
	case bfd.DiagConcatPathDown:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_CONCAT_PATH_DOWN
	case bfd.DiagAdminDown:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ADMIN_DOWN
	case bfd.DiagReverseConcatPathDown:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_REVERSE_CONCAT_PATH_DOWN
	default:
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_UNSPECIFIED
	}
}

// sessionTypeToProto maps internal bfd.SessionType to proto SessionType.
func sessionTypeToProto(st bfd.SessionType) bfdv1.SessionType {
	switch st {
	case bfd.SessionTypeSingleHop:
		return bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP
	case bfd.SessionTypeMultiHop:
		return bfdv1.SessionType_SESSION_TYPE_MULTI_HOP
	default:
		return bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED
	}
}

// mapManagerError translates bfd.Manager errors into appropriate ConnectRPC error codes.
func mapManagerError(err error, operation string) *connect.Error {
	switch {
	case errors.Is(err, bfd.ErrDuplicateSession):
		return connect.NewError(connect.CodeAlreadyExists,
			fmt.Errorf("%s: %w", operation, err))
	case errors.Is(err, bfd.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound,
			fmt.Errorf("%s: %w", operation, err))
	case errors.Is(err, bfd.ErrInvalidPeerAddr),
		errors.Is(err, bfd.ErrInvalidDetectMult),
		errors.Is(err, bfd.ErrInvalidTxInterval),
		errors.Is(err, bfd.ErrInvalidSessionType),
		errors.Is(err, bfd.ErrInvalidSessionRole):
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("%s: %w", operation, err))
	default:
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("%s: %w", operation, err))
	}
}
