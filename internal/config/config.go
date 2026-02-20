// Package config manages GoBFD daemon configuration using koanf/v2.
//
// Supports YAML files, environment variables, and CLI flags.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// -------------------------------------------------------------------------
// Configuration Structures
// -------------------------------------------------------------------------

// Config holds the complete gobfd configuration.
type Config struct {
	GRPC    GRPCConfig    `koanf:"grpc"`
	Metrics MetricsConfig `koanf:"metrics"`
	Log     LogConfig     `koanf:"log"`
	BFD     BFDConfig     `koanf:"bfd"`
}

// GRPCConfig holds the ConnectRPC server configuration.
type GRPCConfig struct {
	// Addr is the gRPC listen address (e.g., ":50051").
	Addr string `koanf:"addr"`
}

// MetricsConfig holds the Prometheus metrics endpoint configuration.
type MetricsConfig struct {
	// Addr is the HTTP listen address for the metrics endpoint (e.g., ":9100").
	Addr string `koanf:"addr"`
	// Path is the URL path for the metrics endpoint (e.g., "/metrics").
	Path string `koanf:"path"`
}

// LogConfig holds the logging configuration.
type LogConfig struct {
	// Level is the log level: "debug", "info", "warn", "error".
	Level string `koanf:"level"`
	// Format is the log output format: "json" or "text".
	Format string `koanf:"format"`
}

// BFDConfig holds the default BFD session parameters.
// These can be overridden per session via the gRPC API.
type BFDConfig struct {
	// DefaultDesiredMinTx is the default desired minimum TX interval.
	// RFC 5880 Section 6.8.1: used as the initial bfd.DesiredMinTxInterval.
	DefaultDesiredMinTx time.Duration `koanf:"default_desired_min_tx"`

	// DefaultRequiredMinRx is the default required minimum RX interval.
	// RFC 5880 Section 6.8.1: used as the initial bfd.RequiredMinRxInterval.
	DefaultRequiredMinRx time.Duration `koanf:"default_required_min_rx"`

	// DefaultDetectMultiplier is the default detection time multiplier.
	// RFC 5880 Section 6.8.1: MUST be nonzero.
	DefaultDetectMultiplier uint32 `koanf:"default_detect_multiplier"`
}

// -------------------------------------------------------------------------
// Defaults
// -------------------------------------------------------------------------

// DefaultConfig returns a Config populated with sensible defaults.
//
// BFD defaults follow RFC 5880 Section 6.8.3: "When bfd.SessionState is not
// Up, the system MUST set bfd.DesiredMinTxInterval to a value of not less
// than one second (1,000,000 microseconds)." The default of 1s is the
// conservative starting point for production deployments.
func DefaultConfig() *Config {
	return &Config{
		GRPC: GRPCConfig{
			Addr: ":50051",
		},
		Metrics: MetricsConfig{
			Addr: ":9100",
			Path: "/metrics",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
		BFD: BFDConfig{
			DefaultDesiredMinTx:     1 * time.Second,
			DefaultRequiredMinRx:    1 * time.Second,
			DefaultDetectMultiplier: 3,
		},
	}
}

// -------------------------------------------------------------------------
// Loader
// -------------------------------------------------------------------------

// Load reads configuration from a YAML file at path and merges it on top
// of DefaultConfig(). Missing fields in the YAML file inherit defaults.
//
// Uses koanf/v2 with the file provider and YAML parser.
func Load(path string) (*Config, error) {
	k := koanf.New(".")

	// Load defaults first.
	defaults := DefaultConfig()
	if err := loadDefaults(k, defaults); err != nil {
		return nil, fmt.Errorf("load config defaults: %w", err)
	}

	// Load YAML file on top of defaults.
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("load config from %s: %w", path, err)
	}

	cfg := &Config{}
	if err := k.Unmarshal("", cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config from %s: %w", path, err)
	}

	return cfg, nil
}

// loadDefaults marshals the default config into koanf as the base layer.
func loadDefaults(k *koanf.Koanf, defaults *Config) error {
	defaultMap := map[string]any{
		"grpc.addr":                     defaults.GRPC.Addr,
		"metrics.addr":                  defaults.Metrics.Addr,
		"metrics.path":                  defaults.Metrics.Path,
		"log.level":                     defaults.Log.Level,
		"log.format":                    defaults.Log.Format,
		"bfd.default_desired_min_tx":    defaults.BFD.DefaultDesiredMinTx.String(),
		"bfd.default_required_min_rx":   defaults.BFD.DefaultRequiredMinRx.String(),
		"bfd.default_detect_multiplier": defaults.BFD.DefaultDetectMultiplier,
	}

	for key, val := range defaultMap {
		if err := k.Set(key, val); err != nil {
			return fmt.Errorf("set default %s: %w", key, err)
		}
	}

	return nil
}

// -------------------------------------------------------------------------
// Validation
// -------------------------------------------------------------------------

// Validation errors.
var (
	// ErrEmptyGRPCAddr indicates the gRPC listen address is empty.
	ErrEmptyGRPCAddr = errors.New("grpc.addr must not be empty")

	// ErrInvalidDetectMultiplier indicates the detect multiplier is zero.
	ErrInvalidDetectMultiplier = errors.New("bfd.default_detect_multiplier must be >= 1")

	// ErrInvalidDesiredMinTx indicates the desired min TX interval is invalid.
	ErrInvalidDesiredMinTx = errors.New("bfd.default_desired_min_tx must be > 0")

	// ErrInvalidRequiredMinRx indicates the required min RX interval is invalid.
	ErrInvalidRequiredMinRx = errors.New("bfd.default_required_min_rx must be > 0")
)

// Validate checks the configuration for logical errors.
// Returns the first validation error encountered.
func Validate(cfg *Config) error {
	if cfg.GRPC.Addr == "" {
		return ErrEmptyGRPCAddr
	}

	if cfg.BFD.DefaultDetectMultiplier < 1 {
		return ErrInvalidDetectMultiplier
	}

	if cfg.BFD.DefaultDesiredMinTx <= 0 {
		return ErrInvalidDesiredMinTx
	}

	if cfg.BFD.DefaultRequiredMinRx <= 0 {
		return ErrInvalidRequiredMinRx
	}

	return nil
}

// -------------------------------------------------------------------------
// Log Level Parsing
// -------------------------------------------------------------------------

// ParseLogLevel maps a configuration log level string to the corresponding
// slog.Level. Unknown values default to slog.LevelInfo.
//
// Recognized values: "debug", "info", "warn", "error" (case-insensitive).
func ParseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
