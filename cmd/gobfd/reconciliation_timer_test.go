package main

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/grpchealth"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestTimerUpdateCompletionWithoutReload(t *testing.T) {
	t.Parallel()
	for _, failCreation := range []bool{false, true} {
		name := "converges"
		if failCreation {
			name = "preserves unrelated failure"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				logger := slog.New(slog.DiscardHandler)
				mgr := bfd.NewManager(logger)
				defer mgr.Close()
				cfg := config.DefaultConfig()
				cfg.BFD.DefaultDesiredMinTx = time.Second
				cfg.BFD.DefaultRequiredMinRx = time.Second
				cfg.Sessions = []config.SessionConfig{{Peer: "192.0.2.90", Local: "127.0.0.1", Interface: "lo"}}
				checker := newDaemonHealthChecker()
				c := newReconciliationCoordinator(cfg, logger, checker)
				sf := newNthFailureDeclarativeSenderFactory(0)
				if err := c.reconcile(t.Context(), cfg, mgr, sf, nil, nil); err != nil {
					t.Fatal(err)
				}
				snapshots := mgr.Sessions()
				if len(snapshots) != 1 {
					t.Fatal("missing initial session")
				}
				s, ok := mgr.LookupByDiscriminator(snapshots[0].LocalDiscr)
				if !ok {
					t.Fatal("missing live session")
				}
				peer := bfd.ControlPacket{
					Version: bfd.Version, State: bfd.StateInit, DetectMult: 3,
					MyDiscriminator: 7, YourDiscriminator: s.LocalDiscriminator(),
					DesiredMinTxInterval: 100_000, RequiredMinRxInterval: 100_000,
				}
				s.RecvPacket(&peer)
				synctest.Wait()
				if s.State() != bfd.StateUp {
					t.Fatal("session did not reach Up")
				}
				cfg.Sessions[0].DesiredMinTx = 200 * time.Millisecond
				cfg.Sessions[0].RequiredMinRx = 200 * time.Millisecond
				cfg.Sessions[0].RequiredMinRxSet = true
				if failCreation {
					sf.failAt = 2
					cfg.Sessions = append(cfg.Sessions, config.SessionConfig{Peer: "192.0.2.91", Local: "127.0.0.1", Interface: "lo"})
				}
				if err := c.reconcile(t.Context(), cfg, mgr, sf, nil, nil); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if snap := c.Snapshot(); snap.Pending != 1 || snap.AppliedGeneration != 1 || !snap.Stale {
					t.Fatalf("admission falsely converged: %+v", snap)
				}
				assertHealthStatus(t, checker, "", grpchealth.StatusNotServing)
				// Start the production worker after admission; its initial
				// inspection and wake-up both use the retained exact references.
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				go c.runTimerUpdateCompletion(ctx, mgr)
				synctest.Sleep(200 * time.Millisecond)
				peer.State, peer.Final = bfd.StateUp, true
				s.RecvPacket(&peer)
				synctest.Wait()
				snapshot := c.Snapshot()
				base := snapshot.LastReceipt.Sources[sourceBase]
				if base.Pending != 0 || base.TimerPending != 0 || base.Updated != 1 ||
					s.LocalDiscriminator() != snapshots[0].LocalDiscr || s.State() != bfd.StateUp {
					t.Fatalf("timer completion not observed without another reload: %+v", snapshot)
				}
				if failCreation {
					if snapshot.Failed != 1 || !snapshot.Stale || snapshot.AppliedGeneration != 1 || sf.calls != 2 {
						t.Fatalf("completion retried/cleared terminal creation failure: %+v calls=%d", snapshot, sf.calls)
					}
				} else {
					if snapshot.Stale || snapshot.AppliedGeneration != 2 {
						t.Fatalf("completion did not advance desired generation: %+v", snapshot)
					}
					assertHealthStatus(t, checker, "", grpchealth.StatusServing)
				}
				c.observeTimerUpdates(1, mgr)
				c.observeTimerUpdates(2, mgr)
				synctest.Wait()
				if c.Snapshot() != snapshot {
					t.Fatal("duplicate/stale observation changed receipt")
				}
				cancel()
				synctest.Wait()
			})
		})
	}
}
