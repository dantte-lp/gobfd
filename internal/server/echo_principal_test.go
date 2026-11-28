package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func echoPrincipalContext(t *testing.T, principal transportPrincipal) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), principalKey{}, principal)
}

func TestEchoPrincipalRetryReusesCanonicalWire(t *testing.T) {
	t.Parallel()
	srv := newTestEchoServer(t)
	factory := new(echoLeaseTestSenderFactory)
	srv.senderFactory = factory
	ctx := echoPrincipalContext(t, transportPrincipal{kind: principalUnix, identity: "uid:1000"})
	request := echoAdmissionRequest()
	first, err := srv.AddEchoSession(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.PeerAddress = "::ffff:192.0.2.10"
	request.LocalAddress = "::ffff:192.0.2.1"
	retry, err := srv.AddEchoSession(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if retry.GetSession().GetLocalDiscriminator() != first.GetSession().GetLocalDiscriminator() ||
		factory.opens != 1 || len(srv.manager.EchoSessions()) != 1 {
		t.Fatal("same-owner retry created a new wire identity or sender")
	}
	request.TxInterval = durationpb.New(2 * time.Second)
	if _, conflictErr := srv.AddEchoSession(ctx, request); connect.CodeOf(conflictErr) != connect.CodeFailedPrecondition {
		t.Errorf("incompatible retry = %v, want FailedPrecondition", conflictErr)
	}
	if factory.opens != 1 || len(srv.manager.EchoSessions()) != 1 {
		t.Fatal("conflicting retry changed the existing session")
	}
	request.TxInterval = durationpb.New(time.Second)
	bob := echoPrincipalContext(t, transportPrincipal{kind: principalUnix, identity: "uid:2000"})
	shared, err := srv.AddEchoSession(bob, request)
	if err != nil || shared.GetSession().GetLocalDiscriminator() != first.GetSession().GetLocalDiscriminator() ||
		factory.opens != 1 {
		t.Fatalf("compatible second principal did not share the wire: %v", err)
	}
	deleteRequest := &bfdv1.DeleteEchoSessionRequest{LocalDiscriminator: first.GetSession().GetLocalDiscriminator()}
	if _, err := srv.DeleteEchoSession(ctx, deleteRequest); err != nil {
		t.Fatal(err)
	}
	if len(srv.manager.EchoSessions()) != 1 || factory.closes != 0 {
		t.Fatal("first principal released another principal's wire")
	}
	if _, err := srv.DeleteEchoSession(bob, deleteRequest); err != nil || factory.closes != 1 {
		t.Fatalf("last principal cleanup = %v, closes %d", err, factory.closes)
	}
}

func TestEchoPrincipalDeletePreservesOtherOwners(t *testing.T) {
	t.Parallel()
	for _, other := range []transportPrincipal{
		{kind: principalUnix, identity: "uid:2000"},
		{kind: principalTLSURI, identity: "uid:1000"},
	} {
		t.Run(string(other.kind)+"/"+other.identity, func(t *testing.T) {
			t.Parallel()
			srv := newTestEchoServer(t)
			factory := new(echoLeaseTestSenderFactory)
			srv.senderFactory = factory
			alice := echoPrincipalContext(t, transportPrincipal{kind: principalUnix, identity: "uid:1000"})
			created, err := srv.AddEchoSession(alice, echoAdmissionRequest())
			if err != nil {
				t.Fatal(err)
			}
			request := &bfdv1.DeleteEchoSessionRequest{LocalDiscriminator: created.GetSession().GetLocalDiscriminator()}
			_, foreignErr := srv.DeleteEchoSession(echoPrincipalContext(t, other), request)
			if connect.CodeOf(foreignErr) != connect.CodePermissionDenied {
				t.Errorf("foreign delete = %v, want PermissionDenied", foreignErr)
			}
			if len(srv.manager.EchoSessions()) != 1 || factory.closes != 0 {
				t.Fatal("foreign principal destroyed another owner's Echo session")
			}
			if _, deleteErr := srv.DeleteEchoSession(alice, request); deleteErr != nil {
				t.Fatal(deleteErr)
			}
			if _, repeatErr := srv.DeleteEchoSession(alice, request); connect.CodeOf(repeatErr) != connect.CodeNotFound {
				t.Errorf("repeated delete = %v, want NotFound", repeatErr)
			}
			if factory.closes != 1 {
				t.Fatalf("sender closes = %d, want 1", factory.closes)
			}
		})
	}
}

func TestEchoPrincipalConfigClaimSurvivesAPIDelete(t *testing.T) {
	t.Parallel()
	for _, configFirst := range []bool{true, false} {
		name := "API first"
		if configFirst {
			name = "config first"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newTestEchoServer(t)
			factory := new(echoLeaseTestSenderFactory)
			srv.senderFactory = factory
			ctx := echoPrincipalContext(t, transportPrincipal{kind: principalUnix, identity: "uid:1000"})
			request := echoAdmissionRequest()
			cfg, err := echoSessionConfigFromProto(request)
			if err != nil {
				t.Fatal(err)
			}
			configOpens, configCloses := 0, 0
			claimConfig := func() {
				t.Helper()
				_, _, reconcileErr := srv.manager.ReconcileEchoSessions(t.Context(), []bfd.EchoReconcileConfig{{
					EchoSessionConfig: cfg,
					SenderLeaseFactory: func() (*bfd.SenderLease, error) {
						configOpens++
						return bfd.NewSenderLease(noopSender{}, func() error { configCloses++; return nil }), nil
					},
				}})
				if reconcileErr != nil {
					t.Fatal(reconcileErr)
				}
			}
			if configFirst {
				claimConfig()
			}
			created, err := srv.AddEchoSession(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if !configFirst {
				claimConfig()
			}
			if len(srv.manager.EchoSessions()) != 1 || factory.opens+configOpens != 1 {
				t.Fatal("compatible config/API claims created separate Echo wires")
			}
			if _, deleteErr := srv.DeleteEchoSession(ctx, &bfdv1.DeleteEchoSessionRequest{
				LocalDiscriminator: created.GetSession().GetLocalDiscriminator(),
			}); deleteErr != nil {
				t.Fatal(deleteErr)
			}
			if len(srv.manager.EchoSessions()) != 1 || factory.closes+configCloses != 0 {
				t.Fatal("API release removed the surviving config claim")
			}
			if _, _, removeErr := srv.manager.ReconcileEchoSessions(t.Context(), nil); removeErr != nil {
				t.Fatal(removeErr)
			}
			if len(srv.manager.EchoSessions()) != 0 || factory.closes+configCloses != 1 {
				t.Fatal("final claim release did not close exactly one wire sender")
			}
		})
	}
}

func TestEchoPrincipalCanceledAddDoesNotAllocate(t *testing.T) {
	t.Parallel()
	srv := newTestEchoServer(t)
	factory := new(echoLeaseTestSenderFactory)
	srv.senderFactory = factory
	ctx, cancel := context.WithCancel(echoPrincipalContext(t, transportPrincipal{
		kind: principalUnix, identity: "uid:1000",
	}))
	cancel()
	if _, err := srv.AddEchoSession(ctx, echoAdmissionRequest()); connect.CodeOf(err) != connect.CodeCanceled {
		t.Errorf("canceled create = %v, want Canceled", err)
	}
	if factory.opens != 0 || len(srv.manager.EchoSessions()) != 0 {
		t.Fatal("canceled request created a sender or Echo wire")
	}
}

func TestEchoPrincipalConcurrentClaims(t *testing.T) {
	t.Parallel()
	srv := newTestEchoServer(t)
	factory := new(echoLeaseTestSenderFactory)
	srv.senderFactory = factory
	owners := []context.Context{
		echoPrincipalContext(t, transportPrincipal{kind: principalUnix, identity: "uid:1000"}),
		echoPrincipalContext(t, transportPrincipal{kind: principalUnix, identity: "uid:2000"}),
	}
	type result struct {
		discr uint32
		err   error
	}
	const calls = 16
	results := make(chan result, calls)
	var workers sync.WaitGroup
	for i := range calls {
		workers.Go(func() {
			response, err := srv.AddEchoSession(owners[i%len(owners)], echoAdmissionRequest())
			results <- result{discr: response.GetSession().GetLocalDiscriminator(), err: err}
		})
	}
	workers.Wait()
	var discriminator uint32
	for range calls {
		item := <-results
		if item.err != nil {
			t.Fatal(item.err)
		}
		if discriminator == 0 {
			discriminator = item.discr
		}
		if item.discr != discriminator {
			t.Error("concurrent claims returned different wire identities")
		}
	}
	if factory.opens != 1 || len(srv.manager.EchoSessions()) != 1 {
		t.Fatalf("concurrent claims allocated %d senders, want 1", factory.opens)
	}
	request := &bfdv1.DeleteEchoSessionRequest{LocalDiscriminator: discriminator}
	for i, owner := range owners {
		if _, err := srv.DeleteEchoSession(owner, request); err != nil {
			t.Fatal(err)
		}
		if factory.closes != i {
			t.Fatalf("release %d closed sender %d times", i, factory.closes)
		}
	}
}
