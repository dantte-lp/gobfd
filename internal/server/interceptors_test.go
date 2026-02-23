package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
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

// setupServerWithInterceptors creates a test server with the given ConnectRPC handler options.
func setupServerWithInterceptors(
	t *testing.T,
	opts ...connect.HandlerOption,
) bfdv1connect.BfdServiceClient {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)

	path, handler := server.New(mgr, nil, logger, opts...)
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return bfdv1connect.NewBfdServiceClient(srv.Client(), srv.URL)
}

// setupPanicServer creates a test server that panics on AddSession,
// using the given handler options (interceptors).
func setupPanicServer(
	t *testing.T,
	opts ...connect.HandlerOption,
) bfdv1connect.BfdServiceClient {
	t.Helper()

	path, handler := bfdv1connect.NewBfdServiceHandler(panicHandler{}, opts...)
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return bfdv1connect.NewBfdServiceClient(srv.Client(), srv.URL)
}

// -------------------------------------------------------------------------
// TestLoggingInterceptor
// -------------------------------------------------------------------------

func TestLoggingInterceptorSuccess(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupServerWithInterceptors(t, server.LoggingInterceptorOption(logger))

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
	client := setupServerWithInterceptors(t, server.LoggingInterceptorOption(logger))

	_, err := client.DeleteSession(context.Background(), &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: 99999,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T: %v", err, err)
	}
	if connectErr.Code() != connect.CodeNotFound {
		t.Errorf("code = %s, want NotFound", connectErr.Code())
	}
}

// -------------------------------------------------------------------------
// TestRecoveryInterceptor
// -------------------------------------------------------------------------

func TestRecoveryInterceptorNoPanic(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupServerWithInterceptors(t, server.RecoveryInterceptorOption(logger))

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
	client := setupPanicServer(t, server.RecoveryInterceptorOption(logger))

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

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T: %v", err, err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Errorf("code = %s, want Internal", connectErr.Code())
	}
}

// -------------------------------------------------------------------------
// TestBothInterceptors — logging + recovery together
// -------------------------------------------------------------------------

func TestBothInterceptors(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client := setupServerWithInterceptors(t,
		server.LoggingInterceptorOption(logger),
		server.RecoveryInterceptorOption(logger),
	)

	resp, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
}
