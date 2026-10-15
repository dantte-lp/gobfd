package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

type restartingBFD struct {
	bfdv1connect.UnimplementedBfdServiceHandler

	calls atomic.Int32
	loss  chan struct{}
}

func (s *restartingBFD) WatchSessionEvents(ctx context.Context, _ *bfdv1.WatchSessionEventsRequest,
	stream bfdv1connect.BfdServiceWatchSessionEventsServerStream,
) error {
	state := bfdv1.SessionState_SESSION_STATE_DOWN
	first := s.calls.Add(1) == 1
	if first {
		state = bfdv1.SessionState_SESSION_STATE_UP
	}
	if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
		Type:    bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED,
		Session: &bfdv1.BfdSession{PeerAddress: testPeerAddr, LocalDiscriminator: 1, LocalState: state},
	}); err != nil {
		return err
	}
	if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE,
	}); err != nil {
		return err
	}
	if first {
		select {
		case <-s.loss:
			return nil // graceful EOF still requires a reconnect
		case <-ctx.Done():
		}
	} else {
		<-ctx.Done()
	}
	return ctx.Err()
}

func TestWatchEventsReconnectsAfterEOF(t *testing.T) {
	t.Parallel()
	svc := &restartingBFD{loss: make(chan struct{})}
	rpc := connect.NewServer()
	bfdv1connect.RegisterBfdServiceHandler(rpc, svc)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	srv := httptest.NewTestServer(t, mux)
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	selector, err := apiclient.ParseSessionSelector(testPeerAddr, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	states := &stateMap{
		m:         make(map[string]bfdv1.SessionState),
		selectors: map[string]apiclient.SessionSelector{testPeerAddr: selector},
	}
	done := make(chan error, 1)
	go func() { done <- watchEvents(ctx, client, states, slog.New(slog.DiscardHandler)) }()
	wait := func(want bfdv1.SessionState) {
		t.Helper()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for states.get(testPeerAddr) != want {
			select {
			case err := <-done:
				t.Fatalf("watch exited before recovery: %v", err)
			case <-ctx.Done():
				t.Fatal("watch did not converge")
			case <-ticker.C:
			}
		}
	}
	wait(bfdv1.SessionState_SESSION_STATE_UP)
	close(svc.loss)
	wait(bfdv1.SessionState_SESSION_STATE_DOWN)
	cancel()
	<-done
}
