package main

import (
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// The GoBGP handler drops a late Down for a peer whose BFD session is gone.
// Session removal publishes no state change, so the daemon answers the
// handler's Managed question from the manager: true while a session to the
// peer exists, false after reconciliation removes it.
func TestGoBGPManagedPeerFollowsReconciliation(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	managed := goBGPManagedPeer(mgr)

	if managed("192.0.2.230") {
		t.Fatal("peer reported managed before any session exists")
	}
	if _, _, err := mgr.ReconcileSessions(t.Context(), []bfd.ReconcileConfig{{
		Key: "gobgp-peer",
		SessionConfig: bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.230"),
			LocalAddr:             netip.MustParseAddr("127.0.0.1"),
			Interface:             "lo",
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
		SenderLeaseFactory: candidateTestSenderLeaseFactory(),
	}}); err != nil {
		t.Fatalf("reconcile session: %v", err)
	}
	if !managed("192.0.2.230") {
		t.Fatal("peer with a BFD session reported unmanaged")
	}

	if _, _, err := mgr.ReconcileSessions(t.Context(), nil); err != nil {
		t.Fatalf("reconcile removal: %v", err)
	}
	if managed("192.0.2.230") {
		t.Fatal("peer reported managed after reconciliation removed its session")
	}
}
