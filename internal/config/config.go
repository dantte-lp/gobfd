package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// -------------------------------------------------------------------------
// Configuration Structures
// -------------------------------------------------------------------------

// Config holds the complete gobfd configuration.
type Config struct {
	GRPC        GRPCConfig        `koanf:"grpc"`
	Metrics     MetricsConfig     `koanf:"metrics"`
	Log         LogConfig         `koanf:"log"`
	BFD         BFDConfig         `koanf:"bfd"`
	Unsolicited UnsolicitedConfig `koanf:"unsolicited"`
	Echo        EchoConfig        `koanf:"echo"`
	GoBGP       GoBGPConfig       `koanf:"gobgp"`
	Sessions    []SessionConfig   `koanf:"sessions"`
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

	// AlignIntervals enables RFC 7419 common interval alignment.
	// When true, DesiredMinTxInterval and RequiredMinRxInterval are
	// rounded UP to the nearest RFC 7419 common interval value
	// (3.3ms, 10ms, 20ms, 50ms, 100ms, 1s) for hardware interop.
	AlignIntervals bool `koanf:"align_intervals"`
}

// UnsolicitedConfig holds the RFC 9468 unsolicited BFD configuration.
// When enabled, GoBFD auto-creates passive sessions for incoming BFD
// packets from unknown peers on configured interfaces.
type UnsolicitedConfig struct {
	// Enabled controls whether unsolicited BFD is active globally.
	// RFC 9468 Section 2: MUST be disabled by default.
	Enabled bool `koanf:"enabled"`

	// MaxSessions limits the number of dynamically created sessions.
	// Zero means no limit.
	MaxSessions int `koanf:"max_sessions"`

	// CleanupTimeout is how long to wait after a passive session goes Down
	// before deleting it. Zero means delete immediately.
	CleanupTimeout time.Duration `koanf:"cleanup_timeout"`

	// Interfaces holds per-interface unsolicited BFD settings.
	Interfaces map[string]UnsolicitedInterfaceConfig `koanf:"interfaces"`

	// SessionDefaults holds default timer parameters for auto-created sessions.
	SessionDefaults UnsolicitedSessionDefaultsConfig `koanf:"session_defaults"`
}

// UnsolicitedInterfaceConfig holds per-interface unsolicited BFD settings.
type UnsolicitedInterfaceConfig struct {
	// Enabled controls unsolicited BFD on this interface.
	Enabled bool `koanf:"enabled"`

	// AllowedPrefixes restricts which source addresses can create sessions.
	// RFC 9468 Section 6.1: apply policy from specific subnets/hosts.
	AllowedPrefixes []string `koanf:"allowed_prefixes"`
}

// UnsolicitedSessionDefaultsConfig holds default timer parameters.
type UnsolicitedSessionDefaultsConfig struct {
	DesiredMinTx  time.Duration `koanf:"desired_min_tx"`
	RequiredMinRx time.Duration `koanf:"required_min_rx"`
	DetectMult    uint32        `koanf:"detect_mult"`
}

// EchoConfig holds the RFC 9747 unaffiliated BFD echo configuration.
// When enabled, GoBFD can create echo sessions that detect forwarding-path
// failures without requiring the remote to run BFD.
type EchoConfig struct {
	// Enabled controls whether BFD echo sessions are available.
	// Disabled by default.
	Enabled bool `koanf:"enabled"`

	// DefaultTxInterval is the default echo transmit interval.
	// RFC 9747 Section 3.3: locally provisioned, not negotiated.
	DefaultTxInterval time.Duration `koanf:"default_tx_interval"`

	// DefaultDetectMultiplier is the default echo detection multiplier.
	DefaultDetectMultiplier uint32 `koanf:"default_detect_multiplier"`
}

// GoBGPConfig holds the GoBGP integration configuration.
// When enabled, BFD state changes are propagated to a GoBGP instance
// via its gRPC API (RFC 5882 Section 4.3).
type GoBGPConfig struct {
	// Enabled controls whether the GoBGP integration is active.
	// When false (default), BFD state changes are not propagated to BGP.
	Enabled bool `koanf:"enabled"`

	// Addr is the GoBGP gRPC API address (e.g., "127.0.0.1:50051").
	Addr string `koanf:"addr"`

	// Strategy determines how BFD state changes affect BGP:
	//   - "disable-peer": disable/enable the BGP peer (default)
	//   - "withdraw-routes": withdraw/restore routes (future)
	Strategy string `koanf:"strategy"`

	// Dampening configures RFC 5882 Section 3.2 flap dampening.
	Dampening GoBGPDampeningConfig `koanf:"dampening"`
}

// GoBGPDampeningConfig holds flap dampening parameters (RFC 5882 Section 3.2).
type GoBGPDampeningConfig struct {
	// Enabled controls whether flap dampening is active.
	Enabled bool `koanf:"enabled"`

	// SuppressThreshold is the penalty value above which events are suppressed.
	SuppressThreshold float64 `koanf:"suppress_threshold"`

	// ReuseThreshold is the penalty value below which suppression is lifted.
	ReuseThreshold float64 `koanf:"reuse_threshold"`

	// MaxSuppressTime is the maximum duration events can be suppressed.
	MaxSuppressTime time.Duration `koanf:"max_suppress_time"`

	// HalfLife is the time for the penalty to decay by half.
	HalfLife time.Duration `koanf:"half_life"`
}

// SessionConfig describes a declarative BFD session from the configuration file.
// Each entry creates a BFD session on daemon startup and SIGHUP reload.
type SessionConfig struct {
	// Peer is the remote system's IP address.
	Peer string `koanf:"peer"`

	// Local is the local system's IP address.
	Local string `koanf:"local"`

	// Interface is the network interface for SO_BINDTODEVICE (optional).
	Interface string `koanf:"interface"`

	// Type is the session type: "single_hop" or "multi_hop".
	Type string `koanf:"type"`

	// DesiredMinTx is the desired minimum TX interval (e.g., "100ms").
	DesiredMinTx time.Duration `koanf:"desired_min_tx"`

	// RequiredMinRx is the required minimum RX interval (e.g., "100ms").
	RequiredMinRx time.Duration `koanf:"required_min_rx"`

	// DetectMult is the detection multiplier (must be >= 1).
	DetectMult uint32 `koanf:"detect_mult"`
}

// SessionKey returns a unique identifier for the session based on
// (peer, local, interface). Used for diffing sessions on SIGHUP reload.
func (sc SessionConfig) SessionKey() string {
	return sc.Peer + "|" + sc.Local + "|" + sc.Interface
}

// PeerAddr parses the Peer string as a netip.Addr.
func (sc SessionConfig) PeerAddr() (netip.Addr, error) {
	if sc.Peer == "" {
		return netip.Addr{}, fmt.Errorf("session peer: %w", ErrInvalidSessionPeer)
	}
	addr, err := netip.ParseAddr(sc.Peer)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse session peer %q: %w", sc.Peer, err)
	}
	return addr, nil
}

// LocalAddr parses the Local string as a netip.Addr.
func (sc SessionConfig) LocalAddr() (netip.Addr, error) {
	if sc.Local == "" {
		return netip.Addr{}, nil
	}
	addr, err := netip.ParseAddr(sc.Local)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse session local %q: %w", sc.Local, err)
	}
	return addr, nil
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
			AlignIntervals:          false,
		},
		GoBGP: GoBGPConfig{
			Enabled:  false,
			Addr:     "127.0.0.1:50051",
			Strategy: "disable-peer",
			Dampening: GoBGPDampeningConfig{
				Enabled:           false,
				SuppressThreshold: 3,
				ReuseThreshold:    2,
				MaxSuppressTime:   60 * time.Second,
				HalfLife:          15 * time.Second,
			},
		},
	}
}

// -------------------------------------------------------------------------
// Loader
// -------------------------------------------------------------------------

// envPrefix is the environment variable prefix for GoBFD configuration.
// Variables are named GOBFD_<section>_<key>, e.g., GOBFD_GRPC_ADDR.
const envPrefix = "GOBFD_"

// Load reads configuration from a YAML file at path, overlays environment
// variable overrides (GOBFD_ prefix), and merges on top of DefaultConfig().
// Missing fields inherit defaults.
//
// Environment variable mapping:
//
//	GOBFD_GRPC_ADDR     -> grpc.addr
//	GOBFD_METRICS_ADDR  -> metrics.addr
//	GOBFD_METRICS_PATH  -> metrics.path
//	GOBFD_LOG_LEVEL     -> log.level
//	GOBFD_LOG_FORMAT    -> log.format
//
// Uses koanf/v2 with file + env providers and YAML parser.
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

	// Load environment variable overrides on top of YAML.
	// GOBFD_GRPC_ADDR -> grpc.addr (strip prefix, lowercase, _ -> .).
	if err := k.Load(env.Provider(envPrefix, ".", envKeyMapper), nil); err != nil {
		return nil, fmt.Errorf("load env overrides: %w", err)
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

// envKeyMapper transforms GOBFD_GRPC_ADDR -> grpc.addr.
// Strips the GOBFD_ prefix, lowercases, and replaces _ with .
func envKeyMapper(s string) string {
	s = strings.TrimPrefix(s, envPrefix)
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "_", ".")
}

// loadDefaults marshals the default config into koanf as the base layer.
func loadDefaults(k *koanf.Koanf, defaults *Config) error {
	defaultMap := map[string]any{
		"grpc.addr":                          defaults.GRPC.Addr,
		"metrics.addr":                       defaults.Metrics.Addr,
		"metrics.path":                       defaults.Metrics.Path,
		"log.level":                          defaults.Log.Level,
		"log.format":                         defaults.Log.Format,
		"bfd.default_desired_min_tx":         defaults.BFD.DefaultDesiredMinTx.String(),
		"bfd.default_required_min_rx":        defaults.BFD.DefaultRequiredMinRx.String(),
		"bfd.default_detect_multiplier":      defaults.BFD.DefaultDetectMultiplier,
		"bfd.align_intervals":                defaults.BFD.AlignIntervals,
		"gobgp.enabled":                      defaults.GoBGP.Enabled,
		"gobgp.addr":                         defaults.GoBGP.Addr,
		"gobgp.strategy":                     defaults.GoBGP.Strategy,
		"gobgp.dampening.enabled":            defaults.GoBGP.Dampening.Enabled,
		"gobgp.dampening.suppress_threshold": defaults.GoBGP.Dampening.SuppressThreshold,
		"gobgp.dampening.reuse_threshold":    defaults.GoBGP.Dampening.ReuseThreshold,
		"gobgp.dampening.max_suppress_time":  defaults.GoBGP.Dampening.MaxSuppressTime.String(),
		"gobgp.dampening.half_life":          defaults.GoBGP.Dampening.HalfLife.String(),
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

	// ErrInvalidSessionPeer indicates a session has an invalid peer address.
	ErrInvalidSessionPeer = errors.New("session peer address is invalid")

	// ErrInvalidSessionType indicates a session has an unrecognized type.
	ErrInvalidSessionType = errors.New("session type must be single_hop or multi_hop")

	// ErrInvalidSessionDetectMult indicates a session detect multiplier is zero.
	ErrInvalidSessionDetectMult = errors.New("session detect_mult must be >= 1")

	// ErrDuplicateSessionKey indicates two sessions share the same (peer, local, interface) key.
	ErrDuplicateSessionKey = errors.New("duplicate session key")

	// ErrEmptyGoBGPAddr indicates the GoBGP address is empty when enabled.
	ErrEmptyGoBGPAddr = errors.New("gobgp.addr must not be empty when gobgp is enabled")

	// ErrInvalidGoBGPStrategy indicates an unrecognized GoBGP strategy.
	ErrInvalidGoBGPStrategy = errors.New("gobgp.strategy must be disable-peer or withdraw-routes")

	// ErrInvalidDampeningThreshold indicates suppress threshold is not greater than reuse.
	ErrInvalidDampeningThreshold = errors.New("gobgp.dampening.suppress_threshold must be > reuse_threshold")

	// ErrInvalidDampeningHalfLife indicates the half-life is not positive.
	ErrInvalidDampeningHalfLife = errors.New("gobgp.dampening.half_life must be > 0 when dampening is enabled")
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

	if err := validateGoBGP(cfg.GoBGP); err != nil {
		return err
	}

	if err := validateSessions(cfg.Sessions); err != nil {
		return err
	}

	return nil
}

// ValidGoBGPStrategies lists the recognized GoBGP strategy strings.
var ValidGoBGPStrategies = map[string]bool{
	"disable-peer":    true,
	"withdraw-routes": true,
}

// validateGoBGP checks the GoBGP integration configuration for logical errors.
func validateGoBGP(cfg GoBGPConfig) error {
	if !cfg.Enabled {
		return nil
	}

	if cfg.Addr == "" {
		return ErrEmptyGoBGPAddr
	}

	if !ValidGoBGPStrategies[cfg.Strategy] {
		return fmt.Errorf("gobgp.strategy %q: %w", cfg.Strategy, ErrInvalidGoBGPStrategy)
	}

	if cfg.Dampening.Enabled {
		if err := validateDampening(cfg.Dampening); err != nil {
			return err
		}
	}

	return nil
}

// validateDampening checks the dampening parameters for logical errors.
func validateDampening(cfg GoBGPDampeningConfig) error {
	if cfg.SuppressThreshold <= cfg.ReuseThreshold {
		return ErrInvalidDampeningThreshold
	}

	if cfg.HalfLife <= 0 {
		return ErrInvalidDampeningHalfLife
	}

	return nil
}

// ValidSessionTypes lists the recognized session type strings.
var ValidSessionTypes = map[string]bool{
	"single_hop": true,
	"multi_hop":  true,
}

// validateSessions checks each declarative session entry for correctness.
func validateSessions(sessions []SessionConfig) error {
	seen := make(map[string]struct{}, len(sessions))

	for i, sc := range sessions {
		if _, err := sc.PeerAddr(); err != nil {
			return fmt.Errorf("sessions[%d]: %w: %w", i, ErrInvalidSessionPeer, err)
		}

		if sc.Type != "" && !ValidSessionTypes[sc.Type] {
			return fmt.Errorf("sessions[%d] type %q: %w", i, sc.Type, ErrInvalidSessionType)
		}

		if sc.DetectMult != 0 && sc.DetectMult < 1 {
			return fmt.Errorf("sessions[%d]: %w", i, ErrInvalidSessionDetectMult)
		}

		key := sc.SessionKey()
		if _, dup := seen[key]; dup {
			return fmt.Errorf("sessions[%d] key %q: %w", i, key, ErrDuplicateSessionKey)
		}
		seen[key] = struct{}{}
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
