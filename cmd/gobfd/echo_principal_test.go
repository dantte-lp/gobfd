package main

import (
	"log/slog"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestEchoPrincipalSharedReceiveLifetime(t *testing.T) {
	t.Parallel()
	for _, configFirst := range []bool{true, false} {
		name := "API first"
		if configFirst {
			name = "config first"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newAPIListenerFixture(t)
			cfg := echoLeaseTestConfig("192.0.2.6")
			cfg.Echo.Peers[0].Local = "192.0.2.5"
			cfg.Echo.Peers[0].Interface = "eth8"
			cfg.Echo.Peers[0].TxInterval = time.Second
			candidates, err := compileEchoSessionCandidates(cfg)
			if err != nil {
				t.Fatal(err)
			}
			listener := echoListener("192.0.2.5", "eth8")
			key := listenerKeyOf(listener)
			if acquireErr := fixture.owner.acquire(sourceClaim(sourceEcho), echoListenerConfigs(cfg)); acquireErr != nil {
				t.Fatal(acquireErr)
			}
			factory := newNthFailureDeclarativeSenderFactory(0)
			claimConfig := func() {
				t.Helper()
				result := applyEchoSessionCandidates(
					t.Context(), candidates, fixture.mgr, factory, slog.New(slog.DiscardHandler), fixture.owner,
				)
				if reconcileErr := result.Err(); reconcileErr != nil {
					t.Fatal(reconcileErr)
				}
			}
			if configFirst {
				claimConfig()
			}
			created, err := fixture.echo.AddEchoSession(t.Context(), &bfdv1.AddEchoSessionRequest{
				PeerAddress: "192.0.2.6", LocalAddress: "192.0.2.5", InterfaceName: "eth8",
				TxInterval: durationpb.New(time.Second), DetectMultiplier: 3,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !configFirst {
				claimConfig()
			}
			if len(fixture.mgr.EchoSessions()) != 1 {
				t.Fatal("config/API claims did not share the Echo wire")
			}
			if _, _, err := fixture.mgr.ReconcileEchoSessions(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			fixture.owner.release(sourceClaim(sourceEcho), nil)
			if len(fixture.mgr.EchoSessions()) != 1 || !fixture.openListener(t, key) {
				t.Fatal("config withdrawal lost the live API session's receiver")
			}
			if _, err := fixture.echo.DeleteEchoSession(t.Context(), &bfdv1.DeleteEchoSessionRequest{
				LocalDiscriminator: created.GetSession().GetLocalDiscriminator(),
			}); err != nil {
				t.Fatal(err)
			}
			if len(fixture.mgr.EchoSessions()) != 0 || fixture.openListener(t, key) {
				t.Fatal("last Echo claim release leaked its wire or receive listener")
			}
		})
	}
}
