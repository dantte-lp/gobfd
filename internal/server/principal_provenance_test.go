package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func principalTLSState(t *testing.T, uri, dns string) *tls.ConnectionState {
	t.Helper()

	leaf := new(x509.Certificate)
	if uri != "" {
		u, err := url.Parse(uri)
		if err != nil {
			t.Fatalf("parse URI SAN: %v", err)
		}
		leaf.URIs = []*url.URL{u}
	}
	if dns != "" {
		leaf.DNSNames = []string{dns}
	}
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
}

// Preserve the real ConnectHTTP TLS context without requiring another test CA.
func rpcTLSContext(t *testing.T, state *tls.ConnectionState) context.Context {
	t.Helper()

	contexts := make(chan context.Context, 1)
	capture := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			contexts <- ctx
			return next(ctx, spec, stream)
		}
	}
	rpc := connect.NewServer(capture)
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	Register(rpc, mgr, nil, logger)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.TLS = state
		mux.ServeHTTP(w, r)
	}))
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	if _, err := client.ListSessions(t.Context(), new(bfdv1.ListSessionsRequest)); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	select {
	case ctx := <-contexts:
		return ctx
	case <-t.Context().Done():
		t.Fatal("RPC context not captured")
		return nil
	}
}

func TestTLSPrincipalMutationRoles(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		uri      string
		dns      string
		allowed  string
		wantDeny bool
	}{
		{"UID URI is not a Unix peer", "uid:1000", "", "spiffe://gobfd.test/ops", true},
		{"DNS-looking URI is unlisted", "dns:ops.gobfd.test", "", "spiffe://gobfd.test/ops", true},
		{"explicit raw UID URI role", "uid:1000", "", "uid:1000", false},
		{"explicit raw DNS-looking URI role", "dns:ops.gobfd.test", "", "dns:ops.gobfd.test", false},
		{"explicit raw URI role", "spiffe://gobfd.test/ops", "", "spiffe://gobfd.test/ops", false},
		{"explicit DNS role", "", "ops.gobfd.test", "dns:ops.gobfd.test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := rpcTLSContext(t, principalTLSState(t, tc.uri, tc.dns))
			wantDisplay := tc.uri
			if wantDisplay == "" {
				wantDisplay = "dns:" + tc.dns
			}
			if got, ok := PrincipalFromContext(ctx); !ok || got != wantDisplay {
				t.Fatalf("principal = %q, %t; want %q", got, ok, wantDisplay)
			}
			policy := MutationPolicy{MutatingIdentities: []string{tc.allowed}}
			for _, procedure := range []string{
				"/bfd.v1.BfdService/AddSession", "/bfd.v1.BfdService/DeleteSession",
				"/bfd.v1.EchoService/AddEchoSession", "/bfd.v1.EchoService/DeleteEchoSession",
				"/bfd.v1.MicroBFDService/AddMicroBFDGroup", "/bfd.v1.MicroBFDService/DeleteMicroBFDGroup",
			} {
				called := false
				next := func(context.Context, connect.Spec, connect.ServerStream) error {
					called = true
					return nil
				}
				err := MutationGuardInterceptor(policy)(next)(ctx, connect.Spec{Procedure: procedure}, nil)
				if tc.wantDeny {
					if connect.CodeOf(err) != connect.CodePermissionDenied || !errors.Is(err, ErrIdentityNotMutating) || called {
						t.Errorf("%s: error = %v, called = %t; want denied TLS identity", procedure, err, called)
					}
				} else if err != nil || !called {
					t.Errorf("%s: error = %v, called = %t; want authorized handler", procedure, err, called)
				}
			}
		})
	}
}

func TestAPIOwnerTransportNamespaces(t *testing.T) {
	t.Parallel()

	unixCtx := context.WithValue(t.Context(), principalKey{}, transportPrincipal{
		kind: principalUnix, identity: "uid:1000",
	})
	contexts := []context.Context{
		unixCtx,
		rpcTLSContext(t, principalTLSState(t, "uid:1000", "")),
		rpcTLSContext(t, principalTLSState(t, "dns:ops.gobfd.test", "")),
		rpcTLSContext(t, principalTLSState(t, "", "ops.gobfd.test")),
		rpcTLSContext(t, principalTLSState(t, "unix:uid:1000", "")),
		context.Background(),
	}
	owners := make(map[bfd.SessionOwner]int)
	for i, ctx := range contexts {
		owner := apiOwnerFromContext(ctx)
		if earlier, ok := owners[owner]; ok {
			t.Errorf("transport identities %d and %d share owner %+v", earlier, i, owner)
		}
		owners[owner] = i
	}
}
