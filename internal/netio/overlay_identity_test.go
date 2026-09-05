package netio

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

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

	vxlan := scope
	vxlan.Kind = bfd.TransportScopeVXLAN
	vxlan.LocalMAC = [6]byte{0x02, 0, 0, 0, 0, 3}
	packet.srcMAC = vxlan.PeerMAC
	packet.dstMAC = innerDstMAC
	if got, want := receiveKeyForPacket(
		bfd.TransportScopeVXLAN, vxlan.OuterPeerAddr, vxlan.OuterLocalAddr, vxlan.VNI, packet,
	), receiveKeyForScope(vxlan); got != want {
		t.Fatalf("VXLAN receive key = %+v, want %+v", got, want)
	}
}

func TestOverlayScopesAllowSameVNIDistinctInnerTuples(t *testing.T) {
	first := bfd.TransportScope{
		Kind: bfd.TransportScopeGeneve, Owner: "geneve-config", Backend: "userspace-udp", VNI: 100,
		OuterPeerAddr: netip.MustParseAddr("198.51.100.1"), OuterLocalAddr: netip.MustParseAddr("198.51.100.2"),
		InnerPeerAddr: netip.MustParseAddr("192.0.2.1"), InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       [6]byte{0x02, 0, 0, 0, 0, 1}, LocalMAC: [6]byte{0x02, 0, 0, 0, 0, 2},
	}
	second := first
	second.InnerPeerAddr = netip.MustParseAddr("192.0.2.3")
	second.InnerLocalAddr = netip.MustParseAddr("192.0.2.4")
	second.PeerMAC[5] = 3
	second.LocalMAC[5] = 4
	if err := validateOverlayScopes(first.Kind, first.OuterLocalAddr, []bfd.TransportScope{first, second}); err != nil {
		t.Fatalf("validate distinct inner tuples: %v", err)
	}
}

func TestGeneveScopedListenerDemuxesSameVNIDistinctInnerTuples(t *testing.T) {
	local := netip.MustParseAddr("127.0.0.1")
	peer := netip.MustParseAddr("127.0.0.2")
	first := bfd.TransportScope{
		Kind: bfd.TransportScopeGeneve, Owner: "geneve-config", Backend: "userspace-udp", VNI: 100,
		OuterPeerAddr: peer, OuterLocalAddr: local,
		InnerPeerAddr: netip.MustParseAddr("192.0.2.1"), InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       [6]byte{0x02, 0, 0, 0, 0, 1}, LocalMAC: [6]byte{0x02, 0, 0, 0, 0, 2},
	}
	second := first
	second.InnerPeerAddr = netip.MustParseAddr("192.0.2.3")
	second.InnerLocalAddr = netip.MustParseAddr("192.0.2.4")
	second.PeerMAC[5] = 3
	second.LocalMAC[5] = 4

	conn, err := NewGeneveConnForScopes(local, 49152, []bfd.TransportScope{first, second}, slog.New(slog.DiscardHandler))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES) {
			t.Skipf("Geneve loopback socket unavailable: %v", err)
		}
		t.Fatalf("create scoped Geneve listener: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if deadlineErr := conn.conn.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
		t.Fatalf("set read deadline: %v", deadlineErr)
	}
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: peer.AsSlice()})
	if err != nil {
		t.Fatalf("open scoped Geneve sender: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	for _, want := range []bfd.TransportScope{first, second} {
		sendScopedGenevePacket(t, sender, local, want)
		_, meta, recvErr := conn.RecvDecapsulated(t.Context())
		if recvErr != nil {
			t.Fatalf("receive scope %+v: %v", want, recvErr)
		}
		if meta.TransportScope != want {
			t.Fatalf("received scope = %+v, want %+v", meta.TransportScope, want)
		}
	}

	mismatch := second
	mismatch.PeerMAC[5]++
	sendScopedGenevePacket(t, sender, local, mismatch)
	if _, _, err := conn.RecvDecapsulated(t.Context()); !errors.Is(err, ErrOverlayIdentityMismatch) {
		t.Fatalf("mismatched VAP identity error = %v, want ErrOverlayIdentityMismatch", err)
	}
}

func sendScopedGenevePacket(t *testing.T, sender *net.UDPConn, local netip.Addr, scope bfd.TransportScope) {
	t.Helper()
	payload := make([]byte, 24)
	packet := make([]byte, GeneveHeaderMinSize+InnerOverheadIPv4+len(payload))
	if _, err := MarshalGeneveHeader(packet[:GeneveHeaderMinSize], GeneveHeader{
		OBit: true, ProtocolType: GeneveProtocolEthernet, VNI: scope.VNI,
	}); err != nil {
		t.Fatalf("marshal Geneve header: %v", err)
	}
	if _, err := buildInnerPacketInto(
		packet[GeneveHeaderMinSize:], payload,
		scope.InnerPeerAddr, scope.InnerLocalAddr, 49152, scope.PeerMAC, scope.LocalMAC,
	); err != nil {
		t.Fatalf("build scoped inner packet: %v", err)
	}
	if _, err := sender.WriteToUDP(packet, net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, GenevePort))); err != nil {
		t.Fatalf("send scoped Geneve packet: %v", err)
	}
}
