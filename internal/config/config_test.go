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
	if s1.Local != "10.0.0.2" {
		t.Errorf("Sessions[0].Local = %q, want %q", s1.Local, "10.0.0.2")
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
					{Peer: "", Local: "10.0.0.2"},
				}
			},
			wantErr: config.ErrInvalidSessionPeer,
		},
		{
			name: "invalid session peer",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{Peer: "not-an-ip", Local: "10.0.0.2"},
				}
			},
			wantErr: config.ErrInvalidSessionPeer,
		},
		{
			name: "invalid session type",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{Peer: "10.0.0.1", Local: "10.0.0.2", Type: "bogus"},
				}
			},
			wantErr: config.ErrInvalidSessionType,
		},
		{
			name: "duplicate session keys",
			modify: func(cfg *config.Config) {
				cfg.Sessions = []config.SessionConfig{
					{Peer: "10.0.0.1", Local: "10.0.0.2", Interface: "eth0"},
					{Peer: "10.0.0.1", Local: "10.0.0.2", Interface: "eth0"},
				}
			},
			wantErr: config.ErrDuplicateSessionKey,
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
			{Peer: "10.0.0.1", Local: "10.0.0.2", Type: typ},
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
		Local:     "10.0.0.2",
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

	sc := config.SessionConfig{Local: "10.0.0.2"}
	addr, err := sc.LocalAddr()
	if err != nil {
		t.Fatalf("LocalAddr() error: %v", err)
	}
	if addr.String() != "10.0.0.2" {
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
