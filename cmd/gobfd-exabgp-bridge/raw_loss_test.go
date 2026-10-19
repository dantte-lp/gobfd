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
)

func TestExaBGPRawLossWithdrawsWithoutReconnect(t *testing.T) {
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
	srv.Start()
	selector, err := apiclient.ParseSessionSelector(peer, "", "", "single_hop")
	if err != nil {
		t.Fatal(err)
	}
	output := make(commandOutput, 16)
	done := make(chan error, 1)
	go func() { done <- watchAndAnnounce(ctx, srv.URL, selector, testPrefixAddr, output, logger) }()
	expect := func(verb string) {
		t.Helper()
		select {
		case command := <-output:
			if command != verb+" route "+testPrefixAddr+" next-hop self\n" {
				t.Fatalf("unexpected route command %q", command)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	expect("withdraw")
	expect("announce")
	testutil.LoseFinalDown(ctx, t, mgr, session)
	go mgr.RunDispatch(ctx)
	expect("withdraw")
	testutil.Wait(ctx, t, func() bool {
		return testutil.DeliveryCount(mgr, bfd.DeliveryNotificationDispatch, bfd.DeliveryAccepted) == 1 &&
			testutil.DeliveryCount(mgr, bfd.DeliveryNotificationDispatch, bfd.DeliveryStale) == 63
	})
	if testutil.DeliveryCount(mgr, bfd.DeliveryWatchRecipient, bfd.DeliveryGapped) != 0 {
		t.Fatal("route recovered through subscriber overflow instead of raw loss")
	}
	cancel()
	select {
	case watchErr := <-done:
		if !errors.Is(watchErr, context.Canceled) {
			t.Fatal(watchErr)
		}
	case <-time.After(time.Second):
		t.Fatal("route Watch did not stop")
	}
	select {
	case command := <-output:
		t.Fatalf("old queued Up emitted a command after withdrawal: %q", command)
	default:
	}
}
