package server

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

type echoAdmissionFlag struct{ atomic.Bool }

func (f *echoAdmissionFlag) EchoReady() bool { return f.Load() }

func readyEchoAdmission() *echoAdmissionFlag {
	flag := new(echoAdmissionFlag)
	flag.Store(true)
	return flag
}

func echoAdmissionRequest() *bfdv1.AddEchoSessionRequest {
	return &bfdv1.AddEchoSessionRequest{
		PeerAddress: "192.0.2.10", LocalAddress: "192.0.2.1",
		TxInterval: durationpb.New(time.Second), DetectMultiplier: 3,
	}
}

func TestEchoAdmissionMissingReadinessFailsClosed(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	factory := &echoLeaseTestSenderFactory{}
	srv := &EchoServer{manager: mgr, senderFactory: factory, logger: logger}
	_, err := srv.AddEchoSession(t.Context(), echoAdmissionRequest())
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("missing readiness: %v, want FailedPrecondition", err)
	}
	if factory.opens != 0 || len(mgr.EchoSessions()) != 0 {
		t.Errorf("rejected admission allocated sender/session: %d/%d", factory.opens, len(mgr.EchoSessions()))
	}
}

func TestEchoAdmissionRevocationKeepsCleanup(t *testing.T) {
	t.Parallel()
	srv := newTestEchoServer(t)
	factory := &echoLeaseTestSenderFactory{}
	srv.senderFactory = factory
	flag := new(echoAdmissionFlag)
	srv.admission = flag
	_, err := srv.AddEchoSession(t.Context(), echoAdmissionRequest())
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unready admission: %v", err)
	}
	if factory.opens != 0 {
		t.Fatal("unready admission opened sender")
	}
	flag.Store(true)
	created, err := srv.AddEchoSession(t.Context(), echoAdmissionRequest())
	if err != nil {
		t.Fatal(err)
	}
	flag.Store(false)
	request := echoAdmissionRequest()
	request.PeerAddress = "192.0.2.11"
	if _, addErr := srv.AddEchoSession(t.Context(), request); connect.CodeOf(addErr) != connect.CodeFailedPrecondition {
		t.Fatalf("revoked admission: %v", addErr)
	}
	if factory.opens != 1 || len(srv.manager.EchoSessions()) != 1 {
		t.Fatal("revoked admission opened a sender or created a session")
	}
	listed, err := srv.ListEchoSessions(t.Context(), new(bfdv1.ListEchoSessionsRequest))
	if err != nil || len(listed.GetSessions()) != 1 {
		t.Fatalf("list while unready: %v, %v", listed, err)
	}
	_, err = srv.DeleteEchoSession(t.Context(), &bfdv1.DeleteEchoSessionRequest{
		LocalDiscriminator: created.GetSession().GetLocalDiscriminator(),
	})
	if err != nil || factory.closes != 1 || len(srv.manager.EchoSessions()) != 0 {
		t.Fatalf("delete while unready: %v, closes %d", err, factory.closes)
	}
}

func TestEchoAdmissionRetainsRequestValidation(t *testing.T) {
	t.Parallel()
	srv := newTestEchoServer(t)
	srv.admission = nil
	request := echoAdmissionRequest()
	request.DetectMultiplier = 0
	if _, err := srv.AddEchoSession(t.Context(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid request: %v, want InvalidArgument", err)
	}
}
