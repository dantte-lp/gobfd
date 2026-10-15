package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"

	gobgpstrategy "github.com/dantte-lp/gobfd/internal/gobgp/strategy"
	"github.com/dantte-lp/gobfd/internal/overlay"
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
	// Addr is the optional TCP listen address; empty (the default) opens no
	// TCP listener. Plaintext TCP must be loopback; a non-loopback address
	// needs TLS (gobfd-330.3).
	Addr string `koanf:"addr"`

	// Socket configures the optional Unix socket listener. Requests over it
	// carry the kernel-reported peer UID as principal (gobfd-330).
	Socket GRPCSocketConfig `koanf:"socket"`

	// TLS turns the TCP listener into mTLS: clients must present a
	// certificate from ClientCAFile (gobfd-330.2).
	TLS GRPCTLSConfig `koanf:"tls"`

	// UnsafeLoopbackMutations lets anonymous plaintext loopback TCP clients
	// call mutating RPCs. Without it they can only read. The gobfd.management
	// health check is NOT_SERVING while it is set.
	UnsafeLoopbackMutations bool `koanf:"unsafe_loopback_mutations"`

	// MutatingIdentities, when not empty, lists the mTLS client identities
	// (URI SAN such as a SPIFFE ID, or "dns:<name>") that may call mutating
	// RPCs; other mTLS clients are read-only. It requires TLS (gobfd-330.4).
	MutatingIdentities []string `koanf:"mutating_identities"`

	// FlightRecorder serves the runtime/trace flight recorder dump at
	// /debug/flightrecorder on the management server, to callers with a
	// principal only. Off by default (gobfd-qj0.8.2.3.2).
	FlightRecorder bool `koanf:"flight_recorder"`
}

// GRPCTLSConfig holds the mTLS files of the TCP management listener. The
// files are re-read on every handshake, so rotation needs no restart.
type GRPCTLSConfig struct {
	CertFile     string `koanf:"cert_file"`
	KeyFile      string `koanf:"key_file"`
	ClientCAFile string `koanf:"client_ca_file"`
}

// Enabled reports whether any mTLS file is configured.
func (c GRPCTLSConfig) Enabled() bool {
	return c.CertFile != "" || c.KeyFile != "" || c.ClientCAFile != ""
}

// GRPCSocketConfig holds the management Unix socket settings.
type GRPCSocketConfig struct {
	// Path is the absolute socket path; empty disables the listener.
	Path string `koanf:"path"`

	// Mode is the octal permission mode of the socket file (default "0660").
	// Access control is this mode plus Group.
	Mode string `koanf:"mode"`

	// Group names the group that owns the socket file; empty keeps the
	// daemon's group.
	Group string `koanf:"group"`
}

// MetricsConfig holds the Prometheus metrics endpoint configuration.
type MetricsConfig struct {
	// Addr is the HTTP listen address for the metrics endpoint (default
	// "127.0.0.1:9100").
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

	// MaxSessions limits the number of dynamically created sessions. It
	// must be >= 1 when unsolicited BFD is enabled (RFC 9468 Section 6.1).
	MaxSessions int `koanf:"max_sessions"`

	// CleanupTimeout is how long to wait after a passive session goes Down
	// before deleting it. Zero means delete immediately.
	CleanupTimeout time.Duration `koanf:"cleanup_timeout"`

	// Interfaces holds per-interface unsolicited BFD settings.
	Interfaces map[string]UnsolicitedInterfaceConfig `koanf:"interfaces"`

	// SessionDefaults holds default timer parameters for auto-created sessions.
	SessionDefaults UnsolicitedSessionDefaultsConfig `koanf:"session_defaults"`

	// Auth authenticates auto-created sessions and the packets that create
	// them (RFC 9468 Section 6.2). Only meticulous_keyed_sha1 is accepted.
	Auth AuthConfig `koanf:"auth"`
}

// UnsolicitedInterfaceConfig holds per-interface unsolicited BFD settings.
type UnsolicitedInterfaceConfig struct {
	// Enabled controls unsolicited BFD on this interface.
	Enabled bool `koanf:"enabled"`

	// AllowedPrefixes further restricts which source addresses can create
	// sessions (RFC 9468 Section 6.1). The source must also belong to the
	// subnet of the local address that received the packet (Section 2).
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

const (
	maxBFDWireUint8 = math.MaxUint8

	// OverlayBackendUserspaceUDP selects the implemented userspace UDP backend.
	OverlayBackendUserspaceUDP = overlay.BackendUserspaceUDP
	// OverlayBackendKernel selects the reserved Linux kernel backend.
	OverlayBackendKernel = overlay.BackendKernel
	// OverlayBackendOVS selects the reserved Open vSwitch backend.
	OverlayBackendOVS = overlay.BackendOVS
	// OverlayBackendOVN selects the reserved OVN backend.
	OverlayBackendOVN = overlay.BackendOVN
	// OverlayBackendCilium selects the reserved Cilium backend.
	OverlayBackendCilium = overlay.BackendCilium
	// OverlayBackendCalico selects the reserved Calico backend.
	OverlayBackendCalico = overlay.BackendCalico
	// OverlayBackendNSX selects the reserved NSX backend.
	OverlayBackendNSX = overlay.BackendNSX
)

// MicroBFDActuatorConfig controls the optional LAG member actuator.
type MicroBFDActuatorConfig struct {
	// Mode controls whether actions are disabled, logged only, or enforced.
	Mode string `koanf:"mode"`

	// Backend selects the owner-specific dataplane backend.
	Backend string `koanf:"backend"`

	// OVSDBEndpoint is the native OVSDB endpoint for backend: ovs.
	OVSDBEndpoint string `koanf:"ovsdb_endpoint"`

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

	// Backend selects the owner-specific overlay transport backend.
	// Only userspace-udp is implemented; other values are reserved.
	Backend string `koanf:"backend"`

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

	// PeerMAC is the remote VTEP source MAC expected in Format A.
	PeerMAC string `koanf:"peer_mac"`

	// LocalMAC is the local VTEP source MAC used in Format A.
	LocalMAC string `koanf:"local_mac"`

	// DesiredMinTx overrides VXLANConfig.DefaultDesiredMinTx when nonzero.
	DesiredMinTx time.Duration `koanf:"desired_min_tx"`

	// RequiredMinRx overrides VXLANConfig.DefaultRequiredMinRx when nonzero.
	RequiredMinRx time.Duration `koanf:"required_min_rx"`

	// DetectMult overrides VXLANConfig.DefaultDetectMultiplier when nonzero.
	DetectMult uint32 `koanf:"detect_mult"`
}

// VXLANSessionKey returns a unique identifier for the VXLAN session.
func (vc VXLANPeerConfig) VXLANSessionKey() string {
	key := "vxlan|" + canonicalOverlayAddr(vc.Peer) + "|" + canonicalOverlayAddr(vc.Local)
	if vc.PeerMAC == "" && vc.LocalMAC == "" {
		return key
	}
	return key + "|" + canonicalOverlayMAC(vc.PeerMAC) + "|" + canonicalOverlayMAC(vc.LocalMAC)
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

// PeerMACAddr parses the remote Format A VTEP source MAC.
func (vc VXLANPeerConfig) PeerMACAddr() ([6]byte, error) {
	return parseRequiredOverlayMAC("vxlan peer MAC", vc.PeerMAC, ErrVXLANEndpointIdentityUnavailable)
}

// LocalMACAddr parses the local Format A VTEP source MAC.
func (vc VXLANPeerConfig) LocalMACAddr() ([6]byte, error) {
	return parseRequiredOverlayMAC("vxlan local MAC", vc.LocalMAC, ErrVXLANEndpointIdentityUnavailable)
}

// GeneveConfig holds the RFC 9521 BFD for Geneve configuration.
// When configured, GoBFD can run BFD sessions encapsulated in Geneve
// to verify NVE-to-NVE forwarding paths at the VAP level.
type GeneveConfig struct {
	// Enabled controls whether Geneve BFD is available.
	Enabled bool `koanf:"enabled"`

	// Backend selects the owner-specific overlay transport backend.
	// Only userspace-udp is implemented; other values are reserved.
	Backend string `koanf:"backend"`

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

	// InnerPeer is the remote VAP IPv4 address carried in Format A.
	InnerPeer string `koanf:"inner_peer"`

	// InnerLocal is the local VAP IPv4 address carried in Format A.
	InnerLocal string `koanf:"inner_local"`

	// PeerMAC is the remote VAP MAC address carried in Format A.
	PeerMAC string `koanf:"peer_mac"`

	// LocalMAC is the local VAP MAC address carried in Format A.
	LocalMAC string `koanf:"local_mac"`

	// DesiredMinTx overrides GeneveConfig.DefaultDesiredMinTx when nonzero.
	DesiredMinTx time.Duration `koanf:"desired_min_tx"`

	// RequiredMinRx overrides GeneveConfig.DefaultRequiredMinRx when nonzero.
	RequiredMinRx time.Duration `koanf:"required_min_rx"`

	// DetectMult overrides GeneveConfig.DefaultDetectMultiplier when nonzero.
	DetectMult uint32 `koanf:"detect_mult"`
}

// GeneveSessionKey returns a unique identifier for the Geneve session.
func (gc GenevePeerConfig) GeneveSessionKey(defaultVNI ...uint32) string {
	key := "geneve|" + canonicalOverlayAddr(gc.Peer) + "|" + canonicalOverlayAddr(gc.Local)
	vni := gc.VNI
	if vni == 0 && len(defaultVNI) > 0 {
		vni = defaultVNI[0]
	}
	if vni == 0 && gc.InnerPeer == "" && gc.InnerLocal == "" && gc.PeerMAC == "" && gc.LocalMAC == "" {
		return key
	}
	return fmt.Sprintf("%s|%d|%s|%s|%s|%s",
		key, vni, canonicalOverlayAddr(gc.InnerPeer), canonicalOverlayAddr(gc.InnerLocal),
		canonicalOverlayMAC(gc.PeerMAC), canonicalOverlayMAC(gc.LocalMAC))
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

// InnerPeerAddr parses the remote Format A VAP address.
func (gc GenevePeerConfig) InnerPeerAddr() (netip.Addr, error) {
	return parseRequiredOverlayIPv4("geneve inner peer", gc.InnerPeer, ErrGeneveVAPIdentityUnavailable)
}

// InnerLocalAddr parses the local Format A VAP address.
func (gc GenevePeerConfig) InnerLocalAddr() (netip.Addr, error) {
	return parseRequiredOverlayIPv4("geneve inner local", gc.InnerLocal, ErrGeneveVAPIdentityUnavailable)
}

// PeerMACAddr parses the remote Format A VAP MAC into a comparable value.
func (gc GenevePeerConfig) PeerMACAddr() ([6]byte, error) {
	return parseRequiredOverlayMAC("geneve peer MAC", gc.PeerMAC, ErrGeneveVAPIdentityUnavailable)
}

// LocalMACAddr parses the local Format A VAP MAC into a comparable value.
func (gc GenevePeerConfig) LocalMACAddr() ([6]byte, error) {
	return parseRequiredOverlayMAC("geneve local MAC", gc.LocalMAC, ErrGeneveVAPIdentityUnavailable)
}

func parseRequiredOverlayIPv4(field, value string, identityErr error) (netip.Addr, error) {
	addr, err := netip.ParseAddr(value)
	if err != nil || !addr.Is4() || !addr.IsGlobalUnicast() {
		return netip.Addr{}, fmt.Errorf("%s %q: %w", field, value, identityErr)
	}
	return addr.Unmap(), nil
}

func parseRequiredOverlayMAC(field, value string, identityErr error) ([6]byte, error) {
	var result [6]byte
	hardwareAddr, err := net.ParseMAC(value)
	if err != nil || len(hardwareAddr) != len(result) || hardwareAddr[0]&1 != 0 {
		return result, fmt.Errorf("%s %q: %w", field, value, identityErr)
	}
	copy(result[:], hardwareAddr)
	if result == [6]byte{} {
		return result, fmt.Errorf("%s %q: %w", field, value, identityErr)
	}
	return result, nil
}

func canonicalOverlayAddr(value string) string {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return value
	}
	return addr.Unmap().String()
}

func canonicalOverlayMAC(value string) string {
	addr, err := net.ParseMAC(value)
	if err != nil {
		return value
	}
	return addr.String()
}

// GoBGPConfig holds the GoBGP integration configuration.
// When enabled, BFD state changes are propagated to a GoBGP instance
// via its gRPC API (RFC 5882 Section 10.2).
type GoBGPConfig struct {
	// Enabled controls whether the GoBGP integration is active.
	// When false (default), BFD state changes are not propagated to BGP.
	Enabled bool `koanf:"enabled"`

	// Addr is the GoBGP gRPC API address (e.g., "127.0.0.1:50051").
	Addr string `koanf:"addr"`

	// Strategy determines how BFD state changes affect BGP:
	//   - "disable-peer": disable/enable the BGP peer (default and only
	//     implemented strategy)
	//   - "withdraw-routes": reserved and rejected as unsupported
	Strategy string `koanf:"strategy"`

	// ActionTimeout bounds each GoBGP API action.
	ActionTimeout time.Duration `koanf:"action_timeout"`

	// TLS configures transport security for the GoBGP gRPC API.
	TLS GoBGPTLSConfig `koanf:"tls"`

	// Dampening configures GoBFD's penalty-based flap dampening policy.
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

// GoBGPDampeningConfig holds GoBFD flap dampening policy parameters.
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

	// RequiredMinRxSet distinguishes an explicit zero from an inherited interval.
	// Load derives it from YAML presence; it is not a configuration key.
	RequiredMinRxSet bool `koanf:"-"`

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
		// Only the management socket: callers carry a peer-UID principal.
		GRPC: GRPCConfig{Socket: GRPCSocketConfig{Path: "/run/gobfd/gobfd.sock", Mode: "0660"}},
		Metrics: MetricsConfig{
			// Metrics are unauthenticated; bind beyond loopback explicitly.
			Addr: "127.0.0.1:9100",
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
				Mode:          MicroBFDActuatorModeDisabled,
				Backend:       MicroBFDActuatorBackendAuto,
				OVSDBEndpoint: "",
				OwnerPolicy:   MicroBFDActuatorOwnerRefuseIfManaged,
				DownAction:    MicroBFDActuatorActionRemoveMember,
				UpAction:      MicroBFDActuatorActionAddMember,
			},
		},
		VXLAN: VXLANConfig{
			Backend: OverlayBackendUserspaceUDP,
		},
		Geneve: GeneveConfig{
			Backend: OverlayBackendUserspaceUDP,
		},
		Unsolicited: UnsolicitedConfig{
			SessionDefaults: UnsolicitedSessionDefaultsConfig{
				DesiredMinTx:  1 * time.Second,
				RequiredMinRx: 1 * time.Second,
				DetectMult:    3,
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
// The explicit supported environment mapping is defined by envKeyMapper;
// unknown or ambiguous GOBFD_ names are ignored.
//
// Uses koanf/v2 with raw bytes + env providers and YAML parser.
func Load(path string) (*Config, error) {
	// os.Root provides sandboxed file access to prevent path traversal.
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
	return loadOpenedConfig(path, f)
}

func loadOpenedConfig(path string, file io.Reader) (*Config, error) {
	contents, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read config from %s: %w", path, err)
	}

	k := koanf.New(".")

	// Load defaults first.
	defaults := DefaultConfig()
	if err := loadDefaults(k, defaults); err != nil {
		return nil, fmt.Errorf("load config defaults: %w", err)
	}

	// Load YAML file on top of defaults.
	if err := k.Load(rawbytes.Provider(contents), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("load config from %s: %w", path, err)
	}

	// Load environment variable overrides on top of YAML.
	if err := k.Load(env.Provider(envPrefix, ".", envKeyMapper), nil); err != nil {
		return nil, fmt.Errorf("load env overrides: %w", err)
	}
	if err := validateRequiredMinRxInput(k); err != nil {
		return nil, fmt.Errorf("validate required min RX input from %s: %w", path, err)
	}
	if !validUint8Scalar(k.Get("unsolicited.session_defaults.detect_mult")) {
		return nil, fmt.Errorf("unsolicited.session_defaults.detect_mult from %s: %w",
			path, ErrInvalidUnsolicitedSessionDefaults)
	}
	if err := validateDetectMultiplierInput(k); err != nil {
		return nil, fmt.Errorf("validate detect multipliers from %s: %w", path, err)
	}

	cfg := &Config{}
	if err := k.Unmarshal("", cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config from %s: %w", path, err)
	}
	applyRequiredMinRxPresence(k, cfg, defaults.BFD.DefaultRequiredMinRx)

	return cfg, nil
}

// validateRequiredMinRxInput checks raw values before weak decoding can truncate
// or overflow numbers, or accept a singleton map in place of a session list.
func validateRequiredMinRxInput(k *koanf.Koanf) error {
	if !validRequiredMinRxScalar(k.Get("bfd.default_required_min_rx")) {
		return fmt.Errorf("default_required_min_rx: %w", ErrInvalidRequiredMinRx)
	}
	value := k.Get("sessions")
	if value == nil {
		return nil
	}
	sessions, ok := value.([]any)
	if !ok {
		return ErrInvalidSessionShape
	}
	for i, value := range sessions {
		session, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("session %d: %w", i, ErrInvalidSessionShape)
		}
		if !validRequiredMinRxScalar(session["required_min_rx"]) {
			return fmt.Errorf("session %d required_min_rx: %w", i, ErrInvalidRequiredMinRx)
		}
	}
	return nil
}

func validRequiredMinRxScalar(value any) bool {
	switch value := value.(type) {
	case nil, string:
		return true // Duration strings are checked by the existing decode hook.
	case int:
		return value >= 0
	case int64:
		return value >= 0
	case uint64:
		return value <= math.MaxInt64
	case float64:
		// The strict upper bound excludes 2^63, which float64(MaxInt64) rounds to.
		return value >= 0 && value < math.MaxInt64 && math.Trunc(value) == value
	default:
		return false
	}
}

// validUint8Scalar reports whether a raw numeric value fits uint8 before weak
// decoding into a wider unsigned field can wrap it into range.
func validUint8Scalar(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case string:
		_, err := strconv.ParseUint(value, 0, 8)
		return err == nil
	case int:
		return value >= 0 && value <= math.MaxUint8
	case int64:
		return value >= 0 && value <= math.MaxUint8
	case uint32: // the loadDefaults value
		return value <= math.MaxUint8
	case uint64:
		return value <= math.MaxUint8
	case float64:
		return value >= 0 && value <= math.MaxUint8 && math.Trunc(value) == value
	default:
		return false
	}
}

func validateDetectMultiplierInput(k *koanf.Koanf) error {
	root := k.Raw()
	for _, path := range []string{
		"bfd.default_detect_multiplier", "echo.default_detect_multiplier",
		"vxlan.default_detect_multiplier", "geneve.default_detect_multiplier",
		"unsolicited.session_defaults.detect_mult",
	} {
		value, err := rawCaseFoldedValue(root, path)
		if err != nil {
			return err
		}
		if !validUint8Scalar(value) {
			return fmt.Errorf("%s: %w", path, ErrDetectMultiplierRange)
		}
	}
	for _, path := range []string{"sessions", "echo.peers", "vxlan.peers", "geneve.peers", "micro_bfd.groups"} {
		value, err := rawCaseFoldedValue(root, path)
		if err != nil {
			return err
		}
		if value == nil {
			continue
		}
		entries, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: %w", path, ErrInvalidPeerListShape)
		}
		for i, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s[%d]: %w", path, i, ErrInvalidPeerListShape)
			}
			multiplier, err := rawCaseFoldedValue(entry, "detect_mult")
			if err != nil {
				return fmt.Errorf("%s[%d]: %w", path, i, err)
			}
			if !validUint8Scalar(multiplier) {
				return fmt.Errorf("%s[%d].detect_mult: %w", path, i, ErrDetectMultiplierRange)
			}
		}
	}
	return nil
}

// Match the decoder's EqualFold lookup without choosing between case aliases.
func rawCaseFoldedValue(root map[string]any, path string) (any, error) {
	var value any = root
	for _, part := range strings.Split(path, ".") {
		fields, ok := value.(map[string]any)
		if !ok {
			return nil, nil
		}
		var next any
		found := false
		for key, field := range fields {
			if strings.EqualFold(key, part) {
				if found {
					return nil, fmt.Errorf("%s: %w", path, ErrAmbiguousConfigKey)
				}
				next, found = field, true
			}
		}
		value = next
	}
	return value, nil
}

// applyRequiredMinRxPresence runs after raw shape validation, so koanf.Slices
// preserves each session's index in cfg.Sessions.
func applyRequiredMinRxPresence(k *koanf.Koanf, cfg *Config, defaultRx time.Duration) {
	if k.Get("bfd.default_required_min_rx") == nil {
		cfg.BFD.DefaultRequiredMinRx = defaultRx
	}
	for i, session := range k.Slices("sessions") {
		// Always overwrite the marker, including any value supplied under '-'.
		cfg.Sessions[i].RequiredMinRxSet = session.Get("required_min_rx") != nil
	}
}

// envKeyMapper returns the exact supported configuration key for a daemon
// environment variable. Returning an empty key makes the env provider ignore
// unknown or ambiguous GOBFD_ names.
func envKeyMapper(s string) string {
	supported := map[string]string{
		"GOBFD_GRPC_ADDR":                "grpc.addr",
		"GOBFD_METRICS_ADDR":             "metrics.addr",
		"GOBFD_METRICS_PATH":             "metrics.path",
		"GOBFD_LOG_LEVEL":                "log.level",
		"GOBFD_LOG_FORMAT":               "log.format",
		"GOBFD_UNSOLICITED_ENABLED":      "unsolicited.enabled",
		"GOBFD_ECHO_ENABLED":             "echo.enabled",
		"GOBFD_VXLAN_ENABLED":            "vxlan.enabled",
		"GOBFD_VXLAN_BACKEND":            "vxlan.backend",
		"GOBFD_GENEVE_ENABLED":           "geneve.enabled",
		"GOBFD_GENEVE_BACKEND":           "geneve.backend",
		"GOBFD_GOBGP_ENABLED":            "gobgp.enabled",
		"GOBFD_GOBGP_ADDR":               "gobgp.addr",
		"GOBFD_GOBGP_STRATEGY":           "gobgp.strategy",
		"GOBFD_GOBGP_TLS_ENABLED":        "gobgp.tls.enabled",
		"GOBFD_GOBGP_DAMPENING_ENABLED":  "gobgp.dampening.enabled",
		"GOBFD_SOCKET_READ_BUFFER_SIZE":  "socket.read_buffer_size",
		"GOBFD_SOCKET_WRITE_BUFFER_SIZE": "socket.write_buffer_size",
	}

	return supported[s]
}

// loadDefaults marshals the default config into koanf as the base layer.
func loadDefaults(k *koanf.Koanf, defaults *Config) error {
	defaultMap := map[string]any{
		"grpc.addr":                          defaults.GRPC.Addr,
		"grpc.socket.path":                   defaults.GRPC.Socket.Path,
		"grpc.socket.mode":                   defaults.GRPC.Socket.Mode,
		"metrics.addr":                       defaults.Metrics.Addr,
		"metrics.path":                       defaults.Metrics.Path,
		"log.level":                          defaults.Log.Level,
		"log.format":                         defaults.Log.Format,
		"socket.read_buffer_size":            defaults.Socket.ReadBufferSize,
		"socket.write_buffer_size":           defaults.Socket.WriteBufferSize,
		"bfd.default_desired_min_tx":         defaults.BFD.DefaultDesiredMinTx.String(),
		"bfd.default_required_min_rx":        defaults.BFD.DefaultRequiredMinRx.String(),
		"bfd.default_detect_multiplier":      defaults.BFD.DefaultDetectMultiplier,
		"bfd.align_intervals":                defaults.BFD.AlignIntervals,
		"micro_bfd.actuator.mode":            defaults.MicroBFD.Actuator.Mode,
		"micro_bfd.actuator.backend":         defaults.MicroBFD.Actuator.Backend,
		"micro_bfd.actuator.ovsdb_endpoint":  defaults.MicroBFD.Actuator.OVSDBEndpoint,
		"micro_bfd.actuator.owner_policy":    defaults.MicroBFD.Actuator.OwnerPolicy,
		"micro_bfd.actuator.down_action":     defaults.MicroBFD.Actuator.DownAction,
		"micro_bfd.actuator.up_action":       defaults.MicroBFD.Actuator.UpAction,
		"vxlan.backend":                      defaults.VXLAN.Backend,
		"geneve.backend":                     defaults.Geneve.Backend,
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

		"unsolicited.session_defaults.desired_min_tx":  defaults.Unsolicited.SessionDefaults.DesiredMinTx.String(),
		"unsolicited.session_defaults.required_min_rx": defaults.Unsolicited.SessionDefaults.RequiredMinRx.String(),
		"unsolicited.session_defaults.detect_mult":     defaults.Unsolicited.SessionDefaults.DetectMult,
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
	ErrEmptyGRPCAddr = errors.New("grpc.addr or grpc.socket.path must be set")

	// ErrInvalidGRPCSocket indicates an invalid management socket setting.
	ErrInvalidGRPCSocket = errors.New("grpc.socket is invalid")

	// ErrPlaintextNonLoopbackGRPC indicates a plaintext management TCP
	// listener on a non-loopback or wildcard address.
	ErrPlaintextNonLoopbackGRPC = errors.New("grpc.addr is not loopback; set grpc.tls for remote management")

	// ErrInvalidGRPCTLS indicates incomplete mTLS settings or mTLS without a
	// TCP listener.
	ErrInvalidGRPCTLS = errors.New("grpc.tls needs cert_file, key_file, client_ca_file and grpc.addr")

	// ErrInvalidDetectMultiplier indicates the detect multiplier is zero.
	ErrInvalidDetectMultiplier = errors.New("bfd.default_detect_multiplier must be >= 1")

	// ErrDetectMultiplierRange indicates raw configuration cannot fit the wire field.
	ErrDetectMultiplierRange = errors.New("detect multiplier must be an integer between 0 and 255")

	// ErrInvalidPeerListShape rejects weak conversion of a peer/group mapping to a list.
	ErrInvalidPeerListShape = errors.New("peers or groups must be a sequence of mappings")

	// ErrAmbiguousConfigKey rejects conflicting case aliases for one decoded field.
	ErrAmbiguousConfigKey = errors.New("configuration contains conflicting case aliases")

	// ErrInvalidDesiredMinTx indicates the desired min TX interval is invalid.
	ErrInvalidDesiredMinTx = errors.New("bfd.default_desired_min_tx must be > 0")

	// ErrInvalidRequiredMinRx indicates the required min RX interval is invalid.
	ErrInvalidRequiredMinRx = errors.New("bfd.default_required_min_rx must be >= 0")

	// ErrInvalidSessionPeer indicates a session has an invalid peer address.
	ErrInvalidSessionPeer = errors.New("session peer address is invalid")

	// ErrInvalidSessionShape indicates sessions is not a sequence of mappings.
	ErrInvalidSessionShape = errors.New("sessions must be a sequence of mappings")

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
	ErrInvalidGoBGPStrategy = errors.New("gobgp.strategy is not recognized")

	// ErrUnsupportedGoBGPStrategy indicates a recognized GoBGP strategy has no implementation.
	ErrUnsupportedGoBGPStrategy = errors.New("gobgp.strategy is recognized but unsupported")

	// ErrInvalidGoBGPActionTimeout indicates a non-positive GoBGP action timeout.
	ErrInvalidGoBGPActionTimeout = errors.New("gobgp.action_timeout must be > 0 when gobgp is enabled")

	// ErrInvalidGoBGPTLS indicates inconsistent GoBGP TLS settings.
	ErrInvalidGoBGPTLS = errors.New("gobgp.tls ca_file/server_name require gobgp.tls.enabled")

	// ErrAmbiguousGoBGPPeer indicates several sessions to one BGP neighbor
	// while the GoBGP integration is enabled (H-31).
	ErrAmbiguousGoBGPPeer = errors.New("gobgp peer has more than one BFD session")

	// ErrInvalidDampeningThreshold indicates suppress threshold is not greater than reuse.
	ErrInvalidDampeningThreshold = errors.New("gobgp.dampening.suppress_threshold must be > reuse_threshold > 0")

	// ErrInvalidDampeningMaxSuppressTime indicates a non-positive
	// max_suppress_time, which would leave recovery unbounded.
	ErrInvalidDampeningMaxSuppressTime = errors.New(
		"gobgp.dampening.max_suppress_time must be > 0 when dampening is enabled")

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

	// ErrVXLANEndpointIdentityUnavailable indicates a missing or unsupported
	// Format A VTEP source MAC identity.
	ErrVXLANEndpointIdentityUnavailable = errors.New("vxlan Format A VTEP MAC identity is invalid")

	// ErrInvalidGenevePeer indicates a Geneve peer has an invalid peer address.
	ErrInvalidGenevePeer = errors.New("geneve peer address is invalid")

	// ErrInvalidGeneveVNI indicates the Geneve VNI exceeds the 24-bit range.
	ErrInvalidGeneveVNI = errors.New("geneve VNI exceeds 24-bit range (0-16777215)")

	// ErrDuplicateGeneveSessionKey indicates two Geneve sessions share the same key.
	ErrDuplicateGeneveSessionKey = errors.New("duplicate geneve session key")

	// ErrGeneveVAPIdentityUnavailable indicates missing or unsupported Format A
	// VAP identity. Geneve remains fail closed unless the exact tuple is valid.
	ErrGeneveVAPIdentityUnavailable = errors.New("geneve Format A VAP MAC/IP identity is invalid")

	// ErrInvalidOverlayBackend indicates an unrecognized overlay dataplane backend.
	ErrInvalidOverlayBackend = errors.New(
		"overlay backend must be userspace-udp, kernel, ovs, ovn, cilium, calico, or nsx",
	)

	// ErrUnsupportedOverlayBackend indicates a recognized but not yet implemented backend.
	ErrUnsupportedOverlayBackend = errors.New(
		"overlay backend must be userspace-udp until kernel/ovs/ovn/cilium/calico/nsx backends are implemented",
	)

	// ErrInvalidMicroBFDActuatorMode indicates an unrecognized Micro-BFD actuator mode.
	ErrInvalidMicroBFDActuatorMode = errors.New("micro_bfd.actuator.mode must be disabled, dry-run, or enforce")

	// ErrInvalidMicroBFDActuatorBackend indicates an unrecognized Micro-BFD actuator backend.
	ErrInvalidMicroBFDActuatorBackend = errors.New(
		"micro_bfd.actuator.backend must be auto, kernel-bond, ovs, or networkmanager",
	)

	// ErrInvalidMicroBFDActuatorOwnerPolicy indicates an unrecognized interface owner policy.
	ErrInvalidMicroBFDActuatorOwnerPolicy = errors.New(
		"micro_bfd.actuator.owner_policy must be refuse-if-managed, allow-external, or networkmanager-dbus",
	)

	// ErrInvalidMicroBFDActuatorAction indicates an unrecognized Micro-BFD actuator action.
	ErrInvalidMicroBFDActuatorAction = errors.New("micro_bfd.actuator action must be none, remove-member, or add-member")

	// ErrInvalidUnsolicitedAuthType indicates unsolicited authentication
	// weaker than Meticulous Keyed SHA1 (RFC 9468 Section 6.2).
	ErrInvalidUnsolicitedAuthType = errors.New("unsolicited.auth.type must be meticulous_keyed_sha1")

	// ErrInvalidUnsolicitedMaxSessions indicates enabled unsolicited BFD
	// without a positive session limit (RFC 9468 Section 6.1).
	ErrInvalidUnsolicitedMaxSessions = errors.New("unsolicited.max_sessions must be >= 1 when unsolicited is enabled")

	// ErrInvalidUnsolicitedSessionDefaults indicates unsolicited session
	// defaults that cannot form a working session: intervals must be
	// 1µs..2^32-1µs, required_min_rx nonzero, detect_mult 1..255.
	ErrInvalidUnsolicitedSessionDefaults = errors.New("unsolicited.session_defaults is invalid")

	// ErrInvalidUnsolicitedCleanupTimeout indicates a negative cleanup timeout.
	ErrInvalidUnsolicitedCleanupTimeout = errors.New("unsolicited.cleanup_timeout must be >= 0")

	// ErrInvalidUnsolicitedInterfaces indicates enabled unsolicited BFD with
	// no enabled interface or with an unparsable allowed prefix.
	ErrInvalidUnsolicitedInterfaces = errors.New("unsolicited.interfaces is invalid")
)

// Validate checks the configuration for logical errors.
// Returns the first validation error encountered.
func Validate(cfg *Config) error {
	if err := validateGRPC(cfg.GRPC); err != nil {
		return err
	}

	if cfg.BFD.DefaultDetectMultiplier < 1 {
		return ErrInvalidDetectMultiplier
	}

	if cfg.BFD.DefaultDesiredMinTx <= 0 {
		return ErrInvalidDesiredMinTx
	}

	if cfg.BFD.DefaultRequiredMinRx < 0 {
		return ErrInvalidRequiredMinRx
	}

	if err := validateGoBGP(cfg.GoBGP); err != nil {
		return err
	}

	if err := validateSessions(cfg.Sessions); err != nil {
		return err
	}

	if err := validateGoBGPPeers(cfg.GoBGP, cfg.Sessions); err != nil {
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

	if cfg.Unsolicited.Enabled {
		if err := validateUnsolicited(cfg.Unsolicited); err != nil {
			return err
		}
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

func validateOverlayBackend(field string, backend string) error {
	normalized, recognized := overlay.ParseBackend(backend)
	if !recognized {
		return fmt.Errorf("%s %q: %w", field, backend, ErrInvalidOverlayBackend)
	}
	if !normalized.Implemented() {
		return fmt.Errorf("%s %q: %w", field, normalized, ErrUnsupportedOverlayBackend)
	}
	return nil
}

// validateGoBGP checks the GoBGP integration configuration for logical errors.
func validateGoBGP(cfg GoBGPConfig) error {
	if !cfg.Enabled {
		return nil
	}

	if cfg.Addr == "" {
		return ErrEmptyGoBGPAddr
	}

	strategy, recognized := gobgpstrategy.Parse(cfg.Strategy)
	if !recognized {
		return fmt.Errorf("gobgp.strategy %q: %w", cfg.Strategy, ErrInvalidGoBGPStrategy)
	}
	if !strategy.Implemented() {
		return fmt.Errorf("gobgp.strategy %q: %w", cfg.Strategy, ErrUnsupportedGoBGPStrategy)
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
	if cfg.SuppressThreshold <= cfg.ReuseThreshold || cfg.ReuseThreshold <= 0 {
		return ErrInvalidDampeningThreshold
	}

	if cfg.MaxSuppressTime <= 0 {
		return ErrInvalidDampeningMaxSuppressTime
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

	return validateControlListenerBindings(sessions)
}

// validateGoBGPPeers rejects two sessions to one peer address when GoBGP is
// enabled: the handler acts on the BGP neighbor, so each neighbor needs one
// canonical BFD session (H-31). Sessions are single-hop or multi-hop only.
func validateGoBGPPeers(gobgp GoBGPConfig, sessions []SessionConfig) error {
	if !gobgp.Enabled {
		return nil
	}
	seen := make(map[netip.Addr]struct{}, len(sessions))
	for i, sc := range sessions {
		peer, err := sc.PeerAddr()
		if err != nil {
			return fmt.Errorf("sessions[%d]: %w: %w", i, ErrInvalidSessionPeer, err)
		}
		peer = peer.Unmap()
		if _, dup := seen[peer]; dup {
			return fmt.Errorf("sessions[%d] peer %s: %w", i, peer, ErrAmbiguousGoBGPPeer)
		}
		seen[peer] = struct{}{}
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
	if auth.KeyID > maxBFDWireUint8 {
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

// maxWireInterval is the largest interval the 32-bit microsecond fields of a
// BFD Control packet carry (RFC 5880 Section 4.1).
const maxWireInterval = time.Duration(math.MaxUint32) * time.Microsecond

// validateUnsolicited checks an enabled RFC 9468 configuration: a positive
// session limit (Section 6.1), session defaults that form a working session,
// at least one enabled interface with parsable prefixes and, when
// authentication is configured, the strongest mechanism GoBFD supports
// (Section 6.2).
func validateUnsolicited(cfg UnsolicitedConfig) error {
	if cfg.MaxSessions < 1 {
		return ErrInvalidUnsolicitedMaxSessions
	}
	if err := validateUnsolicitedSessionDefaults(cfg.SessionDefaults); err != nil {
		return err
	}
	if cfg.CleanupTimeout < 0 {
		return ErrInvalidUnsolicitedCleanupTimeout
	}
	if err := validateUnsolicitedInterfaces(cfg.Interfaces); err != nil {
		return err
	}
	authType := strings.TrimSpace(cfg.Auth.Type)
	if authType == "" || authType == "none" {
		return nil
	}
	if authType != "meticulous_keyed_sha1" {
		return fmt.Errorf("unsolicited.auth.type %q: %w", cfg.Auth.Type, ErrInvalidUnsolicitedAuthType)
	}
	if err := validateSessionAuth(cfg.Auth); err != nil {
		return fmt.Errorf("unsolicited.%w", err)
	}
	return nil
}

// validateUnsolicitedSessionDefaults requires intervals representable on the
// wire and a nonzero required_min_rx: a passive session that asks the peer
// not to send never detects a failure and holds its session slot forever.
func validateUnsolicitedSessionDefaults(d UnsolicitedSessionDefaultsConfig) error {
	for name, interval := range map[string]time.Duration{
		"desired_min_tx":  d.DesiredMinTx,
		"required_min_rx": d.RequiredMinRx,
	} {
		if interval < time.Microsecond || interval > maxWireInterval {
			return fmt.Errorf("%s %s outside 1µs..%s: %w",
				name, interval, maxWireInterval, ErrInvalidUnsolicitedSessionDefaults)
		}
	}
	if d.DetectMult < 1 || d.DetectMult > math.MaxUint8 {
		return fmt.Errorf("detect_mult %d outside 1..255: %w", d.DetectMult, ErrInvalidUnsolicitedSessionDefaults)
	}
	return nil
}

// validateUnsolicitedInterfaces requires one enabled interface and parsable
// allowed prefixes on every listed interface.
func validateUnsolicitedInterfaces(interfaces map[string]UnsolicitedInterfaceConfig) error {
	enabled := false
	for name, ifCfg := range interfaces {
		enabled = enabled || ifCfg.Enabled
		for _, s := range ifCfg.AllowedPrefixes {
			if _, err := netip.ParsePrefix(s); err != nil {
				return fmt.Errorf("interface %q prefix %q: %w: %w", name, s, ErrInvalidUnsolicitedInterfaces, err)
			}
		}
	}
	if !enabled {
		return fmt.Errorf("no enabled interface: %w", ErrInvalidUnsolicitedInterfaces)
	}
	return nil
}

// validateGRPC requires at least one management listener and a usable
// socket path and mode.
func validateGRPC(cfg GRPCConfig) error {
	if cfg.Addr == "" && cfg.Socket.Path == "" {
		return ErrEmptyGRPCAddr
	}
	if err := validateGRPCTCP(cfg); err != nil {
		return err
	}
	if cfg.Socket.Path == "" {
		return nil
	}
	if !filepath.IsAbs(cfg.Socket.Path) {
		return fmt.Errorf("path %q is not absolute: %w", cfg.Socket.Path, ErrInvalidGRPCSocket)
	}
	if _, err := ParseSocketMode(cfg.Socket.Mode); err != nil {
		return err
	}
	return nil
}

// validateGRPCTCP requires complete mTLS settings with a TCP address,
// mutating identities only with mTLS, and plaintext TCP only on loopback.
func validateGRPCTCP(cfg GRPCConfig) error {
	if cfg.TLS.Enabled() && (cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "" ||
		cfg.TLS.ClientCAFile == "" || cfg.Addr == "") {
		return ErrInvalidGRPCTLS
	}
	if len(cfg.MutatingIdentities) > 0 && !cfg.TLS.Enabled() {
		return fmt.Errorf("grpc.mutating_identities: %w", ErrInvalidGRPCTLS)
	}
	if cfg.Addr != "" && !cfg.TLS.Enabled() && !IsLoopbackListenAddr(cfg.Addr) {
		return fmt.Errorf("grpc.addr %q: %w", cfg.Addr, ErrPlaintextNonLoopbackGRPC)
	}
	return nil
}

// IsLoopbackListenAddr reports whether a host:port listen address binds only
// loopback: a loopback IP or "localhost". An empty host is a wildcard.
func IsLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// ParseSocketMode parses an octal permission mode such as "0660".
func ParseSocketMode(mode string) (os.FileMode, error) {
	perm, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || perm > 0o777 {
		return 0, fmt.Errorf("mode %q is not an octal permission 0000-0777: %w", mode, ErrInvalidGRPCSocket)
	}
	return os.FileMode(perm), nil
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

	return validateEchoListenerBindings(cfg.Peers)
}

// vniMax is the maximum valid VNI value (24-bit).
const vniMax uint32 = 0x00FFFFFF

// validateVXLAN checks the VXLAN BFD configuration for logical errors.
func validateVXLAN(cfg VXLANConfig) error {
	if !cfg.Enabled || len(cfg.Peers) == 0 {
		return nil
	}

	if err := validateOverlayBackend("vxlan.backend", cfg.Backend); err != nil {
		return err
	}

	if cfg.ManagementVNI > vniMax {
		return fmt.Errorf("vxlan.management_vni %d: %w", cfg.ManagementVNI, ErrInvalidVXLANVNI)
	}

	seen := make(map[string]struct{}, len(cfg.Peers))
	seenWire := make(map[string]struct{}, len(cfg.Peers))

	for i, peer := range cfg.Peers {
		peerMAC, err := validateVXLANPeer(i, peer)
		if err != nil {
			return err
		}

		key := peer.VXLANSessionKey()
		if _, dup := seen[key]; dup {
			return fmt.Errorf("vxlan.peers[%d] key %q: %w", i, key, ErrDuplicateVXLANSessionKey)
		}
		seen[key] = struct{}{}
		wireKey := fmt.Sprintf("%s|%s|%x",
			canonicalOverlayAddr(peer.Peer), canonicalOverlayAddr(peer.Local), peerMAC)
		if _, dup := seenWire[wireKey]; dup {
			return fmt.Errorf("vxlan.peers[%d] wire key %q: %w", i, wireKey, ErrDuplicateVXLANSessionKey)
		}
		seenWire[wireKey] = struct{}{}
	}

	return nil
}

func validateVXLANPeer(index int, peer VXLANPeerConfig) ([6]byte, error) {
	peerAddr, err := peer.PeerAddr()
	if err != nil || !peerAddr.Is4() || !peerAddr.IsGlobalUnicast() {
		return [6]byte{}, fmt.Errorf("vxlan.peers[%d] peer %q: %w", index, peer.Peer, ErrInvalidVXLANPeer)
	}
	if peer.DetectMult > maxBFDWireUint8 {
		return [6]byte{}, fmt.Errorf("vxlan.peers[%d] detect_mult %d: %w",
			index, peer.DetectMult, ErrInvalidSessionDetectMult)
	}
	localAddr, localErr := peer.LocalAddr()
	if localErr != nil || !localAddr.Is4() || !localAddr.IsGlobalUnicast() {
		return [6]byte{}, fmt.Errorf("vxlan.peers[%d] local %q: %w", index, peer.Local, ErrInvalidVXLANPeer)
	}
	peerMAC, macErr := peer.PeerMACAddr()
	if macErr != nil {
		return [6]byte{}, fmt.Errorf("vxlan.peers[%d]: %w", index, macErr)
	}
	if _, macErr := peer.LocalMACAddr(); macErr != nil {
		return [6]byte{}, fmt.Errorf("vxlan.peers[%d]: %w", index, macErr)
	}
	return peerMAC, nil
}

// validateGeneve checks the Geneve BFD configuration for logical errors.
func validateGeneve(cfg GeneveConfig) error {
	if !cfg.Enabled || len(cfg.Peers) == 0 {
		return nil
	}

	if err := validateOverlayBackend("geneve.backend", cfg.Backend); err != nil {
		return err
	}

	if cfg.DefaultVNI > vniMax {
		return fmt.Errorf("geneve.default_vni %d: %w", cfg.DefaultVNI, ErrInvalidGeneveVNI)
	}

	seen := make(map[string]struct{}, len(cfg.Peers))
	for i, peer := range cfg.Peers {
		if err := validateGenevePeer(i, peer); err != nil {
			return err
		}
		key := peer.GeneveSessionKey(cfg.DefaultVNI)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("geneve.peers[%d] key %q: %w", i, key, ErrDuplicateGeneveSessionKey)
		}
		seen[key] = struct{}{}
	}

	return nil
}

func validateGenevePeer(index int, peer GenevePeerConfig) error {
	peerAddr, err := peer.PeerAddr()
	if err != nil {
		return fmt.Errorf("geneve.peers[%d]: %w: %w", index, ErrInvalidGenevePeer, err)
	}
	if !peerAddr.Is4() || !peerAddr.IsGlobalUnicast() {
		return fmt.Errorf("geneve.peers[%d] peer %q: %w", index, peer.Peer, ErrInvalidGenevePeer)
	}
	if peer.VNI > vniMax {
		return fmt.Errorf("geneve.peers[%d] vni %d: %w", index, peer.VNI, ErrInvalidGeneveVNI)
	}
	if peer.DetectMult > maxBFDWireUint8 {
		return fmt.Errorf("geneve.peers[%d] detect_mult %d: %w",
			index, peer.DetectMult, ErrInvalidSessionDetectMult)
	}
	localAddr, localErr := peer.LocalAddr()
	if localErr != nil || !localAddr.Is4() || !localAddr.IsGlobalUnicast() {
		return fmt.Errorf("geneve.peers[%d] local %q: %w", index, peer.Local, ErrInvalidGenevePeer)
	}
	if _, innerErr := peer.InnerPeerAddr(); innerErr != nil {
		return fmt.Errorf("geneve.peers[%d]: %w", index, innerErr)
	}
	if _, innerErr := peer.InnerLocalAddr(); innerErr != nil {
		return fmt.Errorf("geneve.peers[%d]: %w", index, innerErr)
	}
	if _, macErr := peer.PeerMACAddr(); macErr != nil {
		return fmt.Errorf("geneve.peers[%d]: %w", index, macErr)
	}
	if _, macErr := peer.LocalMACAddr(); macErr != nil {
		return fmt.Errorf("geneve.peers[%d]: %w", index, macErr)
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
