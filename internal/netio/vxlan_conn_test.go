package netio_test

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// VXLAN Encap/Decap Round-Trip Tests (no real sockets)
// -------------------------------------------------------------------------

func TestBuildVXLANPacketRoundTrip(t *testing.T) {
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
			name:    "management_vni_4096",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Build VXLAN packet.
			pkt, err := netio.BuildVXLANPacket(tt.bfd, tt.vni, tt.srcIP, tt.dstIP, tt.srcPort)
			if err != nil {
				t.Fatalf("BuildVXLANPacket: %v", err)
			}

			// Verify total length: VXLAN(8) + Eth(14) + IPv4(20) + UDP(8) + BFD.
			wantLen := netio.VXLANHeaderSize + netio.InnerOverheadIPv4 + len(tt.bfd)
			if len(pkt) != wantLen {
				t.Fatalf("packet length = %d, want %d", len(pkt), wantLen)
			}

			// Parse VXLAN packet.
			gotBFD, gotVNI, gotSrc, gotDst, err := netio.ParseVXLANPacket(pkt)
			if err != nil {
				t.Fatalf("ParseVXLANPacket: %v", err)
			}

			if gotVNI != tt.vni {
				t.Errorf("VNI = %d, want %d", gotVNI, tt.vni)
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

func TestBuildVXLANPacketVXLANHeader(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	vni := uint32(0xABCDEF)

	pkt, err := netio.BuildVXLANPacket(
		bfd, vni,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildVXLANPacket: %v", err)
	}

	// Verify I flag is set (byte 0, bit 3).
	if pkt[0]&0x08 == 0 {
		t.Error("VXLAN I flag not set")
	}

	// Verify VNI in bytes 4-6.
	if pkt[4] != 0xAB || pkt[5] != 0xCD || pkt[6] != 0xEF {
		t.Errorf("VNI bytes = [%02x %02x %02x], want [AB CD EF]",
			pkt[4], pkt[5], pkt[6])
	}

	// Verify reserved byte 7 is zero.
	if pkt[7] != 0 {
		t.Errorf("VXLAN reserved byte[7] = 0x%02x, want 0x00", pkt[7])
	}
}

func TestBuildVXLANPacketInnerDstMAC(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildVXLANPacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildVXLANPacket: %v", err)
	}

	// Inner Ethernet starts after VXLAN header (offset 8).
	// Dst MAC at bytes 8-13 = 00:52:02:00:00:00 (RFC 8971 Section 3.1).
	wantMAC := [6]byte{0x00, 0x52, 0x02, 0x00, 0x00, 0x00}
	var gotMAC [6]byte
	copy(gotMAC[:], pkt[netio.VXLANHeaderSize:netio.VXLANHeaderSize+6])
	if gotMAC != wantMAC {
		t.Errorf("inner dst MAC = %02x:%02x:%02x:%02x:%02x:%02x, want 00:52:02:00:00:00",
			gotMAC[0], gotMAC[1], gotMAC[2], gotMAC[3], gotMAC[4], gotMAC[5])
	}
}

func TestBuildVXLANPacketInnerTTL(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildVXLANPacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildVXLANPacket: %v", err)
	}

	// Inner IPv4 TTL at offset: VXLAN(8) + Eth(14) + IPv4 offset 8 = byte 30.
	ipOff := netio.VXLANHeaderSize + netio.InnerEthSize
	ttl := pkt[ipOff+8]
	if ttl != 255 {
		t.Errorf("inner TTL = %d, want 255 (RFC 5881 Section 5, GTSM)", ttl)
	}
}

func TestBuildVXLANPacketInnerUDPDstPort(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildVXLANPacket(
		bfd, 100,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildVXLANPacket: %v", err)
	}

	// Inner UDP dst port at offset: VXLAN(8) + Eth(14) + IPv4(20) + 2 = byte 44.
	udpOff := netio.VXLANHeaderSize + netio.InnerEthSize + netio.InnerIPv4Size
	dstPort := binary.BigEndian.Uint16(pkt[udpOff+2 : udpOff+4])
	if dstPort != 3784 {
		t.Errorf("inner UDP dst port = %d, want 3784 (RFC 5881 Section 4)", dstPort)
	}
}

// -------------------------------------------------------------------------
// ParseVXLANPacket Error Cases
// -------------------------------------------------------------------------

func TestParseVXLANPacketTooShort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"vxlan_header_only", 8},
		{"vxlan_plus_partial_inner", 30},
		{"one_byte_short", netio.VXLANHeaderSize + netio.InnerOverheadIPv4 - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, tt.size)
			// Set VXLAN I flag to pass header check (if buffer is long enough).
			if tt.size >= 1 {
				buf[0] = 0x08
			}
			_, _, _, _, err := netio.ParseVXLANPacket(buf)
			if err == nil {
				t.Fatal("expected error for short packet")
			}
		})
	}
}

func TestParseVXLANPacketInvalidIFlag(t *testing.T) {
	t.Parallel()

	// Minimum size buffer but VXLAN I flag not set.
	buf := make([]byte, netio.VXLANHeaderSize+netio.InnerOverheadIPv4+24)
	// I flag not set (byte 0 = 0x00).
	_, _, _, _, err := netio.ParseVXLANPacket(buf)
	if err == nil {
		t.Fatal("expected error for missing I flag")
	}
}
