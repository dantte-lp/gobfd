package gobgp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	apipb "github.com/osrg/gobgp/v4/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestBuildTransportCredentialsUsesTLSWhenEnabled(t *testing.T) {
	t.Parallel()

	creds, err := buildTransportCredentials(GRPCClientTLSConfig{
		Enabled:    true,
		ServerName: "gobgp.example.net",
	})
	if err != nil {
		t.Fatalf("buildTransportCredentials: %v", err)
	}

	info := creds.Info()
	if info.SecurityProtocol != "tls" {
		t.Fatalf("SecurityProtocol = %q, want tls", info.SecurityProtocol)
	}
}

func TestBuildTransportCredentialsLoadsCAFile(t *testing.T) {
	t.Parallel()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, testCACertPEM(t), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	creds, err := buildTransportCredentials(GRPCClientTLSConfig{
		Enabled:    true,
		CAFile:     caPath,
		ServerName: "gobgp.example.net",
	})
	if err != nil {
		t.Fatalf("buildTransportCredentials: %v", err)
	}

	if got := creds.Info().SecurityProtocol; got != "tls" {
		t.Fatalf("SecurityProtocol = %q, want tls", got)
	}
}

func TestBuildTransportCredentialsRejectsInvalidCAFile(t *testing.T) {
	t.Parallel()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	_, err := buildTransportCredentials(GRPCClientTLSConfig{
		Enabled: true,
		CAFile:  caPath,
	})
	if err == nil {
		t.Fatal("expected invalid CA file error")
	}
}

func TestNewGRPCClient_EmptyAddr(t *testing.T) {
	t.Parallel()

	_, err := NewGRPCClient(GRPCClientConfig{Addr: ""}, slog.Default())
	if err == nil {
		t.Fatal("expected error for empty address")
	}
	if !errors.Is(err, ErrDialFailed) {
		t.Fatalf("expected ErrDialFailed, got: %v", err)
	}
}

// fakeGoBGPService implements apipb.GoBgpServiceServer for in-memory testing.
type fakeGoBGPService struct {
	apipb.UnimplementedGoBgpServiceServer

	mu          sync.Mutex
	disableReqs []*apipb.DisablePeerRequest
	enableReqs  []*apipb.EnablePeerRequest
	disableErr  error
	enableErr   error
}

func (s *fakeGoBGPService) DisablePeer(_ context.Context, req *apipb.DisablePeerRequest) (*apipb.DisablePeerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disableReqs = append(s.disableReqs, req)
	if s.disableErr != nil {
		return nil, s.disableErr
	}
	return &apipb.DisablePeerResponse{}, nil
}

func (s *fakeGoBGPService) EnablePeer(_ context.Context, req *apipb.EnablePeerRequest) (*apipb.EnablePeerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enableReqs = append(s.enableReqs, req)
	if s.enableErr != nil {
		return nil, s.enableErr
	}
	return &apipb.EnablePeerResponse{}, nil
}

func startTestGoBGPServer(t *testing.T, srv apipb.GoBgpServiceServer) (*GRPCClient, func()) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	apipb.RegisterGoBgpServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcServer.Stop()
		_ = lis.Close()
		t.Fatalf("dial bufnet: %v", err)
	}

	client := &GRPCClient{
		conn: conn,
		api:  apipb.NewGoBgpServiceClient(conn),
		logger: slog.Default().With(
			slog.String("component", "gobgp.client.test"),
		),
	}

	cleanup := func() {
		_ = client.Close()
		grpcServer.GracefulStop()
		_ = lis.Close()
	}

	return client, cleanup
}

func TestGRPCClient_DisablePeer_Success(t *testing.T) {
	t.Parallel()

	fake := &fakeGoBGPService{}
	client, cleanup := startTestGoBGPServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peerAddr := "192.0.2.1"
	reason := "BFD Down (RFC 9384 Cease/10): diag=Control Detection Time Expired"

	if err := client.DisablePeer(ctx, peerAddr, reason); err != nil {
		t.Fatalf("DisablePeer failed: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()

	if len(fake.disableReqs) != 1 {
		t.Fatalf("expected 1 DisablePeerRequest, got %d", len(fake.disableReqs))
	}
	if fake.disableReqs[0].GetAddress() != peerAddr {
		t.Errorf("Address = %q, want %q", fake.disableReqs[0].GetAddress(), peerAddr)
	}
	if fake.disableReqs[0].GetCommunication() != reason {
		t.Errorf("Communication = %q, want %q", fake.disableReqs[0].GetCommunication(), reason)
	}
}

func TestGRPCClient_DisablePeer_ServerError(t *testing.T) {
	t.Parallel()

	fake := &fakeGoBGPService{
		disableErr: errors.New("peer not found in RIB"),
	}
	client, cleanup := startTestGoBGPServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.DisablePeer(ctx, "192.0.2.1", "BFD Down")
	if err == nil {
		t.Fatal("expected error from DisablePeer, got nil")
	}
}

func TestGRPCClient_EnablePeer_Success(t *testing.T) {
	t.Parallel()

	fake := &fakeGoBGPService{}
	client, cleanup := startTestGoBGPServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peerAddr := "192.0.2.1"
	if err := client.EnablePeer(ctx, peerAddr); err != nil {
		t.Fatalf("EnablePeer failed: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()

	if len(fake.enableReqs) != 1 {
		t.Fatalf("expected 1 EnablePeerRequest, got %d", len(fake.enableReqs))
	}
	if fake.enableReqs[0].GetAddress() != peerAddr {
		t.Errorf("Address = %q, want %q", fake.enableReqs[0].GetAddress(), peerAddr)
	}
}

func TestGRPCClient_EnablePeer_ServerError(t *testing.T) {
	t.Parallel()

	fake := &fakeGoBGPService{
		enableErr: errors.New("failed to enable peer"),
	}
	client, cleanup := startTestGoBGPServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.EnablePeer(ctx, "192.0.2.1")
	if err == nil {
		t.Fatal("expected error from EnablePeer, got nil")
	}
}

func TestGRPCClient_Closed(t *testing.T) {
	t.Parallel()

	fake := &fakeGoBGPService{}
	client, cleanup := startTestGoBGPServer(t, fake)
	defer cleanup()

	if err := client.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}

	// Idempotent close.
	if err := client.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}

	ctx := context.Background()

	err := client.DisablePeer(ctx, "192.0.2.1", "test")
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("DisablePeer after Close: expected ErrClientClosed, got %v", err)
	}

	err = client.EnablePeer(ctx, "192.0.2.1")
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("EnablePeer after Close: expected ErrClientClosed, got %v", err)
	}
}

func testCACertPEM(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "gobfd-test-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
}
