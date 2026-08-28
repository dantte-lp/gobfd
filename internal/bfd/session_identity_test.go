package bfd

import (
	"net/netip"
	"testing"
)

func TestSessionKeyCanonicalIdentityDimensions(t *testing.T) {
	base := SessionConfig{
		PeerAddr:  netip.MustParseAddr("192.0.2.1"),
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
		Interface: "eth0",
		Type:      SessionTypeSingleHop,
	}
	baseKey, err := sessionKeyFromConfig(base)
	if err != nil {
		t.Fatalf("sessionKeyFromConfig(base): %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*SessionConfig)
	}{
		{
			name: "session type",
			mutate: func(cfg *SessionConfig) {
				cfg.Type = SessionTypeMultiHop
			},
		},
		{
			name: "address family",
			mutate: func(cfg *SessionConfig) {
				cfg.PeerAddr = netip.MustParseAddr("2001:db8::1")
				cfg.LocalAddr = netip.MustParseAddr("2001:db8::2")
			},
		},
		{
			name: "network scope",
			mutate: func(cfg *SessionConfig) {
				cfg.NetworkScope = "blue"
			},
		},
		{
			name: "transport scope",
			mutate: func(cfg *SessionConfig) {
				cfg.TransportScope = TransportScope{
					Kind:    TransportScopeVXLAN,
					Owner:   "overlay-a",
					Backend: "userspace-udp",
					VNI:     100,
				}
			},
		},
	}

	seen := map[SessionKey]struct{}{baseKey: {}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			key, keyErr := sessionKeyFromConfig(cfg)
			if keyErr != nil {
				t.Fatalf("sessionKeyFromConfig: %v", keyErr)
			}
			if key == baseKey {
				t.Errorf("canonical key did not distinguish %s", tt.name)
			}
			seen[key] = struct{}{}
		})
	}

	if got, want := len(seen), len(tests)+1; got != want {
		t.Errorf("distinct comparable keys = %d, want %d", got, want)
	}
}

func TestSessionKeyCanonicalizesMappedIPv4(t *testing.T) {
	t.Parallel()

	mapped, err := sessionKeyFromConfig(SessionConfig{
		PeerAddr:  netip.MustParseAddr("::ffff:192.0.2.1"),
		LocalAddr: netip.MustParseAddr("::ffff:192.0.2.2"),
		Interface: "eth0",
		Type:      SessionTypeSingleHop,
	})
	if err != nil {
		t.Fatalf("sessionKeyFromConfig(mapped): %v", err)
	}
	unmapped, err := sessionKeyFromConfig(SessionConfig{
		PeerAddr:  netip.MustParseAddr("192.0.2.1"),
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
		Interface: "eth0",
		Type:      SessionTypeSingleHop,
	})
	if err != nil {
		t.Fatalf("sessionKeyFromConfig(unmapped): %v", err)
	}

	if mapped != unmapped {
		t.Errorf("mapped key = %+v, want canonical %+v", mapped, unmapped)
	}
	if mapped.AddressFamily != AddressFamilyIPv4 {
		t.Errorf("mapped address family = %d, want IPv4", mapped.AddressFamily)
	}
}

func TestSessionOwnersAreTypedComparableClaims(t *testing.T) {
	t.Parallel()

	claims := map[SessionOwner]struct{}{
		ConfigReconciliationOwner():    {},
		MicroBFDReconciliationOwner():  {},
		VXLANReconciliationOwner():     {},
		GeneveReconciliationOwner():    {},
		compatibilityAPISessionOwner(): {},
		unsolicitedSessionOwner():      {},
	}
	if got := len(claims); got != 6 {
		t.Fatalf("typed owner claims = %d, want 6", got)
	}
	if configSessionOwner().Source != SessionOwnerSourceConfig {
		t.Errorf("config owner source = %d, want %d",
			configSessionOwner().Source, SessionOwnerSourceConfig)
	}
	if compatibilityAPISessionOwner().Source != SessionOwnerSourceCompatibilityAPI {
		t.Errorf("compatibility owner source = %d, want %d",
			compatibilityAPISessionOwner().Source, SessionOwnerSourceCompatibilityAPI)
	}
	if unsolicitedSessionOwner().Source != SessionOwnerSourceUnsolicited {
		t.Errorf("unsolicited owner source = %d, want %d",
			unsolicitedSessionOwner().Source, SessionOwnerSourceUnsolicited)
	}
	declarative := []struct {
		owner SessionOwner
		want  SessionOwnerSource
	}{
		{MicroBFDReconciliationOwner(), SessionOwnerSourceMicroBFD},
		{VXLANReconciliationOwner(), SessionOwnerSourceVXLAN},
		{GeneveReconciliationOwner(), SessionOwnerSourceGeneve},
	}
	for _, tt := range declarative {
		if tt.owner.Source != tt.want {
			t.Errorf("declarative owner %q source = %d, want %d", tt.owner.ID, tt.owner.Source, tt.want)
		}
	}
}
