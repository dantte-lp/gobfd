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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// Sentinel errors for the EchoService handler.
var (
	// ErrEchoTxIntervalRequired indicates the request omitted tx_interval.
	ErrEchoTxIntervalRequired = errors.New("tx_interval is required for echo sessions")

	// ErrEchoDetectMultZero indicates a zero detect_multiplier on echo.
	ErrEchoDetectMultZero = errors.New("detect_multiplier must be >= 1 for echo sessions")
)

// EchoServer implements bfdv1connect.EchoServiceHandler.
//
// Each RPC delegates to the bfd.Manager for actual lifecycle and
// reuses the same SenderFactory used by BfdService. RFC 9747 Section
// 3.3: TxInterval is locally provisioned, not negotiated, so the
// handler validates only TxInterval and DetectMultiplier and never
// applies remote-side reconciliation.
type EchoServer struct {
	manager       *bfd.Manager
	senderFactory SenderFactory
	logger        *slog.Logger
}

var _ bfdv1connect.EchoServiceHandler = (*EchoServer)(nil)

// NewEcho builds an EchoServer with the given Manager, SenderFactory
// and logger. If sf is nil, a noopSenderFactory is used (test mode).
func NewEcho(
	mgr *bfd.Manager,
	sf SenderFactory,
	logger *slog.Logger,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if sf == nil {
		sf = noopSenderFactory{}
	}
	srv := &EchoServer{
		manager:       mgr,
		senderFactory: sf,
		logger:        logger.With(slog.String("component", "echo-server")),
	}
	return bfdv1connect.NewEchoServiceHandler(srv, opts...)
}

// AddEchoSession creates a new RFC 9747 echo session.
func (s *EchoServer) AddEchoSession(
	ctx context.Context,
	req *bfdv1.AddEchoSessionRequest,
) (*bfdv1.AddEchoSessionResponse, error) {
	cfg, err := echoSessionConfigFromProto(req)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	s.logger.InfoContext(ctx, "AddEchoSession called",
		slog.String("peer", cfg.PeerAddr.String()),
		slog.String("local", cfg.LocalAddr.String()),
		slog.Duration("tx_interval", cfg.TxInterval),
		slog.Uint64("detect_mult", uint64(cfg.DetectMultiplier)),
	)

	sender, _, err := s.senderFactory.CreateSender(cfg.LocalAddr, false, s.logger)
	if err != nil {
		return nil, mapManagerError(fmt.Errorf("create echo sender: %w", err), "add echo session")
	}

	discr, err := s.manager.CreateEchoSession(ctx, cfg, sender)
	if err != nil {
		return nil, mapManagerError(err, "add echo session")
	}

	for _, snap := range s.manager.EchoSessions() {
		if snap.LocalDiscr == discr {
			return &bfdv1.AddEchoSessionResponse{
				Session: echoSnapshotToProto(snap),
			}, nil
		}
	}

	return nil, connect.NewError(connect.CodeInternal,
		fmt.Errorf("echo session %d created but not visible in snapshot: %w",
			discr, bfd.ErrEchoSessionNotFound))
}

// DeleteEchoSession removes an echo session by its local discriminator.
func (s *EchoServer) DeleteEchoSession(
	ctx context.Context,
	req *bfdv1.DeleteEchoSessionRequest,
) (*bfdv1.DeleteEchoSessionResponse, error) {
	discr := req.GetLocalDiscriminator()
	s.logger.InfoContext(ctx, "DeleteEchoSession called",
		slog.Uint64("local_discr", uint64(discr)),
	)

	if err := s.manager.DestroyEchoSession(discr); err != nil {
		return nil, mapManagerError(err, "delete echo session")
	}

	return &bfdv1.DeleteEchoSessionResponse{}, nil
}

// ListEchoSessions returns all active echo sessions.
func (s *EchoServer) ListEchoSessions(
	_ context.Context,
	_ *bfdv1.ListEchoSessionsRequest,
) (*bfdv1.ListEchoSessionsResponse, error) {
	snaps := s.manager.EchoSessions()
	out := &bfdv1.ListEchoSessionsResponse{
		Sessions: make([]*bfdv1.EchoSession, 0, len(snaps)),
	}
	for _, snap := range snaps {
		out.Sessions = append(out.Sessions, echoSnapshotToProto(snap))
	}
	return out, nil
}

func echoSessionConfigFromProto(req *bfdv1.AddEchoSessionRequest) (bfd.EchoSessionConfig, error) {
	peerAddr, err := netip.ParseAddr(req.GetPeerAddress())
	if err != nil {
		return bfd.EchoSessionConfig{}, fmt.Errorf("parse peer address %q: %w", req.GetPeerAddress(), err)
	}

	var localAddr netip.Addr
	if la := req.GetLocalAddress(); la != "" {
		localAddr, err = netip.ParseAddr(la)
		if err != nil {
			return bfd.EchoSessionConfig{}, fmt.Errorf("parse local address %q: %w", la, err)
		}
	}

	if req.GetTxInterval() == nil {
		return bfd.EchoSessionConfig{}, ErrEchoTxIntervalRequired
	}
	tx := req.GetTxInterval().AsDuration()
	if tx <= 0 {
		return bfd.EchoSessionConfig{}, ErrEchoTxIntervalRequired
	}

	mult := req.GetDetectMultiplier()
	if mult == 0 {
		return bfd.EchoSessionConfig{}, ErrEchoDetectMultZero
	}
	if mult > 255 {
		return bfd.EchoSessionConfig{}, fmt.Errorf("value %d: %w", mult, ErrDetectMultOverflow)
	}

	return bfd.EchoSessionConfig{
		PeerAddr:         peerAddr,
		LocalAddr:        localAddr,
		Interface:        req.GetInterfaceName(),
		TxInterval:       tx,
		DetectMultiplier: uint8(mult),
	}, nil
}

func echoSnapshotToProto(snap bfd.EchoSessionSnapshot) *bfdv1.EchoSession {
	out := &bfdv1.EchoSession{
		PeerAddress:        snap.PeerAddr.String(),
		LocalAddress:       snap.LocalAddr.String(),
		InterfaceName:      snap.Interface,
		TxInterval:         durationpb.New(snap.TxInterval),
		DetectMultiplier:   uint32(snap.DetectMultiplier),
		LocalDiscriminator: snap.LocalDiscr,
		LocalState:         stateToProto(snap.State),
		LocalDiagnostic:    diagToProto(snap.LocalDiag),
		PacketsSent:        snap.EchosSent,
		PacketsReceived:    snap.EchosReceived,
	}
	if !snap.LastStateChange.IsZero() {
		out.LastStateChange = timestamppb.New(snap.LastStateChange)
	}
	return out
}
