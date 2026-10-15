package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// ErrIdentityNotMutating reports a mutating RPC from an mTLS identity that is
// not in the configured mutating identities (gobfd-330.4).
var ErrIdentityNotMutating = errors.New("mTLS identity is read-only for the management API")

// MutationPolicy decides which callers may call mutating RPCs.
type MutationPolicy struct {
	// AllowAnonymous admits callers without a principal (plain loopback
	// TCP): grpc.unsafe_loopback_mutations.
	AllowAnonymous bool
	// MutatingIdentities, when not empty, lists the mTLS identities that may
	// mutate; other mTLS identities are read-only. Unix socket principals
	// ("uid:<n>") are gated by the socket mode and group instead.
	MutatingIdentities []string
}

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

// MutationGuardInterceptor applies policy to mutating RPCs. Reads are never
// refused.
func MutationGuardInterceptor(policy MutationPolicy) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if isMutating(spec.Procedure) {
				if err := policy.check(ctx); err != nil {
					err = fmt.Errorf("%s: %w", spec.Procedure, err)
					return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
				}
			}
			return next(ctx, spec, stream)
		}
	}
}

func (p MutationPolicy) check(ctx context.Context) error {
	principal, ok := PrincipalFromContext(ctx)
	return p.allows(principal, ok)
}

func (p MutationPolicy) allows(principal string, ok bool) error {
	switch {
	case !ok && !p.AllowAnonymous:
		return ErrAnonymousMutation
	case !ok, strings.HasPrefix(principal, "uid:"), len(p.MutatingIdentities) == 0:
		return nil
	case slices.Contains(p.MutatingIdentities, principal):
		return nil
	default:
		return ErrIdentityNotMutating
	}
}

// apiOwnerFromContext is the API claim owner of a request: its principal, or
// "local" for an anonymous caller admitted by the unsafe opt-in.
func apiOwnerFromContext(ctx context.Context) bfd.SessionOwner {
	if principal, ok := PrincipalFromContext(ctx); ok {
		return bfd.APISessionOwner(principal)
	}
	return bfd.APISessionOwner("local")
}

// RequirePrincipal wraps a plain HTTP handler on the management server, such
// as the flight recorder dump, so that only callers with a principal reach
// it: anonymous callers are refused even under the unsafe loopback opt-in,
// and with mutating identities set only the listed mTLS identities pass.
func RequirePrincipal(policy MutationPolicy, next http.Handler) http.Handler {
	policy.AllowAnonymous = false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromRequest(r)
		if err := policy.allows(principal, ok); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
