package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/grpchealth/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

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

	// Wire framing needs AddSession over plaintext TCP: opt in explicitly.
	grpcCfg := config.GRPCConfig{Addr: "127.0.0.1:0", UnsafeLoopbackMutations: true}
	srv := newGRPCServer(grpcCfg, false, mgr, nil, nil, newDaemonHealthChecker(), nil, logger, nil)
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

	// One unary call per registered service, so dropping a Register call
	// from newGRPCServer fails here even though health reports it SERVING.
	for _, tc := range []struct {
		method    string
		req, resp proto.Message
	}{
		{"/bfd.v1.BfdService/ListSessions", new(bfdv1.ListSessionsRequest), new(bfdv1.ListSessionsResponse)},
		{"/bfd.v1.EchoService/ListEchoSessions", new(bfdv1.ListEchoSessionsRequest), new(bfdv1.ListEchoSessionsResponse)},
		{
			"/bfd.v1.MicroBFDService/ListMicroBFDGroups",
			new(bfdv1.ListMicroBFDGroupsRequest), new(bfdv1.ListMicroBFDGroupsResponse),
		},
	} {
		t.Run("grpc-go unary "+tc.method, func(t *testing.T) {
			t.Parallel()

			if err := conn.Invoke(t.Context(), tc.method, tc.req, tc.resp); err != nil {
				t.Fatalf("%s over grpc-go: %v", tc.method, err)
			}
		})
	}

	t.Run("grpc-go server stream", func(t *testing.T) {
		t.Parallel()

		add := &bfdv1.AddSessionRequest{
			PeerAddress:           "127.0.0.2",
			LocalAddress:          "127.0.0.1",
			InterfaceName:         "eth0",
			Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
			DesiredMinTxInterval:  durationpb.New(time.Second),
			RequiredMinRxInterval: durationpb.New(time.Second),
			DetectMultiplier:      3,
		}
		if err := conn.Invoke(t.Context(), "/bfd.v1.BfdService/AddSession", add, new(bfdv1.AddSessionResponse)); err != nil {
			t.Fatalf("AddSession over grpc-go: %v", err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/bfd.v1.BfdService/WatchSessionEvents")
		if err != nil {
			t.Fatalf("open WatchSessionEvents: %v", err)
		}
		if err := stream.SendMsg(&bfdv1.WatchSessionEventsRequest{IncludeCurrent: true}); err != nil {
			t.Fatalf("send WatchSessionEvents request: %v", err)
		}
		if err := stream.CloseSend(); err != nil {
			t.Fatalf("close send: %v", err)
		}
		event := new(bfdv1.WatchSessionEventsResponse)
		if err := stream.RecvMsg(event); err != nil {
			t.Fatalf("receive WatchSessionEvents: %v", err)
		}
		if event.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED ||
			event.GetSession().GetPeerAddress() != add.GetPeerAddress() {
			t.Fatalf("first event = %v, want SESSION_ADDED for %s", event, add.GetPeerAddress())
		}
	})

	t.Run("grpc.health.v1", func(t *testing.T) {
		t.Parallel()

		// The liveness service is SERVING; bfd.v1 services stay NOT_SERVING
		// until component-derived readiness publishes a converged state.
		for service, want := range map[string]grpc_health_v1.HealthCheckResponse_ServingStatus{
			grpchealth.HealthV1ServiceName: grpc_health_v1.HealthCheckResponse_SERVING,
			bfdServiceName:                 grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		} {
			resp, err := grpc_health_v1.NewHealthClient(conn).Check(t.Context(),
				&grpc_health_v1.HealthCheckRequest{Service: service})
			if err != nil {
				t.Fatalf("health check %q: %v", service, err)
			}
			if resp.GetStatus() != want {
				t.Fatalf("health status %q = %v, want %v", service, resp.GetStatus(), want)
			}
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
