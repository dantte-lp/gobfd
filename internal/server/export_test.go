package server

import (
	"connectrpc.com/connect"
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
