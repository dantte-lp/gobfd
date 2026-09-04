package netio

import (
	"context"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type identityOverlayConn struct {
	scope bfd.TransportScope
}

func (*identityOverlayConn) SendEncapsulated(context.Context, []byte, netip.Addr) error {
	return nil
}

func (c *identityOverlayConn) SendEncapsulatedFor(
	_ context.Context,
	_ []byte,
	scope bfd.TransportScope,
) error {
	c.scope = scope
	return nil
}

func (*identityOverlayConn) RecvDecapsulated(context.Context) ([]byte, OverlayMeta, error) {
	return nil, OverlayMeta{}, ErrOverlayRecvClosed
}

func (*identityOverlayConn) Close() error { return nil }

func TestOverlaySenderUsesExactSessionScope(t *testing.T) {
	t.Parallel()

	want := bfd.TransportScope{
		Kind: bfd.TransportScopeGeneve, VNI: 200,
		OuterPeerAddr:  netip.MustParseAddr("198.51.100.1"),
		OuterLocalAddr: netip.MustParseAddr("198.51.100.2"),
		InnerPeerAddr:  netip.MustParseAddr("192.0.2.1"),
		InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
		AddressFamily:  bfd.AddressFamilyIPv4,
		PeerMAC:        [6]byte{0x02, 0, 0, 0, 0, 1},
		LocalMAC:       [6]byte{0x02, 0, 0, 0, 0, 2},
	}
	conn := &identityOverlayConn{}
	sender := NewOverlaySender(conn, want)
	if err := sender.SendPacket(context.Background(), []byte{1, 2, 3}, want.InnerPeerAddr); err != nil {
		t.Fatalf("SendPacket: %v", err)
	}
	if conn.scope != want {
		t.Fatalf("sent scope = %+v, want %+v", conn.scope, want)
	}
}

func TestFormatAReceiveKeyIncludesCompleteWireTuple(t *testing.T) {
	t.Parallel()

	scope := bfd.TransportScope{
		Kind: bfd.TransportScopeGeneve, VNI: 200,
		OuterPeerAddr:  netip.MustParseAddr("198.51.100.1"),
		OuterLocalAddr: netip.MustParseAddr("198.51.100.2"),
		InnerPeerAddr:  netip.MustParseAddr("192.0.2.1"),
		InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
		AddressFamily:  bfd.AddressFamilyIPv4,
		PeerMAC:        [6]byte{0x02, 0, 0, 0, 0, 1},
		LocalMAC:       [6]byte{0x02, 0, 0, 0, 0, 2},
	}
	packet := innerPacket{
		srcIP: scope.InnerPeerAddr, dstIP: scope.InnerLocalAddr,
		srcMAC: scope.PeerMAC, dstMAC: scope.LocalMAC,
	}
	want := receiveKeyForScope(scope)
	got := receiveKeyForPacket(
		bfd.TransportScopeGeneve, scope.OuterPeerAddr, scope.OuterLocalAddr, scope.VNI, packet,
	)
	if got != want {
		t.Fatalf("receive key = %+v, want %+v", got, want)
	}

	packet.srcMAC[5]++
	if changed := receiveKeyForPacket(
		bfd.TransportScopeGeneve, scope.OuterPeerAddr, scope.OuterLocalAddr, scope.VNI, packet,
	); changed == want {
		t.Fatal("source MAC change did not change receive identity")
	}
}
