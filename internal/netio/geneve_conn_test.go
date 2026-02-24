package netio_test

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Geneve Encap/Decap Round-Trip Tests (no real sockets)
// -------------------------------------------------------------------------

func TestBuildGenevePacketRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		bfd     []byte
		vni     uint32
		srcIP   netip.Addr
		dstIP   netip.Addr
		srcPort uint16
	}{
		{
			name:    "basic_24_byte_bfd",
			bfd:     makePayload(24),
			vni:     100,
			srcIP:   netip.MustParseAddr("10.0.0.1"),
			dstIP:   netip.MustParseAddr("10.0.0.2"),
			srcPort: 49152,
		},
		{
			name:    "vni_4096",
			bfd:     makePayload(48),
			vni:     4096,
			srcIP:   netip.MustParseAddr("192.168.1.1"),
			dstIP:   netip.MustParseAddr("192.168.1.2"),
			srcPort: 55000,
		},
		{
			name:    "max_vni",
			bfd:     makePayload(24),
			vni:     0x00FFFFFF,
			srcIP:   netip.MustParseAddr("172.16.0.1"),
			dstIP:   netip.MustParseAddr("172.16.0.2"),
			srcPort: 65535,
		},
		{
			name:    "large_bfd_100_bytes",
			bfd:     makePayload(100),
			vni:     42,
			srcIP:   netip.MustParseAddr("203.0.113.1"),
			dstIP:   netip.MustParseAddr("198.51.100.1"),
			srcPort: 50000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Build Geneve packet.
			pkt, err := netio.BuildGenevePacket(tt.bfd, tt.vni, tt.srcIP, tt.dstIP, tt.srcPort)
			if err != nil {
				t.Fatalf("BuildGenevePacket: %v", err)
			}

			// Verify total length: Geneve(8) + Eth(14) + IPv4(20) + UDP(8) + BFD.
			wantLen := netio.GeneveHeaderMinSize + netio.InnerOverheadIPv4 + len(tt.bfd)
			if len(pkt) != wantLen {
				t.Fatalf("packet length = %d, want %d", len(pkt), wantLen)
			}

			// Parse Geneve packet.
			gotBFD, gotHdr, gotSrc, gotDst, err := netio.ParseGenevePacket(pkt)
			if err != nil {
				t.Fatalf("ParseGenevePacket: %v", err)
			}

			if gotHdr.VNI != tt.vni {
				t.Errorf("VNI = %d, want %d", gotHdr.VNI, tt.vni)
			}
			if gotSrc != tt.srcIP {
				t.Errorf("srcIP = %s, want %s", gotSrc, tt.srcIP)
			}
			if gotDst != tt.dstIP {
				t.Errorf("dstIP = %s, want %s", gotDst, tt.dstIP)
			}
			if len(gotBFD) != len(tt.bfd) {
				t.Fatalf("BFD payload length = %d, want %d", len(gotBFD), len(tt.bfd))
			}
			for i := range tt.bfd {
				if gotBFD[i] != tt.bfd[i] {
					t.Errorf("BFD[%d] = 0x%02x, want 0x%02x", i, gotBFD[i], tt.bfd[i])
					break
				}
			}
		})
	}
}

func TestBuildGenevePacketHeader(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	vni := uint32(0xABCDEF)

	pkt, err := netio.BuildGenevePacket(
		bfd, vni,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}

	// Verify version = 0 (bits 7-6 of byte 0).
	ver := pkt[0] >> 6
	if ver != 0 {
		t.Errorf("version = %d, want 0", ver)
	}

	// Verify O bit = 1 (RFC 9521 Section 4).
	if pkt[1]&0x80 == 0 {
		t.Error("O bit (control) not set, required by RFC 9521")
	}

	// Verify C bit = 0 (RFC 9521 Section 4).
	if pkt[1]&0x40 != 0 {
		t.Error("C bit (critical) is set, must be 0 per RFC 9521")
	}

	// Verify Protocol Type = 0x6558 (Format A: Ethernet payload).
	protoType := binary.BigEndian.Uint16(pkt[2:4])
	if protoType != 0x6558 {
		t.Errorf("Protocol Type = 0x%04x, want 0x6558", protoType)
	}

	// Verify VNI in bytes 4-6.
	if pkt[4] != 0xAB || pkt[5] != 0xCD || pkt[6] != 0xEF {
		t.Errorf("VNI bytes = [%02x %02x %02x], want [AB CD EF]",
			pkt[4], pkt[5], pkt[6])
	}

	// Verify reserved byte 7 is zero.
	if pkt[7] != 0 {
		t.Errorf("Geneve reserved byte[7] = 0x%02x, want 0x00", pkt[7])
	}
}

func TestBuildGenevePacketOBitAndCBit(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildGenevePacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}

	// Parse back to verify structured fields.
	_, hdr, _, _, err := netio.ParseGenevePacket(pkt)
	if err != nil {
		t.Fatalf("ParseGenevePacket: %v", err)
	}

	if !hdr.OBit {
		t.Error("OBit should be true (RFC 9521)")
	}
	if hdr.CBit {
		t.Error("CBit should be false (RFC 9521)")
	}
	if hdr.ProtocolType != netio.GeneveProtocolEthernet {
		t.Errorf("ProtocolType = 0x%04x, want 0x%04x (Format A)",
			hdr.ProtocolType, netio.GeneveProtocolEthernet)
	}
}

func TestBuildGenevePacketInnerDstMAC(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildGenevePacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}

	// Inner Ethernet starts after Geneve header (offset 8).
	// Dst MAC at bytes 8-13 = 00:52:02:00:00:00 (RFC 8971 Section 3.1).
	wantMAC := [6]byte{0x00, 0x52, 0x02, 0x00, 0x00, 0x00}
	var gotMAC [6]byte
	copy(gotMAC[:], pkt[netio.GeneveHeaderMinSize:netio.GeneveHeaderMinSize+6])
	if gotMAC != wantMAC {
		t.Errorf("inner dst MAC = %02x:%02x:%02x:%02x:%02x:%02x, want 00:52:02:00:00:00",
			gotMAC[0], gotMAC[1], gotMAC[2], gotMAC[3], gotMAC[4], gotMAC[5])
	}
}

func TestBuildGenevePacketInnerTTL(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildGenevePacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}

	// Inner IPv4 TTL at offset: Geneve(8) + Eth(14) + IPv4 offset 8 = byte 30.
	ipOff := netio.GeneveHeaderMinSize + netio.InnerEthSize
	ttl := pkt[ipOff+8]
	if ttl != 255 {
		t.Errorf("inner TTL = %d, want 255 (RFC 5881 Section 5, GTSM)", ttl)
	}
}

func TestBuildGenevePacketInnerUDPDstPort(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildGenevePacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}

	// Inner UDP dst port at offset: Geneve(8) + Eth(14) + IPv4(20) + 2 = byte 44.
	udpOff := netio.GeneveHeaderMinSize + netio.InnerEthSize + netio.InnerIPv4Size
	dstPort := binary.BigEndian.Uint16(pkt[udpOff+2 : udpOff+4])
	if dstPort != 3784 {
		t.Errorf("inner UDP dst port = %d, want 3784 (RFC 5881 Section 4)", dstPort)
	}
}

// -------------------------------------------------------------------------
// ParseGenevePacket Error Cases
// -------------------------------------------------------------------------

func TestParseGenevePacketTooShort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"geneve_header_only", 8},
		{"partial_inner", 30},
		{"one_byte_short", netio.GeneveHeaderMinSize + netio.InnerOverheadIPv4 - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, tt.size)
			_, _, _, _, err := netio.ParseGenevePacket(buf)
			if err == nil {
				t.Fatal("expected error for short packet")
			}
		})
	}
}

func TestParseGenevePacketInvalidVersion(t *testing.T) {
	t.Parallel()

	// Build a valid packet, then corrupt the version.
	bfd := makePayload(24)
	pkt, err := netio.BuildGenevePacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildGenevePacket: %v", err)
	}

	// Set version to 1 (bits 7-6 of byte 0).
	pkt[0] = (pkt[0] & 0x3F) | 0x40

	_, _, _, _, err = netio.ParseGenevePacket(pkt)
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
	if !errors.Is(err, netio.ErrGeneveInvalidVersion) {
		t.Errorf("error = %v, want ErrGeneveInvalidVersion", err)
	}
}

func TestParseGenevePacketInnerCorruptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		corrupt   func([]byte)
		wantError error
	}{
		{
			name: "bad_ethertype",
			corrupt: func(pkt []byte) {
				off := netio.GeneveHeaderMinSize + 12
				binary.BigEndian.PutUint16(pkt[off:off+2], 0x86DD) // IPv6 instead of IPv4
			},
			wantError: netio.ErrInnerBadEtherType,
		},
		{
			name: "bad_ip_version",
			corrupt: func(pkt []byte) {
				off := netio.GeneveHeaderMinSize + netio.InnerEthSize
				pkt[off] = 0x65 // Version 6 instead of 4
			},
			wantError: netio.ErrInnerBadIPVersion,
		},
		{
			name: "bad_protocol",
			corrupt: func(pkt []byte) {
				off := netio.GeneveHeaderMinSize + netio.InnerEthSize + 9
				pkt[off] = 6 // TCP instead of UDP
			},
			wantError: netio.ErrInnerBadProtocol,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bfd := makePayload(24)
			pkt, err := netio.BuildGenevePacket(
				bfd, 100,
				netip.MustParseAddr("10.0.0.1"),
				netip.MustParseAddr("10.0.0.2"),
				49152,
			)
			if err != nil {
				t.Fatalf("BuildGenevePacket: %v", err)
			}

			tt.corrupt(pkt)

			_, _, _, _, err = netio.ParseGenevePacket(pkt)
			if err == nil {
				t.Fatal("expected error for corrupted inner packet")
			}
			if !errors.Is(err, tt.wantError) {
				t.Errorf("error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Overlay Abstraction Tests
// -------------------------------------------------------------------------

func TestOverlaySenderSendsToConn(t *testing.T) {
	t.Parallel()

	mock := &mockOverlayConn{}
	sender := netio.NewOverlaySender(mock)

	payload := []byte{0x20, 0x40, 0x03, 0x18}
	dstAddr := netip.MustParseAddr("10.0.0.2")

	err := sender.SendPacket(t.Context(), payload, dstAddr)
	if err != nil {
		t.Fatalf("SendPacket: %v", err)
	}

	if mock.sendCount != 1 {
		t.Errorf("send count = %d, want 1", mock.sendCount)
	}
	if mock.lastDstAddr != dstAddr {
		t.Errorf("dst addr = %s, want %s", mock.lastDstAddr, dstAddr)
	}
	if len(mock.lastPayload) != len(payload) {
		t.Errorf("payload length = %d, want %d", len(mock.lastPayload), len(payload))
	}
}

func TestOverlayMetaFields(t *testing.T) {
	t.Parallel()

	meta := netio.OverlayMeta{
		SrcAddr: netip.MustParseAddr("10.0.0.1"),
		DstAddr: netip.MustParseAddr("10.0.0.2"),
		VNI:     4096,
	}

	if meta.SrcAddr != netip.MustParseAddr("10.0.0.1") {
		t.Errorf("SrcAddr = %s, want 10.0.0.1", meta.SrcAddr)
	}
	if meta.DstAddr != netip.MustParseAddr("10.0.0.2") {
		t.Errorf("DstAddr = %s, want 10.0.0.2", meta.DstAddr)
	}
	if meta.VNI != 4096 {
		t.Errorf("VNI = %d, want 4096", meta.VNI)
	}
}

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// mockOverlayConn implements OverlayConn for testing the OverlaySender adapter.
type mockOverlayConn struct {
	sendCount   int
	lastPayload []byte
	lastDstAddr netip.Addr
}

func (m *mockOverlayConn) SendEncapsulated(_ context.Context, bfdPayload []byte, dstAddr netip.Addr) error {
	m.sendCount++
	m.lastPayload = make([]byte, len(bfdPayload))
	copy(m.lastPayload, bfdPayload)
	m.lastDstAddr = dstAddr
	return nil
}

func (m *mockOverlayConn) RecvDecapsulated(_ context.Context) ([]byte, netio.OverlayMeta, error) {
	return nil, netio.OverlayMeta{}, errors.New("mock: not implemented")
}

func (m *mockOverlayConn) Close() error {
	return nil
}
