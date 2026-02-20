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
