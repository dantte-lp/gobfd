package gobgp

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"

	gobgpapi "github.com/osrg/gobgp/v4/api"
	"google.golang.org/grpc"
)

// recordingGoBGPService records peer actuation requests received through the
// GoBGP v4 GoBgpService gRPC service.
type recordingGoBGPService struct {
	gobgpapi.UnimplementedGoBgpServiceServer

	mu       sync.Mutex
	disabled []*gobgpapi.DisablePeerRequest
	enabled  []*gobgpapi.EnablePeerRequest
}

func (s *recordingGoBGPService) DisablePeer(
	_ context.Context, req *gobgpapi.DisablePeerRequest,
) (*gobgpapi.DisablePeerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled = append(s.disabled, req)
	return &gobgpapi.DisablePeerResponse{}, nil
}

func (s *recordingGoBGPService) EnablePeer(
	_ context.Context, req *gobgpapi.EnablePeerRequest,
) (*gobgpapi.EnablePeerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = append(s.enabled, req)
	return &gobgpapi.EnablePeerResponse{}, nil
}

// TestGRPCClientDrivesGoBGPv4Service verifies that the client reaches the
// GoBGP v4 service path with the canonical peer address and reason. GoBGP v3
// serves /apipb.GobgpApi and v4 serves /api.GoBgpService, so a client built
// against the wrong major version fails with Unimplemented.
func TestGRPCClientDrivesGoBGPv4Service(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	service := &recordingGoBGPService{}
	gobgpapi.RegisterGoBgpServiceServer(server, service)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
	})

	client, err := NewGRPCClient(
		GRPCClientConfig{Addr: listener.Addr().String()},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	if err := client.DisablePeer(t.Context(), "192.0.2.1", "BFD Down"); err != nil {
		t.Fatalf("DisablePeer: %v", err)
	}
	if err := client.EnablePeer(t.Context(), "192.0.2.1"); err != nil {
		t.Fatalf("EnablePeer: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.disabled) != 1 || service.disabled[0].GetAddress() != "192.0.2.1" ||
		service.disabled[0].GetCommunication() != "BFD Down" {
		t.Fatalf("DisablePeer requests = %v, want one for 192.0.2.1 with reason", service.disabled)
	}
	if len(service.enabled) != 1 || service.enabled[0].GetAddress() != "192.0.2.1" {
		t.Fatalf("EnablePeer requests = %v, want one for 192.0.2.1", service.enabled)
	}
}
