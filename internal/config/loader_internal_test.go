package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
)

func TestLoadOpenedConfigUsesVerifiedDescriptor(t *testing.T) {
	isolateSupportedEnv(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "gobfd.yml")
	if err := os.WriteFile(path, []byte("bfd:\n  default_detect_multiplier: 7\n"), 0o600); err != nil {
		t.Fatalf("write original config: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open original config: %v", err)
	}
	defer file.Close()

	if renameErr := os.Rename(path, filepath.Join(dir, "opened.yml")); renameErr != nil {
		t.Fatalf("rename original config: %v", renameErr)
	}
	if writeErr := os.WriteFile(path, []byte("bfd:\n  default_detect_multiplier: 8\n"), 0o600); writeErr != nil {
		t.Fatalf("write replacement config: %v", writeErr)
	}

	cfg, err := loadOpenedConfig(path, file)
	if err != nil {
		t.Fatalf("load opened config: %v", err)
	}
	if cfg.BFD.DefaultDetectMultiplier != 7 {
		t.Fatalf("BFD.DefaultDetectMultiplier = %d, want descriptor value 7",
			cfg.BFD.DefaultDetectMultiplier)
	}
}

func TestLoadRejectsUnsupportedGoBGPStrategy(t *testing.T) {
	isolateSupportedEnv(t)

	path := filepath.Join(t.TempDir(), "gobfd.yml")
	contents := []byte(`
gobgp:
  enabled: true
  addr: "127.0.0.1:50051"
  strategy: "withdraw-routes"
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if !errors.Is(err, ErrUnsupportedGoBGPStrategy) {
		t.Fatalf("Load(%q) error = %v, want errors.Is ErrUnsupportedGoBGPStrategy", path, err)
	}
}

func isolateSupportedEnv(t *testing.T) {
	t.Helper()

	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || envKeyMapper(key) == "" {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset supported environment variable %s: %v", key, err)
		}
		t.Cleanup(func() {
			//nolint:usetesting // The test must restore a variable that it temporarily unsets.
			if err := os.Setenv(key, value); err != nil {
				t.Errorf("restore supported environment variable %s: %v", key, err)
			}
		})
	}
}

func TestLoadEmptyYAMLLayerPreservesSocketDefaults(t *testing.T) {
	t.Parallel()

	k := koanf.New(".")
	defaults := DefaultConfig()
	if err := loadDefaults(k, defaults); err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if err := k.Load(rawbytes.Provider(nil), yaml.Parser()); err != nil {
		t.Fatalf("load empty YAML: %v", err)
	}

	if got := k.Int("socket.read_buffer_size"); got != defaults.Socket.ReadBufferSize {
		t.Errorf("socket.read_buffer_size = %d, want %d", got, defaults.Socket.ReadBufferSize)
	}
	if got := k.Int("socket.write_buffer_size"); got != defaults.Socket.WriteBufferSize {
		t.Errorf("socket.write_buffer_size = %d, want %d", got, defaults.Socket.WriteBufferSize)
	}
}

func TestEnvKeyMapperUsesExplicitSupportedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "grpc address", key: "GOBFD_GRPC_ADDR", want: "grpc.addr"},
		{name: "metrics address", key: "GOBFD_METRICS_ADDR", want: "metrics.addr"},
		{name: "metrics path", key: "GOBFD_METRICS_PATH", want: "metrics.path"},
		{name: "log level", key: "GOBFD_LOG_LEVEL", want: "log.level"},
		{name: "log format", key: "GOBFD_LOG_FORMAT", want: "log.format"},
		{name: "unsolicited enabled", key: "GOBFD_UNSOLICITED_ENABLED", want: "unsolicited.enabled"},
		{name: "echo enabled", key: "GOBFD_ECHO_ENABLED", want: "echo.enabled"},
		{name: "vxlan enabled", key: "GOBFD_VXLAN_ENABLED", want: "vxlan.enabled"},
		{name: "vxlan backend", key: "GOBFD_VXLAN_BACKEND", want: "vxlan.backend"},
		{name: "geneve enabled", key: "GOBFD_GENEVE_ENABLED", want: "geneve.enabled"},
		{name: "geneve backend", key: "GOBFD_GENEVE_BACKEND", want: "geneve.backend"},
		{name: "gobgp enabled", key: "GOBFD_GOBGP_ENABLED", want: "gobgp.enabled"},
		{name: "gobgp address", key: "GOBFD_GOBGP_ADDR", want: "gobgp.addr"},
		{name: "gobgp strategy", key: "GOBFD_GOBGP_STRATEGY", want: "gobgp.strategy"},
		{name: "gobgp tls enabled", key: "GOBFD_GOBGP_TLS_ENABLED", want: "gobgp.tls.enabled"},
		{
			name: "gobgp dampening enabled", key: "GOBFD_GOBGP_DAMPENING_ENABLED",
			want: "gobgp.dampening.enabled",
		},
		{
			name: "socket read buffer", key: "GOBFD_SOCKET_READ_BUFFER_SIZE",
			want: "socket.read_buffer_size",
		},
		{
			name: "socket write buffer", key: "GOBFD_SOCKET_WRITE_BUFFER_SIZE",
			want: "socket.write_buffer_size",
		},
		{name: "unknown", key: "GOBFD_UNKNOWN_FIELD", want: ""},
		{name: "collection", key: "GOBFD_SESSIONS", want: ""},
		{name: "ambiguous compound name", key: "GOBFD_SOCKET_READ_BUFFER", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := envKeyMapper(tt.key); got != tt.want {
				t.Errorf("envKeyMapper(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
