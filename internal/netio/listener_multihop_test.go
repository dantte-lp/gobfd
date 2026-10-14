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
