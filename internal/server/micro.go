package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
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
}

var _ bfdv1connect.MicroBFDServiceHandler = (*MicroBFDServer)(nil)

// NewMicroBFD builds a MicroBFDServer with the given Manager and logger.
func NewMicroBFD(
	mgr *bfd.Manager,
	logger *slog.Logger,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	srv := &MicroBFDServer{
		manager: mgr,
		logger:  logger.With(slog.String("component", "micro-bfd-server")),
	}
	return bfdv1connect.NewMicroBFDServiceHandler(srv, opts...)
}

// AddMicroBFDGroup creates a new RFC 7130 micro-BFD group.
func (s *MicroBFDServer) AddMicroBFDGroup(
	ctx context.Context,
	req *bfdv1.AddMicroBFDGroupRequest,
) (*bfdv1.AddMicroBFDGroupResponse, error) {
	cfg, err := microBFDConfigFromProto(req)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	s.logger.InfoContext(ctx, "AddMicroBFDGroup called",
		slog.String("lag", cfg.LAGInterface),
		slog.Int("members", len(cfg.MemberLinks)),
		slog.Int("min_active", cfg.MinActiveLinks),
	)

	if _, err := s.manager.CreateMicroBFDGroup(cfg); err != nil {
		return nil, mapManagerError(err, "add micro-bfd group")
	}

	for _, snap := range s.manager.MicroBFDGroups() {
		if snap.LAGInterface == cfg.LAGInterface {
			return &bfdv1.AddMicroBFDGroupResponse{
				Group: microGroupSnapshotToProto(snap, cfg),
			}, nil
		}
	}

	return nil, connect.NewError(connect.CodeInternal,
		fmt.Errorf("micro-bfd group %q created but not visible: %w",
			cfg.LAGInterface, bfd.ErrMicroBFDGroupNotFound))
}

// DeleteMicroBFDGroup removes a micro-BFD group by LAG interface name.
func (s *MicroBFDServer) DeleteMicroBFDGroup(
	ctx context.Context,
	req *bfdv1.DeleteMicroBFDGroupRequest,
) (*bfdv1.DeleteMicroBFDGroupResponse, error) {
	lag := req.GetLagInterface()
	if lag == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrMicroLAGRequired)
	}

	s.logger.InfoContext(ctx, "DeleteMicroBFDGroup called",
		slog.String("lag", lag),
	)

	if err := s.manager.DestroyMicroBFDGroup(lag); err != nil {
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
		out.Groups = append(out.Groups, microGroupSnapshotToProto(snap, bfd.MicroBFDConfig{}))
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
	if mult > 255 {
		return bfd.MicroBFDConfig{}, fmt.Errorf("value %d: %w", mult, ErrDetectMultOverflow)
	}

	minActive := int(req.GetMinActiveLinks())
	if minActive < 1 || minActive > len(req.GetMemberLinks()) {
		return bfd.MicroBFDConfig{}, fmt.Errorf(
			"min_active_links=%d, members=%d: %w",
			minActive, len(req.GetMemberLinks()), ErrMicroMinActiveRange)
	}

	return bfd.MicroBFDConfig{
		LAGInterface:          req.GetLagInterface(),
		MemberLinks:           req.GetMemberLinks(),
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		DesiredMinTxInterval:  durationFromProto(req.GetDesiredMinTxInterval()),
		RequiredMinRxInterval: durationFromProto(req.GetRequiredMinRxInterval()),
		DetectMultiplier:      uint8(mult),
		MinActiveLinks:        minActive,
	}, nil
}

func microGroupSnapshotToProto(snap bfd.MicroBFDGroupSnapshot, cfg bfd.MicroBFDConfig) *bfdv1.MicroBFDGroup {
	out := &bfdv1.MicroBFDGroup{
		LagInterface: snap.LAGInterface,
		PeerAddress:  snap.PeerAddr.String(),
		LocalAddress: snap.LocalAddr.String(),
		AggregateUp:  snap.AggregateUp,
	}
	if snap.UpCount >= 0 {
		out.UpMemberCount = uint32(snap.UpCount) //nolint:gosec // G115: snap.UpCount is bounded by len(MemberLinks), guarded above.
	}
	if snap.MinActiveLinks >= 0 {
		out.MinActiveLinks = uint32(snap.MinActiveLinks) //nolint:gosec // G115: validated >= 1 in microBFDConfigFromProto.
	}
	if cfg.LAGInterface != "" {
		out.MemberLinks = cfg.MemberLinks
		out.DesiredMinTxInterval = durationpb.New(cfg.DesiredMinTxInterval)
		out.RequiredMinRxInterval = durationpb.New(cfg.RequiredMinRxInterval)
		out.DetectMultiplier = uint32(cfg.DetectMultiplier)
	} else {
		out.MemberLinks = make([]string, 0, len(snap.Members))
		for _, m := range snap.Members {
			out.MemberLinks = append(out.MemberLinks, m.Interface)
		}
	}
	return out
}
