package server

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
)

// gobfd-330.4: with mutating identities configured, an mTLS principal not
// in the list is read-only; socket principals and listed identities mutate.
func TestMutationGuardIdentityRoles(t *testing.T) {
	t.Parallel()

	policy := MutationPolicy{MutatingIdentities: []string{"spiffe://gobfd.test/ops"}}
	tests := []struct {
		name      string
		principal string
		procedure string
		wantDeny  bool
	}{
		{"listed identity mutates", "spiffe://gobfd.test/ops", "/bfd.v1.BfdService/AddSession", false},
		{"unlisted identity cannot mutate", "spiffe://gobfd.test/monitor", "/bfd.v1.BfdService/DeleteSession", true},
		{"unlisted identity reads", "spiffe://gobfd.test/monitor", "/bfd.v1.BfdService/ListSessions", false},
		{"socket principal mutates", "uid:1000", "/bfd.v1.BfdService/AddSession", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.WithValue(context.Background(), principalKey{}, tt.principal)
			called := false
			next := connect.ServerFunc(func(context.Context, connect.Spec, connect.ServerStream) error {
				called = true
				return nil
			})
			err := MutationGuardInterceptor(policy)(next)(ctx, connect.Spec{Procedure: tt.procedure}, nil)
			if tt.wantDeny {
				if connect.CodeOf(err) != connect.CodePermissionDenied || called || !errors.Is(err, ErrIdentityNotMutating) {
					t.Fatalf("error = %v, called = %t; want PermissionDenied ErrIdentityNotMutating", err, called)
				}
				return
			}
			if err != nil || !called {
				t.Fatalf("error = %v, called = %t; want the handler", err, called)
			}
		})
	}
}

// The API owner of a request is its principal, or api:local for an
// anonymous caller admitted by the unsafe loopback opt-in.
func TestAPIOwnerFromContext(t *testing.T) {
	t.Parallel()

	if got := apiOwnerFromContext(context.Background()); got.ID != "local" {
		t.Fatalf("anonymous owner = %+v, want ID local", got)
	}
	ctx := context.WithValue(context.Background(), principalKey{}, "uid:1000")
	if got := apiOwnerFromContext(ctx); got.ID != "uid:1000" {
		t.Fatalf("socket owner = %+v, want ID uid:1000", got)
	}
}
