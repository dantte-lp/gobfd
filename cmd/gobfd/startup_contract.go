package main

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/dantte-lp/gobfd/internal/config"
)

var errStartupConfigChanged = errors.New("startup-owned configuration changed")

type startupFieldID uint8

const (
	startupFieldGRPCAddr startupFieldID = iota
	startupFieldMetricsAddr
	startupFieldMetricsPath
	startupFieldLogFormat
	startupFieldSocketReadBufferSize
	startupFieldSocketWriteBufferSize
	startupFieldUnsolicitedEnabled
	startupFieldUnsolicitedMaxSessions
	startupFieldUnsolicitedCleanupTimeout
	startupFieldUnsolicitedInterfaces
	startupFieldUnsolicitedDesiredMinTx
	startupFieldUnsolicitedRequiredMinRx
	startupFieldUnsolicitedDetectMult
	startupFieldMicroActuatorMode
	startupFieldMicroActuatorBackend
	startupFieldMicroActuatorOVSDBEndpoint
	startupFieldMicroActuatorOwnerPolicy
	startupFieldMicroActuatorDownAction
	startupFieldMicroActuatorUpAction
	startupFieldGoBGPEnabled
	startupFieldGoBGPAddr
	startupFieldGoBGPStrategy
	startupFieldGoBGPActionTimeout
	startupFieldGoBGPTLS
	startupFieldGoBGPDampening
	startupFieldVXLANBackend
	startupFieldVXLANManagementVNI
	startupFieldGeneveBackend
	startupFieldGeneveDefaultVNI
	startupFieldGenevePeerVNI
	startupFieldControlListenerBindings
	startupFieldEchoListenerBindings
	startupFieldMicroListenerBindings
	startupFieldVXLANListenerBinding
	startupFieldGeneveListenerBinding
)

func (f startupFieldID) String() string {
	paths := [...]string{
		"grpc.addr",
		"metrics.addr",
		"metrics.path",
		"log.format",
		"socket.read_buffer_size",
		"socket.write_buffer_size",
		"unsolicited.enabled",
		"unsolicited.max_sessions",
		"unsolicited.cleanup_timeout",
		"unsolicited.interfaces",
		"unsolicited.session_defaults.desired_min_tx",
		"unsolicited.session_defaults.required_min_rx",
		"unsolicited.session_defaults.detect_mult",
		"micro_bfd.actuator.mode",
		"micro_bfd.actuator.backend",
		"micro_bfd.actuator.ovsdb_endpoint",
		"micro_bfd.actuator.owner_policy",
		"micro_bfd.actuator.down_action",
		"micro_bfd.actuator.up_action",
		"gobgp.enabled",
		"gobgp.addr",
		"gobgp.strategy",
		"gobgp.action_timeout",
		"gobgp.tls",
		"gobgp.dampening",
		"vxlan.backend",
		"vxlan.management_vni",
		"geneve.backend",
		"geneve.default_vni",
		"geneve.peers[].vni",
		"sessions[].listener_binding",
		"echo.peers[].listener_binding",
		"micro_bfd.groups[].listener_binding",
		"vxlan.listener_binding",
		"geneve.listener_binding",
	}
	if int(f) >= len(paths) {
		return "unknown"
	}
	return paths[f]
}

type startupConfigChangeError struct {
	fields []startupFieldID
}

func (e *startupConfigChangeError) Error() string {
	paths := make([]string, len(e.fields))
	for i, field := range e.fields {
		paths[i] = field.String()
	}
	return fmt.Sprintf("%v: %s", errStartupConfigChanged, strings.Join(paths, ", "))
}

func (e *startupConfigChangeError) Unwrap() error { return errStartupConfigChanged }

func (e *startupConfigChangeError) FieldIDs() []startupFieldID {
	return slices.Clone(e.fields)
}

type controlListenerCapability struct {
	addr     netip.Addr
	multiHop bool
}

type microListenerCapability struct {
	addr          netip.Addr
	interfaceName string
}

type overlayCapability struct {
	configured bool
	local      netip.Addr
	vni        uint32
}

type startupRuntimeContract struct {
	grpc               config.GRPCConfig
	metrics            config.MetricsConfig
	logFormat          string
	socket             config.SocketConfig
	unsolicited        config.UnsolicitedConfig
	actuator           config.MicroBFDActuatorConfig
	gobgp              config.GoBGPConfig
	vxlanBackend       string
	vxlanManagementVNI uint32
	geneveBackend      string
	geneveDefaultVNI   uint32

	controlListeners map[controlListenerCapability]string
	echoListeners    map[netip.Addr]string
	microListeners   map[microListenerCapability]struct{}
	vxlan            overlayCapability
	geneve           overlayCapability
	vxlanPeerLocals  map[string]netip.Addr
	genevePeerLocals map[string]netip.Addr
	genevePeerVNIs   map[string]uint32
}

func newStartupRuntimeContract(cfg *config.Config) startupRuntimeContract {
	return startupRuntimeContract{
		grpc:               cfg.GRPC,
		metrics:            cfg.Metrics,
		logFormat:          cfg.Log.Format,
		socket:             cfg.Socket,
		unsolicited:        cloneUnsolicitedConfig(cfg.Unsolicited),
		actuator:           cfg.MicroBFD.Actuator,
		gobgp:              cfg.GoBGP,
		vxlanBackend:       cfg.VXLAN.Backend,
		vxlanManagementVNI: cfg.VXLAN.ManagementVNI,
		geneveBackend:      cfg.Geneve.Backend,
		geneveDefaultVNI:   cfg.Geneve.DefaultVNI,
		controlListeners:   controlListenerCapabilities(cfg),
		echoListeners:      echoListenerCapabilities(cfg),
		microListeners:     microListenerCapabilities(cfg),
		vxlan:              vxlanCapability(cfg),
		geneve:             geneveCapability(cfg),
		vxlanPeerLocals:    vxlanPeerLocalCapabilities(cfg),
		genevePeerLocals:   genevePeerLocalCapabilities(cfg),
		genevePeerVNIs:     genevePeerVNICapabilities(cfg),
	}
}

func cloneUnsolicitedConfig(src config.UnsolicitedConfig) config.UnsolicitedConfig {
	dst := src
	dst.Interfaces = make(map[string]config.UnsolicitedInterfaceConfig, len(src.Interfaces))
	for name, iface := range src.Interfaces {
		iface.AllowedPrefixes = slices.Clone(iface.AllowedPrefixes)
		slices.Sort(iface.AllowedPrefixes)
		dst.Interfaces[name] = iface
	}
	return dst
}

func (c startupRuntimeContract) changedFields(cfg *config.Config) []startupFieldID {
	changed := make([]startupFieldID, 0, 8)
	appendChanged := func(field startupFieldID, condition bool) {
		if condition {
			changed = append(changed, field)
		}
	}

	appendChanged(startupFieldGRPCAddr, c.grpc.Addr != cfg.GRPC.Addr)
	appendChanged(startupFieldMetricsAddr, c.metrics.Addr != cfg.Metrics.Addr)
	appendChanged(startupFieldMetricsPath, c.metrics.Path != cfg.Metrics.Path)
	appendChanged(startupFieldLogFormat, c.logFormat != cfg.Log.Format)
	appendChanged(startupFieldSocketReadBufferSize, c.socket.ReadBufferSize != cfg.Socket.ReadBufferSize)
	appendChanged(startupFieldSocketWriteBufferSize, c.socket.WriteBufferSize != cfg.Socket.WriteBufferSize)
	appendChanged(startupFieldUnsolicitedEnabled, c.unsolicited.Enabled != cfg.Unsolicited.Enabled)
	appendChanged(startupFieldUnsolicitedMaxSessions, c.unsolicited.MaxSessions != cfg.Unsolicited.MaxSessions)
	appendChanged(startupFieldUnsolicitedCleanupTimeout, c.unsolicited.CleanupTimeout != cfg.Unsolicited.CleanupTimeout)
	appendChanged(startupFieldUnsolicitedInterfaces,
		!equalUnsolicitedInterfaces(c.unsolicited.Interfaces, cfg.Unsolicited.Interfaces))
	appendChanged(startupFieldUnsolicitedDesiredMinTx,
		c.unsolicited.SessionDefaults.DesiredMinTx != cfg.Unsolicited.SessionDefaults.DesiredMinTx)
	appendChanged(startupFieldUnsolicitedRequiredMinRx,
		c.unsolicited.SessionDefaults.RequiredMinRx != cfg.Unsolicited.SessionDefaults.RequiredMinRx)
	appendChanged(startupFieldUnsolicitedDetectMult,
		c.unsolicited.SessionDefaults.DetectMult != cfg.Unsolicited.SessionDefaults.DetectMult)
	appendChanged(startupFieldMicroActuatorMode, c.actuator.Mode != cfg.MicroBFD.Actuator.Mode)
	appendChanged(startupFieldMicroActuatorBackend, c.actuator.Backend != cfg.MicroBFD.Actuator.Backend)
	appendChanged(startupFieldMicroActuatorOVSDBEndpoint, c.actuator.OVSDBEndpoint != cfg.MicroBFD.Actuator.OVSDBEndpoint)
	appendChanged(startupFieldMicroActuatorOwnerPolicy, c.actuator.OwnerPolicy != cfg.MicroBFD.Actuator.OwnerPolicy)
	appendChanged(startupFieldMicroActuatorDownAction, c.actuator.DownAction != cfg.MicroBFD.Actuator.DownAction)
	appendChanged(startupFieldMicroActuatorUpAction, c.actuator.UpAction != cfg.MicroBFD.Actuator.UpAction)
	appendChanged(startupFieldGoBGPEnabled, c.gobgp.Enabled != cfg.GoBGP.Enabled)
	appendChanged(startupFieldGoBGPAddr, c.gobgp.Addr != cfg.GoBGP.Addr)
	appendChanged(startupFieldGoBGPStrategy, c.gobgp.Strategy != cfg.GoBGP.Strategy)
	appendChanged(startupFieldGoBGPActionTimeout, c.gobgp.ActionTimeout != cfg.GoBGP.ActionTimeout)
	appendChanged(startupFieldGoBGPTLS, c.gobgp.TLS != cfg.GoBGP.TLS)
	appendChanged(startupFieldGoBGPDampening, c.gobgp.Dampening != cfg.GoBGP.Dampening)
	appendChanged(startupFieldVXLANBackend, c.vxlanBackend != cfg.VXLAN.Backend)
	appendChanged(startupFieldVXLANManagementVNI, c.vxlanManagementVNI != cfg.VXLAN.ManagementVNI)
	appendChanged(startupFieldGeneveBackend, c.geneveBackend != cfg.Geneve.Backend)
	appendChanged(startupFieldGeneveDefaultVNI, c.geneveDefaultVNI != cfg.Geneve.DefaultVNI)
	appendChanged(startupFieldGenevePeerVNI, !c.supportsGenevePeerVNIs(cfg))
	appendChanged(startupFieldControlListenerBindings,
		!supportsMapCapabilities(c.controlListeners, controlListenerCapabilities(cfg)))
	appendChanged(startupFieldEchoListenerBindings,
		!supportsMapCapabilities(c.echoListeners, echoListenerCapabilities(cfg)))
	appendChanged(startupFieldMicroListenerBindings,
		!supportsSetCapabilities(c.microListeners, microListenerCapabilities(cfg)))
	appendChanged(startupFieldVXLANListenerBinding,
		!supportsOverlayPeerLocals(c.vxlan, c.vxlanPeerLocals, vxlanPeerLocalCapabilities(cfg)))
	appendChanged(startupFieldGeneveListenerBinding,
		!supportsOverlayPeerLocals(c.geneve, c.genevePeerLocals, genevePeerLocalCapabilities(cfg)))
	return changed
}

func equalUnsolicitedInterfaces(
	want, got map[string]config.UnsolicitedInterfaceConfig,
) bool {
	return maps.EqualFunc(want, got, func(want, got config.UnsolicitedInterfaceConfig) bool {
		prefixes := slices.Clone(got.AllowedPrefixes)
		slices.Sort(prefixes)
		return want.Enabled == got.Enabled && slices.Equal(want.AllowedPrefixes, prefixes)
	})
}

func supportsMapCapabilities[K comparable, V comparable](available, required map[K]V) bool {
	for key, value := range required {
		if availableValue, ok := available[key]; !ok || availableValue != value {
			return false
		}
	}
	return true
}

func supportsSetCapabilities[K comparable](available, required map[K]struct{}) bool {
	for key := range required {
		if _, ok := available[key]; !ok {
			return false
		}
	}
	return true
}

func supportsOverlayPeerLocals(
	available overlayCapability,
	existing, required map[string]netip.Addr,
) bool {
	for key, local := range required {
		if startupLocal, ok := existing[key]; ok {
			if startupLocal != local {
				return false
			}
			continue
		}
		if !available.configured || local != available.local {
			return false
		}
	}
	return true
}

func controlListenerCapabilities(cfg *config.Config) map[controlListenerCapability]string {
	result := make(map[controlListenerCapability]string)
	for _, session := range cfg.Sessions {
		addr, err := session.LocalAddr()
		if err != nil || !addr.IsValid() {
			continue
		}
		key := controlListenerCapability{addr: addr, multiHop: session.Type == "multi_hop"}
		if _, exists := result[key]; !exists {
			result[key] = session.Interface
		}
	}
	return result
}

func echoListenerCapabilities(cfg *config.Config) map[netip.Addr]string {
	result := make(map[netip.Addr]string)
	if !cfg.Echo.Enabled {
		return result
	}
	for _, peer := range cfg.Echo.Peers {
		addr, err := peer.LocalAddr()
		if err != nil || !addr.IsValid() {
			continue
		}
		if _, exists := result[addr]; !exists {
			result[addr] = peer.Interface
		}
	}
	return result
}

func microListenerCapabilities(cfg *config.Config) map[microListenerCapability]struct{} {
	result := make(map[microListenerCapability]struct{})
	for _, group := range cfg.MicroBFD.Groups {
		addr, err := netip.ParseAddr(group.LocalAddr)
		if err != nil || !addr.IsValid() {
			continue
		}
		for _, member := range group.MemberLinks {
			result[microListenerCapability{addr: addr, interfaceName: member}] = struct{}{}
		}
	}
	return result
}

func vxlanCapability(_ *config.Config) overlayCapability {
	return overlayCapability{}
}

func geneveCapability(_ *config.Config) overlayCapability {
	return overlayCapability{}
}

func vxlanPeerLocalCapabilities(cfg *config.Config) map[string]netip.Addr {
	result := make(map[string]netip.Addr, len(cfg.VXLAN.Peers))
	if !cfg.VXLAN.Enabled {
		return result
	}
	for _, peer := range cfg.VXLAN.Peers {
		addr, err := peer.LocalAddr()
		if err == nil && addr.IsValid() {
			result[peer.VXLANSessionKey()] = addr
		}
	}
	return result
}

func genevePeerLocalCapabilities(cfg *config.Config) map[string]netip.Addr {
	result := make(map[string]netip.Addr, len(cfg.Geneve.Peers))
	if !cfg.Geneve.Enabled {
		return result
	}
	for _, peer := range cfg.Geneve.Peers {
		addr, err := peer.LocalAddr()
		if err == nil && addr.IsValid() {
			result[peer.GeneveSessionKey(cfg.Geneve.DefaultVNI)] = addr
		}
	}
	return result
}

func genevePeerVNICapabilities(cfg *config.Config) map[string]uint32 {
	result := make(map[string]uint32, len(cfg.Geneve.Peers))
	if !cfg.Geneve.Enabled {
		return result
	}
	for _, peer := range cfg.Geneve.Peers {
		result[peer.GeneveSessionKey(cfg.Geneve.DefaultVNI)] = effectiveGeneveVNI(peer, cfg.Geneve.DefaultVNI)
	}
	return result
}

func (c startupRuntimeContract) supportsGenevePeerVNIs(cfg *config.Config) bool {
	if !cfg.Geneve.Enabled {
		return true
	}
	for _, peer := range cfg.Geneve.Peers {
		key := peer.GeneveSessionKey(cfg.Geneve.DefaultVNI)
		effectiveVNI := effectiveGeneveVNI(peer, cfg.Geneve.DefaultVNI)
		if startupVNI, exists := c.genevePeerVNIs[key]; exists {
			if startupVNI != effectiveVNI {
				return false
			}
			continue
		}
		if !c.geneve.configured || effectiveVNI != c.geneve.vni {
			return false
		}
	}
	return true
}

func effectiveGeneveVNI(peer config.GenevePeerConfig, defaultVNI uint32) uint32 {
	if peer.VNI != 0 {
		return peer.VNI
	}
	return defaultVNI
}
