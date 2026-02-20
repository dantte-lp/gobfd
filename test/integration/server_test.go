//go:build integration

package integration_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestServerSessionLifecycle(t *testing.T) {
	// Start in-process ConnectRPC server backed by a real Manager.
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)

	path, handler := server.New(mgr, logger)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := bfdv1connect.NewBfdServiceClient(srv.Client(), srv.URL)
	ctx := t.Context()

	// --- AddSession ---
	addResp, err := client.AddSession(ctx, &bfdv1.AddSessionRequest{
		PeerAddress:           "10.0.0.1",
		LocalAddress:          "10.0.0.2",
		Type:                  bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(time.Second),
		RequiredMinRxInterval: durationpb.New(time.Second),
		DetectMultiplier:      3,
	})
	if err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	if addResp.GetSession() == nil {
		t.Fatal("AddSession returned nil session")
	}
	discr := addResp.GetSession().GetLocalDiscriminator()
	if discr == 0 {
		t.Fatal("AddSession returned zero discriminator")
	}
	if addResp.GetSession().GetPeerAddress() != "10.0.0.1" {
		t.Errorf("AddSession peer address = %q, want %q",
			addResp.GetSession().GetPeerAddress(), "10.0.0.1")
	}

	// --- ListSessions: expect 1 session ---
	listResp, err := client.ListSessions(ctx, &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if got := len(listResp.GetSessions()); got != 1 {
		t.Fatalf("ListSessions count = %d, want 1", got)
	}
	if listResp.GetSessions()[0].GetLocalDiscriminator() != discr {
		t.Errorf("ListSessions discriminator = %d, want %d",
			listResp.GetSessions()[0].GetLocalDiscriminator(), discr)
	}

	// --- GetSession by discriminator ---
	getResp, err := client.GetSession(ctx, &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{
			LocalDiscriminator: discr,
		},
	})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if getResp.GetSession().GetLocalDiscriminator() != discr {
		t.Errorf("GetSession discriminator = %d, want %d",
			getResp.GetSession().GetLocalDiscriminator(), discr)
	}
	if getResp.GetSession().GetPeerAddress() != "10.0.0.1" {
		t.Errorf("GetSession peer address = %q, want %q",
			getResp.GetSession().GetPeerAddress(), "10.0.0.1")
	}

	// --- DeleteSession ---
	_, err = client.DeleteSession(ctx, &bfdv1.DeleteSessionRequest{
		LocalDiscriminator: discr,
	})
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// --- ListSessions: expect 0 sessions ---
	listResp, err = client.ListSessions(ctx, &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions after delete: %v", err)
	}
	if got := len(listResp.GetSessions()); got != 0 {
		t.Fatalf("ListSessions after delete count = %d, want 0", got)
	}
}
