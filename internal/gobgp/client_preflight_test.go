package gobgp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"

	gobgpapi "github.com/osrg/gobgp/v4/api"
	"google.golang.org/grpc"
)

// getBgpService answers GetBgp like a running GoBGP v4 daemon.
type getBgpService struct {
	gobgpapi.UnimplementedGoBgpServiceServer
}

func (getBgpService) GetBgp(context.Context, *gobgpapi.GetBgpRequest) (*gobgpapi.GetBgpResponse, error) {
	return &gobgpapi.GetBgpResponse{Global: &gobgpapi.Global{Asn: 65001}}, nil
}

// startPreflightServer serves register's services on loopback and returns
// the listen address.
func startPreflightServer(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	register(server)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if serveErr := <-served; serveErr != nil {
			t.Errorf("serve: %v", serveErr)
		}
	})
	return listener.Addr().String()
}

func newPreflightClient(t *testing.T, addr string) *GRPCClient {
	t.Helper()

	client, err := NewGRPCClient(GRPCClientConfig{Addr: addr}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})
	return client
}

func TestCheckCompatibilityAcceptsGoBGPv4(t *testing.T) {
	t.Parallel()

	addr := startPreflightServer(t, func(s *grpc.Server) {
		gobgpapi.RegisterGoBgpServiceServer(s, getBgpService{})
	})
	if err := newPreflightClient(t, addr).CheckCompatibility(t.Context()); err != nil {
		t.Fatalf("CheckCompatibility() = %v, want nil", err)
	}
}

// TestCheckCompatibilityRejectsMissingV4Service covers a GoBGP v3 daemon or any
// other gRPC server: neither serves /api.GoBgpService.
func TestCheckCompatibilityRejectsMissingV4Service(t *testing.T) {
	t.Parallel()

	addr := startPreflightServer(t, func(*grpc.Server) {})
	err := newPreflightClient(t, addr).CheckCompatibility(t.Context())
	if !errors.Is(err, ErrIncompatibleServer) {
		t.Fatalf("CheckCompatibility() = %v, want ErrIncompatibleServer", err)
	}
}

func TestCheckCompatibilityReportsUnreachableSeparately(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatalf("close listener: %v", closeErr)
	}

	err = newPreflightClient(t, addr).CheckCompatibility(t.Context())
	if err == nil || errors.Is(err, ErrIncompatibleServer) {
		t.Fatalf("CheckCompatibility() = %v, want a non-incompatibility error", err)
	}
}
