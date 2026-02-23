// RFC 9468 — Unsolicited BFD for Sessionless Applications.
//
// Unsolicited BFD allows one endpoint to dynamically create passive BFD
// sessions in response to incoming BFD Control packets, without requiring
// per-session configuration on the passive side. This is useful for
// static route next-hop tracking and IXP route-server deployments.
//
// Security: unsolicited BFD is restricted to single-hop sessions only
// (RFC 5881) and requires source address validation against the
// receiving interface's subnet (RFC 9468 Section 2).

package bfd

import (
	"errors"
	"net/netip"
	"sync/atomic"
	"time"
)

// Sentinel errors for unsolicited BFD operations.
var (
	// ErrUnsolicitedDisabled indicates the unsolicited policy is not enabled.
	ErrUnsolicitedDisabled = errors.New("unsolicited BFD is disabled")

	// ErrUnsolicitedInterfaceNotEnabled indicates the interface is not enabled
	// for unsolicited BFD.
	ErrUnsolicitedInterfaceNotEnabled = errors.New("interface not enabled for unsolicited BFD")

	// ErrUnsolicitedPrefixDenied indicates the source address is not within
	// any allowed prefix for the receiving interface.
	ErrUnsolicitedPrefixDenied = errors.New("source address not in allowed prefix")

	// ErrUnsolicitedMaxSessions indicates the maximum number of unsolicited
	// sessions has been reached.
	ErrUnsolicitedMaxSessions = errors.New("unsolicited session limit reached")

	// ErrUnsolicitedMultihopDenied indicates an attempt to create an unsolicited
	// session for a multi-hop path, which is prohibited by RFC 9468.
	ErrUnsolicitedMultihopDenied = errors.New("unsolicited BFD is restricted to single-hop")
)

// UnsolicitedPolicy configures the passive side of RFC 9468 unsolicited BFD.
// When enabled, the Manager auto-creates passive sessions for incoming BFD
// packets from unknown peers, subject to per-interface policy.
type UnsolicitedPolicy struct {
	// Enabled controls whether unsolicited BFD is active.
	// RFC 9468 Section 2: "MUST be disabled by default."
	Enabled bool

	// Interfaces maps interface names to per-interface unsolicited config.
	// Only interfaces listed here accept unsolicited sessions.
	// RFC 9468 Section 6.1: "Limit the feature to specific interfaces."
	Interfaces map[string]UnsolicitedInterfaceConfig

	// MaxSessions is the global limit on dynamically created sessions.
	// Prevents resource exhaustion from excessive unsolicited peers.
	// Zero means no limit.
	MaxSessions int

	// SessionDefaults provides timer parameters for auto-created sessions.
	// RFC 9468 Section 2: "bfd.DesiredMinTxInterval,
	// bfd.RequiredMinRxInterval, bfd.DetectMult SHOULD be configurable."
	SessionDefaults UnsolicitedSessionDefaults

	// CleanupTimeout is how long to wait after a passive session goes Down
	// before deleting it. RFC 9468 Section 2: "the passive side SHOULD
	// delete the BFD session." Zero means delete immediately.
	CleanupTimeout time.Duration
}

// UnsolicitedInterfaceConfig holds per-interface unsolicited BFD settings.
type UnsolicitedInterfaceConfig struct {
	// Enabled controls unsolicited BFD on this specific interface.
	Enabled bool

	// AllowedPrefixes restricts which source addresses can create sessions.
	// RFC 9468 Section 2: source address MUST belong to the interface subnet.
	// RFC 9468 Section 6.1: apply policy from specific subnets/hosts.
	// Empty means allow any source on the interface subnet (interface
	// address validation still applies).
	AllowedPrefixes []netip.Prefix
}

// UnsolicitedSessionDefaults holds default timer parameters for
// dynamically created passive sessions.
type UnsolicitedSessionDefaults struct {
	DesiredMinTxInterval  time.Duration
	RequiredMinRxInterval time.Duration
	DetectMultiplier      uint8
}

// unsolicitedState tracks runtime state for unsolicited session management.
type unsolicitedState struct {
	policy       *UnsolicitedPolicy
	sessionCount atomic.Int32
}

// newUnsolicitedState creates a new unsolicited state tracker.
func newUnsolicitedState(policy *UnsolicitedPolicy) *unsolicitedState {
	return &unsolicitedState{
		policy: policy,
	}
}

// checkPolicy validates whether an unsolicited session can be created
// for the given source address on the given interface.
func (us *unsolicitedState) checkPolicy(srcAddr netip.Addr, ifName string) error {
	if us.policy == nil || !us.policy.Enabled {
		return ErrUnsolicitedDisabled
	}

	ifCfg, ok := us.policy.Interfaces[ifName]
	if !ok || !ifCfg.Enabled {
		return ErrUnsolicitedInterfaceNotEnabled
	}

	// RFC 9468 Section 6.1: apply subnet/host ACL.
	if len(ifCfg.AllowedPrefixes) > 0 {
		if !matchesAnyPrefix(srcAddr, ifCfg.AllowedPrefixes) {
			return ErrUnsolicitedPrefixDenied
		}
	}

	// Check global session limit.
	if us.policy.MaxSessions > 0 {
		current := int(us.sessionCount.Load())
		if current >= us.policy.MaxSessions {
			return ErrUnsolicitedMaxSessions
		}
	}

	return nil
}

// incrementCount increments the unsolicited session counter.
func (us *unsolicitedState) incrementCount() {
	us.sessionCount.Add(1)
}

// DecrementCount decrements the unsolicited session counter.
// Called when an unsolicited session is destroyed (cleanup on Down).
func (us *unsolicitedState) DecrementCount() {
	us.sessionCount.Add(-1)
}

// matchesAnyPrefix reports whether addr is contained in any of the given prefixes.
func matchesAnyPrefix(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
