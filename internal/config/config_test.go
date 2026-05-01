package config_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/config"
)

const (
	testLocalAddr    = "10.0.0.2"
	testGoBGPAddr    = "127.0.0.1:50051"
	testStrategyPeer = "disable-peer"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()

	if cfg.GRPC.Addr != ":50051" {
		t.Errorf("GRPC.Addr = %q, want %q", cfg.GRPC.Addr, ":50051")
	}

	if cfg.Metrics.Addr != ":9100" {
		t.Errorf("Metrics.Addr = %q, want %q", cfg.Metrics.Addr, ":9100")
	}

	if cfg.Metrics.Path != "/metrics" {
		t.Errorf("Metrics.Path = %q, want %q", cfg.Metrics.Path, "/metrics")
	}

	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "info")
	}

	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "json")
	}

	if cfg.BFD.DefaultDesiredMinTx != 1*time.Second {
		t.Errorf("BFD.DefaultDesiredMinTx = %v, want %v", cfg.BFD.DefaultDesiredMinTx, 1*time.Second)
	}

	if cfg.BFD.DefaultRequiredMinRx != 1*time.Second {
		t.Errorf("BFD.DefaultRequiredMinRx = %v, want %v", cfg.BFD.DefaultRequiredMinRx, 1*time.Second)
	}

	if cfg.BFD.DefaultDetectMultiplier != 3 {
		t.Errorf("BFD.DefaultDetectMultiplier = %d, want %d", cfg.BFD.DefaultDetectMultiplier, 3)
	}

	if cfg.VXLAN.Backend != config.OverlayBackendUserspaceUDP {
		t.Errorf("VXLAN.Backend = %q, want %q", cfg.VXLAN.Backend, config.OverlayBackendUserspaceUDP)
	}

	if cfg.Geneve.Backend != config.OverlayBackendUserspaceUDP {
		t.Errorf("Geneve.Backend = %q, want %q", cfg.Geneve.Backend, config.OverlayBackendUserspaceUDP)
	}

	// Defaults must pass validation.
	if err := config.Validate(cfg); err != nil {
		t.Errorf("DefaultConfig() failed validation: %v", err)
	}
}

func TestLoadFromYAML(t *testing.T) {
	t.Parallel()

	yamlContent := `
grpc:
  addr: ":60000"
metrics:
  addr: ":9200"
  path: "/custom-metrics"
log:
  level: "debug"
  format: "text"
bfd:
  default_desired_min_tx: "500ms"
  default_required_min_rx: "250ms"
  default_detect_multiplier: 5
`

	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", path, err)
	}

	if cfg.GRPC.Addr != ":60000" {
		t.Errorf("GRPC.Addr = %q, want %q", cfg.GRPC.Addr, ":60000")
	}

	if cfg.Metrics.Addr != ":9200" {
		t.Errorf("Metrics.Addr = %q, want %q", cfg.Metrics.Addr, ":9200")
	}

	if cfg.Metrics.Path != "/custom-metrics" {
		t.Errorf("Metrics.Path = %q, want %q", cfg.Metrics.Path, "/custom-metrics")
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "debug")
	}

	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "text")
	}

	if cfg.BFD.DefaultDesiredMinTx != 500*time.Millisecond {
		t.Errorf("BFD.DefaultDesiredMinTx = %v, want %v", cfg.BFD.DefaultDesiredMinTx, 500*time.Millisecond)
	}

	if cfg.BFD.DefaultRequiredMinRx != 250*time.Millisecond {
		t.Errorf("BFD.DefaultRequiredMinRx = %v, want %v", cfg.BFD.DefaultRequiredMinRx, 250*time.Millisecond)
	}

	if cfg.BFD.DefaultDetectMultiplier != 5 {
		t.Errorf("BFD.DefaultDetectMultiplier = %d, want %d", cfg.BFD.DefaultDetectMultiplier, 5)
	}
}

func TestLoadMergesDefaults(t *testing.T) {
	t.Parallel()

	// Partial YAML: only override grpc.addr and log.level.
	// Everything else should inherit from defaults.
	yamlContent := `
grpc:
  addr: ":55555"
log:
  level: "warn"
`

	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", path, err)
	}

	// Overridden values.
	if cfg.GRPC.Addr != ":55555" {
		t.Errorf("GRPC.Addr = %q, want %q", cfg.GRPC.Addr, ":55555")
	}

	if cfg.Log.Level != "warn" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "warn")
	}

	// Default values should be preserved.
	if cfg.Metrics.Addr != ":9100" {
		t.Errorf("Metrics.Addr = %q, want default %q", cfg.Metrics.Addr, ":9100")
	}

	if cfg.Metrics.Path != "/metrics" {
		t.Errorf("Metrics.Path = %q, want default %q", cfg.Metrics.Path, "/metrics")
	}

	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want default %q", cfg.Log.Format, "json")
	}

	if cfg.BFD.DefaultDesiredMinTx != 1*time.Second {
		t.Errorf("BFD.DefaultDesiredMinTx = %v, want default %v", cfg.BFD.DefaultDesiredMinTx, 1*time.Second)
	}

	if cfg.BFD.DefaultRequiredMinRx != 1*time.Second {
		t.Errorf("BFD.DefaultRequiredMinRx = %v, want default %v", cfg.BFD.DefaultRequiredMinRx, 1*time.Second)
	}

	if cfg.BFD.DefaultDetectMultiplier != 3 {
		t.Errorf("BFD.DefaultDetectMultiplier = %d, want default %d", cfg.BFD.DefaultDetectMultiplier, 3)
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "empty grpc addr",
			modify: func(cfg *config.Config) {
				cfg.GRPC.Addr = ""
			},
			wantErr: config.ErrEmptyGRPCAddr,
		},
		{
			name: "zero detect multiplier",
			modify: func(cfg *config.Config) {
				cfg.BFD.DefaultDetectMultiplier = 0
			},
			wantErr: config.ErrInvalidDetectMultiplier,
		},
		{
			name: "zero desired min tx",
			modify: func(cfg *config.Config) {
				cfg.BFD.DefaultDesiredMinTx = 0
			},
			wantErr: config.ErrInvalidDesiredMinTx,
		},
		{
			name: "negative desired min tx",
			modify: func(cfg *config.Config) {
				cfg.BFD.DefaultDesiredMinTx = -1 * time.Second
			},
			wantErr: config.ErrInvalidDesiredMinTx,
		},
		{
			name: "zero required min rx",
			modify: func(cfg *config.Config) {
				cfg.BFD.DefaultRequiredMinRx = 0
			},
			wantErr: config.ErrInvalidRequiredMinRx,
		},
		{
			name: "negative required min rx",
			modify: func(cfg *config.Config) {
				cfg.BFD.DefaultRequiredMinRx = -500 * time.Millisecond
			},
			wantErr: config.ErrInvalidRequiredMinRx,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.DefaultConfig()
			tt.modify(cfg)

			err := config.Validate(cfg)
			if err == nil {
				t.Fatal("Validate() returned nil, want error")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  slog.Level
	}{
		{input: "debug", want: slog.LevelDebug},
		{input: "DEBUG", want: slog.LevelDebug},
		{input: "info", want: slog.LevelInfo},
		{input: "INFO", want: slog.LevelInfo},
		{input: "warn", want: slog.LevelWarn},
		{input: "WARN", want: slog.LevelWarn},
		{input: "error", want: slog.LevelError},
		{input: "Error", want: slog.LevelError},
		{input: "unknown", want: slog.LevelInfo},
		{input: "", want: slog.LevelInfo},
		{input: "trace", want: slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got := config.ParseLogLevel(tt.input)
			if got != tt.want {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	t.Parallel()

	_, err := config.Load("/nonexistent/path/config.yml")
	if err == nil {
		t.Fatal("Load() returned nil error for nonexistent file")
	}
}

// -------------------------------------------------------------------------
// Session Config Tests — Sprint 14 (14.5)
// -------------------------------------------------------------------------

func TestLoadWithSessions(t *testing.T) {
	t.Parallel()

	yamlContent := `
grpc:
  addr: ":50051"
sessions:
  - peer: "10.0.0.1"
    local: "10.0.0.2"
    interface: "eth0"
    type: single_hop
    desired_min_tx: "100ms"
    required_min_rx: "100ms"
    detect_mult: 3
  - peer: "10.0.1.1"
    local: "10.0.1.2"
    type: multi_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 5
`

	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", path, err)
	}

	if len(cfg.Sessions) != 2 {
		t.Fatalf("Sessions count = %d, want 2", len(cfg.Sessions))
	}

	// Verify first session.
	s1 := cfg.Sessions[0]
	if s1.Peer != "10.0.0.1" {
		t.Errorf("Sessions[0].Peer = %q, want %q", s1.Peer, "10.0.0.1")
	}
	if s1.Local != testLocalAddr {
		t.Errorf("Sessions[0].Local = %q, want %q", s1.Local, testLocalAddr)
	}
	if s1.Interface != "eth0" {
		t.Errorf("Sessions[0].Interface = %q, want %q", s1.Interface, "eth0")
	}
	if s1.Type != "single_hop" {
		t.Errorf("Sessions[0].Type = %q, want %q", s1.Type, "single_hop")
	}
	if s1.DesiredMinTx != 100*time.Millisecond {
		t.Errorf("Sessions[0].DesiredMinTx = %v, want %v", s1.DesiredMinTx, 100*time.Millisecond)
	}
	if s1.RequiredMinRx != 100*time.Millisecond {
		t.Errorf("Sessions[0].RequiredMinRx = %v, want %v", s1.RequiredMinRx, 100*time.Millisecond)
	}
	if s1.DetectMult != 3 {
		t.Errorf("Sessions[0].DetectMult = %d, want %d", s1.DetectMult, 3)
	}

	// Verify second session.
	s2 := cfg.Sessions[1]
	if s2.Peer != "10.0.1.1" {
		t.Errorf("Sessions[1].Peer = %q, want %q", s2.Peer, "10.0.1.1")
	}
	if s2.Type != "multi_hop" {
		t.Errorf("Sessions[1].Type = %q, want %q", s2.Type, "multi_hop")
	}
	if s2.DetectMult != 5 {
		t.Errorf("Sessions[1].DetectMult = %d, want %d", s2.DetectMult, 5)
	}

	// Session keys should be distinct.
	if s1.SessionKey() == s2.SessionKey() {
		t.Error("Sessions[0] and Sessions[1] have the same key, expected different")
	}
}

func TestValidateSessionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "empty session peer",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{Peer: "", Local: testLocalAddr},
				}
			},
			wantErr: config.ErrInvalidSessionPeer,
		},
		{
			name: "invalid session peer",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{Peer: "not-an-ip", Local: testLocalAddr},
				}
			},
			wantErr: config.ErrInvalidSessionPeer,
		},
		{
			name: "invalid session type",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr, Type: "bogus"},
				}
			},
			wantErr: config.ErrInvalidSessionType,
		},
		{
			name: "duplicate session keys",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr, Interface: "eth0"},
					{Peer: "10.0.0.1", Local: testLocalAddr, Interface: "eth0"},
				}
			},
			wantErr: config.ErrDuplicateSessionKey,
		},
		{
			name: "invalid auth type",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{
						Peer:  "10.0.0.1",
						Local: testLocalAddr,
						Auth:  config.AuthConfig{Type: "rot13", KeyID: 1, Secret: "secret"},
					},
				}
			},
			wantErr: config.ErrInvalidSessionAuthType,
		},
		{
			name: "auth key ID overflow",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{
						Peer:  "10.0.0.1",
						Local: testLocalAddr,
						Auth:  config.AuthConfig{Type: "keyed_sha1", KeyID: 256, Secret: "secret"},
					},
				}
			},
			wantErr: config.ErrInvalidSessionAuthKeyID,
		},
		{
			name: "sha1 auth secret too long",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{
						Peer:  "10.0.0.1",
						Local: testLocalAddr,
						Auth: config.AuthConfig{
							Type:   "keyed_sha1",
							KeyID:  1,
							Secret: "123456789012345678901",
						},
					},
				}
			},
			wantErr: config.ErrInvalidSessionAuthSecret,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.DefaultConfig()
			tt.modify(cfg)

			err := config.Validate(cfg)
			if err == nil {
				t.Fatal("Validate() returned nil, want error")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSessionValidTypes(t *testing.T) {
	t.Parallel()

	// Both single_hop and multi_hop should be valid.
	for _, typ := range []string{"single_hop", "multi_hop", ""} {
		cfg := config.DefaultConfig()
		cfg.Sessions = []config.SessionConfig{
			{Peer: "10.0.0.1", Local: testLocalAddr, Type: typ},
		}

		if err := config.Validate(cfg); err != nil {
			t.Errorf("Validate() with type %q returned error: %v", typ, err)
		}
	}
}

func TestSessionConfigKey(t *testing.T) {
	t.Parallel()

	sc := config.SessionConfig{
		Peer:      "10.0.0.1",
		Local:     testLocalAddr,
		Interface: "eth0",
	}

	want := "10.0.0.1|10.0.0.2|eth0"
	if got := sc.SessionKey(); got != want {
		t.Errorf("SessionKey() = %q, want %q", got, want)
	}
}

func TestSessionConfigPeerAddr(t *testing.T) {
	t.Parallel()

	sc := config.SessionConfig{Peer: "10.0.0.1"}
	addr, err := sc.PeerAddr()
	if err != nil {
		t.Fatalf("PeerAddr() error: %v", err)
	}
	if addr.String() != "10.0.0.1" {
		t.Errorf("PeerAddr() = %s, want 10.0.0.1", addr)
	}
}

func TestSessionConfigLocalAddr(t *testing.T) {
	t.Parallel()

	sc := config.SessionConfig{Local: testLocalAddr}
	addr, err := sc.LocalAddr()
	if err != nil {
		t.Fatalf("LocalAddr() error: %v", err)
	}
	if addr.String() != testLocalAddr {
		t.Errorf("LocalAddr() = %s, want 10.0.0.2", addr)
	}
}

func TestSessionConfigLocalAddrEmpty(t *testing.T) {
	t.Parallel()

	sc := config.SessionConfig{Local: ""}
	addr, err := sc.LocalAddr()
	if err != nil {
		t.Fatalf("LocalAddr() error: %v", err)
	}
	if addr.IsValid() {
		t.Errorf("LocalAddr() should be zero value for empty, got %s", addr)
	}
}

// -------------------------------------------------------------------------
// Environment Variable Override Tests — Sprint 14 (14.6)
// -------------------------------------------------------------------------

func TestLoadEnvOverrides(t *testing.T) {
	// Environment variable tests cannot be parallel because they modify
	// process-wide state (os.Setenv).

	yamlContent := `
grpc:
  addr: ":50051"
log:
  level: "info"
`
	path := writeTemp(t, yamlContent)

	// Set env overrides.
	t.Setenv("GOBFD_GRPC_ADDR", ":60000")
	t.Setenv("GOBFD_LOG_LEVEL", "debug")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", path, err)
	}

	if cfg.GRPC.Addr != ":60000" {
		t.Errorf("GRPC.Addr = %q, want %q (from env)", cfg.GRPC.Addr, ":60000")
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want %q (from env)", cfg.Log.Level, "debug")
	}
}

func TestLoadEnvOverridesMetrics(t *testing.T) {
	yamlContent := `
grpc:
  addr: ":50051"
metrics:
  addr: ":9100"
  path: "/metrics"
`
	path := writeTemp(t, yamlContent)

	t.Setenv("GOBFD_METRICS_ADDR", ":9200")
	t.Setenv("GOBFD_METRICS_PATH", "/custom")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", path, err)
	}

	if cfg.Metrics.Addr != ":9200" {
		t.Errorf("Metrics.Addr = %q, want %q (from env)", cfg.Metrics.Addr, ":9200")
	}

	if cfg.Metrics.Path != "/custom" {
		t.Errorf("Metrics.Path = %q, want %q (from env)", cfg.Metrics.Path, "/custom")
	}
}

// -------------------------------------------------------------------------
// Echo Peer Config Tests
// -------------------------------------------------------------------------

func TestEchoPeerConfigSessionKey(t *testing.T) {
	t.Parallel()

	ep := config.EchoPeerConfig{Peer: "10.0.0.1", Local: testLocalAddr, Interface: "eth0"}
	want := "echo|10.0.0.1|10.0.0.2|eth0"
	if got := ep.EchoSessionKey(); got != want {
		t.Errorf("EchoSessionKey() = %q, want %q", got, want)
	}
}

func TestEchoPeerConfigPeerAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		peer    string
		wantErr bool
	}{
		{name: "valid IPv4", peer: "10.0.0.1"},
		{name: "valid IPv6", peer: "2001:db8::1"},
		{name: "empty", peer: "", wantErr: true},
		{name: "invalid", peer: "not-an-ip", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ep := config.EchoPeerConfig{Peer: tt.peer}
			_, err := ep.PeerAddr()
			if (err != nil) != tt.wantErr {
				t.Errorf("PeerAddr() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestEchoPeerConfigLocalAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		local string
		valid bool
	}{
		{name: "valid", local: testLocalAddr, valid: true},
		{name: "empty returns zero", local: ""},
		{name: "invalid", local: "bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ep := config.EchoPeerConfig{Local: tt.local}
			addr, err := ep.LocalAddr()
			if tt.local == "" {
				if err != nil || addr.IsValid() {
					t.Errorf("expected zero addr for empty, got %s, err %v", addr, err)
				}
				return
			}
			if tt.valid && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.valid && tt.local != "" && err == nil {
				t.Error("expected error for invalid local")
			}
		})
	}
}

// -------------------------------------------------------------------------
// VXLAN Peer Config Tests
// -------------------------------------------------------------------------

func TestVXLANPeerConfigSessionKey(t *testing.T) {
	t.Parallel()

	vc := config.VXLANPeerConfig{Peer: "10.0.0.1", Local: testLocalAddr}
	want := "vxlan|10.0.0.1|10.0.0.2"
	if got := vc.VXLANSessionKey(); got != want {
		t.Errorf("VXLANSessionKey() = %q, want %q", got, want)
	}
}

func TestVXLANPeerConfigPeerAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		peer    string
		wantErr bool
	}{
		{name: "valid", peer: "10.0.0.1"},
		{name: "empty", peer: "", wantErr: true},
		{name: "invalid", peer: "bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vc := config.VXLANPeerConfig{Peer: tt.peer}
			_, err := vc.PeerAddr()
			if (err != nil) != tt.wantErr {
				t.Errorf("PeerAddr() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestVXLANPeerConfigLocalAddr(t *testing.T) {
	t.Parallel()

	vc := config.VXLANPeerConfig{Local: testLocalAddr}
	addr, err := vc.LocalAddr()
	if err != nil {
		t.Fatalf("LocalAddr() error: %v", err)
	}
	if addr.String() != testLocalAddr {
		t.Errorf("LocalAddr() = %s, want 10.0.0.2", addr)
	}

	// Empty local returns zero.
	vc2 := config.VXLANPeerConfig{Local: ""}
	addr2, err := vc2.LocalAddr()
	if err != nil || addr2.IsValid() {
		t.Errorf("empty Local: addr = %s, err = %v", addr2, err)
	}

	// Invalid local returns error.
	vc3 := config.VXLANPeerConfig{Local: "bad"}
	_, err = vc3.LocalAddr()
	if err == nil {
		t.Error("expected error for invalid local")
	}
}

// -------------------------------------------------------------------------
// Geneve Peer Config Tests
// -------------------------------------------------------------------------

func TestGenevePeerConfigSessionKey(t *testing.T) {
	t.Parallel()

	gc := config.GenevePeerConfig{Peer: "10.0.0.1", Local: testLocalAddr}
	want := "geneve|10.0.0.1|10.0.0.2"
	if got := gc.GeneveSessionKey(); got != want {
		t.Errorf("GeneveSessionKey() = %q, want %q", got, want)
	}
}

func TestGenevePeerConfigPeerAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		peer    string
		wantErr bool
	}{
		{name: "valid", peer: "10.0.0.1"},
		{name: "empty", peer: "", wantErr: true},
		{name: "invalid", peer: "bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gc := config.GenevePeerConfig{Peer: tt.peer}
			_, err := gc.PeerAddr()
			if (err != nil) != tt.wantErr {
				t.Errorf("PeerAddr() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestGenevePeerConfigLocalAddr(t *testing.T) {
	t.Parallel()

	gc := config.GenevePeerConfig{Local: testLocalAddr}
	addr, err := gc.LocalAddr()
	if err != nil {
		t.Fatalf("LocalAddr() error: %v", err)
	}
	if addr.String() != testLocalAddr {
		t.Errorf("LocalAddr() = %s, want 10.0.0.2", addr)
	}

	// Empty local returns zero.
	gc2 := config.GenevePeerConfig{Local: ""}
	addr2, err := gc2.LocalAddr()
	if err != nil || addr2.IsValid() {
		t.Errorf("empty Local: addr = %s, err = %v", addr2, err)
	}

	// Invalid local returns error.
	gc3 := config.GenevePeerConfig{Local: "bad"}
	_, err = gc3.LocalAddr()
	if err == nil {
		t.Error("expected error for invalid local")
	}
}

// -------------------------------------------------------------------------
// Validation: GoBGP, Dampening, Echo, VXLAN, Geneve
// -------------------------------------------------------------------------

func TestValidateGoBGPErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "gobgp disabled passes",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = false
			},
		},
		{
			name: "empty gobgp addr",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = ""
				cfg.GoBGP.Strategy = testStrategyPeer
			},
			wantErr: config.ErrEmptyGoBGPAddr,
		},
		{
			name: "invalid gobgp strategy",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = "bogus"
			},
			wantErr: config.ErrInvalidGoBGPStrategy,
		},
		{
			name: "invalid gobgp action timeout",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = testStrategyPeer
				cfg.GoBGP.ActionTimeout = 0
			},
			wantErr: config.ErrInvalidGoBGPActionTimeout,
		},
		{
			name: "valid gobgp disable-peer",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = testStrategyPeer
			},
		},
		{
			name: "valid gobgp withdraw-routes",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = "withdraw-routes"
			},
		},
		{
			name: "gobgp tls ca requires tls enabled",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = testStrategyPeer
				cfg.GoBGP.TLS.Enabled = false
				cfg.GoBGP.TLS.CAFile = "/etc/ssl/certs/gobgp-ca.pem"
			},
			wantErr: config.ErrInvalidGoBGPTLS,
		},
		{
			name: "valid gobgp tls",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = testStrategyPeer
				cfg.GoBGP.TLS.Enabled = true
				cfg.GoBGP.TLS.CAFile = "/etc/ssl/certs/gobgp-ca.pem"
				cfg.GoBGP.TLS.ServerName = "gobgp.example.net"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			tt.modify(cfg)
			err := config.Validate(cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestGoBGPPlaintextNonLoopback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.GoBGPConfig
		want bool
	}{
		{
			name: "disabled",
			cfg: config.GoBGPConfig{
				Enabled: false,
				Addr:    "10.0.0.10:50051",
			},
			want: false,
		},
		{
			name: "tls enabled remote",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    "10.0.0.10:50051",
				TLS:     config.GoBGPTLSConfig{Enabled: true},
			},
			want: false,
		},
		{
			name: "ipv4 loopback",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    "127.0.0.1:50051",
			},
			want: false,
		},
		{
			name: "localhost",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    "localhost:50051",
			},
			want: false,
		},
		{
			name: "ipv6 loopback",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    "[::1]:50051",
			},
			want: false,
		},
		{
			name: "remote ipv4",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    "10.0.0.10:50051",
			},
			want: true,
		},
		{
			name: "wildcard ipv4",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    "0.0.0.0:50051",
			},
			want: true,
		},
		{
			name: "wildcard empty host",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    ":50051",
			},
			want: true,
		},
		{
			name: "remote dns name",
			cfg: config.GoBGPConfig{
				Enabled: true,
				Addr:    "gobgp.example.net:50051",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := config.GoBGPPlaintextNonLoopback(tt.cfg)
			if got != tt.want {
				t.Fatalf("GoBGPPlaintextNonLoopback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateDampeningErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "suppress <= reuse",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = testStrategyPeer
				cfg.GoBGP.Dampening.Enabled = true
				cfg.GoBGP.Dampening.SuppressThreshold = 2
				cfg.GoBGP.Dampening.ReuseThreshold = 2
				cfg.GoBGP.Dampening.HalfLife = 15 * time.Second
			},
			wantErr: config.ErrInvalidDampeningThreshold,
		},
		{
			name: "zero half-life",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = testStrategyPeer
				cfg.GoBGP.Dampening.Enabled = true
				cfg.GoBGP.Dampening.SuppressThreshold = 5
				cfg.GoBGP.Dampening.ReuseThreshold = 2
				cfg.GoBGP.Dampening.HalfLife = 0
			},
			wantErr: config.ErrInvalidDampeningHalfLife,
		},
		{
			name: "valid dampening",
			modify: func(cfg *config.Config) {
				cfg.GoBGP.Enabled = true
				cfg.GoBGP.Addr = testGoBGPAddr
				cfg.GoBGP.Strategy = testStrategyPeer
				cfg.GoBGP.Dampening.Enabled = true
				cfg.GoBGP.Dampening.SuppressThreshold = 5
				cfg.GoBGP.Dampening.ReuseThreshold = 2
				cfg.GoBGP.Dampening.HalfLife = 15 * time.Second
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			tt.modify(cfg)
			err := config.Validate(cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateEchoErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "disabled echo skips validation",
			modify: func(cfg *config.Config) {
				cfg.Echo.Enabled = false
				cfg.Echo.Peers = []config.EchoPeerConfig{{Peer: "bad"}}
			},
		},
		{
			name: "empty echo peer",
			modify: func(cfg *config.Config) {
				cfg.Echo.Enabled = true
				cfg.Echo.Peers = []config.EchoPeerConfig{{Peer: ""}}
			},
			wantErr: config.ErrInvalidEchoPeer,
		},
		{
			name: "invalid echo peer",
			modify: func(cfg *config.Config) {
				cfg.Echo.Enabled = true
				cfg.Echo.Peers = []config.EchoPeerConfig{{Peer: "not-an-ip"}}
			},
			wantErr: config.ErrInvalidEchoPeer,
		},
		{
			name: "duplicate echo session key",
			modify: func(cfg *config.Config) {
				cfg.Echo.Enabled = true
				cfg.Echo.Peers = []config.EchoPeerConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr},
					{Peer: "10.0.0.1", Local: testLocalAddr},
				}
			},
			wantErr: config.ErrDuplicateEchoSessionKey,
		},
		{
			name: "valid echo peers",
			modify: func(cfg *config.Config) {
				cfg.Echo.Enabled = true
				cfg.Echo.Peers = []config.EchoPeerConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr},
					{Peer: "10.0.0.3", Local: "10.0.0.4"},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			tt.modify(cfg)
			err := config.Validate(cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateVXLANErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "disabled vxlan skips validation",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = false
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{{Peer: "bad"}}
			},
		},
		{
			name: "invalid vxlan backend",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.Backend = "bad"
				cfg.VXLAN.ManagementVNI = 100
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{{Peer: "10.0.0.1", Local: testLocalAddr}}
			},
			wantErr: config.ErrInvalidOverlayBackend,
		},
		{
			name: "reserved vxlan backend not implemented",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.Backend = config.OverlayBackendCilium
				cfg.VXLAN.ManagementVNI = 100
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{{Peer: "10.0.0.1", Local: testLocalAddr}}
			},
			wantErr: config.ErrUnsupportedOverlayBackend,
		},
		{
			name: "reserved vxlan calico backend not implemented",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.Backend = config.OverlayBackendCalico
				cfg.VXLAN.ManagementVNI = 100
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{{Peer: "10.0.0.1", Local: testLocalAddr}}
			},
			wantErr: config.ErrUnsupportedOverlayBackend,
		},
		{
			name: "vni exceeds 24-bit",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.ManagementVNI = 0x01000000 // > 16777215
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{{Peer: "10.0.0.1"}}
			},
			wantErr: config.ErrInvalidVXLANVNI,
		},
		{
			name: "invalid vxlan peer",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{{Peer: "not-an-ip"}}
			},
			wantErr: config.ErrInvalidVXLANPeer,
		},
		{
			name: "detect_mult exceeds 255",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{
					{Peer: "10.0.0.1", DetectMult: 256},
				}
			},
			wantErr: config.ErrInvalidSessionDetectMult,
		},
		{
			name: "duplicate vxlan session key",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr},
					{Peer: "10.0.0.1", Local: testLocalAddr},
				}
			},
			wantErr: config.ErrDuplicateVXLANSessionKey,
		},
		{
			name: "valid vxlan peers",
			modify: func(cfg *config.Config) {
				cfg.VXLAN.Enabled = true
				cfg.VXLAN.ManagementVNI = 100
				cfg.VXLAN.Peers = []config.VXLANPeerConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr},
					{Peer: "10.0.0.3", Local: "10.0.0.4"},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			tt.modify(cfg)
			err := config.Validate(cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateGeneveErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "disabled geneve skips validation",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = false
				cfg.Geneve.Peers = []config.GenevePeerConfig{{Peer: "bad"}}
			},
		},
		{
			name: "invalid geneve backend",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.Backend = "bad"
				cfg.Geneve.DefaultVNI = 42
				cfg.Geneve.Peers = []config.GenevePeerConfig{{Peer: "10.0.0.1", Local: testLocalAddr}}
			},
			wantErr: config.ErrInvalidOverlayBackend,
		},
		{
			name: "reserved geneve backend not implemented",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.Backend = config.OverlayBackendOVS
				cfg.Geneve.DefaultVNI = 42
				cfg.Geneve.Peers = []config.GenevePeerConfig{{Peer: "10.0.0.1", Local: testLocalAddr}}
			},
			wantErr: config.ErrUnsupportedOverlayBackend,
		},
		{
			name: "reserved geneve calico backend not implemented",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.Backend = config.OverlayBackendCalico
				cfg.Geneve.DefaultVNI = 42
				cfg.Geneve.Peers = []config.GenevePeerConfig{{Peer: "10.0.0.1", Local: testLocalAddr}}
			},
			wantErr: config.ErrUnsupportedOverlayBackend,
		},
		{
			name: "default vni exceeds 24-bit",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.DefaultVNI = 0x01000000
				cfg.Geneve.Peers = []config.GenevePeerConfig{{Peer: "10.0.0.1"}}
			},
			wantErr: config.ErrInvalidGeneveVNI,
		},
		{
			name: "invalid geneve peer",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.Peers = []config.GenevePeerConfig{{Peer: "not-an-ip"}}
			},
			wantErr: config.ErrInvalidGenevePeer,
		},
		{
			name: "peer vni exceeds 24-bit",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.Peers = []config.GenevePeerConfig{
					{Peer: "10.0.0.1", VNI: 0x01000000},
				}
			},
			wantErr: config.ErrInvalidGeneveVNI,
		},
		{
			name: "detect_mult exceeds 255",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.Peers = []config.GenevePeerConfig{
					{Peer: "10.0.0.1", DetectMult: 300},
				}
			},
			wantErr: config.ErrInvalidSessionDetectMult,
		},
		{
			name: "duplicate geneve session key",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.Peers = []config.GenevePeerConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr},
					{Peer: "10.0.0.1", Local: testLocalAddr},
				}
			},
			wantErr: config.ErrDuplicateGeneveSessionKey,
		},
		{
			name: "valid geneve peers",
			modify: func(cfg *config.Config) {
				cfg.Geneve.Enabled = true
				cfg.Geneve.DefaultVNI = 42
				cfg.Geneve.Peers = []config.GenevePeerConfig{
					{Peer: "10.0.0.1", Local: testLocalAddr, VNI: 100},
					{Peer: "10.0.0.3", Local: "10.0.0.4"},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			tt.modify(cfg)
			err := config.Validate(cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSessionConfigLocalAddrInvalid(t *testing.T) {
	t.Parallel()

	sc := config.SessionConfig{Local: "not-an-ip"}
	_, err := sc.LocalAddr()
	if err == nil {
		t.Error("expected error for invalid local address")
	}
}

func TestLoadWithGoBGPConfig(t *testing.T) {
	t.Parallel()

	yamlContent := `
grpc:
  addr: ":50051"
gobgp:
  enabled: true
  addr: "127.0.0.1:50051"
  strategy: "disable-peer"
  tls:
    enabled: true
    ca_file: "/etc/ssl/certs/gobgp-ca.pem"
    server_name: "gobgp.example.net"
  dampening:
    enabled: true
    suppress_threshold: 5
    reuse_threshold: 2
    max_suppress_time: "60s"
    half_life: "15s"
`
	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !cfg.GoBGP.Enabled {
		t.Error("GoBGP.Enabled should be true")
	}
	if cfg.GoBGP.Addr != testGoBGPAddr {
		t.Errorf("GoBGP.Addr = %q, want %q", cfg.GoBGP.Addr, testGoBGPAddr)
	}
	if cfg.GoBGP.Strategy != testStrategyPeer {
		t.Errorf("GoBGP.Strategy = %q, want %q", cfg.GoBGP.Strategy, testStrategyPeer)
	}
	if !cfg.GoBGP.TLS.Enabled {
		t.Error("GoBGP.TLS.Enabled should be true")
	}
	if cfg.GoBGP.TLS.CAFile != "/etc/ssl/certs/gobgp-ca.pem" {
		t.Errorf("GoBGP.TLS.CAFile = %q, want /etc/ssl/certs/gobgp-ca.pem", cfg.GoBGP.TLS.CAFile)
	}
	if cfg.GoBGP.TLS.ServerName != "gobgp.example.net" {
		t.Errorf("GoBGP.TLS.ServerName = %q, want gobgp.example.net", cfg.GoBGP.TLS.ServerName)
	}
	if !cfg.GoBGP.Dampening.Enabled {
		t.Error("GoBGP.Dampening.Enabled should be true")
	}
	if cfg.GoBGP.Dampening.SuppressThreshold != 5 {
		t.Errorf("SuppressThreshold = %v, want 5", cfg.GoBGP.Dampening.SuppressThreshold)
	}

	if err := config.Validate(cfg); err != nil {
		t.Errorf("valid config failed validation: %v", err)
	}
}

func TestLoadWithEchoConfig(t *testing.T) {
	t.Parallel()

	yamlContent := `
grpc:
  addr: ":50051"
echo:
  enabled: true
  peers:
    - peer: "10.0.0.1"
      local: "10.0.0.2"
      detect_mult: 3
    - peer: "10.0.0.3"
      local: "10.0.0.4"
`
	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !cfg.Echo.Enabled {
		t.Error("Echo.Enabled should be true")
	}
	if len(cfg.Echo.Peers) != 2 {
		t.Fatalf("Echo.Peers count = %d, want 2", len(cfg.Echo.Peers))
	}

	if err := config.Validate(cfg); err != nil {
		t.Errorf("valid echo config failed validation: %v", err)
	}
}

func TestLoadWithVXLANConfig(t *testing.T) {
	t.Parallel()

	yamlContent := `
grpc:
  addr: ":50051"
vxlan:
  enabled: true
  backend: "userspace-udp"
  management_vni: 100
  peers:
    - peer: "10.0.0.1"
      local: "10.0.0.2"
`
	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !cfg.VXLAN.Enabled {
		t.Error("VXLAN.Enabled should be true")
	}
	if cfg.VXLAN.ManagementVNI != 100 {
		t.Errorf("ManagementVNI = %d, want 100", cfg.VXLAN.ManagementVNI)
	}
	if cfg.VXLAN.Backend != config.OverlayBackendUserspaceUDP {
		t.Errorf("VXLAN.Backend = %q, want %q", cfg.VXLAN.Backend, config.OverlayBackendUserspaceUDP)
	}

	if err := config.Validate(cfg); err != nil {
		t.Errorf("valid vxlan config failed validation: %v", err)
	}
}

func TestLoadWithGeneveConfig(t *testing.T) {
	t.Parallel()

	yamlContent := `
grpc:
  addr: ":50051"
geneve:
  enabled: true
  backend: "userspace-udp"
  default_vni: 42
  peers:
    - peer: "10.0.0.1"
      local: "10.0.0.2"
      vni: 100
`
	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !cfg.Geneve.Enabled {
		t.Error("Geneve.Enabled should be true")
	}
	if cfg.Geneve.DefaultVNI != 42 {
		t.Errorf("DefaultVNI = %d, want 42", cfg.Geneve.DefaultVNI)
	}
	if cfg.Geneve.Backend != config.OverlayBackendUserspaceUDP {
		t.Errorf("Geneve.Backend = %q, want %q", cfg.Geneve.Backend, config.OverlayBackendUserspaceUDP)
	}

	if err := config.Validate(cfg); err != nil {
		t.Errorf("valid geneve config failed validation: %v", err)
	}
}

func TestLoadWithMicroBFDActuatorConfig(t *testing.T) {
	t.Parallel()

	yamlContent := `
micro_bfd:
  actuator:
    mode: "dry-run"
    backend: "networkmanager"
    ovsdb_endpoint: "unix:/run/openvswitch/db.sock"
    owner_policy: "networkmanager-dbus"
    down_action: "remove-member"
    up_action: "add-member"
`

	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	actuator := cfg.MicroBFD.Actuator
	if actuator.Mode != config.MicroBFDActuatorModeDryRun {
		t.Errorf("Mode = %q, want %q", actuator.Mode, config.MicroBFDActuatorModeDryRun)
	}
	if actuator.Backend != config.MicroBFDActuatorBackendNetworkManager {
		t.Errorf("Backend = %q, want %q", actuator.Backend, config.MicroBFDActuatorBackendNetworkManager)
	}
	if actuator.OVSDBEndpoint != "unix:/run/openvswitch/db.sock" {
		t.Errorf("OVSDBEndpoint = %q, want unix:/run/openvswitch/db.sock", actuator.OVSDBEndpoint)
	}
	if actuator.OwnerPolicy != config.MicroBFDActuatorOwnerNetworkManagerDBus {
		t.Errorf("OwnerPolicy = %q, want %q", actuator.OwnerPolicy, config.MicroBFDActuatorOwnerNetworkManagerDBus)
	}
	if actuator.DownAction != config.MicroBFDActuatorActionRemoveMember {
		t.Errorf("DownAction = %q, want %q", actuator.DownAction, config.MicroBFDActuatorActionRemoveMember)
	}
	if actuator.UpAction != config.MicroBFDActuatorActionAddMember {
		t.Errorf("UpAction = %q, want %q", actuator.UpAction, config.MicroBFDActuatorActionAddMember)
	}
	if err := config.Validate(cfg); err != nil {
		t.Errorf("valid micro-BFD actuator config failed validation: %v", err)
	}
}

func TestValidateMicroBFDActuatorErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modify  func(*config.Config)
		wantErr error
	}{
		{
			name: "invalid mode",
			modify: func(cfg *config.Config) {
				cfg.MicroBFD.Actuator.Mode = "active"
			},
			wantErr: config.ErrInvalidMicroBFDActuatorMode,
		},
		{
			name: "invalid backend",
			modify: func(cfg *config.Config) {
				cfg.MicroBFD.Actuator.Backend = "ifupdown"
			},
			wantErr: config.ErrInvalidMicroBFDActuatorBackend,
		},
		{
			name: "invalid owner policy",
			modify: func(cfg *config.Config) {
				cfg.MicroBFD.Actuator.OwnerPolicy = "overwrite"
			},
			wantErr: config.ErrInvalidMicroBFDActuatorOwnerPolicy,
		},
		{
			name: "invalid down action",
			modify: func(cfg *config.Config) {
				cfg.MicroBFD.Actuator.DownAction = "shutdown-lag"
			},
			wantErr: config.ErrInvalidMicroBFDActuatorAction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.DefaultConfig()
			tt.modify(cfg)

			err := config.Validate(cfg)
			if err == nil {
				t.Fatal("Validate() returned nil, want error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// =========================================================================
// SocketConfig Tests — Sprint 9
// =========================================================================

func TestSocketConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	if cfg.Socket.ReadBufferSize != 4*1024*1024 {
		t.Errorf("Socket.ReadBufferSize = %d, want 4194304 (4 MiB)", cfg.Socket.ReadBufferSize)
	}
	if cfg.Socket.WriteBufferSize != 4*1024*1024 {
		t.Errorf("Socket.WriteBufferSize = %d, want 4194304 (4 MiB)", cfg.Socket.WriteBufferSize)
	}
}

func TestSocketConfigCustomOverride(t *testing.T) {
	t.Parallel()

	yaml := `
socket:
  read_buffer_size: 8388608
  write_buffer_size: 2097152
grpc:
  addr: ":50051"
`
	path := writeTemp(t, yaml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Socket.ReadBufferSize != 8388608 {
		t.Errorf("Socket.ReadBufferSize = %d, want 8388608 (8 MiB)", cfg.Socket.ReadBufferSize)
	}
	if cfg.Socket.WriteBufferSize != 2097152 {
		t.Errorf("Socket.WriteBufferSize = %d, want 2097152 (2 MiB)", cfg.Socket.WriteBufferSize)
	}
}

func TestSocketConfigZeroUsesOSDefault(t *testing.T) {
	t.Parallel()

	yaml := `
socket:
  read_buffer_size: 0
  write_buffer_size: 0
grpc:
  addr: ":50051"
`
	path := writeTemp(t, yaml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Socket.ReadBufferSize != 0 {
		t.Errorf("Socket.ReadBufferSize = %d, want 0 (OS default)", cfg.Socket.ReadBufferSize)
	}
	if cfg.Socket.WriteBufferSize != 0 {
		t.Errorf("Socket.WriteBufferSize = %d, want 0 (OS default)", cfg.Socket.WriteBufferSize)
	}
}

// =========================================================================
// os.Root Config Sandboxing Tests — Go 1.26
// =========================================================================

func TestLoadRejectsPathTraversal(t *testing.T) {
	t.Parallel()

	// Attempt to load a config with path traversal components.
	// os.Root should prevent accessing files outside the config directory.
	_, err := config.Load("../../../etc/passwd")
	if err == nil {
		t.Fatal("Load() with path traversal should return error, got nil")
	}
}

func TestLoadValidConfigFileViaOsRoot(t *testing.T) {
	t.Parallel()

	yamlContent := `
grpc:
  addr: ":50051"
log:
  level: "info"
`
	path := writeTemp(t, yamlContent)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", path, err)
	}

	if cfg.GRPC.Addr != ":50051" {
		t.Errorf("GRPC.Addr = %q, want %q", cfg.GRPC.Addr, ":50051")
	}
}

func TestLoadNonexistentDirectoryViaOsRoot(t *testing.T) {
	t.Parallel()

	_, err := config.Load("/nonexistent/directory/config.yml")
	if err == nil {
		t.Fatal("Load() with nonexistent directory should return error, got nil")
	}
}

// writeTemp creates a temporary YAML file and returns its path.
// The file is automatically cleaned up when the test finishes.
func writeTemp(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "gobfd.yml")

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	return path
}
