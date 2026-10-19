package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

const maxBFDWireUint8 = math.MaxUint8

// Sentinel errors for the server package.
var (
	// ErrMissingIdentifier indicates no identifier was provided in a GetSession request.
	ErrMissingIdentifier = errors.New("identifier must be local_discriminator or peer_address")

	// ErrInvalidSessionType indicates an unrecognized session type in the request.
	ErrInvalidSessionType = errors.New("invalid session type")

	// ErrDedicatedTransportRequired indicates AddSession received a recognized
	// session family that needs its own transport-specific configuration path.
	ErrDedicatedTransportRequired = errors.New("session type requires dedicated transport configuration")

	// ErrDetectMultZero indicates a zero detect multiplier in the request.
	ErrDetectMultZero = errors.New("detect multiplier must be >= 1")

	// ErrDetectMultOverflow indicates the detect multiplier exceeds uint8 range.
	ErrDetectMultOverflow = errors.New("detect multiplier exceeds maximum 255")

	// ErrAPIAuthKeyMaterialMissing indicates the API request selected
	// authentication but did not provide the key material required by RFC 5880.
	ErrAPIAuthKeyMaterialMissing = errors.New("api auth key material is required")

	// ErrAPIAuthKeyMaterialUnexpected indicates the API request provided auth
	// key material without enabling authentication.
	ErrAPIAuthKeyMaterialUnexpected = errors.New("api auth key material requires auth_type")

	// ErrAPIAuthKeyIDOverflow indicates auth_key_id exceeds the BFD wire range.
	ErrAPIAuthKeyIDOverflow = errors.New("api auth_key_id exceeds maximum 255")
)

// SenderFactory creates a PacketSender for a BFD session. The factory
// abstracts socket creation so that the server can be tested without
// real network sockets.
//
// On session creation, the factory is called lazily after Manager accepts a
// new physical session. Manager then owns the matching CloseSender call.
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
}

// verify interface compliance at compile time.
var _ bfdv1connect.BfdServiceHandler = (*BFDServer)(nil)

// Register registers the BFD service on rpc. If sf is nil, a
// noopSenderFactory is used (for testing without sockets).
func Register(
	rpc *connect.Server,
	mgr *bfd.Manager,
	sf SenderFactory,
	logger *slog.Logger,
) {
	if sf == nil {
		sf = noopSenderFactory{}
	}
	srv := &BFDServer{
		manager:       mgr,
		senderFactory: sf,
		logger:        logger.With(slog.String("component", "server")),
	}
	bfdv1connect.RegisterBfdServiceHandler(rpc, srv)
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
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	sess, err := s.createSessionWithSenderLease(ctx, cfg)
	if err != nil {
		return nil, err
	}

	current, err := s.getSessionByDiscriminator(sess.LocalDiscriminator())
	if err != nil {
		return nil, err
	}

	return &bfdv1.AddSessionResponse{
		Session: current.GetSession(),
	}, nil
}

// createSessionWithSenderLease gives Manager lazy ownership of both the
// sender socket and its allocated source port.
func (s *BFDServer) createSessionWithSenderLease(
	ctx context.Context,
	cfg bfd.SessionConfig,
) (*bfd.Session, error) {
	multiHop := cfg.Type == bfd.SessionTypeMultiHop
	factory := func() (*bfd.SenderLease, error) {
		sender, srcPort, err := s.senderFactory.CreateSender(
			cfg.LocalAddr, multiHop, s.logger,
		)
		if err != nil {
			return nil, fmt.Errorf("create API session sender: %w", err)
		}
		return bfd.NewSenderLease(sender, func() error {
			if err := s.senderFactory.CloseSender(srcPort); err != nil {
				return fmt.Errorf("close API session sender port %d: %w", srcPort, err)
			}
			return nil
		}), nil
	}

	sess, err := s.manager.CreateSessionAs(ctx, cfg, factory, apiOwnerFromContext(ctx))
	if err != nil {
		return nil, mapManagerError(err, "add session")
	}

	return sess, nil
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

	owner := apiOwnerFromContext(ctx)
	if err := s.manager.DestroySessionAs(ctx, discr, owner); err != nil {
		if errors.Is(err, bfd.ErrSessionOwnerClaimNotFound) {
			s.logger.WarnContext(ctx, "DeleteSession refused: the caller holds no claim on the session",
				slog.Uint64("discriminator", uint64(discr)),
				slog.String("owner", owner.ID),
			)
		}
		return nil, mapManagerError(err, "delete session")
	}

	return &bfdv1.DeleteSessionResponse{}, nil
}

// ListSessions returns all active BFD sessions.
func (s *BFDServer) ListSessions(
	ctx context.Context,
	_ *bfdv1.ListSessionsRequest,
) (*bfdv1.ListSessionsResponse, error) {
	s.logger.InfoContext(ctx, "ListSessions called")

	snapshots, rev := s.manager.SessionsWithRevision()
	sessions := make([]*bfdv1.BfdSession, 0, len(snapshots))
	for _, snap := range snapshots {
		sessions = append(sessions, snapshotToProto(snap))
	}

	return &bfdv1.ListSessionsResponse{
		Sessions: sessions,
		Revision: rev,
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
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrMissingIdentifier.Error()).
			WithCause(ErrMissingIdentifier)
	}
}

// WatchSessionEvents streams BFD session state changes (server-side streaming).
func (s *BFDServer) WatchSessionEvents(
	ctx context.Context,
	req *bfdv1.WatchSessionEventsRequest,
	stream bfdv1connect.BfdServiceWatchSessionEventsServerStream,
) error {
	s.logger.InfoContext(ctx, "WatchSessionEvents called",
		slog.Bool("include_current", req.GetIncludeCurrent()),
	)

	// Subscribe before the snapshot: every change after the revision the
	// snapshot covers reaches this subscriber (M-08).
	ch, err := s.manager.SubscribeStateChanges(ctx)
	if err != nil {
		return fmt.Errorf("subscribe to session state changes: %w", err)
	}
	return s.watchLoop(ctx, ch, req.GetIncludeCurrent(), stream.Send)
}

// watchLoop streams state changes from ch. With includeCurrent, and after
// every gap whatever includeCurrent was, it first sends a snapshot of
// SESSION_ADDED events closed by SYNC_COMPLETE, then skips changes the
// snapshot already covers.
func (s *BFDServer) watchLoop(
	ctx context.Context,
	ch <-chan bfd.StateChange,
	includeCurrent bool,
	send func(*bfdv1.WatchSessionEventsResponse) error,
) error {
	covered := s.manager.Revision()
	if includeCurrent {
		rev, err := s.sendSnapshot(send)
		if err != nil {
			return err
		}
		covered = rev
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch session events: %w", ctx.Err())
		case sc, ok := <-ch:
			if !ok {
				return nil
			}
			var err error
			if covered, err = s.forwardStateChange(ctx, sc, covered, send); err != nil {
				return err
			}
		}
	}
}

// forwardStateChange sends one state change unless the snapshot at
// covered already includes it, or answers a gap marker with GAP and a
// fresh snapshot. It returns the revision now covered.
func (s *BFDServer) forwardStateChange(
	ctx context.Context,
	sc bfd.StateChange,
	covered uint64,
	send func(*bfdv1.WatchSessionEventsResponse) error,
) (uint64, error) {
	if sc.Gap {
		s.logger.WarnContext(ctx, "watch stream fell behind, resyncing",
			slog.Uint64("revision", sc.Revision))
		if err := send(&bfdv1.WatchSessionEventsResponse{
			Type:      bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP,
			Revision:  sc.Revision,
			Timestamp: timestamppb.Now(),
		}); err != nil {
			return covered, fmt.Errorf("send gap event: %w", err)
		}
		return s.sendSnapshot(send)
	}
	if sc.Revision <= covered {
		return covered, nil
	}
	if err := send(stateChangeToProto(sc)); err != nil {
		return covered, fmt.Errorf("send state change event: %w", err)
	}
	return covered, nil
}

// sendSnapshot sends every session as SESSION_ADDED and closes the
// snapshot with SYNC_COMPLETE. It returns the revision the snapshot covers.
func (s *BFDServer) sendSnapshot(send func(*bfdv1.WatchSessionEventsResponse) error) (uint64, error) {
	snapshots, rev := s.manager.SessionsWithRevision()
	now := timestamppb.Now()
	for _, snap := range snapshots {
		if err := send(&bfdv1.WatchSessionEventsResponse{
			Type:      bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED,
			Session:   snapshotToProto(snap),
			Timestamp: now,
			Revision:  rev,
		}); err != nil {
			return 0, fmt.Errorf("send current session event: %w", err)
		}
	}
	if err := send(&bfdv1.WatchSessionEventsResponse{
		Type:      bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE,
		Timestamp: now,
		Revision:  rev,
	}); err != nil {
		return 0, fmt.Errorf("send sync complete event: %w", err)
	}
	return rev, nil
}

// -------------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------------

// getSessionByDiscriminator looks up a session by its local discriminator
// and returns it as a GetSessionResponse.
func (s *BFDServer) getSessionByDiscriminator(discr uint32) (*bfdv1.GetSessionResponse, error) {
	snap, rev, ok := s.manager.SessionWithRevision(discr)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("session with discriminator %d: %w", discr, bfd.ErrSessionNotFound).Error())
	}
	return &bfdv1.GetSessionResponse{Session: snapshotToProto(snap), Revision: rev}, nil
}

// getSessionByPeerAddress iterates all sessions to find one matching the
// given peer address string.
func (s *BFDServer) getSessionByPeerAddress(peerAddrStr string) (*bfdv1.GetSessionResponse, error) {
	addr, err := netip.ParseAddr(peerAddrStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("parse peer address %q: %w", peerAddrStr, err).Error())
	}

	snapshots, rev := s.manager.SessionsWithRevision()
	var (
		match  bfd.SessionSnapshot
		discrs []string
	)
	for _, snap := range snapshots {
		if snap.PeerAddr == addr {
			match = snap
			discrs = append(discrs, strconv.FormatUint(uint64(snap.LocalDiscr), 10))
		}
	}
	switch len(discrs) {
	case 0:
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("session with peer address %s: %w", addr, bfd.ErrSessionNotFound).Error())
	case 1:
		return &bfdv1.GetSessionResponse{Session: snapshotToProto(match), Revision: rev}, nil
	default:
		// M-07: the peer address is not a session identity. The system
		// state, not the argument, precludes an answer (gRPC
		// doc/statuscodes.md, FAILED_PRECONDITION).
		slices.Sort(discrs)
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("peer address %s matches %d sessions; get by local discriminator: %s",
				addr, len(discrs), strings.Join(discrs, ", ")))
	}
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
	if !isGenericAddSessionType(sessType) {
		return bfd.SessionConfig{}, wrapError(req.GetType().String(), ErrDedicatedTransportRequired)
	}

	auth, authKeys, err := authConfigFromProto(req)
	if err != nil {
		return bfd.SessionConfig{}, err
	}

	desiredMinTx := durationFromProto(req.GetDesiredMinTxInterval())
	if rx := req.GetRequiredMinRxInterval(); rx != nil {
		if err := rx.CheckValid(); err != nil {
			return bfd.SessionConfig{}, fmt.Errorf("invalid required min RX interval: %w", err)
		}
	}
	requiredMinRx := durationFromProto(req.GetRequiredMinRxInterval())

	detectMult := req.GetDetectMultiplier()
	if detectMult == 0 {
		return bfd.SessionConfig{}, ErrDetectMultZero
	}
	if detectMult > maxBFDWireUint8 {
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
		Auth:                  auth,
		AuthKeys:              authKeys,
	}, nil
}

func authConfigFromProto(req *bfdv1.AddSessionRequest) (bfd.Authenticator, bfd.AuthKeyStore, error) {
	authType, err := authTypeFromProto(req.GetAuthType())
	if err != nil {
		return nil, nil, err
	}

	if authType == bfd.AuthTypeNone {
		if req.GetAuthKeyId() != 0 || len(req.GetAuthSecret()) != 0 {
			return nil, nil, ErrAPIAuthKeyMaterialUnexpected
		}
		return nil, nil, nil
	}

	if len(req.GetAuthSecret()) == 0 {
		return nil, nil, ErrAPIAuthKeyMaterialMissing
	}
	authKeyID := req.GetAuthKeyId()
	if authKeyID > maxBFDWireUint8 {
		return nil, nil, fmt.Errorf("auth_key_id %d: %w",
			authKeyID, ErrAPIAuthKeyIDOverflow)
	}

	auth, err := bfd.NewAuthenticator(authType)
	if err != nil {
		return nil, nil, fmt.Errorf("create API authenticator type %s: %w", authType, err)
	}

	keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{
		ID:     uint8(authKeyID),
		Type:   authType,
		Secret: req.GetAuthSecret(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("api auth key material: %w", err)
	}

	return auth, keys, nil
}

func authTypeFromProto(authType bfdv1.AuthenticationType) (bfd.AuthType, error) {
	switch authType {
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_UNSPECIFIED,
		bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE:
		return bfd.AuthTypeNone, nil
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_SIMPLE_PASSWORD:
		return bfd.AuthTypeSimplePassword, nil
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_MD5:
		return bfd.AuthTypeKeyedMD5, nil
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_MD5:
		return bfd.AuthTypeMeticulousKeyedMD5, nil
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_SHA1:
		return bfd.AuthTypeKeyedSHA1, nil
	case bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_SHA1:
		return bfd.AuthTypeMeticulousKeyedSHA1, nil
	default:
		return bfd.AuthTypeNone, fmt.Errorf("auth_type %s: %w",
			authType, bfd.ErrAuthTypeMismatch)
	}
}

// sessionTypeFromProto converts a proto SessionType to bfd.SessionType.
func sessionTypeFromProto(pt bfdv1.SessionType) (bfd.SessionType, error) {
	switch pt {
	case bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP:
		return bfd.SessionTypeSingleHop, nil
	case bfdv1.SessionType_SESSION_TYPE_MULTI_HOP:
		return bfd.SessionTypeMultiHop, nil
	case bfdv1.SessionType_SESSION_TYPE_ECHO:
		return bfd.SessionTypeEcho, nil
	case bfdv1.SessionType_SESSION_TYPE_MICRO_BFD:
		return bfd.SessionTypeMicroBFD, nil
	case bfdv1.SessionType_SESSION_TYPE_VXLAN:
		return bfd.SessionTypeVXLAN, nil
	case bfdv1.SessionType_SESSION_TYPE_GENEVE:
		return bfd.SessionTypeGeneve, nil
	default:
		return 0, wrapError(pt.String(), ErrInvalidSessionType)
	}
}

func isGenericAddSessionType(st bfd.SessionType) bool {
	return st == bfd.SessionTypeSingleHop || st == bfd.SessionTypeMultiHop
}

// durationFromProto converts a protobuf Duration to time.Duration.
// Returns 1 second as default if the proto duration is nil (RFC 5880 Section 6.8.1).
func durationFromProto(d *durationpb.Duration) time.Duration {
	if d == nil {
		return time.Second
	}
	return d.AsDuration()
}

// snapshotToProto converts an internal SessionSnapshot to a proto BfdSession message.
func snapshotToProto(snap bfd.SessionSnapshot) *bfdv1.BfdSession {
	pb := &bfdv1.BfdSession{
		PeerAddress:                   snap.PeerAddr.String(),
		LocalAddress:                  addrString(snap.LocalAddr),
		InterfaceName:                 snap.Interface,
		Type:                          sessionTypeToProto(snap.Type),
		LocalState:                    stateToProto(snap.State),
		RemoteState:                   observedRemoteState(snap.RemoteKnown, snap.RemoteState),
		LocalDiagnostic:               diagToProto(snap.LocalDiag),
		RemoteKnown:                   snap.RemoteKnown,
		RemoteDiagnostic:              observedRemoteDiag(snap.RemoteKnown, snap.RemoteDiag),
		LocalControlPlaneIndependent:  snap.LocalC,
		RemoteControlPlaneIndependent: snap.RemoteC,
		Cause:                         causeToProto(snap.Cause),
		ObservationVersion:            snap.ObservationVersion,
		AuthType:                      authTypeToProto(snap.AuthType),
		LocalDiscriminator:            snap.LocalDiscr,
		RemoteDiscriminator:           snap.RemoteDiscr,
		DesiredMinTxInterval:          durationpb.New(snap.DesiredMinTx),
		RequiredMinRxInterval:         durationpb.New(snap.RequiredMinRx),
		RemoteMinRxInterval:           durationpb.New(snap.RemoteMinRx),
		DetectMultiplier:              uint32(snap.DetectMultiplier),
		NegotiatedTxInterval:          durationpb.New(snap.NegotiatedTxInterval),
		DetectionTime:                 durationpb.New(snap.DetectionTime),
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

// addrString renders a, or "" when a is unset.
func addrString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

// stateChangeToProto converts an internal StateChange to a WatchSessionEventsResponse.
func stateChangeToProto(sc bfd.StateChange) *bfdv1.WatchSessionEventsResponse {
	eventType := bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE
	if sc.Deleted {
		eventType = bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED
	} else if sc.Added {
		eventType = bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED
	}
	var status *bfdv1.BfdSession
	if sc.Type == bfd.SessionTypeEcho {
		status = &bfdv1.BfdSession{
			PeerAddress:                   sc.PeerAddr.String(),
			LocalAddress:                  addrString(sc.LocalAddr),
			InterfaceName:                 sc.Interface,
			Type:                          sessionTypeToProto(sc.Type),
			LocalDiscriminator:            sc.LocalDiscr,
			LocalState:                    stateToProto(sc.NewState),
			LocalDiagnostic:               diagToProto(sc.Diag),
			RemoteKnown:                   sc.RemoteKnown,
			RemoteState:                   observedRemoteState(sc.RemoteKnown, sc.RemoteState),
			RemoteDiagnostic:              observedRemoteDiag(sc.RemoteKnown, sc.RemoteDiag),
			LocalControlPlaneIndependent:  sc.LocalC,
			RemoteControlPlaneIndependent: sc.RemoteC,
			Cause:                         causeToProto(sc.Cause),
			ObservationVersion:            sc.ObservationVersion,
		}
	} else {
		status = snapshotToProto(sc.Status)
	}
	return &bfdv1.WatchSessionEventsResponse{
		Type:          eventType,
		Session:       status,
		PreviousState: stateToProto(sc.OldState),
		Timestamp:     timestamppb.New(sc.Timestamp),
		Revision:      sc.Revision,
	}
}

func observedRemoteState(known bool, state bfd.State) bfdv1.SessionState {
	if !known {
		return bfdv1.SessionState_SESSION_STATE_UNSPECIFIED
	}
	return stateToProto(state)
}

func observedRemoteDiag(known bool, diag bfd.Diag) bfdv1.DiagnosticCode {
	if !known {
		return bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_UNSPECIFIED
	}
	return diagToProto(diag)
}

func causeToProto(cause bfd.Cause) bfdv1.ObservationCause {
	switch cause {
	case bfd.CauseRemoteAdminDown:
		return bfdv1.ObservationCause_OBSERVATION_CAUSE_REMOTE_ADMIN_DOWN
	case bfd.CauseRemoteDown:
		return bfdv1.ObservationCause_OBSERVATION_CAUSE_REMOTE_DOWN
	case bfd.CauseDetectionTimeout:
		return bfdv1.ObservationCause_OBSERVATION_CAUSE_DETECTION_TIMEOUT
	case bfd.CauseLocalAdmin:
		return bfdv1.ObservationCause_OBSERVATION_CAUSE_LOCAL_ADMIN
	case bfd.CausePathDown:
		return bfdv1.ObservationCause_OBSERVATION_CAUSE_PATH_DOWN
	case bfd.CauseConfiguration:
		return bfdv1.ObservationCause_OBSERVATION_CAUSE_CONFIGURATION
	default:
		return bfdv1.ObservationCause_OBSERVATION_CAUSE_UNSPECIFIED
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
	case bfd.SessionTypeEcho:
		return bfdv1.SessionType_SESSION_TYPE_ECHO
	case bfd.SessionTypeMicroBFD:
		return bfdv1.SessionType_SESSION_TYPE_MICRO_BFD
	case bfd.SessionTypeVXLAN:
		return bfdv1.SessionType_SESSION_TYPE_VXLAN
	case bfd.SessionTypeGeneve:
		return bfdv1.SessionType_SESSION_TYPE_GENEVE
	default:
		return bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED
	}
}

func authTypeToProto(authType bfd.AuthType) bfdv1.AuthenticationType {
	switch authType {
	case bfd.AuthTypeNone:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE
	case bfd.AuthTypeSimplePassword:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_SIMPLE_PASSWORD
	case bfd.AuthTypeKeyedMD5:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_MD5
	case bfd.AuthTypeMeticulousKeyedMD5:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_MD5
	case bfd.AuthTypeKeyedSHA1:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_SHA1
	case bfd.AuthTypeMeticulousKeyedSHA1:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_SHA1
	default:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_UNSPECIFIED
	}
}

// wrapError returns an error that wraps err with a context prefix.
func wrapError(context string, err error) error {
	return fmt.Errorf("%s: %w", context, err)
}

// mapManagerError translates bfd.Manager errors into appropriate ConnectRPC error codes.
func mapManagerError(err error, operation string) *connect.Error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, wrapError(operation, err).Error())
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, wrapError(operation, err).Error())
	case errors.Is(err, bfd.ErrDuplicateSession):
		return connect.NewError(connect.CodeAlreadyExists, wrapError(operation, err).Error())
	case errors.Is(err, bfd.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, wrapError(operation, err).Error())
	case errors.Is(err, bfd.ErrSessionOwnerClaimNotFound):
		return connect.NewError(connect.CodePermissionDenied, wrapError(operation, err).Error())
	case errors.Is(err, bfd.ErrInvalidPeerAddr),
		errors.Is(err, bfd.ErrInvalidDetectMult),
		errors.Is(err, bfd.ErrInvalidTxInterval),
		errors.Is(err, bfd.ErrInvalidSessionType),
		errors.Is(err, bfd.ErrInvalidSessionRole):
		return connect.NewError(connect.CodeInvalidArgument, wrapError(operation, err).Error())
	default:
		return connect.NewError(connect.CodeInternal, wrapError(operation, err).Error())
	}
}
