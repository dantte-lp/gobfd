package bfd

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

// connectedTo returns a connected-prefix lookup for a host that owns the
// given interface addresses, in "address/length" form.
func connectedTo(ifaceAddrs ...string) func(netip.Addr) []netip.Prefix {
	return func(local netip.Addr) []netip.Prefix {
		var prefixes []netip.Prefix
		for _, s := range ifaceAddrs {
			p := netip.MustParsePrefix(s)
			if p.Addr() == local {
				prefixes = append(prefixes, p.Masked())
			}
		}
		return prefixes
	}
}

func admissionPolicy(maxSessions int, prefixes ...string) *UnsolicitedPolicy {
	allowed := make([]netip.Prefix, 0, len(prefixes))
	for _, s := range prefixes {
		allowed = append(allowed, netip.MustParsePrefix(s))
	}
	return &UnsolicitedPolicy{
		Enabled:     true,
		MaxSessions: maxSessions,
		Interfaces: map[string]UnsolicitedInterfaceConfig{
			"eth0": {Enabled: true, AllowedPrefixes: allowed},
		},
		SessionDefaults: UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
}

func admissionMeta(src, dst string) PacketMeta {
	return PacketMeta{
		SrcAddr: netip.MustParseAddr(src),
		DstAddr: netip.MustParseAddr(dst),
		IfName:  "eth0",
		TTL:     255,
	}
}

// RFC 9468 Section 2: on a numbered interface the source address MUST
// belong to the subnet of the receiving interface, else the packet MUST NOT
// be processed. The check applies with and without allowed_prefixes.
func TestUnsolicitedReserveEnforcesConnectedSubnet(t *testing.T) {
	t.Parallel()

	connected := connectedTo("192.0.2.1/24", "2001:db8:1::1/64", "fe80::1/64")
	tests := []struct {
		name     string
		prefixes []string
		src, dst string
		wantErr  error
	}{
		{"IPv4 in subnet, no prefixes", nil, "192.0.2.10", "192.0.2.1", nil},
		{"IPv4 off subnet, no prefixes", nil, "198.51.100.10", "192.0.2.1", ErrUnsolicitedSubnetDenied},
		{"IPv4 off subnet, wider prefix", []string{"0.0.0.0/0"}, "198.51.100.10", "192.0.2.1", ErrUnsolicitedSubnetDenied},
		{"IPv4 in subnet, outside prefix", []string{"192.0.2.128/25"}, "192.0.2.10", "192.0.2.1", ErrUnsolicitedPrefixDenied},
		{"IPv4 in subnet and prefix", []string{"192.0.2.0/25"}, "192.0.2.10", "192.0.2.1", nil},
		{"IPv6 in subnet", nil, "2001:db8:1::10", "2001:db8:1::1", nil},
		{"IPv6 off subnet", nil, "2001:db8:2::10", "2001:db8:1::1", ErrUnsolicitedSubnetDenied},
		{"IPv6 link-local", nil, "fe80::10", "fe80::1", nil},
		{"destination not a local address", nil, "192.0.2.10", "192.0.2.2", ErrUnsolicitedSubnetDenied},
		{"source equals local address", nil, "192.0.2.1", "192.0.2.1", ErrUnsolicitedSubnetDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			us := newUnsolicitedState(admissionPolicy(10, tt.prefixes...))
			us.connectedPrefix = connected
			err := us.reserve(admissionMeta(tt.src, tt.dst))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("reserve error = %v, want %v", err, tt.wantErr)
			}
			wantCount := int32(0)
			if tt.wantErr == nil {
				wantCount = 1
			}
			if got := us.sessionCount.Load(); got != wantCount {
				t.Fatalf("session count = %d, want %d", got, wantCount)
			}
		})
	}
}

// RFC 9468 Section 6.1 requires bounded resource use; a nonpositive
// MaxSessions is no longer "unlimited".
func TestUnsolicitedReserveRequiresSessionLimit(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, -1} {
		us := newUnsolicitedState(admissionPolicy(limit))
		us.connectedPrefix = connectedTo("192.0.2.1/24")
		if err := us.reserve(admissionMeta("192.0.2.10", "192.0.2.1")); !errors.Is(err, ErrUnsolicitedMaxSessions) {
			t.Fatalf("MaxSessions=%d: reserve error = %v, want ErrUnsolicitedMaxSessions", limit, err)
		}
	}
}

// Admission is rate limited before the connected-subnet lookup, so a flood
// of packets from unknown peers cannot create sessions or interface-address
// lookups faster than the configured rate.
func TestUnsolicitedReserveRateLimitsAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		us := newUnsolicitedState(admissionPolicy(1000))
		lookups := 0
		us.connectedPrefix = func(local netip.Addr) []netip.Prefix {
			lookups++
			return connectedTo("192.0.2.1/16")(local)
		}

		admit := func(i int) error {
			src := netip.AddrFrom4([4]byte{192, 0, byte(3 + i/250), byte(1 + i%250)})
			return us.reserve(PacketMeta{SrcAddr: src, DstAddr: netip.MustParseAddr("192.0.2.1"), IfName: "eth0", TTL: 255})
		}

		for i := range unsolicitedAdmissionBurst {
			if err := admit(i); err != nil {
				t.Fatalf("admission %d within burst: %v", i, err)
			}
		}
		if err := admit(unsolicitedAdmissionBurst); !errors.Is(err, ErrUnsolicitedRateLimited) {
			t.Fatalf("admission beyond burst: error = %v, want ErrUnsolicitedRateLimited", err)
		}
		if lookups != unsolicitedAdmissionBurst {
			t.Fatalf("connected-prefix lookups = %d, want %d", lookups, unsolicitedAdmissionBurst)
		}

		time.Sleep(time.Second)
		for i := range unsolicitedAdmissionRate {
			if err := admit(1 + unsolicitedAdmissionBurst + i); err != nil {
				t.Fatalf("admission %d after one second: %v", i, err)
			}
		}
		if err := admit(500); !errors.Is(err, ErrUnsolicitedRateLimited) {
			t.Fatalf("admission beyond refill: error = %v, want ErrUnsolicitedRateLimited", err)
		}
	})
}

// The system lookup resolves loopback addresses on every Linux host.
func TestSystemConnectedPrefixLoopback(t *testing.T) {
	t.Parallel()

	prefixes := systemConnectedPrefixes(netip.MustParseAddr("127.0.0.1"))
	if len(prefixes) == 0 || !prefixes[0].Contains(netip.MustParseAddr("127.0.0.2")) {
		t.Fatalf("systemConnectedPrefixes(127.0.0.1) = %v; want a prefix containing 127.0.0.2", prefixes)
	}
	if got := systemConnectedPrefixes(netip.MustParseAddr("192.0.2.123")); len(got) != 0 {
		t.Fatalf("systemConnectedPrefixes found %v for an address the host does not own", got)
	}
}

type recordingLeaseSender struct{ senderLeaseTestSender }

// C-07: an unsolicited session gets its own sender for the local address
// and interface that received the triggering packet, not a shared wildcard
// sender, and its lease closes exactly once with the session.
func TestUnsolicitedSessionSenderUsesReceivedTuple(t *testing.T) {
	t.Parallel()

	var (
		got    []PacketMeta
		closes int
	)
	mgr := NewManager(
		slog.New(slog.DiscardHandler),
		WithUnsolicitedPolicy(admissionPolicy(1)),
		WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("2001:db8:1::/64")),
		WithUnsolicitedSessionSender(func(meta PacketMeta) (*SenderLease, error) {
			got = append(got, meta)
			return NewSenderLease(recordingLeaseSender{}, func() error {
				closes++
				return nil
			}), nil
		}),
	)

	meta := admissionMeta("2001:db8:1::10", "2001:db8:1::1")
	if err := mgr.tryCreateUnsolicited(ownershipDownPacket(41), meta, nil); err != nil {
		t.Fatalf("tryCreateUnsolicited: %v", err)
	}
	if len(got) != 1 || got[0].DstAddr != meta.DstAddr || got[0].IfName != meta.IfName {
		t.Fatalf("sender requested for %+v, want local %s on %s", got, meta.DstAddr, meta.IfName)
	}

	mgr.Close()
	if closes != 1 {
		t.Fatalf("sender lease closes = %d, want 1", closes)
	}
}

// RFC 9468 Section 6.1: unsolicited BFD MUST be limited to single-hop
// sessions with the RFC 5082 procedures, so only TTL 255 packets from a
// single-hop listener create sessions. The check runs before the admission
// rate limit, so off-link traffic cannot spend admission tokens.
func TestUnsolicitedReserveSingleHopOnly(t *testing.T) {
	t.Parallel()

	us := newUnsolicitedState(admissionPolicy(1000))
	us.connectedPrefix = connectedTo("192.0.2.1/24")

	ttl254 := admissionMeta("192.0.2.10", "192.0.2.1")
	ttl254.TTL = 254
	multiHop := admissionMeta("192.0.2.11", "192.0.2.1")
	multiHop.MultiHop = true
	for range unsolicitedAdmissionBurst + 5 {
		for _, meta := range []PacketMeta{ttl254, multiHop} {
			if err := us.reserve(meta); !errors.Is(err, ErrUnsolicitedMultihopDenied) {
				t.Fatalf("reserve(TTL %d, multihop %t) error = %v, want ErrUnsolicitedMultihopDenied",
					meta.TTL, meta.MultiHop, err)
			}
		}
	}
	if err := us.reserve(admissionMeta("192.0.2.12", "192.0.2.1")); err != nil {
		t.Fatalf("single-hop reserve after denied multi-hop traffic: %v", err)
	}
}

// A host can own one address with several prefixes, such as an anycast /32
// on lo and a /24 on the uplink; any matching subnet admits the source.
func TestUnsolicitedReserveDuplicateLocalAddress(t *testing.T) {
	t.Parallel()

	us := newUnsolicitedState(admissionPolicy(10))
	us.connectedPrefix = connectedTo("192.0.2.1/32", "192.0.2.1/24")
	if err := us.reserve(admissionMeta("192.0.2.10", "192.0.2.1")); err != nil {
		t.Fatalf("reserve with /32 and /24 on the local address: %v", err)
	}
}
