//go:build e2e_overlay

package overlay_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// Run in an isolated Linux network namespace: both peers bind the standard
// tunnel ports. These are actual userspace UDP packets, not vendor interop.
func TestOverlayUserspaceWire(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("wire qualification requires an isolated Linux network namespace")
	}
	for _, test := range []struct {
		name string
		kind bfd.TransportScopeKind
	}{
		{name: "vxlan", kind: bfd.TransportScopeVXLAN},
		{name: "geneve", kind: bfd.TransportScopeGeneve},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind := test.kind
			scopes := wireScopes(kind)
			peerScopes := []bfd.TransportScope{reverseWireScope(scopes[0]), reverseWireScope(scopes[1])}
			local := openWireOverlay(t, scopes)
			peer := openWireOverlay(t, peerScopes)
			payload := []byte{
				0x20, 0x40, 0x03, 0x18, 0, 0, 0, 1, 0, 0, 0, 0,
				0, 0x0f, 0x42, 0x40, 0, 0x0f, 0x42, 0x40, 0, 0, 0, 0,
			}
			var discriminator uint32
			if kind == bfd.TransportScopeGeneve {
				discriminator = 1000
			}
			for i, scope := range scopes {
				discriminator++
				binary.BigEndian.PutUint32(payload[4:8], discriminator)
				sendReceiveWire(t, peer, local, peerScopes[i], scope, payload)
				discriminator++
				binary.BigEndian.PutUint32(payload[4:8], discriminator)
				sendReceiveWire(t, local, peer, scope, peerScopes[i], payload)
			}
			rejectWirePackets(t, local, scopes[0], payload)
			checkWireCancellation(t, local)
			// Rejection and cancellation must not poison either configured session.
			for i, scope := range scopes {
				discriminator++
				binary.BigEndian.PutUint32(payload[4:8], discriminator)
				sendReceiveWire(t, peer, local, peerScopes[i], scope, payload)
			}
			checkWireClose(t, local, scopes[0], payload)
		})
	}
}

func wireScopes(kind bfd.TransportScopeKind) []bfd.TransportScope {
	first := bfd.TransportScope{
		Kind: kind, Owner: "wire-qualification", Backend: "userspace-udp", VNI: 100,
		OuterLocalAddr: netip.MustParseAddr("127.0.0.1"), OuterPeerAddr: netip.MustParseAddr("127.0.0.2"),
		InnerLocalAddr: netip.MustParseAddr("192.0.2.2"), InnerPeerAddr: netip.MustParseAddr("192.0.2.1"),
		AddressFamily: bfd.AddressFamilyIPv4,
		LocalMAC:      [6]byte{0x02, 0, 0, 0, 0, 2}, PeerMAC: [6]byte{0x02, 0, 0, 0, 0, 1},
	}
	second := first
	second.InnerLocalAddr = netip.MustParseAddr("192.0.2.4")
	second.InnerPeerAddr = netip.MustParseAddr("192.0.2.3")
	second.LocalMAC[5], second.PeerMAC[5] = 4, 3
	return []bfd.TransportScope{first, second}
}

func reverseWireScope(scope bfd.TransportScope) bfd.TransportScope {
	scope.OuterLocalAddr, scope.OuterPeerAddr = scope.OuterPeerAddr, scope.OuterLocalAddr
	scope.InnerLocalAddr, scope.InnerPeerAddr = scope.InnerPeerAddr, scope.InnerLocalAddr
	scope.LocalMAC, scope.PeerMAC = scope.PeerMAC, scope.LocalMAC
	return scope
}

func openWireOverlay(t *testing.T, scopes []bfd.TransportScope) netio.OverlayConn {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var conn netio.OverlayConn
	var err error
	if scopes[0].Kind == bfd.TransportScopeVXLAN {
		conn, err = netio.NewVXLANConnForScopes(scopes[0].OuterLocalAddr, 49152, scopes, logger)
	} else {
		conn, err = netio.NewGeneveConnForScopes(scopes[0].OuterLocalAddr, 49152, scopes, logger)
	}
	if err != nil {
		t.Fatalf("open scoped overlay: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close overlay: %v", closeErr)
		}
	})
	return conn
}

func sendReceiveWire(
	t *testing.T, sender, receiver netio.OverlayConn, sendScope, wantScope bfd.TransportScope, payload []byte,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := netio.NewOverlaySender(sender, sendScope).SendPacket(ctx, payload, sendScope.InnerPeerAddr); err != nil {
		t.Fatalf("send scoped packet: %v", err)
	}
	got, meta, err := receiver.RecvDecapsulated(ctx)
	if err != nil {
		t.Fatalf("receive scoped packet: %v", err)
	}
	if !bytes.Equal(got, payload) || meta.TransportScope != wantScope || meta.TTL != 255 ||
		meta.SrcAddr != wantScope.OuterPeerAddr || meta.DstAddr != wantScope.OuterLocalAddr || meta.VNI != wantScope.VNI {
		t.Fatalf("received payload=%x metadata=%+v; want payload=%x scope=%+v TTL=255", got, meta, payload, wantScope)
	}
	t.Logf("wire accepted kind=%d discriminator=%d outer=%s->%s vni=%d inner=%s->%s ttl=%d",
		wantScope.Kind, binary.BigEndian.Uint32(got[4:8]), meta.SrcAddr, meta.DstAddr,
		meta.VNI, wantScope.InnerPeerAddr, wantScope.InnerLocalAddr, meta.TTL)
}

func rejectWirePackets(t *testing.T, receiver netio.OverlayConn, scope bfd.TransportScope, payload []byte) {
	t.Helper()
	sender := openWireUDP(t, scope.OuterPeerAddr)
	wrongOuter := openWireUDP(t, scope.OuterLocalAddr)
	port := netio.GenevePort
	if scope.Kind == bfd.TransportScopeVXLAN {
		port = netio.VXLANPort
	}
	for i, name := range []string{
		"outer-peer", "vni", "inner-peer", "inner-local", "peer-mac", "local-mac",
		"short-inner", "ip-checksum", "udp-destination", "overlay-flags",
	} {
		t.Run(name, func(t *testing.T) {
			vectorPayload := bytes.Clone(payload)
			discriminator := binary.BigEndian.Uint32(payload[4:8]) + 100 + uint32(i)
			binary.BigEndian.PutUint32(vectorPayload[4:8], discriminator)
			packet, wantErr := rejectedWirePacket(t, scope, vectorPayload, name)
			udp := sender
			if name == "outer-peer" {
				udp = wrongOuter
			}
			if err := udp.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatalf("bound raw UDP send: %v", err)
			}
			dst := net.UDPAddrFromAddrPort(netip.AddrPortFrom(scope.OuterLocalAddr, port))
			if _, err := udp.WriteToUDP(packet, dst); err != nil {
				t.Fatalf("send rejected packet: %v", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			got, meta, err := receiver.RecvDecapsulated(ctx)
			if !errors.Is(err, wantErr) || len(got) != 0 || meta != (netio.OverlayMeta{}) {
				t.Fatalf("rejected packet returned payload=%x metadata=%+v error=%v; want %v", got, meta, err, wantErr)
			}
			t.Logf("wire rejected vector=%s discriminator=%d bytes=%d error=%v", name, discriminator, len(packet), err)
		})
	}
}

func openWireUDP(t *testing.T, local netip.Addr) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: local.AsSlice()})
	if err != nil {
		t.Fatalf("open raw UDP sender: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw UDP sender: %v", closeErr)
		}
	})
	return conn
}

func rejectedWirePacket(t *testing.T, scope bfd.TransportScope, payload []byte, name string) ([]byte, error) {
	t.Helper()
	switch name {
	case "vni":
		scope.VNI++
	case "inner-peer":
		scope.InnerPeerAddr = netip.MustParseAddr("192.0.2.99")
	case "inner-local":
		scope.InnerLocalAddr = netip.MustParseAddr("192.0.2.99")
	case "peer-mac":
		scope.PeerMAC[5]++
	}
	build := netio.BuildGenevePacket
	headerSize := netio.GeneveHeaderMinSize
	if scope.Kind == bfd.TransportScopeVXLAN {
		build, headerSize = netio.BuildVXLANPacket, netio.VXLANHeaderSize
	}
	packet, err := build(payload, scope.VNI, scope.InnerPeerAddr, scope.InnerLocalAddr, 49152)
	if err != nil {
		t.Fatalf("build rejection packet: %v", err)
	}
	// Ethernet changes do not alter the IPv4 or UDP checksums.
	copy(packet[headerSize+6:headerSize+12], scope.PeerMAC[:])
	if scope.Kind == bfd.TransportScopeGeneve {
		copy(packet[headerSize:headerSize+6], scope.LocalMAC[:])
	}
	wantErr := netio.ErrOverlayIdentityMismatch
	ipOffset := headerSize + netio.InnerEthSize
	switch name {
	case "local-mac":
		packet[headerSize+5]++
	case "short-inner":
		packet, wantErr = packet[:headerSize], netio.ErrInnerPacketTooShort
	case "ip-checksum":
		packet[ipOffset+10] ^= 1
		wantErr = netio.ErrInnerBadIPChecksum
	case "udp-destination":
		binary.BigEndian.PutUint16(packet[ipOffset+netio.InnerIPv4Size+2:], bfdControlPort+1)
		wantErr = netio.ErrInnerBadUDPDestinationPort
	case "overlay-flags":
		if scope.Kind == bfd.TransportScopeVXLAN {
			packet[0], wantErr = 0, netio.ErrVXLANInvalidFlags
		} else {
			packet[1], wantErr = 0, netio.ErrGeneveOBitNotSet
		}
	}
	return packet, wantErr
}

func checkWireCancellation(t *testing.T, conn netio.OverlayConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := conn.RecvDecapsulated(ctx)
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("receive cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("receive ignored context deadline")
	}
}

func checkWireClose(t *testing.T, conn netio.OverlayConn, scope bfd.TransportScope, payload []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := conn.RecvDecapsulated(ctx)
		result <- err
	}()
	if err := conn.Close(); err != nil {
		t.Fatalf("close receiver: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, netio.ErrOverlayRecvClosed) {
			t.Fatalf("receive after close: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("receive did not finish after Close")
	}
	sender := netio.NewOverlaySender(conn, scope)
	if err := sender.SendPacket(ctx, payload, scope.InnerPeerAddr); !errors.Is(err, netio.ErrOverlayRecvClosed) {
		t.Fatalf("send after close: %v", err)
	}
}
