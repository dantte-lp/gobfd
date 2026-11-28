//go:build linux

package bfdload

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
)

func TestLoopbackTopologyPreservesMixedReferenceIdentities(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("reference")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoopbackConfig(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sessions) != 1000 || cfg.Profile.Sessions != 1000 || cfg.SourcePortBase != 49152 {
		t.Fatalf("topology silently shrank qualification inputs: %+v", cfg.Profile)
	}
	seen := make(map[netip.Addr]bool)
	ipv4 := 0
	for _, endpoint := range cfg.Sessions {
		if seen[endpoint.Peer.Addr()] || endpoint.Local == endpoint.Peer || endpoint.Interface != "lo" ||
			endpoint.Local.Port() != 3784 || endpoint.Peer.Port() != 3784 {
			t.Fatalf("duplicate, self-reflecting, unbound or non-RFC identity: %+v", endpoint)
		}
		seen[endpoint.Peer.Addr()] = true
		if endpoint.Peer.Addr().Is4() {
			ipv4++
		}
	}
	if ipv4 != 500 || cfg.Validate() != nil {
		t.Fatalf("mixed identity balance or wire config invalid: IPv4=%d", ipv4)
	}
}

func TestLoopbackSetupRefusesHostNamespaceBeforeWrites(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoopbackConfig(profile)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := NetworkNamespaceID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareLoopback(t.Context(), cfg, namespace); !errors.Is(err, ErrTopology) {
		t.Fatalf("current host namespace accepted for mutation: %v", err)
	}
	lo := net.Interface{Name: "lo", Index: 1, Flags: net.FlagLoopback | net.FlagUp}
	for _, tc := range []struct {
		current, host uint64
		interfaces    []net.Interface
	}{
		{7, 7, []net.Interface{lo}},
		{7, 0, []net.Interface{lo}},
		{0, 7, []net.Interface{lo}},
		{7, 8, nil},
		{7, 8, []net.Interface{lo, {Name: "eth0", Index: 2}}},
	} {
		if err := validateLoopbackNamespace(tc.current, tc.host, tc.interfaces); !errors.Is(err, ErrTopology) {
			t.Fatalf("unisolated namespace accepted: %+v: %v", tc, err)
		}
	}
}

func TestAddressRequestMatchesKernelABI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		address string
		family  byte
		prefix  byte
		flags   byte
		length  uint32
		attrs   []uint16
	}{
		{"10.253.0.1", 2, 32, 0, 40, []uint16{2, 1}},
		{"fd00:676f:6266::1", 10, 128, 2, 44, []uint16{1}},
	} {
		t.Run(tc.address, func(t *testing.T) {
			t.Parallel()
			wire, err := addressRequest(1, 7, netip.MustParseAddr(tc.address))
			if err != nil {
				t.Fatal(err)
			}
			messages, err := syscall.ParseNetlinkMessage(wire)
			if err != nil || len(messages) != 1 {
				t.Fatalf("invalid kernel message: %x: %v", wire, err)
			}
			message := messages[0]
			if message.Header.Len != tc.length || message.Header.Type != 20 || message.Header.Flags != 0x605 ||
				message.Header.Seq != 1 || message.Header.Pid != 0 || message.Data[0] != tc.family ||
				message.Data[1] != tc.prefix || message.Data[2] != tc.flags || message.Data[3] != 0 ||
				binary.NativeEndian.Uint32(message.Data[4:8]) != 7 {
				t.Fatalf("incorrect RTM_NEWADDR fields: %x", wire)
			}
			attrs, err := syscall.ParseNetlinkRouteAttr(&message)
			if err != nil || len(attrs) != len(tc.attrs) {
				t.Fatalf("invalid address attributes: %+v: %v", attrs, err)
			}
			for i, attribute := range attrs {
				got, valid := netip.AddrFromSlice(attribute.Value)
				if !valid || got.String() != tc.address || attribute.Attr.Type != tc.attrs[i] {
					t.Fatalf("wrong local/address attribute: %+v", attribute)
				}
			}
		})
	}
	for _, addr := range []netip.Addr{{}, netip.MustParseAddr("::ffff:192.0.2.1"), netip.MustParseAddr("224.0.0.1")} {
		if _, err := addressRequest(1, 7, addr); !errors.Is(err, ErrTopology) {
			t.Fatalf("invalid address emitted to kernel: %v: %v", addr, err)
		}
	}
	if _, err := addressRequest(1, 0, netip.MustParseAddr("192.0.2.1")); !errors.Is(err, ErrTopology) {
		t.Fatalf("zero interface index emitted to kernel: %v", err)
	}
}
