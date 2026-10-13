package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// panicHandler wraps a real server and panics on AddSession calls.
// Used to test the RecoveryInterceptor.
type panicHandler struct {
	bfdv1connect.UnimplementedBfdServiceHandler
}

func (panicHandler) AddSession(
	_ context.Context,
	_ *bfdv1.AddSessionRequest,
) (*bfdv1.AddSessionResponse, error) {
	panic("intentional test panic")
}

// setupServerWithInterceptors creates a test server with the given ConnectRPC server interceptors.
func setupServerWithInterceptors(
	t *testing.T,
	interceptors ...connect.ServerInterceptor,
) bfdv1connect.BfdServiceClient {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)

	rpc := connect.NewServer(interceptors...)
	server.Register(rpc, mgr, nil, logger)
	return serveBfdClient(t, rpc)
}

// setupPanicServer creates a test server that panics on AddSession,
// using the given server interceptors.
func setupPanicServer(
	t *testing.T,
	interceptors ...connect.ServerInterceptor,
) bfdv1connect.BfdServiceClient {
	t.Helper()

	rpc := connect.NewServer(interceptors...)
	bfdv1connect.RegisterBfdServiceHandler(rpc, panicHandler{})
	return serveBfdClient(t, rpc)
}

// serveBfdClient mounts rpc on a test HTTP server and returns a BfdService client for it.
func serveBfdClient(t *testing.T, rpc *connect.Server) bfdv1connect.BfdServiceClient {
	t.Helper()

	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	srv := httptest.NewTestServer(t, mux)

	return bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
}

// -------------------------------------------------------------------------
// TestLoggingInterceptor
// -------------------------------------------------------------------------

func TestLoggingInterceptorSuccess(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupServerWithInterceptors(t, server.LoggingInterceptor(logger))

	resp, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
}

func TestLoggingInterceptorError(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupServerWithInterceptors(t, server.LoggingInterceptor(logger))

	_, err := client.DeleteSession(context.Background(), &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: 99999,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		if connectErr.Code() != connect.CodeNotFound {
			t.Errorf("code = %s, want NotFound", connectErr.Code())
		}
	} else {
		t.Fatalf("expected connect.Error, got %T: %v", err, err)
	}
}

// -------------------------------------------------------------------------
// TestRecoveryInterceptor
// -------------------------------------------------------------------------

func TestRecoveryInterceptorNoPanic(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupServerWithInterceptors(t, server.RecoveryInterceptor(logger))

	resp, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
}

func TestRecoveryInterceptorPanic(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupPanicServer(t, server.RecoveryInterceptor(logger))

	_, err := client.AddSession(context.Background(), &bfdv1.AddSessionRequest{
		PeerAddress:           "192.0.2.1",
		LocalAddress:          "192.0.2.2",
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(1_000_000_000),
		RequiredMinRxInterval: durationpb.New(1_000_000_000),
		DetectMultiplier:      3,
	})
	if err == nil {
		t.Fatal("expected error after panic, got nil")
	}

	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		if connectErr.Code() != connect.CodeInternal {
			t.Errorf("code = %s, want Internal", connectErr.Code())
		}
	} else {
		t.Fatalf("expected connect.Error, got %T: %v", err, err)
	}
}

// -------------------------------------------------------------------------
// TestBothInterceptors — logging + recovery together
// -------------------------------------------------------------------------

func TestBothInterceptors(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupServerWithInterceptors(t,
		server.LoggingInterceptor(logger),
		server.RecoveryInterceptor(logger),
	)

	resp, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
}

func TestLoggingInterceptorLevels(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want slog.Level
	}{
		{"success", nil, slog.LevelInfo},
		{"client canceled stream", fmt.Errorf("watch session events: %w", context.Canceled), slog.LevelInfo},
		{"canceled code", connect.NewError(connect.CodeCanceled, "canceled"), slog.LevelInfo},
		{"handler error", connect.NewError(connect.CodeInternal, "boom"), slog.LevelWarn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			next := func(context.Context, connect.Spec, connect.ServerStream) error { return tc.err }
			spec := connect.Spec{Procedure: "/bfd.v1.BfdService/WatchSessionEvents"}

			err := server.LoggingInterceptor(logger)(next)(t.Context(), spec, nil)
			if !errors.Is(err, tc.err) {
				t.Fatalf("interceptor returned %v, want %v", err, tc.err)
			}
			var entry struct {
				Level string `json:"level"`
			}
			if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
				t.Fatalf("decode log %q: %v", buf.String(), err)
			}
			if entry.Level != tc.want.String() {
				t.Fatalf("log level = %s, want %s", entry.Level, tc.want)
			}
		})
	}
}
