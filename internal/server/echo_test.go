package server

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

type echoLeaseTestSenderFactory struct {
	opens  int
	closes int
}

func (f *echoLeaseTestSenderFactory) CreateSender(
	_ netip.Addr,
	_ bool,
	_ *slog.Logger,
) (bfd.PacketSender, uint16, error) {
	f.opens++
	return noopSender{}, uint16(50000 + f.opens), nil
}

func (f *echoLeaseTestSenderFactory) CloseSender(_ uint16) error {
	f.closes++
	return nil
}

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

func TestEchoServer_DeleteEchoSession_ReleasesSenderExactlyOnce(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	factory := &echoLeaseTestSenderFactory{}
	s := &EchoServer{manager: mgr, senderFactory: factory, logger: logger}
	add, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
		PeerAddress:      "192.0.2.20",
		LocalAddress:     "192.0.2.1",
		TxInterval:       durationpb.New(50 * time.Millisecond),
		DetectMultiplier: 3,
	})
	if err != nil {
		t.Fatalf("AddEchoSession: %v", err)
	}
	if factory.opens != 1 {
		t.Fatalf("sender opens = %d, want 1", factory.opens)
	}

	if _, err := s.DeleteEchoSession(context.Background(), &bfdv1.DeleteEchoSessionRequest{
		LocalDiscriminator: add.GetSession().GetLocalDiscriminator(),
	}); err != nil {
		t.Fatalf("DeleteEchoSession: %v", err)
	}
	if factory.closes != 1 {
		t.Errorf("sender closes after delete = %d, want 1", factory.closes)
	}

	mgr.Close()
	if factory.closes != 1 {
		t.Errorf("sender closes after delete and Manager.Close = %d, want 1", factory.closes)
	}
}

func TestEchoServer_AddEchoSession_DoesNotOpenSenderAfterManagerClose(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	mgr.Close()
	factory := &echoLeaseTestSenderFactory{}
	s := &EchoServer{manager: mgr, senderFactory: factory, logger: logger}
	_, err := s.AddEchoSession(context.Background(), &bfdv1.AddEchoSessionRequest{
		PeerAddress:      "192.0.2.21",
		LocalAddress:     "192.0.2.1",
		TxInterval:       durationpb.New(50 * time.Millisecond),
		DetectMultiplier: 3,
	})
	if err == nil {
		t.Fatal("AddEchoSession after Manager.Close succeeded")
	}
	if factory.opens != 0 || factory.closes != 0 {
		t.Errorf("sender lifecycle after rejected add = opens %d closes %d, want 0/0",
			factory.opens, factory.closes)
	}
}
