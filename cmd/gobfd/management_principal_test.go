package main

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net"
	"path/filepath"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// gobfd-330.4 over real mTLS: a session claim belongs to the identity that
// created it, and mutating identities restrict which mTLS callers mutate.
func TestManagementClaimsAndRolesOverMutualTLS(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	serverCert, serverKey := pki.issue(t, "server", x509.ExtKeyUsageServerAuth, "")
	opsCert, opsKey := pki.issue(t, "ops", x509.ExtKeyUsageClientAuth, "spiffe://gobfd.test/ops")
	monCert, monKey := pki.issue(t, "monitor", x509.ExtKeyUsageClientAuth, "spiffe://gobfd.test/monitor")
	ca := filepath.Join(pki.dir, "ca.pem")
	tlsCfg := config.GRPCTLSConfig{CertFile: serverCert, KeyFile: serverKey, ClientCAFile: ca}

	start := func(identities []string) string {
		t.Helper()
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		t.Cleanup(mgr.Close)
		srv := newGRPCServer(config.GRPCConfig{MutatingIdentities: identities}, false, mgr, nil, nil,
			newDaemonHealthChecker(), nil, slog.New(slog.DiscardHandler))
		tcp, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		ln, err := newManagementTLSListener(tcp, tlsCfg)
		if err != nil {
			t.Fatalf("newManagementTLSListener: %v", err)
		}
		serveOn(t, srv, ln)
		return "https://" + tcp.Addr().String()
	}
	client := func(base, cert, key string) bfdv1connect.BfdServiceClient {
		t.Helper()
		ep, err := apiclient.ParseEndpoint(base)
		if err != nil {
			t.Fatalf("ParseEndpoint: %v", err)
		}
		httpClient, err := ep.HTTPClientTLS(apiclient.TLSFiles{CAFile: ca, CertFile: cert, KeyFile: key})
		if err != nil {
			t.Fatalf("HTTPClientTLS: %v", err)
		}
		return bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, ep.BaseURL)))
	}

	open := start(nil)
	ops, monitor := client(open, opsCert, opsKey), client(open, monCert, monKey)
	created, err := ops.AddSession(context.Background(), addSessionRequest("127.0.0.4"))
	if err != nil {
		t.Fatalf("ops AddSession: %v", err)
	}
	discr := created.GetSession().GetLocalDiscriminator()
	_, err = monitor.DeleteSession(context.Background(), &bfdv1.DeleteSessionRequest{LocalDiscriminator: discr})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("monitor DeleteSession of the ops claim error = %v, want PermissionDenied", err)
	}
	release := &bfdv1.DeleteSessionRequest{LocalDiscriminator: discr}
	if _, err = ops.DeleteSession(context.Background(), release); err != nil {
		t.Fatalf("ops DeleteSession: %v", err)
	}

	restricted := start([]string{"spiffe://gobfd.test/ops"})
	restrictedMonitor := client(restricted, monCert, monKey)
	_, err = restrictedMonitor.AddSession(context.Background(), addSessionRequest("127.0.0.5"))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unlisted monitor AddSession error = %v, want PermissionDenied", err)
	}
	if _, err = restrictedMonitor.ListSessions(context.Background(), new(bfdv1.ListSessionsRequest)); err != nil {
		t.Fatalf("unlisted monitor ListSessions: %v", err)
	}
	restrictedOps := client(restricted, opsCert, opsKey)
	if _, err = restrictedOps.AddSession(context.Background(), addSessionRequest("127.0.0.6")); err != nil {
		t.Fatalf("listed ops AddSession: %v", err)
	}
}
