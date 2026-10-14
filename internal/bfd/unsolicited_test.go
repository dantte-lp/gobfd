package bfd

import (
	"errors"
	"net/netip"
	"testing"
)

// RFC 9468 Section 2: passive unsolicited BFD MUST be disabled by default;
// Section 6.1: limit the feature to specific interfaces.
func TestUnsolicitedReserveInterfacePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  *UnsolicitedPolicy
		ifName  string
		wantErr error
	}{
		{"policy disabled", &UnsolicitedPolicy{MaxSessions: 1}, "eth0", ErrUnsolicitedDisabled},
		{
			"interface not listed", &UnsolicitedPolicy{Enabled: true, MaxSessions: 1},
			"eth0", ErrUnsolicitedInterfaceNotEnabled,
		},
		{
			"interface disabled",
			&UnsolicitedPolicy{
				Enabled: true, MaxSessions: 1,
				Interfaces: map[string]UnsolicitedInterfaceConfig{"eth0": {}},
			},
			"eth0", ErrUnsolicitedInterfaceNotEnabled,
		},
		{"interface enabled", admissionPolicy(1), "eth0", nil},
		{"other interface", admissionPolicy(1), "eth1", ErrUnsolicitedInterfaceNotEnabled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			us := newUnsolicitedState(tt.policy)
			us.connectedPrefix = connectedTo("192.0.2.1/24")
			meta := admissionMeta("192.0.2.10", "192.0.2.1")
			meta.IfName = tt.ifName
			if err := us.reserve(meta); !errors.Is(err, tt.wantErr) {
				t.Fatalf("reserve error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// Allowed prefixes are per interface.
func TestUnsolicitedReservePrefixesPerInterface(t *testing.T) {
	t.Parallel()

	policy := &UnsolicitedPolicy{
		Enabled:     true,
		MaxSessions: 10,
		Interfaces: map[string]UnsolicitedInterfaceConfig{
			"eth0": {Enabled: true, AllowedPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}},
			"eth1": {Enabled: true, AllowedPrefixes: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}},
		},
	}
	us := newUnsolicitedState(policy)
	us.connectedPrefix = connectedTo("192.0.2.1/24", "198.51.100.1/24")

	for _, tc := range []struct {
		src, dst, ifName string
		wantErr          error
	}{
		{"192.0.2.10", "192.0.2.1", "eth0", nil},
		{"198.51.100.10", "198.51.100.1", "eth1", nil},
		{"198.51.100.11", "198.51.100.1", "eth0", ErrUnsolicitedPrefixDenied},
		{"192.0.2.11", "192.0.2.1", "eth1", ErrUnsolicitedPrefixDenied},
	} {
		meta := admissionMeta(tc.src, tc.dst)
		meta.IfName = tc.ifName
		if err := us.reserve(meta); !errors.Is(err, tc.wantErr) {
			t.Fatalf("reserve(%s on %s) error = %v, want %v", tc.src, tc.ifName, err, tc.wantErr)
		}
	}
}

// MaxSessions bounds reservations, and release returns a slot.
func TestUnsolicitedReserveSessionLimitAndRelease(t *testing.T) {
	t.Parallel()

	us := newUnsolicitedState(admissionPolicy(2))
	us.connectedPrefix = connectedTo("192.0.2.1/24")

	for _, src := range []string{"192.0.2.10", "192.0.2.11"} {
		if err := us.reserve(admissionMeta(src, "192.0.2.1")); err != nil {
			t.Fatalf("reserve(%s): %v", src, err)
		}
	}
	if err := us.reserve(admissionMeta("192.0.2.12", "192.0.2.1")); !errors.Is(err, ErrUnsolicitedMaxSessions) {
		t.Fatalf("reserve beyond limit error = %v, want ErrUnsolicitedMaxSessions", err)
	}

	us.release()
	if err := us.reserve(admissionMeta("192.0.2.12", "192.0.2.1")); err != nil {
		t.Fatalf("reserve after release: %v", err)
	}
	us.release()
	us.release()
	us.release()
	if got := us.sessionCount.Load(); got != 0 {
		t.Fatalf("session count after extra releases = %d, want 0", got)
	}
}
