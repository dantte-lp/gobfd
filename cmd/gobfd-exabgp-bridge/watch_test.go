package main

import (
	"context"
	"errors"
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

type bridgeEvents struct {
	bfdv1connect.UnimplementedBfdServiceHandler

	calls               atomic.Int32
	loss, recovery, gap chan struct{}
}

func (s *bridgeEvents) WatchSessionEvents(ctx context.Context, _ *bfdv1.WatchSessionEventsRequest,
	stream bfdv1connect.BfdServiceWatchSessionEventsServerStream,
) error {
	first := s.calls.Add(1) == 1
	state, revision := bfdv1.SessionState_SESSION_STATE_DOWN, uint64(0)
	if first {
		state, revision = bfdv1.SessionState_SESSION_STATE_UP, 10
	}
	session := &bfdv1.BfdSession{PeerAddress: testPeerAddr, LocalDiscriminator: 1, LocalState: state}
	if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, Session: session, Revision: revision,
	}); err != nil {
		return err
	}
	if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, Revision: revision,
	}); err != nil {
		return err
	}
	if first {
		select {
		case <-s.loss:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-s.recovery:
	case <-ctx.Done():
		return ctx.Err()
	}
	session.LocalState = bfdv1.SessionState_SESSION_STATE_UP
	if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE, Session: session, Revision: 1,
	}); err != nil {
		return err
	}
	select {
	case <-s.gap:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, kind := range []bfdv1.WatchSessionEventsResponse_EventType{
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP,
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE,
	} {
		if err := stream.Send(&bfdv1.WatchSessionEventsResponse{Type: kind, Revision: 2}); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

type commandOutput chan string

func (w commandOutput) Write(command []byte) (int, error) {
	w <- string(command)
	return len(command), nil
}

func TestBridgeReconnectsNewGenerationAndWithdrawsOnGap(t *testing.T) {
	t.Parallel()
	svc := &bridgeEvents{loss: make(chan struct{}), recovery: make(chan struct{}), gap: make(chan struct{})}
	rpc := connect.NewServer()
	bfdv1connect.RegisterBfdServiceHandler(rpc, svc)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	// The bridge constructs its HTTP client from a real endpoint.
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	selector, err := apiclient.ParseSessionSelector(testPeerAddr, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output := make(commandOutput, 16)
	done := make(chan error, 1)
	go func() {
		done <- watchAndAnnounce(ctx, srv.URL, selector, testPrefixAddr, output, slog.New(slog.DiscardHandler))
	}()
	expect := func(verb string) {
		t.Helper()
		select {
		case command := <-output:
			if want := verb + " route " + testPrefixAddr + " next-hop self\n"; command != want {
				t.Fatalf("command=%q, want %q", command, want)
			}
		case err := <-done:
			t.Fatalf("bridge exited: %v", err)
		case <-ctx.Done():
			t.Fatal("route did not converge")
		}
	}
	expect("withdraw")
	expect("announce")
	close(svc.loss)
	expect("withdraw")
	close(svc.recovery)
	expect("announce") // revision 1 after previous generation's revision 10
	close(svc.gap)
	expect("withdraw")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
}
