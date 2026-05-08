package main

import (
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func newUDPSenderFactory() *udpSenderFactory {
	return &udpSenderFactory{
		portAlloc: netio.NewSourcePortAllocator(),
		senders:   make(map[uint16]*netio.UDPSender),
	}
}

func (f *udpSenderFactory) CreateSender(
	localAddr netip.Addr,
	multiHop bool,
	logger *slog.Logger,
) (bfd.PacketSender, uint16, error) {
	srcPort, err := f.portAlloc.Allocate()
	if err != nil {
		return nil, 0, fmt.Errorf("allocate source port: %w", err)
	}

	sender, err := netio.NewUDPSender(localAddr, srcPort, multiHop, logger)
	if err != nil {
		f.portAlloc.Release(srcPort)
		return nil, 0, fmt.Errorf("create UDP sender %s:%d: %w", localAddr, srcPort, err)
	}

	f.mu.Lock()
	f.senders[srcPort] = sender
	f.mu.Unlock()

	return sender, srcPort, nil
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
) (bfd.PacketSender, error) {
	srcPort, err := f.portAlloc.Allocate()
	if err != nil {
		return nil, fmt.Errorf("allocate source port: %w", err)
	}

	sender, err := netio.NewUDPSender(localAddr, srcPort, multiHop, logger, senderOpts...)
	if err != nil {
		f.portAlloc.Release(srcPort)
		return nil, fmt.Errorf("create UDP sender %s:%d: %w", localAddr, srcPort, err)
	}

	f.mu.Lock()
	f.senders[srcPort] = sender
	f.mu.Unlock()

	return sender, nil
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
	if requiredMinRx == 0 {
		requiredMinRx = defaults.DefaultRequiredMinRx
	}

	detectMult := sc.DetectMult
	if detectMult == 0 {
		detectMult = defaults.DefaultDetectMultiplier
	}

	if detectMult > 255 {
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
		Interface:             sc.Interface,
		Type:                  sessType,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
		PaddedPduSize:         paddedPduSize,
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
	if authCfg.KeyID > 255 {
		return nil, nil, fmt.Errorf("auth key ID %d exceeds 255: %w",
			authCfg.KeyID, config.ErrInvalidSessionAuthKeyID)
	}

	auth, err := bfd.NewAuthenticator(authType)
	if err != nil {
		return nil, nil, err
	}
	keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{
		ID:     uint8(authCfg.KeyID), // Range checked above.
		Type:   authType,
		Secret: []byte(authCfg.Secret),
	})
	if err != nil {
		return nil, nil, err
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
func newManager(cfg *config.Config, collector *bfdmetrics.Collector, logger *slog.Logger) (*bfd.Manager, error) {
	opts := []bfd.ManagerOption{bfd.WithManagerMetrics(collector)}
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
		opts = append(opts, bfd.WithUnsolicitedPolicy(policy))

		sf := newUDPSenderFactory()
		sender, err := sf.createSenderForSession(netip.IPv4Unspecified(), false, logger)
		if err != nil {
			return nil, fmt.Errorf("create unsolicited BFD sender: %w", err)
		}
		opts = append(opts, bfd.WithUnsolicitedSender(sender))
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
			return nil, false, err
		}
	}
	actuator, err := netio.NewLAGActuator(actuatorCfg, backend, logger)
	if err != nil {
		return nil, false, err
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
		interfaces[name] = bfd.UnsolicitedInterfaceConfig{
			Enabled:         ifCfg.Enabled,
			AllowedPrefixes: prefixes,
		}
	}

	detectMult := cfg.SessionDefaults.DetectMult
	if detectMult > 255 {
		return nil, fmt.Errorf("unsolicited detect_mult %d: %w", detectMult, errDetectMultOverflow)
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
	}, nil
}
