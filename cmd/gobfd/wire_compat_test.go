package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// TestGRPCServerWireCompatibility pins the wire contract that connect-go v2
// must keep for consumers that do not use the generated Go client: grpc-go
// over h2c, grpc.health.v1, Connect and gRPC-Web over HTTP/1.1, and the
// 4 MiB per-message read limit.
func TestGRPCServerWireCompatibility(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)

	srv := newGRPCServer(config.GRPCConfig{Addr: "127.0.0.1:0"}, mgr, nil, newDaemonHealthChecker(), logger)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	t.Cleanup(func() {
		if closeErr := srv.Close(); closeErr != nil {
			t.Errorf("close server: %v", closeErr)
		}
		if serveErr := <-serveDone; !errors.Is(serveErr, http.ErrServerClosed) {
			t.Errorf("serve: %v", serveErr)
		}
	})
	addr := listener.Addr().String()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	t.Run("grpc-go unary", func(t *testing.T) {
		t.Parallel()

		resp := new(bfdv1.ListSessionsResponse)
		err := conn.Invoke(t.Context(), "/bfd.v1.BfdService/ListSessions", new(bfdv1.ListSessionsRequest), resp)
		if err != nil {
			t.Fatalf("ListSessions over grpc-go: %v", err)
		}
	})

	t.Run("grpc.health.v1", func(t *testing.T) {
		t.Parallel()

		resp, err := grpc_health_v1.NewHealthClient(conn).Check(t.Context(),
			&grpc_health_v1.HealthCheckRequest{Service: bfdServiceName})
		if err != nil {
			t.Fatalf("health check: %v", err)
		}
		if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Fatalf("health status = %v, want SERVING", resp.GetStatus())
		}
	})

	t.Run("read limit 4 MiB", func(t *testing.T) {
		t.Parallel()

		req := &bfdv1.AddSessionRequest{InterfaceName: strings.Repeat("x", 4<<20)}
		err := conn.Invoke(t.Context(), "/bfd.v1.BfdService/AddSession", req, new(bfdv1.AddSessionResponse))
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("oversized AddSession: code = %v (%v), want ResourceExhausted", status.Code(err), err)
		}
	})

	// HTTP/1.1 protocols that connect-go v1 clients and browsers speak:
	// Connect with the JSON and proto codecs, and gRPC-Web. The gRPC-Web
	// body is one uncompressed frame holding an empty ListSessionsRequest.
	for _, tc := range []struct{ name, contentType, body, wantInBody string }{
		{"connect json", "application/json", "{}", ""},
		{"connect proto", "application/proto", "", ""},
		{"grpc-web proto", "application/grpc-web+proto", "\x00\x00\x00\x00\x00", "grpc-status:0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				"http://"+addr+"/bfd.v1.BfdService/ListSessions", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST ListSessions: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 1 {
				t.Fatalf("POST ListSessions: %s %s, body %q", resp.Proto, resp.Status, body)
			}
			if got := resp.Header.Get("Content-Type"); got != tc.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tc.contentType)
			}
			trailers := strings.ToLower(strings.ReplaceAll(string(body), " ", ""))
			if !strings.Contains(trailers, tc.wantInBody) {
				t.Fatalf("body %q does not contain %q", body, tc.wantInBody)
			}
		})
	}
}
