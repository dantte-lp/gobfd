package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func serveOn(t *testing.T, srv *http.Server, ln net.Listener) {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	})
}

func addSessionRequest(peer string) *bfdv1.AddSessionRequest {
	return &bfdv1.AddSessionRequest{
		PeerAddress: peer, LocalAddress: "127.0.0.1", InterfaceName: "lo",
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(time.Second),
		RequiredMinRxInterval: durationpb.New(time.Second),
		DetectMultiplier:      3,
	}
}

// gobfd-330.3: anonymous loopback TCP clients read but cannot mutate; the
// management socket, whose callers carry a peer-UID principal, can.
func TestGRPCServerGuardsAnonymousLoopbackMutation(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	srv := newGRPCServer(config.GRPCConfig{}, mgr, nil, newDaemonHealthChecker(), nil, slog.New(slog.DiscardHandler))

	tcp, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	serveOn(t, srv, tcp)
	path := filepath.Join(t.TempDir(), "gobfd.sock")
	unixLn, err := listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: path, Mode: "0660"})
	if err != nil {
		t.Fatalf("listenManagementSocket: %v", err)
	}
	serveOn(t, srv, unixLn)

	client := func(raw string) bfdv1connect.BfdServiceClient {
		t.Helper()
		ep, parseErr := apiclient.ParseEndpoint(raw)
		if parseErr != nil {
			t.Fatalf("ParseEndpoint(%s): %v", raw, parseErr)
		}
		return bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(ep.HTTPClient(), ep.BaseURL)))
	}
	tcpClient := client("http://" + tcp.Addr().String())
	if _, err = tcpClient.ListSessions(context.Background(), new(bfdv1.ListSessionsRequest)); err != nil {
		t.Fatalf("anonymous ListSessions: %v", err)
	}
	_, err = tcpClient.AddSession(context.Background(), addSessionRequest("127.0.0.2"))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("anonymous AddSession error = %v, want PermissionDenied", err)
	}
	if _, err = client("unix://"+path).AddSession(context.Background(), addSessionRequest("127.0.0.3")); err != nil {
		t.Fatalf("AddSession over the management socket: %v", err)
	}
}

// The gobfd.management health check is NOT_SERVING while the unsafe
// loopback mutation opt-in is enabled.
func TestManagementHealthReportsUnsafeLoopbackMutations(t *testing.T) {
	t.Parallel()

	for _, unsafe := range []bool{false, true} {
		checker := newDaemonHealthChecker()
		reportManagementSafety(checker, config.GRPCConfig{UnsafeLoopbackMutations: unsafe})
		resp, err := checker.Check(context.Background(), &grpchealth.CheckRequest{Service: managementHealthServiceName})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		want := grpchealth.StatusServing
		if unsafe {
			want = grpchealth.StatusNotServing
		}
		if resp.Status != want {
			t.Fatalf("unsafe=%t: gobfd.management = %v, want %v", unsafe, resp.Status, want)
		}
	}
}

// A missing socket directory is created, as under a root container without
// systemd RuntimeDirectory=.
func TestListenManagementSocketCreatesDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "run", "gobfd", "gobfd.sock")
	ln, err := listenManagementSocket(t.Context(), config.GRPCSocketConfig{Path: path, Mode: "0660"})
	if err != nil {
		t.Fatalf("listenManagementSocket: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("socket directory = %v, %v; want mode 0755", info, err)
	}
}
