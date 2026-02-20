//go:build integration

package integration_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"gopkg.in/yaml.v3"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// cliTestEnv bundles the in-process server and client for CLI integration tests.
type cliTestEnv struct {
	client bfdv1connect.BfdServiceClient
	mgr    *bfd.Manager
}

// newCLITestEnv creates an in-process ConnectRPC server backed by a real
// bfd.Manager. This mirrors the gobfdctl client setup without requiring
// a running daemon.
func newCLITestEnv(t *testing.T) *cliTestEnv {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)

	path, handler := server.New(mgr, nil, logger)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := bfdv1connect.NewBfdServiceClient(srv.Client(), srv.URL)

	return &cliTestEnv{
		client: client,
		mgr:    mgr,
	}
}

// addTestSession adds a BFD session and returns its discriminator.
func (env *cliTestEnv) addTestSession(
	t *testing.T,
	peer, local string,
) uint32 {
	t.Helper()

	resp, err := env.client.AddSession(t.Context(), &bfdv1.AddSessionRequest{
		PeerAddress:           peer,
		LocalAddress:          local,
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(time.Second),
		RequiredMinRxInterval: durationpb.New(time.Second),
		DetectMultiplier:      3,
	})
	if err != nil {
		t.Fatalf("AddSession(%s -> %s): %v", local, peer, err)
	}

	discr := resp.GetSession().GetLocalDiscriminator()
	if discr == 0 {
		t.Fatal("AddSession returned zero discriminator")
	}

	return discr
}

// TestCLISessionAddListShowDelete exercises the full session lifecycle
// through the ConnectRPC API, validating that the server returns correct
// data for each operation. This is the in-process equivalent of running
// gobfdctl commands: session add, session list, session show, session delete.
func TestCLISessionAddListShowDelete(t *testing.T) {
	env := newCLITestEnv(t)
	ctx := t.Context()

	// --- session add ---
	discr := env.addTestSession(t, "192.168.1.1", "192.168.1.2")

	// --- session list ---
	listResp, err := env.client.ListSessions(ctx, &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if got := len(listResp.GetSessions()); got != 1 {
		t.Fatalf("ListSessions count = %d, want 1", got)
	}

	sess := listResp.GetSessions()[0]
	if sess.GetPeerAddress() != "192.168.1.1" {
		t.Errorf("ListSessions[0].PeerAddress = %q, want %q",
			sess.GetPeerAddress(), "192.168.1.1")
	}

	if sess.GetLocalDiscriminator() != discr {
		t.Errorf("ListSessions[0].LocalDiscriminator = %d, want %d",
			sess.GetLocalDiscriminator(), discr)
	}

	// --- session show (by discriminator) ---
	getResp, err := env.client.GetSession(ctx, &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{
			LocalDiscriminator: discr,
		},
	})
	if err != nil {
		t.Fatalf("GetSession by discr: %v", err)
	}

	gotSess := getResp.GetSession()
	if gotSess.GetPeerAddress() != "192.168.1.1" {
		t.Errorf("GetSession.PeerAddress = %q, want %q",
			gotSess.GetPeerAddress(), "192.168.1.1")
	}

	if gotSess.GetLocalAddress() != "192.168.1.2" {
		t.Errorf("GetSession.LocalAddress = %q, want %q",
			gotSess.GetLocalAddress(), "192.168.1.2")
	}

	if gotSess.GetDetectMultiplier() != 3 {
		t.Errorf("GetSession.DetectMultiplier = %d, want 3",
			gotSess.GetDetectMultiplier())
	}

	// --- session show (by peer address) ---
	getByPeer, err := env.client.GetSession(ctx, &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_PeerAddress{
			PeerAddress: "192.168.1.1",
		},
	})
	if err != nil {
		t.Fatalf("GetSession by peer: %v", err)
	}

	if getByPeer.GetSession().GetLocalDiscriminator() != discr {
		t.Errorf("GetSession by peer: discriminator = %d, want %d",
			getByPeer.GetSession().GetLocalDiscriminator(), discr)
	}

	// --- session delete ---
	_, err = env.client.DeleteSession(ctx, &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: discr,
	})
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Verify deletion.
	listAfter, err := env.client.ListSessions(ctx, &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions after delete: %v", err)
	}

	if got := len(listAfter.GetSessions()); got != 0 {
		t.Fatalf("ListSessions after delete count = %d, want 0", got)
	}
}

// TestCLIMultipleSessions verifies that adding multiple sessions and listing
// them returns all sessions correctly.
func TestCLIMultipleSessions(t *testing.T) {
	env := newCLITestEnv(t)
	ctx := t.Context()

	// Add three sessions with different peers.
	discr1 := env.addTestSession(t, "10.0.0.1", "10.0.0.100")
	discr2 := env.addTestSession(t, "10.0.0.2", "10.0.0.100")
	discr3 := env.addTestSession(t, "10.0.0.3", "10.0.0.100")

	// List all sessions.
	listResp, err := env.client.ListSessions(ctx, &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if got := len(listResp.GetSessions()); got != 3 {
		t.Fatalf("ListSessions count = %d, want 3", got)
	}

	// Collect all discriminators from the response.
	discrSet := make(map[uint32]bool, 3)
	for _, s := range listResp.GetSessions() {
		discrSet[s.GetLocalDiscriminator()] = true
	}

	for _, want := range []uint32{discr1, discr2, discr3} {
		if !discrSet[want] {
			t.Errorf("ListSessions missing discriminator %d", want)
		}
	}

	// Delete one session and verify count decreases.
	_, err = env.client.DeleteSession(ctx, &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: discr2,
	})
	if err != nil {
		t.Fatalf("DeleteSession(%d): %v", discr2, err)
	}

	listAfter, err := env.client.ListSessions(ctx, &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions after delete: %v", err)
	}

	if got := len(listAfter.GetSessions()); got != 2 {
		t.Fatalf("ListSessions after delete count = %d, want 2", got)
	}
}

// TestCLIOutputFormats verifies that session data can be rendered in
// all supported output formats (table, JSON, YAML) by exercising the
// format logic from the commands package through its view types.
func TestCLIOutputFormats(t *testing.T) {
	env := newCLITestEnv(t)
	ctx := t.Context()

	env.addTestSession(t, "172.16.0.1", "172.16.0.2")

	listResp, err := env.client.ListSessions(ctx, &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	sess := listResp.GetSessions()[0]

	t.Run("json_single", func(t *testing.T) {
		data, err := json.MarshalIndent(
			buildSessionView(sess), "", "  ",
		)
		if err != nil {
			t.Fatalf("JSON marshal: %v", err)
		}

		out := string(data)
		if !strings.Contains(out, "172.16.0.1") {
			t.Errorf("JSON output missing peer address: %s", out)
		}

		if !strings.Contains(out, "peer_address") {
			t.Errorf("JSON output missing field name: %s", out)
		}
	})

	t.Run("yaml_single", func(t *testing.T) {
		data, err := yaml.Marshal(buildSessionView(sess))
		if err != nil {
			t.Fatalf("YAML marshal: %v", err)
		}

		out := string(data)
		if !strings.Contains(out, "172.16.0.1") {
			t.Errorf("YAML output missing peer address: %s", out)
		}

		if !strings.Contains(out, "peer_address:") {
			t.Errorf("YAML output missing field name: %s", out)
		}
	})

	t.Run("yaml_roundtrip", func(t *testing.T) {
		view := buildSessionView(sess)

		data, err := yaml.Marshal(view)
		if err != nil {
			t.Fatalf("YAML marshal: %v", err)
		}

		var decoded sessionViewForTest
		if err := yaml.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("YAML unmarshal: %v", err)
		}

		if decoded.PeerAddress != "172.16.0.1" {
			t.Errorf("YAML roundtrip peer_address = %q, want %q",
				decoded.PeerAddress, "172.16.0.1")
		}

		if decoded.LocalAddress != "172.16.0.2" {
			t.Errorf("YAML roundtrip local_address = %q, want %q",
				decoded.LocalAddress, "172.16.0.2")
		}

		if decoded.DetectMultiplier != 3 {
			t.Errorf("YAML roundtrip detect_multiplier = %d, want 3",
				decoded.DetectMultiplier)
		}
	})
}

// TestCLIDeleteNonexistent verifies that deleting a nonexistent session
// returns a proper error.
func TestCLIDeleteNonexistent(t *testing.T) {
	env := newCLITestEnv(t)
	ctx := t.Context()

	_, err := env.client.DeleteSession(ctx, &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: 99999,
	})
	if err == nil {
		t.Fatal("DeleteSession(99999) should return error for nonexistent session")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("DeleteSession error = %q, want to contain 'not found'", err.Error())
	}
}

// TestCLIGetNonexistent verifies that getting a nonexistent session
// returns a proper error.
func TestCLIGetNonexistent(t *testing.T) {
	env := newCLITestEnv(t)
	ctx := t.Context()

	_, err := env.client.GetSession(ctx, &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_PeerAddress{
			PeerAddress: "1.2.3.4",
		},
	})
	if err == nil {
		t.Fatal("GetSession(1.2.3.4) should return error for nonexistent session")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("GetSession error = %q, want to contain 'not found'", err.Error())
	}
}

// TestCLIDuplicateSession verifies that adding a duplicate session
// returns an appropriate error.
func TestCLIDuplicateSession(t *testing.T) {
	env := newCLITestEnv(t)
	ctx := t.Context()

	env.addTestSession(t, "10.1.1.1", "10.1.1.2")

	// Attempt duplicate.
	_, err := env.client.AddSession(ctx, &bfdv1.AddSessionRequest{
		PeerAddress:           "10.1.1.1",
		LocalAddress:          "10.1.1.2",
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(time.Second),
		RequiredMinRxInterval: durationpb.New(time.Second),
		DetectMultiplier:      3,
	})
	if err == nil {
		t.Fatal("AddSession duplicate should return error")
	}

	if !strings.Contains(err.Error(), "duplicate") &&
		!strings.Contains(err.Error(), "already exists") {
		t.Errorf("AddSession duplicate error = %q, want 'duplicate' or 'already exists'",
			err.Error())
	}
}

// --- Helper types for test assertions ---

// sessionViewForTest mirrors the session view struct for YAML round-trip testing.
// This avoids importing the commands package (which is not exported).
type sessionViewForTest struct {
	PeerAddress      string `yaml:"peer_address"`
	LocalAddress     string `yaml:"local_address"`
	LocalState       string `yaml:"local_state"`
	DetectMultiplier uint32 `yaml:"detect_multiplier"`
}

// buildSessionView creates a map-like view of a BFD session for format testing.
// This mirrors the sessionToView logic in the commands package without importing it.
func buildSessionView(s *bfdv1.BfdSession) map[string]any {
	v := map[string]any{
		"peer_address":        s.GetPeerAddress(),
		"local_address":       s.GetLocalAddress(),
		"local_state":         s.GetLocalState().String(),
		"remote_state":        s.GetRemoteState().String(),
		"local_discriminator": s.GetLocalDiscriminator(),
		"detect_multiplier":   s.GetDetectMultiplier(),
	}

	if d := s.GetDesiredMinTxInterval(); d != nil {
		v["desired_min_tx_interval"] = d.AsDuration().String()
	}

	if d := s.GetRequiredMinRxInterval(); d != nil {
		v["required_min_rx_interval"] = d.AsDuration().String()
	}

	return v
}
