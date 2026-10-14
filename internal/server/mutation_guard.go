package server

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
)

// ErrAnonymousMutation reports a mutating RPC from a caller without a
// transport principal (plain loopback TCP) while the unsafe loopback opt-in
// is off (gobfd-330.3).
var ErrAnonymousMutation = errors.New(
	"mutating RPC needs the management socket or mTLS; anonymous loopback TCP is read-only")

// isMutating reports whether a bfd.v1 procedure changes daemon state.
func isMutating(procedure string) bool {
	switch procedure {
	case "/bfd.v1.BfdService/AddSession", "/bfd.v1.BfdService/DeleteSession",
		"/bfd.v1.EchoService/AddEchoSession", "/bfd.v1.EchoService/DeleteEchoSession",
		"/bfd.v1.MicroBFDService/AddMicroBFDGroup", "/bfd.v1.MicroBFDService/DeleteMicroBFDGroup":
		return true
	default:
		return false
	}
}

// MutationGuardInterceptor refuses mutating RPCs from callers without a
// principal unless allowAnonymous is set. Reads are never refused.
func MutationGuardInterceptor(allowAnonymous bool) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if isMutating(spec.Procedure) && !allowAnonymous {
				if _, ok := PrincipalFromContext(ctx); !ok {
					err := fmt.Errorf("%s: %w", spec.Procedure, ErrAnonymousMutation)
					return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
				}
			}
			return next(ctx, spec, stream)
		}
	}
}
