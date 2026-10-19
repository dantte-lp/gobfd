package main

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/grpchealth/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestCausalGoBGPCanonicalPolicy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := gobgpTestSession("peer", "127.0.0.1")
		sess, err := mgr.CreateSession(t.Context(), cfg.SessionConfig, cfg.SenderLeaseFactory)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		actuates := goBGPActuates(mgr, nil, slog.New(slog.DiscardHandler))
		sc := bfd.StateChange{LocalDiscr: sess.LocalDiscriminator(), PeerAddr: cfg.SessionConfig.PeerAddr, Ref: sess.Ref()}
		if !actuates(sc) {
			t.Fatal("canonical initial session rejected")
		}
		wrong := sc
		wrong.Ref = new(bfd.Session).Ref()
		if actuates(wrong) {
			t.Fatal("different session identity accepted")
		}
		sess.RecvPacket(&bfd.ControlPacket{
			State: bfd.StateAdminDown, Diag: bfd.DiagAdminDown,
			DetectMult: 3, MyDiscriminator: 7, DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
		})
		synctest.Wait()
		if actuates(sc) {
			t.Fatal("known current remote AdminDown did not veto historical failure")
		}
		if sess.SetPathDown() != bfd.DeliveryQueued {
			t.Fatal("PathDown admission failed")
		}
		synctest.Wait()
		if !actuates(sc) {
			t.Fatal("explicit current local PathDown was masked by retained remote AdminDown")
		}
		if err := sess.SetAdminDown(t.Context()); err != nil {
			t.Fatal(err)
		}
		if actuates(sc) {
			t.Fatal("current local AdminDown did not veto historical failure")
		}
	})
}

func gobgpTestSession(key, local string) bfd.ReconcileConfig {
	return bfd.ReconcileConfig{
		Key: key,
		SessionConfig: bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.230"),
			LocalAddr:             netip.MustParseAddr(local),
			Interface:             "lo",
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
		SenderLeaseFactory: candidateTestSenderLeaseFactory(),
	}
}

func gobgpSessionChange(t *testing.T, mgr *bfd.Manager, peer netip.Addr) bfd.StateChange {
	t.Helper()
	for _, snap := range mgr.Sessions() {
		return bfd.StateChange{LocalDiscr: snap.LocalDiscr, PeerAddr: peer, Type: snap.Type}
	}
	t.Fatal("manager has no session")
	return bfd.StateChange{}
}

// H-31: the GoBGP handler acts only on the canonical BFD session for its
// neighbor. Session removal publishes no state change, so the daemon
// answers from the manager: true while exactly one base session to the
// peer exists, false after reconciliation removes it.
func TestGoBGPActuatesFollowsReconciliation(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	actuates := goBGPActuates(mgr, nil, slog.New(slog.DiscardHandler))

	if _, _, err := mgr.ReconcileSessions(t.Context(), []bfd.ReconcileConfig{
		gobgpTestSession("gobgp-peer", "127.0.0.1"),
	}); err != nil {
		t.Fatalf("reconcile session: %v", err)
	}
	sc := gobgpSessionChange(t, mgr, netip.MustParseAddr("192.0.2.230"))
	if !actuates(sc) {
		t.Fatal("the only base session to the peer does not actuate it")
	}
	mapped := sc
	mapped.PeerAddr = netip.MustParseAddr("::ffff:192.0.2.230")
	if !actuates(mapped) {
		t.Fatal("IPv4-mapped peer address does not match its session")
	}

	if _, _, err := mgr.ReconcileSessions(t.Context(), nil); err != nil {
		t.Fatalf("reconcile removal: %v", err)
	}
	if actuates(sc) {
		t.Fatal("removed session still actuates the peer")
	}
}

// H-31: two base sessions to one BGP neighbor leave no canonical session;
// neither actuates and the gobfd.gobgp health service reports it.
func TestGoBGPActuatesRejectsAmbiguousPeer(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	checker := newDaemonHealthChecker()
	status := newGoBGPStatus(checker, bfdmetrics.NewCollector(prometheus.NewRegistry()))
	status.action("DisablePeer", nil)
	actuates := goBGPActuates(mgr, status, slog.New(slog.DiscardHandler))

	if _, _, err := mgr.ReconcileSessions(t.Context(), []bfd.ReconcileConfig{
		gobgpTestSession("gobgp-a", "127.0.0.1"),
		gobgpTestSession("gobgp-b", "127.0.0.2"),
	}); err != nil {
		t.Fatalf("reconcile sessions: %v", err)
	}
	for _, snap := range mgr.Sessions() {
		if actuates(bfd.StateChange{LocalDiscr: snap.LocalDiscr, PeerAddr: snap.PeerAddr, Type: snap.Type}) {
			t.Fatalf("session %d actuates an ambiguous peer", snap.LocalDiscr)
		}
	}
	resp, err := checker.Check(context.Background(), &grpchealth.CheckRequest{Service: goBGPHealthServiceName})
	if err != nil {
		t.Fatalf("Check(%s): %v", goBGPHealthServiceName, err)
	}
	if resp.Status != grpchealth.StatusNotServing {
		t.Fatalf("gobfd.gobgp = %v, want NOT_SERVING for an ambiguous peer", resp.Status)
	}
}
