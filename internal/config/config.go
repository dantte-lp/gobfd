package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
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
	Socket      SocketConfig      `koanf:"socket"`
	Unsolicited UnsolicitedConfig `koanf:"unsolicited"`
	Echo        EchoConfig        `koanf:"echo"`
	MicroBFD    MicroBFDConfig    `koanf:"micro_bfd"`
	VXLAN       VXLANConfig       `koanf:"vxlan"`
	Geneve      GeneveConfig      `koanf:"geneve"`
	GoBGP       GoBGPConfig       `koanf:"gobgp"`
	Sessions    []SessionConfig   `koanf:"sessions"`
}

// SocketConfig holds UDP socket buffer tuning parameters.
// These control the kernel-level buffer sizes for BFD UDP sockets,
// helping to prevent packet loss under high session counts.
type SocketConfig struct {
	// ReadBufferSize is the SO_RCVBUF size for UDP listeners in bytes.
	// Default: 4 MiB. Set to 0 to use the OS default.
	ReadBufferSize int `koanf:"read_buffer_size"`

	// WriteBufferSize is the SO_SNDBUF size for UDP senders in bytes.
	// Default: 4 MiB. Set to 0 to use the OS default.
	WriteBufferSize int `koanf:"write_buffer_size"`
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

	// DefaultPaddedPduSize is the default padded PDU size for RFC 9764.
	// When nonzero, all sessions pad BFD Control packets to this length
	// and set the DF bit for path MTU verification.
	// Valid range: 24-9000. Zero means no padding (default).
	DefaultPaddedPduSize uint16 `koanf:"default_padded_pdu_size"`
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

	// Peers holds per-peer echo session configurations.
	// Each entry creates an echo session on daemon startup and SIGHUP reload.
	Peers []EchoPeerConfig `koanf:"peers"`
}

// EchoPeerConfig describes a declarative echo session from the configuration file.
// Each entry creates an RFC 9747 echo session targeting a specific peer.
type EchoPeerConfig struct {
	// Peer is the remote system's IP address (echo target).
	Peer string `koanf:"peer"`

	// Local is the local system's IP address.
	Local string `koanf:"local"`

	// Interface is the network interface for SO_BINDTODEVICE (optional).
	Interface string `koanf:"interface"`

	// TxInterval is the echo transmit interval (e.g., "100ms").
	// Overrides EchoConfig.DefaultTxInterval when nonzero.
	TxInterval time.Duration `koanf:"tx_interval"`

	// DetectMult is the detection multiplier (must be >= 1).
	// Overrides EchoConfig.DefaultDetectMultiplier when nonzero.
	DetectMult uint32 `koanf:"detect_mult"`
}

// EchoSessionKey returns a unique identifier for the echo session based on
// (peer, local, interface). Used for diffing echo sessions on SIGHUP reload.
func (ec EchoPeerConfig) EchoSessionKey() string {
	return "echo|" + ec.Peer + "|" + ec.Local + "|" + ec.Interface
}

// PeerAddr parses the Peer string as a netip.Addr.
func (ec EchoPeerConfig) PeerAddr() (netip.Addr, error) {
	if ec.Peer == "" {
		return netip.Addr{}, fmt.Errorf("echo peer: %w", ErrInvalidEchoPeer)
	}
	addr, err := netip.ParseAddr(ec.Peer)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse echo peer %q: %w", ec.Peer, err)
	}
	return addr, nil
}

// LocalAddr parses the Local string as a netip.Addr.
func (ec EchoPeerConfig) LocalAddr() (netip.Addr, error) {
	if ec.Local == "" {
		return netip.Addr{}, nil
	}
	addr, err := netip.ParseAddr(ec.Local)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse echo local %q: %w", ec.Local, err)
	}
	return addr, nil
}

// MicroBFDConfig holds the RFC 7130 Micro-BFD for LAG configuration.
// When configured, GoBFD runs independent BFD sessions on each LAG member
// link and tracks the aggregate LAG state.
type MicroBFDConfig struct {
	// Groups holds per-LAG Micro-BFD group configurations.
	Groups []MicroBFDGroupConfig `koanf:"groups"`

	// Actuator controls optional RFC 7130 LAG member enforcement.
	Actuator MicroBFDActuatorConfig `koanf:"actuator"`
}

// Micro-BFD actuator vocabulary.
const (
	MicroBFDActuatorModeDisabled = "disabled"
	MicroBFDActuatorModeDryRun   = "dry-run"
	MicroBFDActuatorModeEnforce  = "enforce"

	MicroBFDActuatorBackendAuto           = "auto"
	MicroBFDActuatorBackendKernelBond     = "kernel-bond"
	MicroBFDActuatorBackendOVS            = "ovs"
	MicroBFDActuatorBackendNetworkManager = "networkmanager"

	MicroBFDActuatorOwnerRefuseIfManaged    = "refuse-if-managed"
	MicroBFDActuatorOwnerAllowExternal      = "allow-external"
	MicroBFDActuatorOwnerNetworkManagerDBus = "networkmanager-dbus"

	MicroBFDActuatorActionNone         = "none"
	MicroBFDActuatorActionRemoveMember = "remove-member"
	MicroBFDActuatorActionAddMember    = "add-member"
)

// MicroBFDActuatorConfig controls the optional LAG member actuator.
type MicroBFDActuatorConfig struct {
	// Mode controls whether actions are disabled, logged only, or enforced.
	Mode string `koanf:"mode"`

	// Backend selects the owner-specific dataplane backend.
	Backend string `koanf:"backend"`

	// OwnerPolicy controls behavior when another manager owns the interface.
	OwnerPolicy string `koanf:"owner_policy"`

	// DownAction is applied when a member transitions from Up to non-Up.
	DownAction string `koanf:"down_action"`

	// UpAction is applied when a member transitions back to Up.
	UpAction string `koanf:"up_action"`
}

// MicroBFDGroupConfig holds the configuration for a single LAG's Micro-BFD group.
type MicroBFDGroupConfig struct {
	// LAGInterface is the logical LAG interface name (e.g., "bond0").
	LAGInterface string `koanf:"lag_interface"`

	// MemberLinks lists the physical member link names (e.g., ["eth0", "eth1"]).
	// RFC 7130 Section 2: one micro-BFD session per member link.
	MemberLinks []string `koanf:"member_links"`

	// PeerAddr is the remote system's IP address for all member sessions.
	PeerAddr string `koanf:"peer_addr"`

	// LocalAddr is the local system's IP address.
	LocalAddr string `koanf:"local_addr"`

	// DesiredMinTx is the BFD timer interval for member sessions.
	DesiredMinTx time.Duration `koanf:"desired_min_tx"`

	// RequiredMinRx is the minimum acceptable RX interval.
	RequiredMinRx time.Duration `koanf:"required_min_rx"`

	// DetectMult is the detection time multiplier.
	DetectMult uint32 `koanf:"detect_mult"`

	// MinActiveLinks is the minimum number of member links that must be
	// Up for the LAG to be considered operational.
	// Must be >= 1 and <= len(MemberLinks).
	MinActiveLinks int `koanf:"min_active_links"`
}

// VXLANConfig holds the RFC 8971 BFD for VXLAN configuration.
// When configured, GoBFD can run BFD sessions encapsulated in VXLAN
// to verify VTEP-to-VTEP forwarding paths.
type VXLANConfig struct {
	// Enabled controls whether VXLAN BFD is available.
	Enabled bool `koanf:"enabled"`

	// ManagementVNI is the VXLAN Network Identifier used for BFD
	// control messages. RFC 8971 Section 3: all BFD packets use
	// a dedicated Management VNI.
	ManagementVNI uint32 `koanf:"management_vni"`

	// DefaultDesiredMinTx is the default TX interval for VXLAN BFD sessions.
	DefaultDesiredMinTx time.Duration `koanf:"default_desired_min_tx"`

	// DefaultRequiredMinRx is the default RX interval for VXLAN BFD sessions.
	DefaultRequiredMinRx time.Duration `koanf:"default_required_min_rx"`

	// DefaultDetectMultiplier is the default detection multiplier.
	DefaultDetectMultiplier uint32 `koanf:"default_detect_multiplier"`

	// Peers holds per-VTEP BFD session configurations.
	// Each entry creates a VXLAN-encapsulated BFD session targeting a remote VTEP.
	Peers []VXLANPeerConfig `koanf:"peers"`
}

// VXLANPeerConfig describes a declarative VXLAN BFD session.
// Each entry creates an RFC 8971 BFD-over-VXLAN session at daemon startup.
type VXLANPeerConfig struct {
	// Peer is the remote VTEP IP address.
	Peer string `koanf:"peer"`

	// Local is the local VTEP IP address.
	Local string `koanf:"local"`

	// DesiredMinTx overrides VXLANConfig.DefaultDesiredMinTx when nonzero.
	DesiredMinTx time.Duration `koanf:"desired_min_tx"`

	// RequiredMinRx overrides VXLANConfig.DefaultRequiredMinRx when nonzero.
	RequiredMinRx time.Duration `koanf:"required_min_rx"`

	// DetectMult overrides VXLANConfig.DefaultDetectMultiplier when nonzero.
	DetectMult uint32 `koanf:"detect_mult"`
}

// VXLANSessionKey returns a unique identifier for the VXLAN session.
func (vc VXLANPeerConfig) VXLANSessionKey() string {
	return "vxlan|" + vc.Peer + "|" + vc.Local
}

// PeerAddr parses the Peer string as a netip.Addr.
func (vc VXLANPeerConfig) PeerAddr() (netip.Addr, error) {
	if vc.Peer == "" {
		return netip.Addr{}, fmt.Errorf("vxlan peer: %w", ErrInvalidVXLANPeer)
	}
	addr, err := netip.ParseAddr(vc.Peer)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse vxlan peer %q: %w", vc.Peer, err)
	}
	return addr, nil
}

// LocalAddr parses the Local string as a netip.Addr.
func (vc VXLANPeerConfig) LocalAddr() (netip.Addr, error) {
	if vc.Local == "" {
		return netip.Addr{}, nil
	}
	addr, err := netip.ParseAddr(vc.Local)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse vxlan local %q: %w", vc.Local, err)
	}
	return addr, nil
}

// GeneveConfig holds the RFC 9521 BFD for Geneve configuration.
// When configured, GoBFD can run BFD sessions encapsulated in Geneve
// to verify NVE-to-NVE forwarding paths at the VAP level.
type GeneveConfig struct {
	// Enabled controls whether Geneve BFD is available.
	Enabled bool `koanf:"enabled"`

	// DefaultVNI is the default Geneve VNI for BFD sessions.
	DefaultVNI uint32 `koanf:"default_vni"`

	// DefaultDesiredMinTx is the default TX interval for Geneve BFD sessions.
	DefaultDesiredMinTx time.Duration `koanf:"default_desired_min_tx"`

	// DefaultRequiredMinRx is the default RX interval for Geneve BFD sessions.
	DefaultRequiredMinRx time.Duration `koanf:"default_required_min_rx"`

	// DefaultDetectMultiplier is the default detection multiplier.
	DefaultDetectMultiplier uint32 `koanf:"default_detect_multiplier"`

	// Peers holds per-NVE BFD session configurations.
	// Each entry creates a Geneve-encapsulated BFD session targeting a remote NVE.
	Peers []GenevePeerConfig `koanf:"peers"`
}

// GenevePeerConfig describes a declarative Geneve BFD session.
// Each entry creates an RFC 9521 BFD-over-Geneve session at daemon startup.
type GenevePeerConfig struct {
	// Peer is the remote NVE IP address.
	Peer string `koanf:"peer"`

	// Local is the local NVE IP address.
	Local string `koanf:"local"`

	// VNI overrides GeneveConfig.DefaultVNI for this specific peer.
	// Zero means use the default.
	VNI uint32 `koanf:"vni"`

	// DesiredMinTx overrides GeneveConfig.DefaultDesiredMinTx when nonzero.
	DesiredMinTx time.Duration `koanf:"desired_min_tx"`

	// RequiredMinRx overrides GeneveConfig.DefaultRequiredMinRx when nonzero.
	RequiredMinRx time.Duration `koanf:"required_min_rx"`

	// DetectMult overrides GeneveConfig.DefaultDetectMultiplier when nonzero.
	DetectMult uint32 `koanf:"detect_mult"`
}

// GeneveSessionKey returns a unique identifier for the Geneve session.
func (gc GenevePeerConfig) GeneveSessionKey() string {
	return "geneve|" + gc.Peer + "|" + gc.Local
}

// PeerAddr parses the Peer string as a netip.Addr.
func (gc GenevePeerConfig) PeerAddr() (netip.Addr, error) {
	if gc.Peer == "" {
		return netip.Addr{}, fmt.Errorf("geneve peer: %w", ErrInvalidGenevePeer)
	}
	addr, err := netip.ParseAddr(gc.Peer)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse geneve peer %q: %w", gc.Peer, err)
	}
	return addr, nil
}

// LocalAddr parses the Local string as a netip.Addr.
func (gc GenevePeerConfig) LocalAddr() (netip.Addr, error) {
	if gc.Local == "" {
		return netip.Addr{}, nil
	}
	addr, err := netip.ParseAddr(gc.Local)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse geneve local %q: %w", gc.Local, err)
	}
	return addr, nil
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

	// ActionTimeout bounds each GoBGP API action.
	ActionTimeout time.Duration `koanf:"action_timeout"`

	// TLS configures transport security for the GoBGP gRPC API.
	TLS GoBGPTLSConfig `koanf:"tls"`

	// Dampening configures RFC 5882 Section 3.2 flap dampening.
	Dampening GoBGPDampeningConfig `koanf:"dampening"`
}

// GoBGPTLSConfig holds TLS settings for the GoBGP gRPC client.
type GoBGPTLSConfig struct {
	// Enabled switches the GoBGP gRPC client from plaintext to TLS.
	Enabled bool `koanf:"enabled"`

	// CAFile optionally points to a PEM root CA bundle.
	// Empty means use the host root CA pool.
	CAFile string `koanf:"ca_file"`

	// ServerName overrides the certificate verification name.
	ServerName string `koanf:"server_name"`
}

// GoBGPPlaintextNonLoopback reports whether the GoBGP integration is enabled,
// TLS is disabled, and the configured endpoint is not a loopback host.
func GoBGPPlaintextNonLoopback(cfg GoBGPConfig) bool {
	if !cfg.Enabled || cfg.TLS.Enabled {
		return false
	}

	host := goBGPAddrHost(cfg.Addr)
	if host == "" {
		return true
	}

	if strings.EqualFold(host, "localhost") {
		return false
	}

	addr, err := netip.ParseAddr(host)
	if err != nil {
		return true
	}
	return !addr.IsLoopback()
}

func goBGPAddrHost(addr string) string {
	addr = strings.TrimSpace(addr)
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(addr, "[]")
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

	// PaddedPduSize is the per-session padded PDU size (RFC 9764).
	// Overrides BFDConfig.DefaultPaddedPduSize when nonzero.
	// Valid range: 24-9000. Zero means use the global default.
	PaddedPduSize uint16 `koanf:"padded_pdu_size"`

	// Auth configures RFC 5880 Section 6.7 authentication for this session.
	// Empty Type means authentication is disabled.
	Auth AuthConfig `koanf:"auth"`
}

// AuthConfig describes a single BFD authentication key for a session.
// Multiple-key rotation is handled by the internal AuthKeyStore and can be
// expanded in a later config schema without changing the wire behavior.
type AuthConfig struct {
	// Type is one of: simple_password, keyed_md5, meticulous_keyed_md5,
	// keyed_sha1, meticulous_keyed_sha1. Empty or "none" disables auth.
	Type string `koanf:"type"`

	// KeyID is the RFC 5880 Auth Key ID. Valid range: 0-255.
	KeyID uint32 `koanf:"key_id"`

	// Secret is the authentication secret. Length limits are RFC-defined:
	// 1-16 bytes for Simple Password and MD5, 1-20 bytes for SHA1.
	Secret string `koanf:"secret"`
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
		Socket: SocketConfig{
			ReadBufferSize:  4 * 1024 * 1024, // 4 MiB
			WriteBufferSize: 4 * 1024 * 1024, // 4 MiB
		},
		MicroBFD: MicroBFDConfig{
			Actuator: MicroBFDActuatorConfig{
				Mode:        MicroBFDActuatorModeDisabled,
				Backend:     MicroBFDActuatorBackendAuto,
				OwnerPolicy: MicroBFDActuatorOwnerRefuseIfManaged,
				DownAction:  MicroBFDActuatorActionRemoveMember,
				UpAction:    MicroBFDActuatorActionAddMember,
			},
		},
		GoBGP: GoBGPConfig{
			Enabled:       false,
			Addr:          "127.0.0.1:50051",
			Strategy:      "disable-peer",
			ActionTimeout: 5 * time.Second,
			TLS: GoBGPTLSConfig{
				Enabled: false,
			},
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
	// Go 1.26 os.Root: sandboxed file access to prevent path traversal.
	// Validate that the config file is within the expected directory
	// before allowing koanf to read it.
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open config root %s: %w", dir, err)
	}
	defer root.Close()

	// Validate the file is accessible within the root.
	f, err := root.Open(base)
	if err != nil {
		return nil, fmt.Errorf("open config file %s in root %s: %w", base, dir, err)
	}
	defer f.Close()

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
		"micro_bfd.actuator.mode":            defaults.MicroBFD.Actuator.Mode,
		"micro_bfd.actuator.backend":         defaults.MicroBFD.Actuator.Backend,
		"micro_bfd.actuator.owner_policy":    defaults.MicroBFD.Actuator.OwnerPolicy,
		"micro_bfd.actuator.down_action":     defaults.MicroBFD.Actuator.DownAction,
		"micro_bfd.actuator.up_action":       defaults.MicroBFD.Actuator.UpAction,
		"gobgp.enabled":                      defaults.GoBGP.Enabled,
		"gobgp.addr":                         defaults.GoBGP.Addr,
		"gobgp.strategy":                     defaults.GoBGP.Strategy,
		"gobgp.action_timeout":               defaults.GoBGP.ActionTimeout.String(),
		"gobgp.tls.enabled":                  defaults.GoBGP.TLS.Enabled,
		"gobgp.tls.ca_file":                  defaults.GoBGP.TLS.CAFile,
		"gobgp.tls.server_name":              defaults.GoBGP.TLS.ServerName,
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

	// ErrInvalidSessionAuthType indicates a session has an unrecognized auth type.
	ErrInvalidSessionAuthType = errors.New("session auth.type is invalid")

	// ErrInvalidSessionAuthKeyID indicates a session auth key ID exceeds the wire range.
	ErrInvalidSessionAuthKeyID = errors.New("session auth.key_id must fit uint8")

	// ErrInvalidSessionAuthSecret indicates a session auth secret has an invalid length.
	ErrInvalidSessionAuthSecret = errors.New("session auth.secret length is invalid")

	// ErrEmptyGoBGPAddr indicates the GoBGP address is empty when enabled.
	ErrEmptyGoBGPAddr = errors.New("gobgp.addr must not be empty when gobgp is enabled")

	// ErrInvalidGoBGPStrategy indicates an unrecognized GoBGP strategy.
	ErrInvalidGoBGPStrategy = errors.New("gobgp.strategy must be disable-peer or withdraw-routes")

	// ErrInvalidGoBGPActionTimeout indicates a non-positive GoBGP action timeout.
	ErrInvalidGoBGPActionTimeout = errors.New("gobgp.action_timeout must be > 0 when gobgp is enabled")

	// ErrInvalidGoBGPTLS indicates inconsistent GoBGP TLS settings.
	ErrInvalidGoBGPTLS = errors.New("gobgp.tls ca_file/server_name require gobgp.tls.enabled")

	// ErrInvalidDampeningThreshold indicates suppress threshold is not greater than reuse.
	ErrInvalidDampeningThreshold = errors.New("gobgp.dampening.suppress_threshold must be > reuse_threshold")

	// ErrInvalidDampeningHalfLife indicates the half-life is not positive.
	ErrInvalidDampeningHalfLife = errors.New("gobgp.dampening.half_life must be > 0 when dampening is enabled")

	// ErrInvalidEchoPeer indicates an echo peer has an invalid peer address.
	ErrInvalidEchoPeer = errors.New("echo peer address is invalid")

	// ErrInvalidEchoDetectMult indicates an echo detect multiplier is zero.
	ErrInvalidEchoDetectMult = errors.New("echo detect_mult must be >= 1")

	// ErrDuplicateEchoSessionKey indicates two echo sessions share the same key.
	ErrDuplicateEchoSessionKey = errors.New("duplicate echo session key")

	// ErrInvalidVXLANPeer indicates a VXLAN peer has an invalid peer address.
	ErrInvalidVXLANPeer = errors.New("vxlan peer address is invalid")

	// ErrInvalidVXLANVNI indicates the VXLAN VNI exceeds the 24-bit range.
	ErrInvalidVXLANVNI = errors.New("vxlan VNI exceeds 24-bit range (0-16777215)")

	// ErrDuplicateVXLANSessionKey indicates two VXLAN sessions share the same key.
	ErrDuplicateVXLANSessionKey = errors.New("duplicate vxlan session key")

	// ErrInvalidGenevePeer indicates a Geneve peer has an invalid peer address.
	ErrInvalidGenevePeer = errors.New("geneve peer address is invalid")

	// ErrInvalidGeneveVNI indicates the Geneve VNI exceeds the 24-bit range.
	ErrInvalidGeneveVNI = errors.New("geneve VNI exceeds 24-bit range (0-16777215)")

	// ErrDuplicateGeneveSessionKey indicates two Geneve sessions share the same key.
	ErrDuplicateGeneveSessionKey = errors.New("duplicate geneve session key")

	// ErrInvalidMicroBFDActuatorMode indicates an unrecognized Micro-BFD actuator mode.
	ErrInvalidMicroBFDActuatorMode = errors.New("micro_bfd.actuator.mode must be disabled, dry-run, or enforce")

	// ErrInvalidMicroBFDActuatorBackend indicates an unrecognized Micro-BFD actuator backend.
	ErrInvalidMicroBFDActuatorBackend = errors.New("micro_bfd.actuator.backend must be auto, kernel-bond, ovs, or networkmanager")

	// ErrInvalidMicroBFDActuatorOwnerPolicy indicates an unrecognized interface owner policy.
	ErrInvalidMicroBFDActuatorOwnerPolicy = errors.New("micro_bfd.actuator.owner_policy must be refuse-if-managed, allow-external, or networkmanager-dbus")

	// ErrInvalidMicroBFDActuatorAction indicates an unrecognized Micro-BFD actuator action.
	ErrInvalidMicroBFDActuatorAction = errors.New("micro_bfd.actuator action must be none, remove-member, or add-member")
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

	if err := validateEchoPeers(cfg.Echo); err != nil {
		return err
	}

	if err := validateMicroBFDActuator(cfg.MicroBFD.Actuator); err != nil {
		return err
	}

	if err := validateVXLAN(cfg.VXLAN); err != nil {
		return err
	}

	if err := validateGeneve(cfg.Geneve); err != nil {
		return err
	}

	return nil
}

var validMicroBFDActuatorModes = map[string]bool{
	MicroBFDActuatorModeDisabled: true,
	MicroBFDActuatorModeDryRun:   true,
	MicroBFDActuatorModeEnforce:  true,
}

var validMicroBFDActuatorBackends = map[string]bool{
	MicroBFDActuatorBackendAuto:           true,
	MicroBFDActuatorBackendKernelBond:     true,
	MicroBFDActuatorBackendOVS:            true,
	MicroBFDActuatorBackendNetworkManager: true,
}

var validMicroBFDActuatorOwnerPolicies = map[string]bool{
	MicroBFDActuatorOwnerRefuseIfManaged:    true,
	MicroBFDActuatorOwnerAllowExternal:      true,
	MicroBFDActuatorOwnerNetworkManagerDBus: true,
}

var validMicroBFDActuatorActions = map[string]bool{
	MicroBFDActuatorActionNone:         true,
	MicroBFDActuatorActionRemoveMember: true,
	MicroBFDActuatorActionAddMember:    true,
}

func validateMicroBFDActuator(cfg MicroBFDActuatorConfig) error {
	if !validMicroBFDActuatorModes[cfg.Mode] {
		return fmt.Errorf("micro_bfd.actuator.mode %q: %w",
			cfg.Mode, ErrInvalidMicroBFDActuatorMode)
	}
	if !validMicroBFDActuatorBackends[cfg.Backend] {
		return fmt.Errorf("micro_bfd.actuator.backend %q: %w",
			cfg.Backend, ErrInvalidMicroBFDActuatorBackend)
	}
	if !validMicroBFDActuatorOwnerPolicies[cfg.OwnerPolicy] {
		return fmt.Errorf("micro_bfd.actuator.owner_policy %q: %w",
			cfg.OwnerPolicy, ErrInvalidMicroBFDActuatorOwnerPolicy)
	}
	if !validMicroBFDActuatorActions[cfg.DownAction] {
		return fmt.Errorf("micro_bfd.actuator.down_action %q: %w",
			cfg.DownAction, ErrInvalidMicroBFDActuatorAction)
	}
	if !validMicroBFDActuatorActions[cfg.UpAction] {
		return fmt.Errorf("micro_bfd.actuator.up_action %q: %w",
			cfg.UpAction, ErrInvalidMicroBFDActuatorAction)
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

	if cfg.ActionTimeout <= 0 {
		return ErrInvalidGoBGPActionTimeout
	}

	if !cfg.TLS.Enabled && (cfg.TLS.CAFile != "" || cfg.TLS.ServerName != "") {
		return ErrInvalidGoBGPTLS
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

// ValidAuthTypes lists recognized RFC 5880 authentication type strings.
var ValidAuthTypes = map[string]bool{
	"none":                  true,
	"simple_password":       true,
	"keyed_md5":             true,
	"meticulous_keyed_md5":  true,
	"keyed_sha1":            true,
	"meticulous_keyed_sha1": true,
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

		if err := validateSessionAuth(sc.Auth); err != nil {
			return fmt.Errorf("sessions[%d]: %w", i, err)
		}

		key := sc.SessionKey()
		if _, dup := seen[key]; dup {
			return fmt.Errorf("sessions[%d] key %q: %w", i, key, ErrDuplicateSessionKey)
		}
		seen[key] = struct{}{}
	}

	return nil
}

func validateSessionAuth(auth AuthConfig) error {
	authType := strings.TrimSpace(auth.Type)
	if authType == "" || authType == "none" {
		return nil
	}
	if !ValidAuthTypes[authType] {
		return fmt.Errorf("auth.type %q: %w", auth.Type, ErrInvalidSessionAuthType)
	}
	if auth.KeyID > 255 {
		return fmt.Errorf("auth.key_id %d: %w", auth.KeyID, ErrInvalidSessionAuthKeyID)
	}

	secretLen := len([]byte(auth.Secret))
	switch authType {
	case "simple_password", "keyed_md5", "meticulous_keyed_md5":
		if secretLen < 1 || secretLen > 16 {
			return fmt.Errorf("auth.secret length %d: %w", secretLen, ErrInvalidSessionAuthSecret)
		}
	case "keyed_sha1", "meticulous_keyed_sha1":
		if secretLen < 1 || secretLen > 20 {
			return fmt.Errorf("auth.secret length %d: %w", secretLen, ErrInvalidSessionAuthSecret)
		}
	}
	return nil
}

// validateEchoPeers checks each declarative echo peer entry for correctness.
func validateEchoPeers(cfg EchoConfig) error {
	if !cfg.Enabled || len(cfg.Peers) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(cfg.Peers))

	for i, ep := range cfg.Peers {
		if _, err := ep.PeerAddr(); err != nil {
			return fmt.Errorf("echo.peers[%d]: %w: %w", i, ErrInvalidEchoPeer, err)
		}

		if ep.DetectMult != 0 && ep.DetectMult < 1 {
			return fmt.Errorf("echo.peers[%d]: %w", i, ErrInvalidEchoDetectMult)
		}

		key := ep.EchoSessionKey()
		if _, dup := seen[key]; dup {
			return fmt.Errorf("echo.peers[%d] key %q: %w", i, key, ErrDuplicateEchoSessionKey)
		}
		seen[key] = struct{}{}
	}

	return nil
}

// vniMax is the maximum valid VNI value (24-bit).
const vniMax uint32 = 0x00FFFFFF

// validateVXLAN checks the VXLAN BFD configuration for logical errors.
func validateVXLAN(cfg VXLANConfig) error {
	if !cfg.Enabled || len(cfg.Peers) == 0 {
		return nil
	}

	if cfg.ManagementVNI > vniMax {
		return fmt.Errorf("vxlan.management_vni %d: %w", cfg.ManagementVNI, ErrInvalidVXLANVNI)
	}

	seen := make(map[string]struct{}, len(cfg.Peers))

	for i, peer := range cfg.Peers {
		if _, err := peer.PeerAddr(); err != nil {
			return fmt.Errorf("vxlan.peers[%d]: %w: %w", i, ErrInvalidVXLANPeer, err)
		}

		if peer.DetectMult > 255 {
			return fmt.Errorf("vxlan.peers[%d] detect_mult %d: %w",
				i, peer.DetectMult, ErrInvalidSessionDetectMult)
		}

		key := peer.VXLANSessionKey()
		if _, dup := seen[key]; dup {
			return fmt.Errorf("vxlan.peers[%d] key %q: %w", i, key, ErrDuplicateVXLANSessionKey)
		}
		seen[key] = struct{}{}
	}

	return nil
}

// validateGeneve checks the Geneve BFD configuration for logical errors.
func validateGeneve(cfg GeneveConfig) error {
	if !cfg.Enabled || len(cfg.Peers) == 0 {
		return nil
	}

	if cfg.DefaultVNI > vniMax {
		return fmt.Errorf("geneve.default_vni %d: %w", cfg.DefaultVNI, ErrInvalidGeneveVNI)
	}

	seen := make(map[string]struct{}, len(cfg.Peers))

	for i, peer := range cfg.Peers {
		if _, err := peer.PeerAddr(); err != nil {
			return fmt.Errorf("geneve.peers[%d]: %w: %w", i, ErrInvalidGenevePeer, err)
		}

		if peer.VNI > vniMax {
			return fmt.Errorf("geneve.peers[%d] vni %d: %w", i, peer.VNI, ErrInvalidGeneveVNI)
		}

		if peer.DetectMult > 255 {
			return fmt.Errorf("geneve.peers[%d] detect_mult %d: %w",
				i, peer.DetectMult, ErrInvalidSessionDetectMult)
		}

		key := peer.GeneveSessionKey()
		if _, dup := seen[key]; dup {
			return fmt.Errorf("geneve.peers[%d] key %q: %w", i, key, ErrDuplicateGeneveSessionKey)
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
