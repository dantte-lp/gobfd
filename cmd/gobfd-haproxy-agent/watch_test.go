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

type restartingBFD struct {
	bfdv1connect.UnimplementedBfdServiceHandler

	calls atomic.Int32
	loss  chan struct{}
	gap   chan struct{}
	mixed bool
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
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED,
		Session: &bfdv1.BfdSession{
			PeerAddress: testPeerAddr, LocalDiscriminator: 1, LocalState: state,
			Type: bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		},
	}); err != nil {
		return err
	}
	if s.mixed {
		for _, row := range []*bfdv1.BfdSession{
			{
				PeerAddress: testPeerAddr, LocalDiscriminator: 2, Type: bfdv1.SessionType_SESSION_TYPE_ECHO,
				LocalState: bfdv1.SessionState_SESSION_STATE_DOWN, ObservationVersion: 2,
			},
			{
				PeerAddress: "192.0.2.254", LocalDiscriminator: 3, Type: bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
				LocalState: bfdv1.SessionState_SESSION_STATE_UP, ObservationVersion: 3,
			},
		} {
			if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
				Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, Session: row,
			}); err != nil {
				return err
			}
		}
	}
	if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE,
	}); err != nil {
		return err
	}
	if first {
		if s.gap != nil {
			select {
			case <-s.gap:
				if err := stream.Send(&bfdv1.WatchSessionEventsResponse{
					Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP, Revision: 1,
				}); err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
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

func TestEchoWatchHAProxyMixedPeerGapAndReconnect(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"", "single_hop"} {
		t.Run("type="+kind, func(t *testing.T) {
			t.Parallel()
			svc := &restartingBFD{loss: make(chan struct{}), gap: make(chan struct{}), mixed: true}
			rpc := connect.NewServer()
			bfdv1connect.RegisterBfdServiceHandler(rpc, svc)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, rpc)
			srv := httptest.NewTestServer(t, mux)
			client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			selector, err := apiclient.ParseSessionSelector(testPeerAddr, "", "", kind)
			if err != nil {
				t.Fatal(err)
			}
			barrier, err := apiclient.ParseSessionSelector("192.0.2.254", "", "", "single_hop")
			if err != nil {
				t.Fatal(err)
			}
			states := &stateMap{
				m:         make(map[string]bfdv1.SessionState),
				selectors: map[string]apiclient.SessionSelector{testPeerAddr: selector, "barrier": barrier},
			}
			done := make(chan error, 1)
			go func() { done <- watchEvents(ctx, client, states, slog.New(slog.DiscardHandler)) }()
			wait := func(want bfdv1.SessionState) {
				t.Helper()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for states.get("barrier") != want {
					select {
					case watchErr := <-done:
						t.Fatalf("watch exited: %v", watchErr)
					case <-ctx.Done():
						t.Fatal("mixed snapshot did not converge")
					case <-ticker.C:
					}
				}
			}
			wait(bfdv1.SessionState_SESSION_STATE_UP)
			want := bfdv1.SessionState_SESSION_STATE_UNSPECIFIED
			if kind != "" {
				want = bfdv1.SessionState_SESSION_STATE_UP
			}
			if got := states.get(testPeerAddr); got != want {
				t.Fatalf("mixed peer health=%v, want %v", got, want)
			}
			close(svc.gap)
			wait(bfdv1.SessionState_SESSION_STATE_UNSPECIFIED)
			if got := states.get(testPeerAddr); got != bfdv1.SessionState_SESSION_STATE_UNSPECIFIED {
				t.Fatalf("GAP retained backend health: %v", got)
			}
			close(svc.loss)
			wait(bfdv1.SessionState_SESSION_STATE_UP)
			if kind != "" {
				want = bfdv1.SessionState_SESSION_STATE_DOWN
			}
			if got := states.get(testPeerAddr); got != want || svc.calls.Load() < 2 {
				t.Fatalf("mixed reconnect health=%v, want %v, calls=%d", got, want, svc.calls.Load())
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("watch shutdown error = %v, want context cancellation", err)
			}
		})
	}
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
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("watch shutdown error = %v, want context cancellation", err)
	}
}
