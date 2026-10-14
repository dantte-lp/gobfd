package server

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
)

// gobfd-330.3: without a transport principal (plain loopback TCP) mutating
// RPCs are refused unless the unsafe loopback opt-in is set; reads and
// authenticated callers are unaffected.
func TestMutationGuard(t *testing.T) {
	t.Parallel()

	anonymous := context.Background()
	authenticated := context.WithValue(context.Background(), principalKey{}, "uid:1000")
	tests := []struct {
		name      string
		ctx       context.Context
		procedure string
		unsafe    bool
		wantDeny  bool
	}{
		{"anonymous AddSession", anonymous, "/bfd.v1.BfdService/AddSession", false, true},
		{"anonymous DeleteSession", anonymous, "/bfd.v1.BfdService/DeleteSession", false, true},
		{"anonymous AddEchoSession", anonymous, "/bfd.v1.EchoService/AddEchoSession", false, true},
		{"anonymous DeleteEchoSession", anonymous, "/bfd.v1.EchoService/DeleteEchoSession", false, true},
		{"anonymous AddMicroBFDGroup", anonymous, "/bfd.v1.MicroBFDService/AddMicroBFDGroup", false, true},
		{"anonymous DeleteMicroBFDGroup", anonymous, "/bfd.v1.MicroBFDService/DeleteMicroBFDGroup", false, true},
		{"anonymous ListSessions", anonymous, "/bfd.v1.BfdService/ListSessions", false, false},
		{"anonymous WatchSessionEvents", anonymous, "/bfd.v1.BfdService/WatchSessionEvents", false, false},
		{"anonymous AddSession, unsafe opt-in", anonymous, "/bfd.v1.BfdService/AddSession", true, false},
		{"authenticated AddSession", authenticated, "/bfd.v1.BfdService/AddSession", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			next := connect.ServerFunc(func(context.Context, connect.Spec, connect.ServerStream) error {
				called = true
				return nil
			})
			err := MutationGuardInterceptor(tt.unsafe)(next)(tt.ctx, connect.Spec{Procedure: tt.procedure}, nil)
			if tt.wantDeny {
				if connect.CodeOf(err) != connect.CodePermissionDenied || called {
					t.Fatalf("error = %v, called = %t; want PermissionDenied without the handler", err, called)
				}
				if !errors.Is(err, ErrAnonymousMutation) {
					t.Fatalf("error = %v, want ErrAnonymousMutation", err)
				}
				return
			}
			if err != nil || !called {
				t.Fatalf("error = %v, called = %t; want the handler", err, called)
			}
		})
	}
}
