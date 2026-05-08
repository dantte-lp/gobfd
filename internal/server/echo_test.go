package server

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func newTestEchoServer(t *testing.T) *EchoServer {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	return &EchoServer{
		manager:       mgr,
		senderFactory: noopSenderFactory{},
		logger:        logger,
	}
}

func TestEchoServer_AddEchoSession_HappyPath(t *testing.T) {
	t.Parallel()

	s := newTestEchoServer(t)
	resp, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
		PeerAddress:      "192.0.2.10",
		LocalAddress:     "192.0.2.1",
		TxInterval:       durationpb.New(50 * time.Millisecond),
		DetectMultiplier: 3,
	})
	if err != nil {
		t.Fatalf("AddEchoSession: %v", err)
	}
	if resp.GetSession().GetLocalDiscriminator() == 0 {
		t.Fatalf("expected non-zero local discriminator")
	}
	if got, want := resp.GetSession().GetPeerAddress(), "192.0.2.10"; got != want {
		t.Errorf("peer_address: got %q, want %q", got, want)
	}
	if got, want := resp.GetSession().GetDetectMultiplier(), uint32(3); got != want {
		t.Errorf("detect_multiplier: got %d, want %d", got, want)
	}
}

func TestEchoServer_AddEchoSession_RejectsZeroTxInterval(t *testing.T) {
	t.Parallel()

	s := newTestEchoServer(t)
	_, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
		PeerAddress:      "192.0.2.10",
		TxInterval:       nil,
		DetectMultiplier: 3,
	})
	if err == nil {
		t.Fatalf("expected error for nil tx_interval")
	}
}

func TestEchoServer_AddEchoSession_RejectsZeroDetectMultiplier(t *testing.T) {
	t.Parallel()

	s := newTestEchoServer(t)
	_, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
		PeerAddress:      "192.0.2.10",
		TxInterval:       durationpb.New(50 * time.Millisecond),
		DetectMultiplier: 0,
	})
	if err == nil {
		t.Fatalf("expected error for zero detect_multiplier")
	}
}

func TestEchoServer_AddEchoSession_RejectsBadPeerAddress(t *testing.T) {
	t.Parallel()

	s := newTestEchoServer(t)
	_, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
		PeerAddress:      "not-an-ip",
		TxInterval:       durationpb.New(50 * time.Millisecond),
		DetectMultiplier: 3,
	})
	if err == nil {
		t.Fatalf("expected error for invalid peer address")
	}
}

func TestEchoServer_ListEchoSessions_ReturnsAll(t *testing.T) {
	t.Parallel()

	s := newTestEchoServer(t)
	for i, peer := range []string{"192.0.2.10", "192.0.2.11", "192.0.2.12"} {
		if _, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
			PeerAddress:      peer,
			LocalAddress:     "192.0.2.1",
			TxInterval:       durationpb.New(50 * time.Millisecond),
			DetectMultiplier: 3,
		}); err != nil {
			t.Fatalf("AddEchoSession #%d: %v", i, err)
		}
	}

	resp, err := s.ListEchoSessions(context.Background(), &bfdv1.ListEchoSessionsRequest{})
	if err != nil {
		t.Fatalf("ListEchoSessions: %v", err)
	}
	if got, want := len(resp.GetSessions()), 3; got != want {
		t.Fatalf("session count: got %d, want %d", got, want)
	}
}

func TestEchoServer_DeleteEchoSession_RemovesByDiscriminator(t *testing.T) {
	t.Parallel()

	s := newTestEchoServer(t)
	add, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
		PeerAddress:      "192.0.2.10",
		LocalAddress:     "192.0.2.1",
		TxInterval:       durationpb.New(50 * time.Millisecond),
		DetectMultiplier: 3,
	})
	if err != nil {
		t.Fatalf("AddEchoSession: %v", err)
	}
	discr := add.GetSession().GetLocalDiscriminator()

	if _, derr := s.DeleteEchoSession(context.Background(), &bfdv1.DeleteEchoSessionRequest{
		LocalDiscriminator: discr,
	}); derr != nil {
		t.Fatalf("DeleteEchoSession: %v", derr)
	}

	list, err := s.ListEchoSessions(context.Background(), &bfdv1.ListEchoSessionsRequest{})
	if err != nil {
		t.Fatalf("ListEchoSessions: %v", err)
	}
	if got := len(list.GetSessions()); got != 0 {
		t.Fatalf("expected zero sessions after delete, got %d", got)
	}
}
