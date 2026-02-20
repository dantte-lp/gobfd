package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

const (
	// testPeerAddr is a documentation IP address (RFC 5737) used as peer in tests.
	testPeerAddr = "192.0.2.1"
	// testLocalAddr is a documentation IP address (RFC 5737) used as local in tests.
	testLocalAddr = "192.0.2.2"
)

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// setupTestServer creates a real HTTP server backed by a BFD Manager and
// returns a ConnectRPC client connected to it. The server and manager are
// cleaned up when the test finishes.
func setupTestServer(t *testing.T) bfdv1connect.BfdServiceClient {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)

	path, handler := server.New(mgr, nil, logger)
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return bfdv1connect.NewBfdServiceClient(srv.Client(), srv.URL)
}

// validAddRequest returns a valid AddSessionRequest for testing.
func validAddRequest() *bfdv1.AddSessionRequest {
	return &bfdv1.AddSessionRequest{
		PeerAddress:           testPeerAddr,
		LocalAddress:          testLocalAddr,
		InterfaceName:         "eth0",
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(time.Second),
		RequiredMinRxInterval: durationpb.New(time.Second),
		DetectMultiplier:      3,
	}
}

// -------------------------------------------------------------------------
// TestAddSession
// -------------------------------------------------------------------------

func TestAddSession(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	req := validAddRequest()
	resp, err := client.AddSession(context.Background(), req)
	if err != nil {
		t.Fatalf("AddSession: %v", err)
	}

	sess := resp.GetSession()
	if sess == nil {
		t.Fatal("response session is nil")
	}

	if sess.GetPeerAddress() != testPeerAddr {
		t.Errorf("PeerAddress = %q, want %q", sess.GetPeerAddress(), testPeerAddr)
	}
	if sess.GetLocalAddress() != testLocalAddr {
		t.Errorf("LocalAddress = %q, want %q", sess.GetLocalAddress(), testLocalAddr)
	}
	if sess.GetInterfaceName() != "eth0" {
		t.Errorf("InterfaceName = %q, want %q", sess.GetInterfaceName(), "eth0")
	}
	if sess.GetType() != bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP {
		t.Errorf("Type = %s, want SINGLE_HOP", sess.GetType())
	}
	if sess.GetLocalState() != bfdv1.SessionState_SESSION_STATE_DOWN {
		t.Errorf("LocalState = %s, want DOWN", sess.GetLocalState())
	}
	if sess.GetLocalDiscriminator() == 0 {
		t.Error("LocalDiscriminator is zero")
	}
	if sess.GetDetectMultiplier() != 3 {
		t.Errorf("DetectMultiplier = %d, want 3", sess.GetDetectMultiplier())
	}
}

// -------------------------------------------------------------------------
// TestAddSessionInvalidArgs
// -------------------------------------------------------------------------

func TestAddSessionInvalidArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *bfdv1.AddSessionRequest
	}{
		{
			name: "invalid peer address",
			req: &bfdv1.AddSessionRequest{
				PeerAddress:           "not-an-ip",
				LocalAddress:          testLocalAddr,
				Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
				DesiredMinTxInterval:  durationpb.New(time.Second),
				RequiredMinRxInterval: durationpb.New(time.Second),
				DetectMultiplier:      3,
			},
		},
		{
			name: "zero detect multiplier",
			req: &bfdv1.AddSessionRequest{
				PeerAddress:           testPeerAddr,
				LocalAddress:          testLocalAddr,
				Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
				DesiredMinTxInterval:  durationpb.New(time.Second),
				RequiredMinRxInterval: durationpb.New(time.Second),
				DetectMultiplier:      0,
			},
		},
		{
			name: "unspecified session type",
			req: &bfdv1.AddSessionRequest{
				PeerAddress:           testPeerAddr,
				LocalAddress:          testLocalAddr,
				Type:                  bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED,
				DesiredMinTxInterval:  durationpb.New(time.Second),
				RequiredMinRxInterval: durationpb.New(time.Second),
				DetectMultiplier:      3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := setupTestServer(t)

			_, err := client.AddSession(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("expected connect.Error, got %T: %v", err, err)
			}
			if connectErr.Code() != connect.CodeInvalidArgument {
				t.Errorf("code = %s, want InvalidArgument", connectErr.Code())
			}
		})
	}
}

// -------------------------------------------------------------------------
// TestAddSessionDuplicate
// -------------------------------------------------------------------------

func TestAddSessionDuplicate(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	req := validAddRequest()

	// First add should succeed.
	_, err := client.AddSession(context.Background(), req)
	if err != nil {
		t.Fatalf("first AddSession: %v", err)
	}

	// Second add with same peer should fail with AlreadyExists.
	_, err = client.AddSession(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for duplicate session, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T: %v", err, err)
	}
	if connectErr.Code() != connect.CodeAlreadyExists {
		t.Errorf("code = %s, want AlreadyExists", connectErr.Code())
	}
}

// -------------------------------------------------------------------------
// TestDeleteSession
// -------------------------------------------------------------------------

func TestDeleteSession(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	// Create a session first.
	addResp, err := client.AddSession(context.Background(), validAddRequest())
	if err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	discr := addResp.GetSession().GetLocalDiscriminator()

	// Delete it.
	_, err = client.DeleteSession(context.Background(), &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: discr,
	})
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Verify it is gone via ListSessions.
	listResp, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(listResp.GetSessions()) != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", len(listResp.GetSessions()))
	}
}

// -------------------------------------------------------------------------
// TestDeleteSessionNotFound
// -------------------------------------------------------------------------

func TestDeleteSessionNotFound(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	_, err := client.DeleteSession(context.Background(), &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: 99999,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
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
// TestListSessions
// -------------------------------------------------------------------------

func TestListSessions(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	// Add two sessions with different peers.
	_, err := client.AddSession(context.Background(), validAddRequest())
	if err != nil {
		t.Fatalf("AddSession 1: %v", err)
	}

	req2 := &bfdv1.AddSessionRequest{
		PeerAddress:           "198.51.100.1",
		LocalAddress:          "198.51.100.2",
		InterfaceName:         "eth1",
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(500 * time.Millisecond),
		RequiredMinRxInterval: durationpb.New(500 * time.Millisecond),
		DetectMultiplier:      5,
	}
	_, err = client.AddSession(context.Background(), req2)
	if err != nil {
		t.Fatalf("AddSession 2: %v", err)
	}

	// List should return 2 sessions.
	listResp, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if len(listResp.GetSessions()) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(listResp.GetSessions()))
	}

	// Build a map by peer address for order-independent assertions.
	byPeer := make(map[string]*bfdv1.BfdSession, len(listResp.GetSessions()))
	for _, s := range listResp.GetSessions() {
		byPeer[s.GetPeerAddress()] = s
	}

	s1, ok := byPeer[testPeerAddr]
	if !ok {
		t.Fatal("session with peer " + testPeerAddr + " not found")
	}
	if s1.GetDetectMultiplier() != 3 {
		t.Errorf("session 1 DetectMultiplier = %d, want 3", s1.GetDetectMultiplier())
	}

	s2, ok := byPeer["198.51.100.1"]
	if !ok {
		t.Fatal("session with peer 198.51.100.1 not found")
	}
	if s2.GetDetectMultiplier() != 5 {
		t.Errorf("session 2 DetectMultiplier = %d, want 5", s2.GetDetectMultiplier())
	}
}

// -------------------------------------------------------------------------
// TestGetSessionByDiscriminator
// -------------------------------------------------------------------------

func TestGetSessionByDiscriminator(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	addResp, err := client.AddSession(context.Background(), validAddRequest())
	if err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	discr := addResp.GetSession().GetLocalDiscriminator()

	getResp, err := client.GetSession(context.Background(), &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{
			LocalDiscriminator: discr,
		},
	})
	if err != nil {
		t.Fatalf("GetSession by discriminator: %v", err)
	}

	sess := getResp.GetSession()
	if sess.GetLocalDiscriminator() != discr {
		t.Errorf("LocalDiscriminator = %d, want %d", sess.GetLocalDiscriminator(), discr)
	}
	if sess.GetPeerAddress() != testPeerAddr {
		t.Errorf("PeerAddress = %q, want %q", sess.GetPeerAddress(), testPeerAddr)
	}
}

// -------------------------------------------------------------------------
// TestGetSessionByPeerAddress
// -------------------------------------------------------------------------

func TestGetSessionByPeerAddress(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	_, err := client.AddSession(context.Background(), validAddRequest())
	if err != nil {
		t.Fatalf("AddSession: %v", err)
	}

	getResp, err := client.GetSession(context.Background(), &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_PeerAddress{
			PeerAddress: testPeerAddr,
		},
	})
	if err != nil {
		t.Fatalf("GetSession by peer address: %v", err)
	}

	sess := getResp.GetSession()
	if sess.GetPeerAddress() != testPeerAddr {
		t.Errorf("PeerAddress = %q, want %q", sess.GetPeerAddress(), testPeerAddr)
	}
	if sess.GetLocalDiscriminator() == 0 {
		t.Error("LocalDiscriminator is zero")
	}
}

// -------------------------------------------------------------------------
// TestGetSessionNotFound
// -------------------------------------------------------------------------

func TestGetSessionNotFound(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)

	tests := []struct {
		name string
		req  *bfdv1.GetSessionRequest
	}{
		{
			name: "nonexistent discriminator",
			req: &bfdv1.GetSessionRequest{
				Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{
					LocalDiscriminator: 99999,
				},
			},
		},
		{
			name: "nonexistent peer address",
			req: &bfdv1.GetSessionRequest{
				Identifier: &bfdv1.GetSessionRequest_PeerAddress{
					PeerAddress: "10.0.0.1",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := client.GetSession(context.Background(), tt.req)
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
		})
	}
}
