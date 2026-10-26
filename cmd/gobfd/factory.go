package main

import (
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"strings"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/netio"
)

const maxBFDWireUint8 = math.MaxUint8

func newUDPSenderFactory(socket config.SocketConfig) *udpSenderFactory {
	return &udpSenderFactory{
		portAlloc: netio.NewSourcePortAllocator(),
		socket:    socket,
		senders:   make(map[uint16]*netio.UDPSender),
	}
}

func (f *udpSenderFactory) CreateSender(
	localAddr netip.Addr,
	ifName string,
	kind bfd.SessionType,
	doNotFragment bool,
	logger *slog.Logger,
) (bfd.PacketSender, uint16, error) {
	var opts []netio.SenderOption
	switch kind {
	case bfd.SessionTypeSingleHop, bfd.SessionTypeMultiHop:
	case bfd.SessionTypeEcho:
		opts = append(opts, netio.WithDstPort(netio.PortEcho))
	default:
		return nil, 0, fmt.Errorf("API sender type %d: %w", kind, bfd.ErrInvalidSessionType)
	}
	if doNotFragment {
		opts = append(opts, netio.WithDFBit())
	}
	localAddr, opts = singleHopSenderBinding(localAddr, ifName, opts)
	return f.createSenderForSession(localAddr, kind == bfd.SessionTypeMultiHop, logger, opts...)
}

func (f *udpSenderFactory) CloseSender(srcPort uint16) error {
	f.mu.Lock()
	sender, ok := f.senders[srcPort]
	if ok {
		delete(f.senders, srcPort)
	}
	f.mu.Unlock()

	if !ok {
		return nil
	}

	f.portAlloc.Release(srcPort)

	if err := sender.Close(); err != nil {
		return fmt.Errorf("close sender port %d: %w", srcPort, err)
	}
	return nil
}

// createSenderForSession allocates a source port and creates a UDPSender
// for a declarative session. Used by reconcileSessions.
// When paddedPduSize is nonzero, the DF bit is set on the socket (RFC 9764).
func (f *udpSenderFactory) createSenderForSession(
	localAddr netip.Addr,
	multiHop bool,
	logger *slog.Logger,
	senderOpts ...netio.SenderOption,
) (bfd.PacketSender, uint16, error) {
	srcPort, err := f.portAlloc.Allocate()
	if err != nil {
		return nil, 0, fmt.Errorf("allocate source port: %w", err)
	}

	// Apply the immutable startup policy after caller-specific transport options.
	opts := make([]netio.SenderOption, 0, len(senderOpts)+2)
	opts = append(opts, senderOpts...)
	opts = append(opts, netio.WithWriteBuffer(f.socket.WriteBufferSize), netio.WithSocketQoS(netio.SocketQoS{
		IPv4DSCP: f.socket.IPv4DSCP, IPv6TrafficClass: f.socket.IPv6TrafficClass,
		Priority: f.socket.Priority, Mark: f.socket.Mark,
		PriorityRequired: f.socket.PriorityRequired, MarkRequired: f.socket.MarkRequired,
	}))
	sender, err := netio.NewUDPSender(localAddr, srcPort, multiHop, logger, opts...)
	if err != nil {
		f.portAlloc.Release(srcPort)
		return nil, 0, fmt.Errorf("create UDP sender %s:%d: %w", localAddr, srcPort, err)
	}

	f.mu.Lock()
	f.senders[srcPort] = sender
	f.mu.Unlock()

	return sender, srcPort, nil
}

// configSessionToBFD converts a config.SessionConfig to a bfd.SessionConfig,
// applying defaults from BFDConfig where per-session values are zero.
func configSessionToBFD(sc config.SessionConfig, defaults config.BFDConfig) (bfd.SessionConfig, error) {
	peerAddr, err := sc.PeerAddr()
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("parse peer address: %w", err)
	}

	localAddr, err := sc.LocalAddr()
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("parse local address: %w", err)
	}

	sessType := configSessionTypeToBFD(sc.Type)

	desiredMinTx := sc.DesiredMinTx
	if desiredMinTx == 0 {
		desiredMinTx = defaults.DefaultDesiredMinTx
	}

	requiredMinRx := sc.RequiredMinRx
	if requiredMinRx == 0 && !sc.RequiredMinRxSet {
		requiredMinRx = defaults.DefaultRequiredMinRx
	}

	detectMult := sc.DetectMult
	if detectMult == 0 {
		detectMult = defaults.DefaultDetectMultiplier
	}

	if detectMult > maxBFDWireUint8 {
		return bfd.SessionConfig{}, fmt.Errorf("detect_mult %d: %w", detectMult, errDetectMultOverflow)
	}

	// RFC 7419: align intervals to common set for hardware interop.
	if defaults.AlignIntervals {
		desiredMinTx = bfd.AlignToCommonInterval(desiredMinTx)
		requiredMinRx = bfd.AlignToCommonInterval(requiredMinRx)
	}

	// RFC 9764: per-session padded PDU size, falling back to global default.
	paddedPduSize := sc.PaddedPduSize
	if paddedPduSize == 0 {
		paddedPduSize = defaults.DefaultPaddedPduSize
	}

	auth, authKeys, err := configAuthToBFD(sc.Auth)
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("configure authentication: %w", err)
	}

	return bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Interface:             sessionInterface(sc.Interface, sessType, localAddr, peerAddr),
		Type:                  sessType,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
		PaddedPduSize:         paddedPduSize,
		MultiHopMaxHops:       uint8(min(sc.MultihopMaxHops, maxBFDWireUint8)), // 255 fails bfd validation.
		Auth:                  auth,
		AuthKeys:              authKeys,
	}, nil
}

func configSessionTypeToBFD(sessionType string) bfd.SessionType {
	if sessionType == "multi_hop" {
		return bfd.SessionTypeMultiHop
	}
	return bfd.SessionTypeSingleHop
}

func configAuthToBFD(authCfg config.AuthConfig) (bfd.Authenticator, bfd.AuthKeyStore, error) {
	authTypeName := strings.TrimSpace(authCfg.Type)
	if authTypeName == "" || authTypeName == "none" {
		return nil, nil, nil
	}

	authType, err := configAuthTypeToBFD(authTypeName)
	if err != nil {
		return nil, nil, err
	}
	if authCfg.KeyID > maxBFDWireUint8 {
		return nil, nil, fmt.Errorf("auth key ID %d exceeds 255: %w",
			authCfg.KeyID, config.ErrInvalidSessionAuthKeyID)
	}

	auth, err := bfd.NewAuthenticator(authType)
	if err != nil {
		return nil, nil, fmt.Errorf("create %q authenticator: %w", authTypeName, err)
	}
	keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{
		ID:     uint8(authCfg.KeyID), // Range checked above.
		Type:   authType,
		Secret: []byte(authCfg.Secret),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create authentication key store for key ID %d: %w", authCfg.KeyID, err)
	}

	return auth, keys, nil
}

func configAuthTypeToBFD(authType string) (bfd.AuthType, error) {
	switch authType {
	case "simple_password":
		return bfd.AuthTypeSimplePassword, nil
	case "keyed_md5":
		return bfd.AuthTypeKeyedMD5, nil
	case "meticulous_keyed_md5":
		return bfd.AuthTypeMeticulousKeyedMD5, nil
	case "keyed_sha1":
		return bfd.AuthTypeKeyedSHA1, nil
	case "meticulous_keyed_sha1":
		return bfd.AuthTypeMeticulousKeyedSHA1, nil
	default:
		return bfd.AuthTypeNone, fmt.Errorf("auth type %q: %w",
			authType, config.ErrInvalidSessionAuthType)
	}
}

// newManager creates a BFD session manager with metrics and optional unsolicited BFD (RFC 9468).
func newManager(
	cfg *config.Config,
	collector *bfdmetrics.Collector,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (*bfd.Manager, error) {
	if err := config.ValidateMicroBFDPreview(cfg.MicroBFD); err != nil {
		return nil, fmt.Errorf("validate Micro-BFD preview: %w", err)
	}
	if cfg.MicroBFD.Enabled {
		logger.Warn("Micro-BFD preview enabled; RFC 7130 L2 qualification is incomplete",
			slog.String("capability", "micro-bfd"), slog.String("maturity", "preview"))
	}
	opts := []bfd.ManagerOption{
		bfd.WithManagerMetrics(collector),
		bfd.WithSessionInterfaceResolver(netio.ResolveSessionIfIndex),
	}
	actuator, actuatorEnabled, err := buildMicroBFDActuator(cfg.MicroBFD.Actuator, logger)
	if err != nil {
		return nil, fmt.Errorf("invalid micro-BFD actuator config: %w", err)
	}
	if actuatorEnabled {
		opts = append(opts, bfd.WithMicroBFDActuator(actuator))
	}
	if cfg.Unsolicited.Enabled {
		policy, err := buildUnsolicitedPolicy(cfg.Unsolicited)
		if err != nil {
			return nil, fmt.Errorf("invalid unsolicited BFD config: %w", err)
		}
		opts = append(opts,
			bfd.WithUnsolicitedPolicy(policy),
			bfd.WithUnsolicitedSessionSender(unsolicitedSessionSender(sf, logger)),
		)
		logger.Info("unsolicited BFD enabled (RFC 9468)",
			slog.Int("max_sessions", policy.MaxSessions),
		)
	}
	return bfd.NewManager(logger, opts...), nil
}

func buildMicroBFDActuator(
	cfg config.MicroBFDActuatorConfig,
	logger *slog.Logger,
) (bfd.MicroBFDActuator, bool, error) {
	if cfg.Mode == config.MicroBFDActuatorModeDisabled {
		return nil, false, nil
	}
	actuatorCfg := configMicroBFDActuatorToNetio(cfg)
	var backend netio.LAGActuatorBackend
	var err error
	if actuatorCfg.Mode == netio.LAGActuatorModeEnforce {
		backend, err = netio.NewLAGActuatorBackend(actuatorCfg)
		if err != nil {
			return nil, false, fmt.Errorf("create micro-BFD LAG actuator backend: %w", err)
		}
	}
	actuator, err := netio.NewLAGActuator(actuatorCfg, backend, logger)
	if err != nil {
		return nil, false, fmt.Errorf("create micro-BFD LAG actuator: %w", err)
	}
	return actuator, true, nil
}

func configMicroBFDActuatorToNetio(cfg config.MicroBFDActuatorConfig) netio.LAGActuatorConfig {
	return netio.LAGActuatorConfig{
		Mode:          netio.LAGActuatorMode(cfg.Mode),
		Backend:       netio.LAGActuatorBackendType(cfg.Backend),
		OVSDBEndpoint: cfg.OVSDBEndpoint,
		OwnerPolicy:   netio.LAGOwnerPolicy(cfg.OwnerPolicy),
		DownAction:    netio.LAGActuatorAction(cfg.DownAction),
		UpAction:      netio.LAGActuatorAction(cfg.UpAction),
	}
}

// buildUnsolicitedPolicy converts config.UnsolicitedConfig to bfd.UnsolicitedPolicy.
func buildUnsolicitedPolicy(cfg config.UnsolicitedConfig) (*bfd.UnsolicitedPolicy, error) {
	interfaces := make(map[string]bfd.UnsolicitedInterfaceConfig, len(cfg.Interfaces))
	for name, ifCfg := range cfg.Interfaces {
		prefixes := make([]netip.Prefix, 0, len(ifCfg.AllowedPrefixes))
		for _, s := range ifCfg.AllowedPrefixes {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("interface %s: parse prefix %q: %w", name, s, err)
			}
			prefixes = append(prefixes, p)
		}
		var ifIndex int
		if cfg.Enabled && ifCfg.Enabled {
			iface, err := net.InterfaceByName(name)
			if err != nil {
				return nil, fmt.Errorf("resolve unsolicited interface %q: %w", name, err)
			}
			ifIndex = iface.Index
		}
		interfaces[name] = bfd.UnsolicitedInterfaceConfig{
			Enabled: ifCfg.Enabled, IfIndex: ifIndex, AllowedPrefixes: prefixes,
		}
	}

	detectMult := cfg.SessionDefaults.DetectMult
	if detectMult > maxBFDWireUint8 {
		return nil, fmt.Errorf("unsolicited detect_mult %d: %w", detectMult, errDetectMultOverflow)
	}
	auth, authKeys, err := configAuthToBFD(cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("unsolicited auth: %w", err)
	}

	return &bfd.UnsolicitedPolicy{
		Enabled:     cfg.Enabled,
		Interfaces:  interfaces,
		MaxSessions: cfg.MaxSessions,
		SessionDefaults: bfd.UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  cfg.SessionDefaults.DesiredMinTx,
			RequiredMinRxInterval: cfg.SessionDefaults.RequiredMinRx,
			DetectMultiplier:      uint8(detectMult),
		},
		CleanupTimeout: cfg.CleanupTimeout,
		Auth:           auth,
		AuthKeys:       authKeys,
	}, nil
}

// unsolicitedSessionSender builds the sender of one RFC 9468 passive session
// on the local address and interface that received the triggering packet,
// with its own RFC 5881 source port. An IPv6 link-local address is scoped
// to the receiving interface.
func unsolicitedSessionSender(
	sf declarativeSenderFactory,
	logger *slog.Logger,
) func(bfd.PacketMeta) (*bfd.SenderLease, error) {
	return func(meta bfd.PacketMeta) (*bfd.SenderLease, error) {
		local, opts := singleHopSenderBinding(meta.DstAddr.Unmap(), meta.IfName, nil)
		return declarativeSenderLeaseFactoryFor(sf, local, false, logger, opts...)()
	}
}

// sessionInterface returns the single-hop interface, which a link-local zone
// selects when interface is empty; multihop sessions are never device-bound.
func sessionInterface(ifName string, sessType bfd.SessionType, local, peer netip.Addr) string {
	if sessType != bfd.SessionTypeSingleHop {
		return ifName
	}
	return bfd.SingleHopInterface(ifName, local, peer)
}

// singleHopSenderBinding binds a single-hop sender to ifName so control
// packets leave over the protected one-hop path (RFC 5881 Section 6). An
// unzoned IPv6 link-local local address is scoped to that interface. An
// empty ifName leaves the route lookup to the kernel.
func singleHopSenderBinding(
	local netip.Addr, ifName string, opts []netio.SenderOption,
) (netip.Addr, []netio.SenderOption) {
	if ifName == "" {
		return local, opts
	}
	opts = append(opts, netio.WithBindDevice(ifName))
	if local.Is6() && local.IsLinkLocalUnicast() && local.Zone() == "" {
		local = local.WithZone(ifName)
	}
	return local, opts
}
