package bfd

import (
	"fmt"
	"net/netip"
	"time"
)

// AddressFamily identifies the canonical address family of a BFD session.
type AddressFamily uint8

const (
	// AddressFamilyIPv4 identifies an IPv4 session.
	AddressFamilyIPv4 AddressFamily = 4

	// AddressFamilyIPv6 identifies an IPv6 session.
	AddressFamilyIPv6 AddressFamily = 6
)

// TransportScopeKind identifies the transport-specific part of session identity.
type TransportScopeKind uint8

const (
	// TransportScopeBase is the empty transport scope used by base BFD.
	TransportScopeBase TransportScopeKind = iota

	// TransportScopeMicroBFD identifies a Micro-BFD member transport.
	TransportScopeMicroBFD

	// TransportScopeVXLAN identifies a VXLAN transport.
	TransportScopeVXLAN

	// TransportScopeGeneve identifies a Geneve transport.
	TransportScopeGeneve
)

// TransportScope is a comparable, exact transport identity. The zero value is
// the base BFD transport. Transport-specific adapters populate the remaining
// fields when they migrate to canonical ownership.
type TransportScope struct {
	Kind            TransportScopeKind
	MemberInterface string
	Owner           string
	Backend         string
	VNI             uint32
	VAP             uint32
}

// SessionKey is the comparable canonical identity of one wire session. It is
// deliberately distinct from packetDemuxKey, which exists only for initial
// packet delivery before the remote knows our discriminator.
type SessionKey struct {
	Type           SessionType
	AddressFamily  AddressFamily
	PeerAddr       netip.Addr
	LocalAddr      netip.Addr
	Interface      string
	NetworkScope   string
	TransportScope TransportScope
}

// SessionOwnerSource is the typed source of a desired-session claim.
type SessionOwnerSource uint8

const (
	// SessionOwnerSourceConfig is the declarative configuration source.
	SessionOwnerSourceConfig SessionOwnerSource = iota + 1

	// SessionOwnerSourceCompatibilityAPI is the existing CreateSession API.
	SessionOwnerSourceCompatibilityAPI

	// SessionOwnerSourceUnsolicited is an RFC 9468 dynamically learned claim.
	SessionOwnerSourceUnsolicited
)

// SessionOwner identifies one source-scoped claim. ID permits future sources
// to distinguish owners without using an untyped string as the source itself.
type SessionOwner struct {
	Source SessionOwnerSource
	ID     string
}

func configSessionOwner() SessionOwner {
	return SessionOwner{
		Source: SessionOwnerSourceConfig,
		ID:     "config",
	}
}

func compatibilityAPISessionOwner() SessionOwner {
	return SessionOwner{
		Source: SessionOwnerSourceCompatibilityAPI,
		ID:     "compatibility",
	}
}

func unsolicitedSessionOwner() SessionOwner {
	return SessionOwner{
		Source: SessionOwnerSourceUnsolicited,
		ID:     "unsolicited",
	}
}

func canonicalSessionConfig(cfg SessionConfig) SessionConfig {
	if cfg.PeerAddr.IsValid() {
		cfg.PeerAddr = cfg.PeerAddr.Unmap()
	}
	if cfg.LocalAddr.IsValid() {
		cfg.LocalAddr = cfg.LocalAddr.Unmap()
	}
	return cfg
}

func sessionKeyFromConfig(cfg SessionConfig) (SessionKey, error) {
	cfg = canonicalSessionConfig(cfg)
	if !cfg.PeerAddr.IsValid() {
		return SessionKey{}, fmt.Errorf("canonical session key: %w", ErrInvalidPeerAddr)
	}

	var family AddressFamily
	switch cfg.PeerAddr.BitLen() {
	case 32:
		family = AddressFamilyIPv4
	case 128:
		family = AddressFamilyIPv6
	default:
		return SessionKey{}, fmt.Errorf("canonical session key for peer %s: %w",
			cfg.PeerAddr, ErrInvalidPeerAddr)
	}

	return SessionKey{
		Type:           cfg.Type,
		AddressFamily:  family,
		PeerAddr:       cfg.PeerAddr,
		LocalAddr:      cfg.LocalAddr,
		Interface:      cfg.Interface,
		NetworkScope:   cfg.NetworkScope,
		TransportScope: cfg.TransportScope,
	}, nil
}

type effectiveSessionConfig struct {
	Role                  SessionRole
	DesiredMinTxInterval  time.Duration
	RequiredMinRxInterval time.Duration
	DetectMultiplier      uint8
	PaddedPduSize         uint16
	AuthType              AuthType
	AuthKeys              authKeyStoreFingerprint
}

func normalizeEffectiveSessionConfig(cfg SessionConfig) (effectiveSessionConfig, error) {
	effective := effectiveSessionConfig{
		Role:                  cfg.Role,
		DesiredMinTxInterval:  cfg.DesiredMinTxInterval,
		RequiredMinRxInterval: cfg.RequiredMinRxInterval,
		DetectMultiplier:      cfg.DetectMultiplier,
		PaddedPduSize:         cfg.PaddedPduSize,
	}
	if cfg.Auth == nil {
		return effective, nil
	}

	authType, ok := authenticatorType(cfg.Auth)
	if !ok {
		return effectiveSessionConfig{}, fmt.Errorf("normalize authenticator %T: %w",
			cfg.Auth, ErrAuthTypeMismatch)
	}
	store, ok := cfg.AuthKeys.(effectiveAuthKeyStore)
	if !ok {
		return effectiveSessionConfig{}, fmt.Errorf("normalize auth key store %T: %w",
			cfg.AuthKeys, ErrAuthKeyStoreIdentityUnavailable)
	}
	effective.AuthType = authType
	effective.AuthKeys = store.effectiveAuthKeyStoreFingerprint()
	return effective, nil
}
