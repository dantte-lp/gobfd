package server

import (
	"context"
	"log/slog"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// Export unexported functions for testing.
var (
	StateToProto       = stateToProto
	DiagToProto        = diagToProto
	SessionTypeToProto = sessionTypeToProto
	SessionTypeFromPB  = sessionTypeFromProto
	DurationFromPB     = durationFromProto
	StateChangeToProto = stateChangeToProto
	SnapshotToProto    = snapshotToProto
	MapManagerError    = mapManagerError
	WrapError          = wrapError
)

// Re-export types needed by external tests.
type (
	StateToProtoFn       = func(bfd.State) bfdv1.SessionState
	DiagToProtoFn        = func(bfd.Diag) bfdv1.DiagnosticCode
	SessionTypeToProtoFn = func(bfd.SessionType) bfdv1.SessionType
	SessionTypeFromPBFn  = func(bfdv1.SessionType) (bfd.SessionType, error)
	DurationFromPBFn     = func(*durationpb.Duration) any
	MapManagerErrorFn    = func(error, string) *connect.Error
)

// WatchLoop runs the WatchSessionEvents loop of a server backed by mgr.
func WatchLoop(
	ctx context.Context,
	mgr *bfd.Manager,
	ch <-chan bfd.StateChange,
	includeCurrent bool,
	send func(*bfdv1.WatchSessionEventsResponse) error,
) error {
	s := &BFDServer{manager: mgr, logger: slog.New(slog.DiscardHandler)}
	return s.watchLoop(ctx, ch, includeCurrent, send)
}
