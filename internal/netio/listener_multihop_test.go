package netio_test

import (
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// The listener marks packets from a multi-hop (RFC 5883, port 4784) socket,
// so RFC 9468 unsolicited admission can refuse them (Section 6.1).
func TestListenerMarksMultiHopPackets(t *testing.T) {
	t.Parallel()

	for _, multiHop := range []bool{false, true} {
		mock := NewMockPacketConn(netip.MustParseAddrPort("192.0.2.1:3784"))
		mock.ReadFunc = func([]byte) (int, netio.PacketMeta, error) {
			return 24, netio.PacketMeta{
				SrcAddr: netip.MustParseAddr("192.0.2.10"),
				DstAddr: netip.MustParseAddr("192.0.2.1"),
				TTL:     255,
			}, nil
		}
		pkt, err := netio.NewListenerFromConn(mock, multiHop).RecvPacket(t.Context())
		if err != nil {
			t.Fatalf("multiHop=%t: RecvPacket: %v", multiHop, err)
		}
		if pkt.Meta.MultiHop != multiHop {
			t.Fatalf("multiHop=%t: Meta.MultiHop = %t", multiHop, pkt.Meta.MultiHop)
		}
		pkt.Release()
	}
}

// RFC 5883 defines no receive TTL rule, so the multihop listener passes every
// TTL to the demultiplexed session's hop policy (RFC 5082 Section 3). The
// single-hop listener keeps its RFC 5881 Section 5 TTL 255 pre-filter.
func TestMultiHopListenerDefersTTLToSession(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		src  netip.Addr
	}{
		{name: "ipv4", src: netip.MustParseAddr("192.0.2.10")},
		{name: "ipv6", src: netip.MustParseAddr("2001:db8::10")},
	} {
		for _, ttl := range []uint8{255, 254, 250, 1} {
			mock := NewMockPacketConn(netip.MustParseAddrPort("192.0.2.1:4784"))
			mock.ReadFunc = func([]byte) (int, netio.PacketMeta, error) {
				return 24, netio.PacketMeta{SrcAddr: tc.src, TTL: ttl}, nil
			}
			pkt, err := netio.NewListenerFromConn(mock, true).RecvPacket(t.Context())
			if err != nil {
				t.Fatalf("%s TTL %d: RecvPacket: %v", tc.name, ttl, err)
			}
			if pkt.Meta.TTL != ttl || !pkt.Meta.MultiHop {
				t.Fatalf("%s TTL %d: meta = %+v", tc.name, ttl, pkt.Meta)
			}
			pkt.Release()
		}
	}
}
