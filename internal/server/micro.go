package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/netip"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// Sentinel errors for the MicroBFDService handler.
var (
	// ErrMicroLAGRequired indicates the request omitted lag_interface.
	ErrMicroLAGRequired = errors.New("lag_interface is required")

	// ErrMicroMembersRequired indicates an empty member_links list.
	ErrMicroMembersRequired = errors.New("member_links must contain at least one entry")

	// ErrMicroMinActiveRange indicates min_active_links is outside
	// [1, len(member_links)].
	ErrMicroMinActiveRange = errors.New("min_active_links must satisfy 1 <= n <= len(member_links)")

	// ErrMicroDetectMultZero indicates a zero detect_multiplier on micro-BFD.
	ErrMicroDetectMultZero = errors.New("detect_multiplier must be >= 1 for micro-BFD")

	errMicroSnapshotCountOverflow = errors.New("micro-bfd snapshot count exceeds uint32")
)

// MicroBFDServer implements bfdv1connect.MicroBFDServiceHandler.
//
// Each RPC delegates to bfd.Manager for group lifecycle. Per-member
// session creation remains a separate concern: the operator is
// responsible for binding sessions of SessionTypeMicroBFD via
// BfdService.AddSession or via YAML config.
type MicroBFDServer struct {
	manager *bfd.Manager
	logger  *slog.Logger
	enabled bool
}

var _ bfdv1connect.MicroBFDServiceHandler = (*MicroBFDServer)(nil)

// RegisterMicroBFD registers the RFC 7130 Micro-BFD service on rpc.
func RegisterMicroBFD(
	rpc *connect.Server,
	mgr *bfd.Manager,
	enabled bool,
	logger *slog.Logger,
) {
	srv := &MicroBFDServer{
		manager: mgr,
		enabled: enabled,
		logger:  logger.With(slog.String("component", "micro-bfd-server")),
	}
	bfdv1connect.RegisterMicroBFDServiceHandler(rpc, srv)
}

// AddMicroBFDGroup creates a new RFC 7130 micro-BFD group.
func (s *MicroBFDServer) AddMicroBFDGroup(
	ctx context.Context,
	req *bfdv1.AddMicroBFDGroupRequest,
) (*bfdv1.AddMicroBFDGroupResponse, error) {
	if !s.enabled {
		err := config.ErrMicroBFDPreviewDisabled
		return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	}
	cfg, err := microBFDConfigFromProto(req)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	s.logger.InfoContext(ctx, "AddMicroBFDGroup called",
		slog.String("lag", cfg.LAGInterface),
		slog.Int("members", len(cfg.MemberLinks)),
		slog.Int("min_active", cfg.MinActiveLinks),
	)

	created, err := s.manager.CreateMicroBFDGroupAs(cfg, apiOwnerFromContext(ctx))
	if err != nil {
		return nil, mapManagerError(err, "add micro-bfd group")
	}

	group, err := microGroupSnapshotToProto(created.Snapshot())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &bfdv1.AddMicroBFDGroupResponse{Group: group}, nil
}

// DeleteMicroBFDGroup removes a micro-BFD group by LAG interface name.
func (s *MicroBFDServer) DeleteMicroBFDGroup(
	ctx context.Context,
	req *bfdv1.DeleteMicroBFDGroupRequest,
) (*bfdv1.DeleteMicroBFDGroupResponse, error) {
	lag := req.GetLagInterface()
	if lag == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrMicroLAGRequired.Error()).WithCause(ErrMicroLAGRequired)
	}

	s.logger.InfoContext(ctx, "DeleteMicroBFDGroup called",
		slog.String("lag", lag),
	)

	owner := apiOwnerFromContext(ctx)
	if err := s.manager.DestroyMicroBFDGroupAs(lag, owner); err != nil {
		if errors.Is(err, bfd.ErrMicroBFDGroupOwnerClaimNotFound) {
			s.logger.WarnContext(ctx, "DeleteMicroBFDGroup refused: the caller holds no claim on the group",
				slog.String("lag", lag),
				slog.String("owner", owner.ID),
			)
		}
		return nil, mapManagerError(err, "delete micro-bfd group")
	}

	return &bfdv1.DeleteMicroBFDGroupResponse{}, nil
}

// ListMicroBFDGroups returns all active micro-BFD groups.
func (s *MicroBFDServer) ListMicroBFDGroups(
	_ context.Context,
	_ *bfdv1.ListMicroBFDGroupsRequest,
) (*bfdv1.ListMicroBFDGroupsResponse, error) {
	snaps := s.manager.MicroBFDGroups()
	out := &bfdv1.ListMicroBFDGroupsResponse{
		Groups: make([]*bfdv1.MicroBFDGroup, 0, len(snaps)),
	}
	for _, snap := range snaps {
		group, err := microGroupSnapshotToProto(snap)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		out.Groups = append(out.Groups, group)
	}
	return out, nil
}

func microBFDConfigFromProto(req *bfdv1.AddMicroBFDGroupRequest) (bfd.MicroBFDConfig, error) {
	if req.GetLagInterface() == "" {
		return bfd.MicroBFDConfig{}, ErrMicroLAGRequired
	}
	if len(req.GetMemberLinks()) == 0 {
		return bfd.MicroBFDConfig{}, ErrMicroMembersRequired
	}

	peerAddr, err := netip.ParseAddr(req.GetPeerAddress())
	if err != nil {
		return bfd.MicroBFDConfig{}, fmt.Errorf("parse peer address %q: %w", req.GetPeerAddress(), err)
	}

	var localAddr netip.Addr
	if la := req.GetLocalAddress(); la != "" {
		localAddr, err = netip.ParseAddr(la)
		if err != nil {
			return bfd.MicroBFDConfig{}, fmt.Errorf("parse local address %q: %w", la, err)
		}
	}

	mult := req.GetDetectMultiplier()
	if mult == 0 {
		return bfd.MicroBFDConfig{}, ErrMicroDetectMultZero
	}
	if mult > maxBFDWireUint8 {
		return bfd.MicroBFDConfig{}, fmt.Errorf("value %d: %w", mult, ErrDetectMultOverflow)
	}

	minActive := int(req.GetMinActiveLinks())
	if minActive < 1 || minActive > len(req.GetMemberLinks()) {
		return bfd.MicroBFDConfig{}, fmt.Errorf(
			"min_active_links=%d, members=%d: %w",
			minActive, len(req.GetMemberLinks()), ErrMicroMinActiveRange)
	}

	desiredMinTx, err := durationFromProto(req.GetDesiredMinTxInterval())
	if err != nil {
		return bfd.MicroBFDConfig{}, fmt.Errorf("invalid desired min TX interval: %w", err)
	}
	requiredMinRx, err := durationFromProto(req.GetRequiredMinRxInterval())
	if err != nil {
		return bfd.MicroBFDConfig{}, fmt.Errorf("invalid required min RX interval: %w", err)
	}

	return bfd.MicroBFDConfig{
		LAGInterface:          req.GetLagInterface(),
		MemberLinks:           req.GetMemberLinks(),
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(mult),
		MinActiveLinks:        minActive,
	}, nil
}

func microGroupSnapshotToProto(snap bfd.MicroBFDGroupSnapshot) (*bfdv1.MicroBFDGroup, error) {
	out := &bfdv1.MicroBFDGroup{
		LagInterface:          snap.LAGInterface,
		PeerAddress:           snap.PeerAddr.String(),
		LocalAddress:          snap.LocalAddr.String(),
		AggregateUp:           snap.AggregateUp,
		DesiredMinTxInterval:  durationpb.New(snap.DesiredMinTxInterval),
		RequiredMinRxInterval: durationpb.New(snap.RequiredMinRxInterval),
		DetectMultiplier:      uint32(snap.DetectMultiplier),
		MemberLinks:           make([]string, 0, len(snap.Members)),
	}
	for _, member := range snap.Members {
		out.MemberLinks = append(out.MemberLinks, member.Interface)
	}
	if snap.UpCount >= 0 {
		upCount, err := microSnapshotCountToUint32("up_member_count", snap.UpCount)
		if err != nil {
			return nil, err
		}
		out.UpMemberCount = upCount
	}
	if snap.MinActiveLinks >= 0 {
		minActiveLinks, err := microSnapshotCountToUint32("min_active_links", snap.MinActiveLinks)
		if err != nil {
			return nil, err
		}
		out.MinActiveLinks = minActiveLinks
	}
	return out, nil
}

func microSnapshotCountToUint32(name string, value int) (uint32, error) {
	if value < 0 || uint64(value) > math.MaxUint32 {
		return 0, fmt.Errorf("%s=%d: %w", name, value, errMicroSnapshotCountOverflow)
	}
	return uint32(value), nil // #nosec G115 -- the explicit uint64 bound above proves the conversion fits.
}
