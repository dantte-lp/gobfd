package netio

import (
	"log/slog"
	"net"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// BenchmarkScopedVXLANDecap measures one scoped VXLAN decapsulation, the
// identity lookup included; it must stay allocation-free.
func BenchmarkScopedVXLANDecap(b *testing.B) {
	local, peer := netip.MustParseAddr("127.0.56.1"), netip.MustParseAddr("127.0.56.2")
	scope := bfd.TransportScope{
		Kind: bfd.TransportScopeVXLAN, Owner: "vxlan-config", Backend: "userspace-udp", VNI: 7,
		OuterPeerAddr: peer, OuterLocalAddr: local,
		InnerPeerAddr: netip.MustParseAddr("192.0.2.1"), InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       [6]byte{0x02, 0, 0, 0, 0, 1}, LocalMAC: [6]byte{0x02, 0, 0, 0, 0, 2},
	}
	conn, err := NewVXLANConnForScopes(local, 49152, []bfd.TransportScope{scope}, slog.New(slog.DiscardHandler))
	if err != nil {
		b.Skipf("VXLAN loopback socket unavailable: %v", err)
	}
	b.Cleanup(func() { _ = conn.Close() })
	payload := make([]byte, 24)
	packet := make([]byte, VXLANHeaderSize+InnerOverheadIPv4+len(payload))
	if _, err := MarshalVXLANHeader(packet[:VXLANHeaderSize], scope.VNI); err != nil {
		b.Fatal(err)
	}
	if _, err := buildInnerPacketInto(packet[VXLANHeaderSize:], payload,
		scope.InnerPeerAddr, scope.InnerLocalAddr, 49152, scope.PeerMAC, innerDstMAC); err != nil {
		b.Fatal(err)
	}
	remote := net.UDPAddrFromAddrPort(netip.AddrPortFrom(peer, VXLANPort))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := conn.decapVXLANDatagram(packet, remote); err != nil {
			b.Fatal(err)
		}
	}
}
