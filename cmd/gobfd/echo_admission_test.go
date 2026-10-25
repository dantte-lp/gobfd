package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestEchoPreviewDaemonDefaultFailsClosed(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	srv := newGRPCServer(config.GRPCConfig{UnsafeLoopbackMutations: true}, false,
		mgr, nil, nil, newDaemonHealthChecker(), nil, logger, nil)
	baseURL := serveLoopback(t, srv)
	client := bfdv1connect.NewEchoServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, baseURL)))
	_, err := client.AddEchoSession(t.Context(), &bfdv1.AddEchoSessionRequest{
		PeerAddress: "192.0.2.10", LocalAddress: "192.0.2.1",
		TxInterval: durationpb.New(time.Second), DetectMultiplier: 3,
	})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("default Echo admission = %v, want FailedPrecondition", err)
	}
	if len(mgr.EchoSessions()) != 0 {
		t.Fatal("disabled Echo API created a session")
	}
}

func TestEchoPreviewDaemonAdmissionFollowsRuntime(t *testing.T) {
	t.Parallel()
	for _, state := range []string{
		"disabled", "unreconciled", "source failed", "binding lost", "runtime lost", "closed", "startup failed",
	} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			f := newAPIListenerFixture(t)
			r := f.owner.readiness
			switch state {
			case "disabled":
				r.setEchoEnabled(false)
			case "unreconciled", "source failed":
				r.setSources(allReconciliationSources &^ sourceMask(sourceEcho))
			case "binding lost":
				r.setNotReady(componentEchoBindings)
			case "runtime lost":
				r.setNotReady(componentRuntime)
			case "closed":
				f.owner.close()
			case "startup failed":
				cfg := config.DefaultConfig()
				cfg.Echo.Enabled = true
				cfg.Echo.Peers = []config.EchoPeerConfig{{Peer: "192.0.2.10", Local: "192.0.2.1", Interface: "eth1"}}
				f.opener.fail["eth1"] = errors.New("fixture listener failure")
				if err := f.owner.openStartup(cfg); err == nil {
					t.Fatal("startup accepted failed Echo listener")
				}
			}
			_, err := f.echo.AddEchoSession(t.Context(), &bfdv1.AddEchoSessionRequest{
				PeerAddress: "192.0.2.10", LocalAddress: "192.0.2.1",
				TxInterval: durationpb.New(time.Second), DetectMultiplier: 3,
			})
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("%s admission: %v, want FailedPrecondition", state, err)
			}
			if len(f.mgr.EchoSessions()) != 0 || len(f.opener.openedKeys()) != 0 {
				t.Fatal("unready admission created a session or receive listener")
			}
		})
	}
}

func TestEchoPreviewReceiverLossRevokesAdmission(t *testing.T) {
	t.Parallel()
	f := newAPIListenerFixture(t)
	request := &bfdv1.AddEchoSessionRequest{
		PeerAddress: "192.0.2.10", LocalAddress: "192.0.2.1",
		TxInterval: durationpb.New(time.Hour), DetectMultiplier: 3,
	}
	created, err := f.echo.AddEchoSession(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	keys := f.opener.openedKeys()
	if len(keys) != 1 {
		t.Fatalf("API-only admission opened %d receive listeners, want 1", len(keys))
	}
	if err = f.opener.conn(t, keys[0]).Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), readinessTestWait)
	defer cancel()
	select {
	case <-f.owner.receivers.lost:
	case <-ctx.Done():
		t.Fatal("receiver loss did not revoke runtime readiness")
	}
	request.PeerAddress = "192.0.2.11"
	if _, addErr := f.echo.AddEchoSession(t.Context(), request); connect.CodeOf(addErr) != connect.CodeFailedPrecondition {
		t.Fatalf("lost receiver admission: %v", addErr)
	}
	listed, err := f.echo.ListEchoSessions(t.Context(), new(bfdv1.ListEchoSessionsRequest))
	if err != nil || len(listed.GetSessions()) != 1 || len(f.opener.openedKeys()) != 1 {
		t.Fatalf("receiver loss changed existing sessions or opened a listener: %v, %v", listed, err)
	}
	if _, err := f.echo.DeleteEchoSession(t.Context(), &bfdv1.DeleteEchoSessionRequest{
		LocalDiscriminator: created.GetSession().GetLocalDiscriminator(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEchoPreviewReloadUpdatesAdmission(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	cfg := config.DefaultConfig()
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
	if c.readiness.echoAPIReady() {
		t.Fatal("unreconciled startup admitted Echo")
	}
	for _, enabled := range []bool{false, true, false, true} {
		cfg.Echo.Enabled = enabled
		err := c.reconcile(t.Context(), cfg, mgr, newNthFailureDeclarativeSenderFactory(0), new(slog.LevelVar))
		if err != nil {
			t.Fatal(err)
		}
		if got := c.readiness.echoAPIReady(); got != enabled {
			t.Fatalf("enablement %v, admission %v", enabled, got)
		}
	}
	invalid := cloneStartupContractTestConfig(cfg)
	invalid.Echo.Enabled = false
	invalid.Sessions = []config.SessionConfig{{Peer: "invalid-address"}}
	err := c.reconcile(t.Context(), invalid, mgr, newNthFailureDeclarativeSenderFactory(0), new(slog.LevelVar))
	if err == nil {
		t.Fatal("invalid reload accepted")
	}
	if !c.readiness.echoAPIReady() {
		t.Fatal("invalid reload changed Echo admission")
	}
}
