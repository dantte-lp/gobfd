package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestStartupRuntimeContractReportsBoundedChangedFields(t *testing.T) {
	t.Parallel()

	startup := config.DefaultConfig()
	startup.Sessions = []config.SessionConfig{{
		Peer: "192.0.2.1", Local: "127.0.0.1", Interface: "lo",
	}}
	startup.VXLAN.Enabled = true
	startup.VXLAN.Peers = []config.VXLANPeerConfig{
		{Peer: "192.0.2.20", Local: "127.0.0.1"},
		{Peer: "192.0.2.21", Local: "127.0.0.2"},
	}
	startup.Geneve.Enabled = true
	startup.Geneve.DefaultVNI = 100
	startup.Geneve.Peers = []config.GenevePeerConfig{
		{Peer: "192.0.2.30", Local: "127.0.0.1"},
		{Peer: "192.0.2.31", Local: "127.0.0.2", VNI: 200},
	}
	contract := newStartupRuntimeContract(startup)

	tests := []struct {
		name string
		edit func(*config.Config)
		want startupFieldID
	}{
		{"grpc address", func(cfg *config.Config) { cfg.GRPC.Addr = ":50052" }, startupFieldGRPCAddr},
		{"metrics path", func(cfg *config.Config) { cfg.Metrics.Path = "/prometheus" }, startupFieldMetricsPath},
		{"log format", func(cfg *config.Config) { cfg.Log.Format = "text" }, startupFieldLogFormat},
		{"socket read buffer", func(cfg *config.Config) { cfg.Socket.ReadBufferSize++ }, startupFieldSocketReadBufferSize},
		{"gobgp TLS", func(cfg *config.Config) { cfg.GoBGP.TLS.ServerName = "router.example" }, startupFieldGoBGPTLS},
		{"new control listener", func(cfg *config.Config) {
			cfg.Sessions = append(cfg.Sessions, config.SessionConfig{
				Peer: "192.0.2.2", Local: "127.0.0.2", Interface: "lo",
			})
		}, startupFieldControlListenerBindings},
		{"new VXLAN local binding", func(cfg *config.Config) {
			cfg.VXLAN.Peers = append(cfg.VXLAN.Peers, config.VXLANPeerConfig{
				Peer: "192.0.2.22", Local: "127.0.0.3",
			})
		}, startupFieldVXLANListenerBinding},
		{"new Geneve local binding", func(cfg *config.Config) {
			cfg.Geneve.Peers = append(cfg.Geneve.Peers, config.GenevePeerConfig{
				Peer: "192.0.2.32", Local: "127.0.0.3", VNI: 100,
			})
		}, startupFieldGeneveListenerBinding},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			candidate := cloneStartupContractTestConfig(startup)
			tc.edit(candidate)
			changed := contract.changedFields(candidate)
			if !slices.Contains(changed, tc.want) {
				t.Fatalf("changed fields = %v, want %s", changed, tc.want)
			}
		})
	}

	dynamic := cloneStartupContractTestConfig(startup)
	dynamic.Log.Level = "debug"
	dynamic.Sessions[0].Peer = "192.0.2.9"
	dynamic.Sessions[0].DesiredMinTx *= 2
	dynamic.Geneve.Peers[0].VNI = startup.Geneve.DefaultVNI
	if changed := contract.changedFields(dynamic); len(changed) != 0 {
		t.Fatalf("dynamic-only changed fields = %v, want none", changed)
	}

	remaining := cloneStartupContractTestConfig(startup)
	remaining.VXLAN.Peers = remaining.VXLAN.Peers[1:]
	remaining.Geneve.Peers = remaining.Geneve.Peers[1:]
	if changed := contract.changedFields(remaining); len(changed) != 0 {
		t.Fatalf("removed first overlay peer changed fields = %v, want none", changed)
	}
}

func TestReconciliationCoordinatorRejectsStartupChangeBeforeMutation(t *testing.T) {
	t.Parallel()

	startup := config.DefaultConfig()
	startup.Sessions = []config.SessionConfig{{
		Peer: "192.0.2.1", Local: "127.0.0.1", Interface: "lo",
	}}
	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(startup, slog.New(slog.DiscardHandler), checker)
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	before := coordinator.Snapshot()
	candidate := cloneStartupContractTestConfig(startup)
	candidate.GRPC.Addr = ":50052"
	candidate.Log.Level = "debug"
	candidate.Sessions = []config.SessionConfig{{
		Peer: "192.0.2.10", Local: "127.0.0.1", Interface: "lo",
		Auth: config.AuthConfig{Type: "simple_password", Secret: "reload-secret"},
	}}
	err := coordinator.reconcile(
		context.Background(), candidate, mgr,
		newNthFailureDeclarativeSenderFactory(0), &overlayRuntime{}, level,
	)
	if !errors.Is(err, errStartupConfigChanged) {
		t.Fatalf("reconcile error = %v, want errStartupConfigChanged", err)
	}
	var changeErr *startupConfigChangeError
	if !errors.As(err, &changeErr) {
		t.Fatalf("reconcile error type = %T, want *startupConfigChangeError", err)
	}
	if got := changeErr.FieldIDs(); !slices.Equal(got, []startupFieldID{startupFieldGRPCAddr}) {
		t.Fatalf("changed fields = %v, want [%s]", got, startupFieldGRPCAddr)
	}
	if strings.Contains(err.Error(), "reload-secret") || strings.Contains(err.Error(), candidate.GRPC.Addr) {
		t.Fatalf("startup change error leaked configuration value: %q", err)
	}
	if got := coordinator.Snapshot(); got != before {
		t.Fatalf("snapshot after rejection = %+v, want %+v", got, before)
	}
	if got := level.Level(); got != slog.LevelInfo {
		t.Fatalf("log level after rejection = %s, want info", got)
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Fatalf("sessions after rejection = %d, want 0", got)
	}
}

func cloneStartupContractTestConfig(src *config.Config) *config.Config {
	dst := *src
	dst.Sessions = slices.Clone(src.Sessions)
	dst.Echo.Peers = slices.Clone(src.Echo.Peers)
	dst.MicroBFD.Groups = slices.Clone(src.MicroBFD.Groups)
	dst.VXLAN.Peers = slices.Clone(src.VXLAN.Peers)
	dst.Geneve.Peers = slices.Clone(src.Geneve.Peers)
	return &dst
}
