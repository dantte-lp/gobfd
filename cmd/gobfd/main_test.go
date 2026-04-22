package main

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

// =========================================================================
// 4.1 — configSessionToBFD
// =========================================================================

func TestConfigSessionToBFD(t *testing.T) {
	t.Parallel()

	defaults := config.BFDConfig{
		DefaultDesiredMinTx:     1 * time.Second,
		DefaultRequiredMinRx:    1 * time.Second,
		DefaultDetectMultiplier: 3,
		AlignIntervals:          false,
		DefaultPaddedPduSize:    0,
	}

	tests := []struct {
		name     string
		sc       config.SessionConfig
		defaults config.BFDConfig
		wantErr  bool
		check    func(t *testing.T, cfg bfd.SessionConfig)
	}{
		{
			name: "valid IPv4 single_hop",
			sc: config.SessionConfig{
				Peer:  "192.0.2.1",
				Local: "192.0.2.2",
				Type:  "single_hop",
			},
			defaults: defaults,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.PeerAddr != netip.MustParseAddr("192.0.2.1") {
					t.Errorf("PeerAddr = %v, want 192.0.2.1", cfg.PeerAddr)
				}
				if cfg.LocalAddr != netip.MustParseAddr("192.0.2.2") {
					t.Errorf("LocalAddr = %v, want 192.0.2.2", cfg.LocalAddr)
				}
				if cfg.Type != bfd.SessionTypeSingleHop {
					t.Errorf("Type = %v, want SingleHop", cfg.Type)
				}
				if cfg.Role != bfd.RoleActive {
					t.Errorf("Role = %v, want Active", cfg.Role)
				}
			},
		},
		{
			name: "valid IPv6 single_hop",
			sc: config.SessionConfig{
				Peer:  "2001:db8::1",
				Local: "2001:db8::2",
				Type:  "single_hop",
			},
			defaults: defaults,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.PeerAddr != netip.MustParseAddr("2001:db8::1") {
					t.Errorf("PeerAddr = %v, want 2001:db8::1", cfg.PeerAddr)
				}
			},
		},
		{
			name: "multi_hop type",
			sc: config.SessionConfig{
				Peer:  "10.0.0.1",
				Local: "10.0.0.2",
				Type:  "multi_hop",
			},
			defaults: defaults,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.Type != bfd.SessionTypeMultiHop {
					t.Errorf("Type = %v, want MultiHop", cfg.Type)
				}
			},
		},
		{
			name: "defaults applied when per-session values are zero",
			sc: config.SessionConfig{
				Peer: "10.0.0.1",
			},
			defaults: defaults,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.DesiredMinTxInterval != 1*time.Second {
					t.Errorf("DesiredMinTxInterval = %v, want 1s", cfg.DesiredMinTxInterval)
				}
				if cfg.RequiredMinRxInterval != 1*time.Second {
					t.Errorf("RequiredMinRxInterval = %v, want 1s", cfg.RequiredMinRxInterval)
				}
				if cfg.DetectMultiplier != 3 {
					t.Errorf("DetectMultiplier = %v, want 3", cfg.DetectMultiplier)
				}
			},
		},
		{
			name: "per-session timers override defaults",
			sc: config.SessionConfig{
				Peer:          "10.0.0.1",
				DesiredMinTx:  100 * time.Millisecond,
				RequiredMinRx: 200 * time.Millisecond,
				DetectMult:    5,
			},
			defaults: defaults,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.DesiredMinTxInterval != 100*time.Millisecond {
					t.Errorf("DesiredMinTxInterval = %v, want 100ms", cfg.DesiredMinTxInterval)
				}
				if cfg.RequiredMinRxInterval != 200*time.Millisecond {
					t.Errorf("RequiredMinRxInterval = %v, want 200ms", cfg.RequiredMinRxInterval)
				}
				if cfg.DetectMultiplier != 5 {
					t.Errorf("DetectMultiplier = %v, want 5", cfg.DetectMultiplier)
				}
			},
		},
		{
			name: "align_intervals rounds up to RFC 7419 common interval",
			sc: config.SessionConfig{
				Peer:          "10.0.0.1",
				DesiredMinTx:  15 * time.Millisecond, // Between 10ms and 20ms
				RequiredMinRx: 15 * time.Millisecond,
			},
			defaults: config.BFDConfig{
				DefaultDesiredMinTx:     1 * time.Second,
				DefaultRequiredMinRx:    1 * time.Second,
				DefaultDetectMultiplier: 3,
				AlignIntervals:          true,
			},
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				// 15ms aligned → 20ms (next RFC 7419 common interval).
				if cfg.DesiredMinTxInterval != 20*time.Millisecond {
					t.Errorf("DesiredMinTxInterval = %v, want 20ms (aligned)", cfg.DesiredMinTxInterval)
				}
				if cfg.RequiredMinRxInterval != 20*time.Millisecond {
					t.Errorf("RequiredMinRxInterval = %v, want 20ms (aligned)", cfg.RequiredMinRxInterval)
				}
			},
		},
		{
			name: "padded_pdu_size from per-session overrides global default",
			sc: config.SessionConfig{
				Peer:          "10.0.0.1",
				PaddedPduSize: 128,
			},
			defaults: config.BFDConfig{
				DefaultDesiredMinTx:     1 * time.Second,
				DefaultRequiredMinRx:    1 * time.Second,
				DefaultDetectMultiplier: 3,
				DefaultPaddedPduSize:    64,
			},
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.PaddedPduSize != 128 {
					t.Errorf("PaddedPduSize = %v, want 128", cfg.PaddedPduSize)
				}
			},
		},
		{
			name: "padded_pdu_size falls back to global default",
			sc: config.SessionConfig{
				Peer: "10.0.0.1",
			},
			defaults: config.BFDConfig{
				DefaultDesiredMinTx:     1 * time.Second,
				DefaultRequiredMinRx:    1 * time.Second,
				DefaultDetectMultiplier: 3,
				DefaultPaddedPduSize:    64,
			},
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.PaddedPduSize != 64 {
					t.Errorf("PaddedPduSize = %v, want 64 (global default)", cfg.PaddedPduSize)
				}
			},
		},
		{
			name: "detect_mult overflow (>255)",
			sc: config.SessionConfig{
				Peer:       "10.0.0.1",
				DetectMult: 256,
			},
			defaults: defaults,
			wantErr:  true,
		},
		{
			name: "invalid peer address",
			sc: config.SessionConfig{
				Peer: "not-an-ip",
			},
			defaults: defaults,
			wantErr:  true,
		},
		{
			name: "empty peer address",
			sc: config.SessionConfig{
				Peer: "",
			},
			defaults: defaults,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := configSessionToBFD(tt.sc, tt.defaults)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// =========================================================================
// 4.2 — buildUnsolicitedPolicy
// =========================================================================

func TestBuildUnsolicitedPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     config.UnsolicitedConfig
		wantErr bool
		check   func(t *testing.T, p *bfd.UnsolicitedPolicy)
	}{
		{
			name: "valid prefixes",
			cfg: config.UnsolicitedConfig{
				Enabled:     true,
				MaxSessions: 100,
				Interfaces: map[string]config.UnsolicitedInterfaceConfig{
					"eth0": {
						Enabled:         true,
						AllowedPrefixes: []string{"10.0.0.0/24", "172.16.0.0/16"},
					},
				},
				SessionDefaults: config.UnsolicitedSessionDefaultsConfig{
					DesiredMinTx:  1 * time.Second,
					RequiredMinRx: 1 * time.Second,
					DetectMult:    3,
				},
			},
			check: func(t *testing.T, p *bfd.UnsolicitedPolicy) {
				t.Helper()
				if !p.Enabled {
					t.Error("Enabled = false, want true")
				}
				if p.MaxSessions != 100 {
					t.Errorf("MaxSessions = %d, want 100", p.MaxSessions)
				}
				ifCfg, ok := p.Interfaces["eth0"]
				if !ok {
					t.Fatal("missing eth0 interface")
				}
				if len(ifCfg.AllowedPrefixes) != 2 {
					t.Errorf("AllowedPrefixes count = %d, want 2", len(ifCfg.AllowedPrefixes))
				}
			},
		},
		{
			name: "invalid prefix",
			cfg: config.UnsolicitedConfig{
				Enabled: true,
				Interfaces: map[string]config.UnsolicitedInterfaceConfig{
					"eth0": {
						Enabled:         true,
						AllowedPrefixes: []string{"not-a-prefix"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "detect_mult overflow",
			cfg: config.UnsolicitedConfig{
				Enabled: true,
				Interfaces: map[string]config.UnsolicitedInterfaceConfig{
					"eth0": {Enabled: true},
				},
				SessionDefaults: config.UnsolicitedSessionDefaultsConfig{
					DetectMult: 256,
				},
			},
			wantErr: true,
		},
		{
			name: "empty interfaces map",
			cfg: config.UnsolicitedConfig{
				Enabled:    true,
				Interfaces: map[string]config.UnsolicitedInterfaceConfig{},
				SessionDefaults: config.UnsolicitedSessionDefaultsConfig{
					DetectMult: 3,
				},
			},
			check: func(t *testing.T, p *bfd.UnsolicitedPolicy) {
				t.Helper()
				if len(p.Interfaces) != 0 {
					t.Errorf("Interfaces count = %d, want 0", len(p.Interfaces))
				}
			},
		},
		{
			name: "session defaults propagated",
			cfg: config.UnsolicitedConfig{
				Enabled:    true,
				Interfaces: map[string]config.UnsolicitedInterfaceConfig{},
				SessionDefaults: config.UnsolicitedSessionDefaultsConfig{
					DesiredMinTx:  500 * time.Millisecond,
					RequiredMinRx: 500 * time.Millisecond,
					DetectMult:    5,
				},
				CleanupTimeout: 30 * time.Second,
			},
			check: func(t *testing.T, p *bfd.UnsolicitedPolicy) {
				t.Helper()
				if p.SessionDefaults.DesiredMinTxInterval != 500*time.Millisecond {
					t.Errorf("DesiredMinTxInterval = %v, want 500ms", p.SessionDefaults.DesiredMinTxInterval)
				}
				if p.SessionDefaults.DetectMultiplier != 5 {
					t.Errorf("DetectMultiplier = %v, want 5", p.SessionDefaults.DetectMultiplier)
				}
				if p.CleanupTimeout != 30*time.Second {
					t.Errorf("CleanupTimeout = %v, want 30s", p.CleanupTimeout)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := buildUnsolicitedPolicy(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

// =========================================================================
// 4.3 — configEchoToBFD
// =========================================================================

func TestConfigEchoToBFD(t *testing.T) {
	t.Parallel()

	defaults := config.EchoConfig{
		Enabled:                 true,
		DefaultTxInterval:       100 * time.Millisecond,
		DefaultDetectMultiplier: 3,
	}

	tests := []struct {
		name     string
		ep       config.EchoPeerConfig
		defaults config.EchoConfig
		wantErr  bool
		check    func(t *testing.T, cfg bfd.EchoSessionConfig)
	}{
		{
			name: "valid with default fallback",
			ep: config.EchoPeerConfig{
				Peer:  "192.0.2.1",
				Local: "192.0.2.2",
			},
			defaults: defaults,
			check: func(t *testing.T, cfg bfd.EchoSessionConfig) {
				t.Helper()
				if cfg.PeerAddr != netip.MustParseAddr("192.0.2.1") {
					t.Errorf("PeerAddr = %v, want 192.0.2.1", cfg.PeerAddr)
				}
				if cfg.TxInterval != 100*time.Millisecond {
					t.Errorf("TxInterval = %v, want 100ms", cfg.TxInterval)
				}
				if cfg.DetectMultiplier != 3 {
					t.Errorf("DetectMultiplier = %v, want 3", cfg.DetectMultiplier)
				}
			},
		},
		{
			name: "per-peer overrides defaults",
			ep: config.EchoPeerConfig{
				Peer:       "192.0.2.1",
				Local:      "192.0.2.2",
				TxInterval: 50 * time.Millisecond,
				DetectMult: 5,
			},
			defaults: defaults,
			check: func(t *testing.T, cfg bfd.EchoSessionConfig) {
				t.Helper()
				if cfg.TxInterval != 50*time.Millisecond {
					t.Errorf("TxInterval = %v, want 50ms", cfg.TxInterval)
				}
				if cfg.DetectMultiplier != 5 {
					t.Errorf("DetectMultiplier = %v, want 5", cfg.DetectMultiplier)
				}
			},
		},
		{
			name: "detect_mult overflow",
			ep: config.EchoPeerConfig{
				Peer:       "192.0.2.1",
				Local:      "192.0.2.2",
				DetectMult: 256,
			},
			defaults: defaults,
			wantErr:  true,
		},
		{
			name: "invalid peer address",
			ep: config.EchoPeerConfig{
				Peer:  "invalid",
				Local: "192.0.2.2",
			},
			defaults: defaults,
			wantErr:  true,
		},
		{
			name: "invalid local address",
			ep: config.EchoPeerConfig{
				Peer:  "192.0.2.1",
				Local: "not-an-ip",
			},
			defaults: defaults,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := configEchoToBFD(tt.ep, tt.defaults)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// =========================================================================
// 4.4 — configMicroBFDToBFD
// =========================================================================

func TestConfigMicroBFDToBFD(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		gc      config.MicroBFDGroupConfig
		wantErr bool
		check   func(t *testing.T, cfg bfd.MicroBFDConfig)
	}{
		{
			name: "valid group",
			gc: config.MicroBFDGroupConfig{
				LAGInterface:   "bond0",
				MemberLinks:    []string{"eth0", "eth1"},
				PeerAddr:       "192.0.2.1",
				LocalAddr:      "192.0.2.2",
				DesiredMinTx:   100 * time.Millisecond,
				RequiredMinRx:  100 * time.Millisecond,
				DetectMult:     3,
				MinActiveLinks: 1,
			},
			check: func(t *testing.T, cfg bfd.MicroBFDConfig) {
				t.Helper()
				if cfg.LAGInterface != "bond0" {
					t.Errorf("LAGInterface = %q, want bond0", cfg.LAGInterface)
				}
				if cfg.PeerAddr != netip.MustParseAddr("192.0.2.1") {
					t.Errorf("PeerAddr = %v, want 192.0.2.1", cfg.PeerAddr)
				}
				if cfg.LocalAddr != netip.MustParseAddr("192.0.2.2") {
					t.Errorf("LocalAddr = %v, want 192.0.2.2", cfg.LocalAddr)
				}
				if len(cfg.MemberLinks) != 2 {
					t.Errorf("MemberLinks count = %d, want 2", len(cfg.MemberLinks))
				}
				if cfg.DetectMultiplier != 3 {
					t.Errorf("DetectMultiplier = %d, want 3", cfg.DetectMultiplier)
				}
			},
		},
		{
			name: "invalid peer address",
			gc: config.MicroBFDGroupConfig{
				PeerAddr:  "not-valid",
				LocalAddr: "192.0.2.2",
			},
			wantErr: true,
		},
		{
			name: "invalid local address",
			gc: config.MicroBFDGroupConfig{
				PeerAddr:  "192.0.2.1",
				LocalAddr: "not-valid",
			},
			wantErr: true,
		},
		{
			name: "detect_mult overflow",
			gc: config.MicroBFDGroupConfig{
				PeerAddr:   "192.0.2.1",
				LocalAddr:  "192.0.2.2",
				DetectMult: 256,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := configMicroBFDToBFD(tt.gc)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// =========================================================================
// 4.5 — buildOverlaySessionConfig
// =========================================================================

func TestBuildOverlaySessionConfig(t *testing.T) {
	t.Parallel()

	defaults := overlayTimerDefaults{
		desiredMinTx:  1 * time.Second,
		requiredMinRx: 1 * time.Second,
		detectMult:    3,
	}

	tests := []struct {
		name       string
		peerStr    string
		localStr   string
		peerTx     time.Duration
		peerRx     time.Duration
		peerDetect uint32
		defaults   overlayTimerDefaults
		sessType   bfd.SessionType
		wantErr    bool
		check      func(t *testing.T, cfg bfd.SessionConfig)
	}{
		{
			name:     "VXLAN defaults applied",
			peerStr:  "10.0.0.1",
			localStr: "10.0.0.2",
			defaults: defaults,
			sessType: bfd.SessionTypeVXLAN,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.Type != bfd.SessionTypeVXLAN {
					t.Errorf("Type = %v, want VXLAN", cfg.Type)
				}
				if cfg.DesiredMinTxInterval != 1*time.Second {
					t.Errorf("DesiredMinTxInterval = %v, want 1s", cfg.DesiredMinTxInterval)
				}
				if cfg.DetectMultiplier != 3 {
					t.Errorf("DetectMultiplier = %d, want 3", cfg.DetectMultiplier)
				}
			},
		},
		{
			name:     "Geneve with per-peer overrides",
			peerStr:  "10.0.0.1",
			localStr: "10.0.0.2",
			peerTx:   200 * time.Millisecond,
			peerRx:   300 * time.Millisecond,
			defaults: defaults,
			sessType: bfd.SessionTypeGeneve,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.Type != bfd.SessionTypeGeneve {
					t.Errorf("Type = %v, want Geneve", cfg.Type)
				}
				if cfg.DesiredMinTxInterval != 200*time.Millisecond {
					t.Errorf("DesiredMinTxInterval = %v, want 200ms", cfg.DesiredMinTxInterval)
				}
				if cfg.RequiredMinRxInterval != 300*time.Millisecond {
					t.Errorf("RequiredMinRxInterval = %v, want 300ms", cfg.RequiredMinRxInterval)
				}
			},
		},
		{
			name:     "empty local address allowed",
			peerStr:  "10.0.0.1",
			localStr: "",
			defaults: defaults,
			sessType: bfd.SessionTypeVXLAN,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.LocalAddr.IsValid() {
					t.Errorf("LocalAddr = %v, want invalid (zero)", cfg.LocalAddr)
				}
			},
		},
		{
			name:     "invalid peer address",
			peerStr:  "not-an-ip",
			localStr: "10.0.0.2",
			defaults: defaults,
			sessType: bfd.SessionTypeVXLAN,
			wantErr:  true,
		},
		{
			name:     "invalid local address",
			peerStr:  "10.0.0.1",
			localStr: "invalid",
			defaults: defaults,
			sessType: bfd.SessionTypeVXLAN,
			wantErr:  true,
		},
		{
			name:       "detect_mult overflow",
			peerStr:    "10.0.0.1",
			localStr:   "10.0.0.2",
			peerDetect: 256,
			defaults:   overlayTimerDefaults{detectMult: 0},
			sessType:   bfd.SessionTypeVXLAN,
			wantErr:    true,
		},
		{
			name:     "role is always Active",
			peerStr:  "10.0.0.1",
			localStr: "10.0.0.2",
			defaults: defaults,
			sessType: bfd.SessionTypeGeneve,
			check: func(t *testing.T, cfg bfd.SessionConfig) {
				t.Helper()
				if cfg.Role != bfd.RoleActive {
					t.Errorf("Role = %v, want Active", cfg.Role)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := buildOverlaySessionConfig(
				tt.peerStr, tt.localStr,
				tt.peerTx, tt.peerRx, tt.peerDetect,
				tt.defaults, tt.sessType,
			)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// =========================================================================
// 4.6 — loadConfig
// =========================================================================

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
		check   func(t *testing.T, cfg *config.Config)
	}{
		{
			name: "empty path returns defaults",
			path: "",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.GRPC.Addr != ":50051" {
					t.Errorf("GRPC.Addr = %q, want :50051", cfg.GRPC.Addr)
				}
				if cfg.BFD.DefaultDetectMultiplier != 3 {
					t.Errorf("DefaultDetectMultiplier = %d, want 3", cfg.BFD.DefaultDetectMultiplier)
				}
			},
		},
		{
			name:    "nonexistent file returns error",
			path:    "/tmp/gobfd-test-nonexistent-config-file.yaml",
			wantErr: true,
		},
		{
			name: "valid YAML file",
			path: func() string {
				f, err := os.CreateTemp(t.TempDir(), "gobfd-test-*.yaml")
				if err != nil {
					t.Fatalf("create temp file: %v", err)
				}
				if _, err := f.WriteString("grpc:\n  addr: ':9999'\n"); err != nil {
					t.Fatalf("write temp file: %v", err)
				}
				f.Close()
				return f.Name()
			}(),
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.GRPC.Addr != ":9999" {
					t.Errorf("GRPC.Addr = %q, want :9999", cfg.GRPC.Addr)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := loadConfig(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// =========================================================================
// 4.7 — newLoggerWithLevel
// =========================================================================

func TestNewLoggerWithLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
	}{
		{name: "text format", format: "text"},
		{name: "json format", format: "json"},
		{name: "empty format defaults to json", format: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			level := new(slog.LevelVar)
			level.Set(slog.LevelInfo)

			cfg := config.LogConfig{
				Level:  "info",
				Format: tt.format,
			}

			logger := newLoggerWithLevel(cfg, level)
			if logger == nil {
				t.Fatal("logger is nil")
			}

			// Verify the logger can write without panic.
			logger.Info("test log message")
		})
	}

	t.Run("dynamic level change", func(t *testing.T) {
		t.Parallel()
		level := new(slog.LevelVar)
		level.Set(slog.LevelInfo)

		cfg := config.LogConfig{Format: "json"}
		logger := newLoggerWithLevel(cfg, level)

		// Initially Info level.
		if !logger.Enabled(context.Background(), slog.LevelInfo) {
			t.Error("expected Info to be enabled at Info level")
		}
		if logger.Enabled(context.Background(), slog.LevelDebug) {
			t.Error("expected Debug to be disabled at Info level")
		}

		// Change to Debug level dynamically.
		level.Set(slog.LevelDebug)

		if !logger.Enabled(context.Background(), slog.LevelDebug) {
			t.Error("expected Debug to be enabled after level change")
		}
	})
}

// =========================================================================
// Additional edge case: errDetectMultOverflow sentinel
// =========================================================================

func TestErrDetectMultOverflow(t *testing.T) {
	t.Parallel()

	// configSessionToBFD
	_, err := configSessionToBFD(config.SessionConfig{
		Peer:       "10.0.0.1",
		DetectMult: 300,
	}, config.BFDConfig{DefaultDetectMultiplier: 3})
	if !errors.Is(err, errDetectMultOverflow) {
		t.Errorf("configSessionToBFD: expected errDetectMultOverflow, got %v", err)
	}

	// configEchoToBFD
	_, err = configEchoToBFD(config.EchoPeerConfig{
		Peer:       "10.0.0.1",
		Local:      "10.0.0.2",
		DetectMult: 300,
	}, config.EchoConfig{DefaultDetectMultiplier: 3})
	if !errors.Is(err, errDetectMultOverflow) {
		t.Errorf("configEchoToBFD: expected errDetectMultOverflow, got %v", err)
	}

	// configMicroBFDToBFD
	_, err = configMicroBFDToBFD(config.MicroBFDGroupConfig{
		PeerAddr:   "10.0.0.1",
		LocalAddr:  "10.0.0.2",
		DetectMult: 300,
	})
	if !errors.Is(err, errDetectMultOverflow) {
		t.Errorf("configMicroBFDToBFD: expected errDetectMultOverflow, got %v", err)
	}

	// buildOverlaySessionConfig
	_, err = buildOverlaySessionConfig("10.0.0.1", "10.0.0.2",
		0, 0, 300, overlayTimerDefaults{}, bfd.SessionTypeVXLAN)
	if !errors.Is(err, errDetectMultOverflow) {
		t.Errorf("buildOverlaySessionConfig: expected errDetectMultOverflow, got %v", err)
	}
}
