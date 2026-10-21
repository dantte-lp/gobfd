package main

import (
	"errors"
	"fmt"
	"maps"
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
	startupFieldUnsolicitedAuth
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
	startupFieldGRPCSocket
	startupFieldGRPCTLS
	startupFieldGRPCMutatingIdentities
	startupFieldGRPCFlightRecorder
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
		"unsolicited.auth",
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
		"grpc.socket",
		"grpc.tls",
		"grpc.mutating_identities",
		"grpc.flight_recorder",
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

// appendUnsolicitedChanges reports RFC 9468 fields that need a restart.
func (c startupRuntimeContract) appendUnsolicitedChanges(
	cfg config.UnsolicitedConfig,
	appendChanged func(startupFieldID, bool),
) {
	appendChanged(startupFieldUnsolicitedEnabled, c.unsolicited.Enabled != cfg.Enabled)
	appendChanged(startupFieldUnsolicitedMaxSessions, c.unsolicited.MaxSessions != cfg.MaxSessions)
	appendChanged(startupFieldUnsolicitedCleanupTimeout, c.unsolicited.CleanupTimeout != cfg.CleanupTimeout)
	appendChanged(startupFieldUnsolicitedInterfaces,
		!equalUnsolicitedInterfaces(c.unsolicited.Interfaces, cfg.Interfaces))
	appendChanged(startupFieldUnsolicitedDesiredMinTx,
		c.unsolicited.SessionDefaults.DesiredMinTx != cfg.SessionDefaults.DesiredMinTx)
	appendChanged(startupFieldUnsolicitedRequiredMinRx,
		c.unsolicited.SessionDefaults.RequiredMinRx != cfg.SessionDefaults.RequiredMinRx)
	appendChanged(startupFieldUnsolicitedDetectMult,
		c.unsolicited.SessionDefaults.DetectMult != cfg.SessionDefaults.DetectMult)
	appendChanged(startupFieldUnsolicitedAuth, c.unsolicited.Auth != cfg.Auth)
}

func (c startupRuntimeContract) changedFields(cfg *config.Config) []startupFieldID {
	changed := make([]startupFieldID, 0, 8)
	appendChanged := func(field startupFieldID, condition bool) {
		if condition {
			changed = append(changed, field)
		}
	}

	appendChanged(startupFieldGRPCAddr, c.grpc.Addr != cfg.GRPC.Addr)
	appendChanged(startupFieldGRPCSocket, c.grpc.Socket != cfg.GRPC.Socket)
	appendChanged(startupFieldGRPCTLS, c.grpc.TLS != cfg.GRPC.TLS)
	appendChanged(startupFieldGRPCFlightRecorder, c.grpc.FlightRecorder != cfg.GRPC.FlightRecorder)
	appendChanged(startupFieldGRPCMutatingIdentities,
		!slices.Equal(c.grpc.MutatingIdentities, cfg.GRPC.MutatingIdentities))
	appendChanged(startupFieldMetricsAddr, c.metrics.Addr != cfg.Metrics.Addr)
	appendChanged(startupFieldMetricsPath, c.metrics.Path != cfg.Metrics.Path)
	appendChanged(startupFieldLogFormat, c.logFormat != cfg.Log.Format)
	appendChanged(startupFieldSocketReadBufferSize, c.socket.ReadBufferSize != cfg.Socket.ReadBufferSize)
	appendChanged(startupFieldSocketWriteBufferSize, c.socket.WriteBufferSize != cfg.Socket.WriteBufferSize)
	c.appendUnsolicitedChanges(cfg.Unsolicited, appendChanged)
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

func effectiveGeneveVNI(peer config.GenevePeerConfig, defaultVNI uint32) uint32 {
	if peer.VNI != 0 {
		return peer.VNI
	}
	return defaultVNI
}
