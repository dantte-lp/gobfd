package main

import (
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestReconciliationRejectsUnqualifiedPolicyBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		before, after uint16
		sharedTTL     bool
	}{
		{name: "enable unprepared padding", after: 128},
		{name: "disable padding", before: 128},
		{name: "resize unknown sender", before: 128, after: 512},
		{name: "shared receive policy", sharedTTL: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				logger := slog.New(slog.DiscardHandler)
				cfg := config.DefaultConfig()
				cfg.Sessions = []config.SessionConfig{{
					Peer: "192.0.2.90", Local: "127.0.0.1", Interface: "lo", PaddedPduSize: tc.before,
				}}
				if tc.sharedTTL {
					cfg.Sessions[0].Type, cfg.Sessions[0].Interface = "multi_hop", ""
				}
				mgr := bfd.NewManager(logger)
				defer mgr.Close()
				c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
				sf := newNthFailureDeclarativeSenderFactory(0)
				level := new(slog.LevelVar)
				if err := c.reconcile(t.Context(), cfg, mgr, sf, level); err != nil {
					t.Fatal(err)
				}
				if tc.sharedTTL {
					params, err := configSessionToBFD(cfg.Sessions[0], cfg.BFD)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = mgr.CreateSession(t.Context(), params, candidateTestSenderLeaseFactory()); err != nil {
						t.Fatal(err)
					}
				}
				synctest.Wait()
				before, original := c.Snapshot(), mgr.Sessions()[0]
				calls, closed := sf.calls, len(sf.closed)
				candidate := cloneStartupContractTestConfig(cfg)
				candidate.Log.Level = "debug"
				candidate.Sessions[0].PaddedPduSize = tc.after
				candidate.Sessions[0].DetectMult = 5
				if tc.sharedTTL {
					candidate.Sessions[0].MultihopMaxHops, candidate.Sessions[0].MultihopMaxHopsSet = 5, true
				}
				err := c.reconcile(t.Context(), candidate, mgr, sf, level)
				if !errors.Is(err, bfd.ErrSessionParameterConflict) {
					t.Fatalf("unqualified policy admission = %v, want ErrSessionParameterConflict", err)
				}
				synctest.Wait()
				if c.Snapshot() != before || sf.calls != calls || len(sf.closed) != closed || level.Level() != slog.LevelInfo {
					t.Fatal("unqualified policy changed generation, resources or log level")
				}
				session, exists := mgr.LookupByDiscriminator(original.LocalDiscr)
				if !exists || session.PaddedPduSize() != tc.before || session.DetectMultiplier() != original.DetectMultiplier {
					t.Fatal("unqualified mixed policy partially changed the original session")
				}
			})
		})
	}
}
