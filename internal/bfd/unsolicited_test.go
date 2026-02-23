package bfd_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestUnsolicitedPolicyCheckDisabled(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{Enabled: false}
	state := newTestUnsolicitedState(&us)

	err := state.CheckPolicy(netip.MustParseAddr("10.0.0.1"), "eth0")
	if err == nil {
		t.Fatal("expected error for disabled policy")
	}
}

func TestUnsolicitedPolicyCheckInterfaceNotEnabled(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{
		Enabled:    true,
		Interfaces: map[string]bfd.UnsolicitedInterfaceConfig{},
	}
	state := newTestUnsolicitedState(&us)

	err := state.CheckPolicy(netip.MustParseAddr("10.0.0.1"), "eth0")
	if err == nil {
		t.Fatal("expected error for interface not in policy")
	}
}

func TestUnsolicitedPolicyCheckInterfaceDisabled(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{
		Enabled: true,
		Interfaces: map[string]bfd.UnsolicitedInterfaceConfig{
			"eth0": {Enabled: false},
		},
	}
	state := newTestUnsolicitedState(&us)

	err := state.CheckPolicy(netip.MustParseAddr("10.0.0.1"), "eth0")
	if err == nil {
		t.Fatal("expected error for disabled interface")
	}
}

func TestUnsolicitedPolicyCheckAllowedPrefix(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{
		Enabled: true,
		Interfaces: map[string]bfd.UnsolicitedInterfaceConfig{
			"eth0": {
				Enabled: true,
				AllowedPrefixes: []netip.Prefix{
					netip.MustParsePrefix("10.0.0.0/24"),
				},
			},
		},
		SessionDefaults: bfd.UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
	state := newTestUnsolicitedState(&us)

	// Allowed.
	if err := state.CheckPolicy(netip.MustParseAddr("10.0.0.1"), "eth0"); err != nil {
		t.Fatalf("unexpected error for allowed address: %v", err)
	}

	// Denied.
	if err := state.CheckPolicy(netip.MustParseAddr("192.168.1.1"), "eth0"); err == nil {
		t.Fatal("expected error for denied address")
	}
}

func TestUnsolicitedPolicyCheckNoPrefix(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{
		Enabled: true,
		Interfaces: map[string]bfd.UnsolicitedInterfaceConfig{
			"eth0": {
				Enabled:         true,
				AllowedPrefixes: nil, // no ACL — allow all
			},
		},
		SessionDefaults: bfd.UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
	state := newTestUnsolicitedState(&us)

	if err := state.CheckPolicy(netip.MustParseAddr("192.168.1.1"), "eth0"); err != nil {
		t.Fatalf("unexpected error with no prefix restriction: %v", err)
	}
}

func TestUnsolicitedPolicyCheckMaxSessions(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{
		Enabled:     true,
		MaxSessions: 2,
		Interfaces: map[string]bfd.UnsolicitedInterfaceConfig{
			"eth0": {Enabled: true},
		},
		SessionDefaults: bfd.UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
	state := newTestUnsolicitedState(&us)

	// First two should succeed.
	if err := state.CheckPolicy(netip.MustParseAddr("10.0.0.1"), "eth0"); err != nil {
		t.Fatalf("unexpected error for session 1: %v", err)
	}
	state.IncrementCount()

	if err := state.CheckPolicy(netip.MustParseAddr("10.0.0.2"), "eth0"); err != nil {
		t.Fatalf("unexpected error for session 2: %v", err)
	}
	state.IncrementCount()

	// Third should be denied.
	if err := state.CheckPolicy(netip.MustParseAddr("10.0.0.3"), "eth0"); err == nil {
		t.Fatal("expected error for session limit")
	}

	// After decrement, should succeed again.
	state.DecrementCount()
	if err := state.CheckPolicy(netip.MustParseAddr("10.0.0.3"), "eth0"); err != nil {
		t.Fatalf("unexpected error after decrement: %v", err)
	}
}

func TestUnsolicitedPolicyMultipleInterfaces(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{
		Enabled: true,
		Interfaces: map[string]bfd.UnsolicitedInterfaceConfig{
			"eth0": {
				Enabled: true,
				AllowedPrefixes: []netip.Prefix{
					netip.MustParsePrefix("10.0.0.0/24"),
				},
			},
			"eth1": {
				Enabled: true,
				AllowedPrefixes: []netip.Prefix{
					netip.MustParsePrefix("192.168.1.0/24"),
				},
			},
		},
		SessionDefaults: bfd.UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
	state := newTestUnsolicitedState(&us)

	// 10.0.0.1 allowed on eth0 but not eth1.
	if err := state.CheckPolicy(netip.MustParseAddr("10.0.0.1"), "eth0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := state.CheckPolicy(netip.MustParseAddr("10.0.0.1"), "eth1"); err == nil {
		t.Fatal("expected error for 10.0.0.1 on eth1")
	}

	// 192.168.1.1 allowed on eth1 but not eth0.
	if err := state.CheckPolicy(netip.MustParseAddr("192.168.1.1"), "eth1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := state.CheckPolicy(netip.MustParseAddr("192.168.1.1"), "eth0"); err == nil {
		t.Fatal("expected error for 192.168.1.1 on eth0")
	}
}

func TestUnsolicitedPolicyIPv6(t *testing.T) {
	t.Parallel()

	us := bfd.UnsolicitedPolicy{
		Enabled: true,
		Interfaces: map[string]bfd.UnsolicitedInterfaceConfig{
			"eth0": {
				Enabled: true,
				AllowedPrefixes: []netip.Prefix{
					netip.MustParsePrefix("2001:db8::/32"),
				},
			},
		},
		SessionDefaults: bfd.UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
	state := newTestUnsolicitedState(&us)

	if err := state.CheckPolicy(netip.MustParseAddr("2001:db8::1"), "eth0"); err != nil {
		t.Fatalf("unexpected error for IPv6 allowed: %v", err)
	}

	if err := state.CheckPolicy(netip.MustParseAddr("2001:db9::1"), "eth0"); err == nil {
		t.Fatal("expected error for IPv6 not in prefix")
	}
}

// testUnsolicitedState wraps the internal unsolicited state for testing.
// This adapts the unexported type through the public Manager API patterns.
type testUnsolicitedState struct {
	policy *bfd.UnsolicitedPolicy
	count  int
}

func newTestUnsolicitedState(policy *bfd.UnsolicitedPolicy) *testUnsolicitedState {
	return &testUnsolicitedState{policy: policy}
}

func (ts *testUnsolicitedState) CheckPolicy(srcAddr netip.Addr, ifName string) error {
	if !ts.policy.Enabled {
		return bfd.ErrUnsolicitedDisabled
	}

	ifCfg, ok := ts.policy.Interfaces[ifName]
	if !ok || !ifCfg.Enabled {
		return bfd.ErrUnsolicitedInterfaceNotEnabled
	}

	if len(ifCfg.AllowedPrefixes) > 0 {
		found := false
		for _, p := range ifCfg.AllowedPrefixes {
			if p.Contains(srcAddr) {
				found = true
				break
			}
		}
		if !found {
			return bfd.ErrUnsolicitedPrefixDenied
		}
	}

	if ts.policy.MaxSessions > 0 && ts.count >= ts.policy.MaxSessions {
		return bfd.ErrUnsolicitedMaxSessions
	}

	return nil
}

func (ts *testUnsolicitedState) IncrementCount() { ts.count++ }

func (ts *testUnsolicitedState) DecrementCount() {
	if ts.count > 0 {
		ts.count--
	}
}
