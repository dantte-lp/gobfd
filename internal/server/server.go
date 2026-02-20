// Package server implements the ConnectRPC server for the BFD daemon.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"

	bfdv1 "github.com/wolfguard/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/wolfguard/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

var errNotImplemented = errors.New("not yet implemented")

// BFDServer implements bfdv1connect.BfdServiceHandler.
//
// Each RPC delegates to the session Manager for actual BFD operations.
// The server is a thin adapter between gRPC API and internal domain.
type BFDServer struct {
	logger *slog.Logger
}

// verify interface compliance at compile time.
var _ bfdv1connect.BfdServiceHandler = (*BFDServer)(nil)

// New creates a new BFDServer and returns the HTTP handler and path.
func New(logger *slog.Logger, opts ...connect.HandlerOption) (string, http.Handler) {
	srv := &BFDServer{
		logger: logger,
	}
	return bfdv1connect.NewBfdServiceHandler(srv, opts...)
}

// AddSession creates a new BFD session with the given parameters.
func (s *BFDServer) AddSession(ctx context.Context, req *bfdv1.AddSessionRequest) (*bfdv1.AddSessionResponse, error) {
	s.logger.InfoContext(ctx, "AddSession called",
		slog.String("peer", req.GetPeerAddress()),
		slog.String("local", req.GetLocalAddress()),
	)
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

// DeleteSession removes a BFD session by its local discriminator.
func (s *BFDServer) DeleteSession(ctx context.Context, req *bfdv1.DeleteSessionRequest) (*bfdv1.DeleteSessionResponse, error) {
	s.logger.InfoContext(ctx, "DeleteSession called",
		slog.Uint64("discriminator", uint64(req.GetLocalDiscriminator())),
	)
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

// ListSessions returns all active BFD sessions.
func (s *BFDServer) ListSessions(ctx context.Context, _ *bfdv1.ListSessionsRequest) (*bfdv1.ListSessionsResponse, error) {
	s.logger.InfoContext(ctx, "ListSessions called")
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

// GetSession returns a single session by discriminator or peer address.
func (s *BFDServer) GetSession(ctx context.Context, req *bfdv1.GetSessionRequest) (*bfdv1.GetSessionResponse, error) {
	s.logger.InfoContext(ctx, "GetSession called")
	_ = req // will dispatch to Manager once implemented
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

// WatchSessionEvents streams BFD session state changes (server-side streaming).
func (s *BFDServer) WatchSessionEvents(ctx context.Context, req *bfdv1.WatchSessionEventsRequest, _ *connect.ServerStream[bfdv1.WatchSessionEventsResponse]) error {
	s.logger.InfoContext(ctx, "WatchSessionEvents called",
		slog.Bool("include_current", req.GetIncludeCurrent()),
	)
	return connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}
