package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"testing"

	"connectrpc.com/connect/v2"
)

func TestMutationGuardRejectsTLSWithoutPrincipal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		state *tls.ConnectionState
	}{
		{"verified without SAN", &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{new(x509.Certificate)}}}},
		{"verified IP SAN only", &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		}}}}},
		{"unverified TLS", new(tls.ConnectionState)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := rpcTLSContext(t, tc.state)
			for _, identities := range [][]string{nil, {"spiffe://gobfd.test/ops"}} {
				policy := MutationPolicy{AllowAnonymous: true, MutatingIdentities: identities}
				for _, procedure := range []string{
					"/bfd.v1.BfdService/AddSession", "/bfd.v1.BfdService/DeleteSession",
					"/bfd.v1.EchoService/AddEchoSession", "/bfd.v1.EchoService/DeleteEchoSession",
					"/bfd.v1.MicroBFDService/AddMicroBFDGroup", "/bfd.v1.MicroBFDService/DeleteMicroBFDGroup",
					"/bfd.v1.BfdService/ListSessions",
				} {
					called := false
					next := func(context.Context, connect.Spec, connect.ServerStream) error {
						called = true
						return nil
					}
					err := MutationGuardInterceptor(policy)(next)(ctx, connect.Spec{Procedure: procedure}, nil)
					if procedure == "/bfd.v1.BfdService/ListSessions" {
						if err != nil || !called {
							t.Errorf("read: error = %v, called = %t", err, called)
						}
					} else if connect.CodeOf(err) != connect.CodePermissionDenied || !errors.Is(err, ErrAnonymousMutation) || called {
						t.Errorf("%s: error = %v, called = %t; want denied without invoking handler", procedure, err, called)
					}
				}
			}
		})
	}
}

func TestMutationGuardRejectsUnknownAnonymousTransport(t *testing.T) {
	t.Parallel()

	for _, ctx := range []context.Context{t.Context(), context.WithValue(t.Context(), loopbackTCPKey{}, true)} {
		called := false
		next := func(context.Context, connect.Spec, connect.ServerStream) error {
			called = true
			return nil
		}
		err := MutationGuardInterceptor(MutationPolicy{AllowAnonymous: true})(next)(
			ctx, connect.Spec{Procedure: "/bfd.v1.BfdService/AddSession"}, nil)
		if connect.CodeOf(err) != connect.CodePermissionDenied || !errors.Is(err, ErrAnonymousMutation) || called {
			t.Fatalf("unknown transport: error = %v, called = %t; want denied", err, called)
		}
	}
}

func TestConnContextMarksOnlyLoopbackTCP(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()

			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			accepted, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = accepted.Close() })
			ctx := ConnContext(t.Context(), accepted)
			if marked, _ := ctx.Value(loopbackTCPKey{}).(bool); !marked {
				t.Fatal("accepted loopback TCP connection was not admitted for unsafe opt-in")
			}
			if principal, ok := PrincipalFromContext(ctx); ok {
				t.Fatalf("anonymous TCP acquired principal %q", principal)
			}
		})
	}

	client, peer := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	ctx := ConnContext(t.Context(), peer)
	if marked, _ := ctx.Value(loopbackTCPKey{}).(bool); marked {
		t.Fatal("unknown connection was marked as loopback TCP")
	}
}
