package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	"github.com/dantte-lp/gobfd/internal/testutil"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestHAProxyRawLossConvergesWithoutReconnect(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	mgr, session := testutil.RawLossSession(ctx, t)
	peer := session.PeerAddr().String()
	logger := slog.New(slog.DiscardHandler)
	rpc := connect.NewServer()
	server.Register(rpc, mgr, nil, logger)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	srv := httptest.NewTestServer(t, mux)
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	selector, err := apiclient.ParseSessionSelector(peer, "", "", "single_hop")
	if err != nil {
		t.Fatal(err)
	}
	states := &stateMap{
		m:         make(map[string]bfdv1.SessionState),
		selectors: map[string]apiclient.SessionSelector{peer: selector},
	}
	done := make(chan error, 1)
	go func() { done <- watchEvents(ctx, client, states, logger) }()
	testutil.Wait(ctx, t, func() bool { return states.get(peer) == bfdv1.SessionState_SESSION_STATE_UP })
	testutil.LoseFinalDown(ctx, t, mgr, session)
	go mgr.RunDispatch(ctx)
	testutil.Wait(ctx, t, func() bool {
		return states.get(peer) == bfdv1.SessionState_SESSION_STATE_DOWN &&
			testutil.DeliveryCount(mgr, bfd.DeliveryNotificationDispatch, bfd.DeliveryAccepted) == 1 &&
			testutil.DeliveryCount(mgr, bfd.DeliveryNotificationDispatch, bfd.DeliveryStale) == 63
	})
	if testutil.DeliveryCount(mgr, bfd.DeliveryWatchRecipient, bfd.DeliveryGapped) != 0 {
		t.Fatal("backend health recovered through subscriber overflow instead of the raw loss hint")
	}
	cancel()
	select {
	case watchErr := <-done:
		if !errors.Is(watchErr, context.Canceled) {
			t.Fatal(watchErr)
		}
	case <-time.After(time.Second):
		t.Fatal("backend Watch did not stop")
	}
}
