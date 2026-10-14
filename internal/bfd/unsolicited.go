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
	"net"
	"net/netip"
	"sync"
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

	// ErrUnsolicitedSubnetDenied indicates the source address is not in the
	// subnet of the local address that received the packet.
	ErrUnsolicitedSubnetDenied = errors.New("source address not in the receiving interface subnet")

	// ErrUnsolicitedRateLimited indicates the session admission rate is
	// exhausted.
	ErrUnsolicitedRateLimited = errors.New("unsolicited session admission rate exceeded")
)

// Admission rate for new unsolicited sessions (token bucket). RFC 9468
// Section 6.1 requires bounding resource use by unexpected remote systems
// but sets no rate; an active peer retransmits every second, so a refused
// peer is admitted on a later packet.
//
// ponytail: fixed global rate; make it configurable or per interface if a
// large IXP needs faster convergence after a restart.
const (
	unsolicitedAdmissionRate  = 10 // sessions per second
	unsolicitedAdmissionBurst = 20
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
	// Prevents resource exhaustion from excessive unsolicited peers
	// (RFC 9468 Section 6.1). A value below 1 admits no session.
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

	// AllowedPrefixes further restricts which source addresses can create
	// sessions (RFC 9468 Section 6.1). The RFC 9468 Section 2 subnet check
	// always applies: the source must belong to the subnet of the local
	// address that received the packet. Empty means any source in that
	// subnet.
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

	// connectedPrefix returns the subnet of a local interface address.
	connectedPrefix func(local netip.Addr) (netip.Prefix, bool)

	admitMu     sync.Mutex
	admitTokens float64
	admitLast   time.Time
}

// newUnsolicitedState creates a new unsolicited state tracker.
func newUnsolicitedState(policy *UnsolicitedPolicy) *unsolicitedState {
	return &unsolicitedState{
		policy:          policy,
		connectedPrefix: systemConnectedPrefix,
		admitTokens:     unsolicitedAdmissionBurst,
		admitLast:       time.Now(),
	}
}

func (us *unsolicitedState) checkInterfacePolicy(srcAddr netip.Addr, ifName string) error {
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

	return nil
}

// reserve admits a new unsolicited session for the packet described by meta
// and takes one slot of MaxSessions. Cheap policy checks run first; the
// admission rate limit runs before the interface-address lookup.
func (us *unsolicitedState) reserve(meta PacketMeta) error {
	if err := us.checkInterfacePolicy(meta.SrcAddr, meta.IfName); err != nil {
		return err
	}
	if int(us.sessionCount.Load()) >= us.policy.MaxSessions {
		return ErrUnsolicitedMaxSessions
	}
	if !us.allowAdmission() {
		return ErrUnsolicitedRateLimited
	}
	if !us.inConnectedSubnet(meta.SrcAddr, meta.DstAddr) {
		return ErrUnsolicitedSubnetDenied
	}

	for {
		current := us.sessionCount.Load()
		if int(current) >= us.policy.MaxSessions {
			return ErrUnsolicitedMaxSessions
		}
		if us.sessionCount.CompareAndSwap(current, current+1) {
			return nil
		}
	}
}

// release decrements the unsolicited session counter.
// Called when an unsolicited session is destroyed (cleanup on Down).
func (us *unsolicitedState) release() {
	for {
		current := us.sessionCount.Load()
		if current == 0 {
			return
		}
		if us.sessionCount.CompareAndSwap(current, current-1) {
			return
		}
	}
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

// allowAdmission takes one admission token, refilling the bucket at
// unsolicitedAdmissionRate per second up to unsolicitedAdmissionBurst.
func (us *unsolicitedState) allowAdmission() bool {
	us.admitMu.Lock()
	defer us.admitMu.Unlock()

	now := time.Now()
	us.admitTokens = min(unsolicitedAdmissionBurst,
		us.admitTokens+now.Sub(us.admitLast).Seconds()*unsolicitedAdmissionRate)
	us.admitLast = now
	if us.admitTokens < 1 {
		return false
	}
	us.admitTokens--
	return true
}

// inConnectedSubnet reports whether src belongs to the subnet of the local
// address local (RFC 9468 Section 2). An address the host does not own, an
// unnumbered receive path and a source equal to the local address fail
// closed.
func (us *unsolicitedState) inConnectedSubnet(src, local netip.Addr) bool {
	src = src.Unmap().WithZone("")
	local = local.Unmap().WithZone("")
	prefix, ok := us.connectedPrefix(local)
	return ok && src != local && prefix.Contains(src)
}

// systemConnectedPrefix returns the subnet of the host interface address
// equal to local.
func systemConnectedPrefix(local netip.Addr) (netip.Prefix, bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return netip.Prefix{}, false
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok || ip.Unmap() != local {
			continue
		}
		ones, _ := ipNet.Mask.Size()
		return netip.PrefixFrom(ip.Unmap(), ones).Masked(), true
	}
	return netip.Prefix{}, false
}
