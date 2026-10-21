package main

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"slices"
	"testing"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestInvalidLogYAMLReloadPreservesRuntime(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	startup := config.DefaultConfig()
	startup.Log.Level = "warn"
	startup.Sessions = []config.SessionConfig{{Peer: "192.0.2.45", Local: "127.0.28.1"}}
	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(startup, logger, checker)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	sf := newUDPSenderFactory(startup.Socket.WriteBufferSize)
	t.Cleanup(func() { cleanupCandidateTestSenders(t, sf) })
	level := new(slog.LevelVar)
	if err := coordinator.reconcile(context.Background(), startup, mgr, sf, level); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	before := coordinator.Snapshot()
	sessions := mgr.Sessions()
	resources := maps.Clone(sf.senders)
	if before.DesiredGeneration != 1 || before.AppliedGeneration != 1 || before.Stale ||
		len(sessions) != 1 || len(resources) != 1 || level.Level() != slog.LevelWarn {
		t.Fatalf("initial runtime not converged: snapshot=%+v sessions=%d resources=%d level=%s",
			before, len(sessions), len(resources), level.Level())
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusServing)
	path := t.TempDir() + "/invalid-log.yml"
	if err := os.WriteFile(path, []byte("log:\n  level: verbose\nsessions: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reloadConfig(context.Background(), path, level, mgr, sf, coordinator, logger)
	if got := coordinator.Snapshot(); got != before {
		t.Errorf("generation/status changed: got%+v want%+v", got, before)
	}
	if got := level.Level(); got != slog.LevelWarn {
		t.Errorf("log level=%s, want WARN", got)
	}
	if got := mgr.Sessions(); !slices.Equal(got, sessions) {
		t.Errorf("sessions changed: got%+v want%+v", got, sessions)
	}
	if !maps.Equal(sf.senders, resources) {
		t.Errorf("sender resources changed: got%v want%v", sf.senders, resources)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusServing)
}
