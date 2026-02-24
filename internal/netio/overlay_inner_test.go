package netio_test

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// BuildInnerPacket + StripInnerPacket Round-Trip Tests
// -------------------------------------------------------------------------

func TestInnerPacketRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		bfdPayload []byte
		srcIP      netip.Addr
		dstIP      netip.Addr
		srcPort    uint16
	}{
		{
			name:       "24_byte_bfd_no_auth",
			bfdPayload: makePayload(24),
			srcIP:      netip.MustParseAddr("10.0.0.1"),
			dstIP:      netip.MustParseAddr("10.0.0.2"),
			srcPort:    49152,
		},
		{
			name:       "48_byte_bfd_with_auth",
			bfdPayload: makePayload(48),
			srcIP:      netip.MustParseAddr("192.168.1.100"),
			dstIP:      netip.MustParseAddr("192.168.1.200"),
			srcPort:    55000,
		},
		{
			name:       "100_byte_bfd_large",
			bfdPayload: makePayload(100),
			srcIP:      netip.MustParseAddr("172.16.0.1"),
			dstIP:      netip.MustParseAddr("172.16.0.2"),
			srcPort:    65535,
		},
		{
			name:       "minimal_1_byte_payload",
			bfdPayload: []byte{0xFF},
			srcIP:      netip.MustParseAddr("1.2.3.4"),
			dstIP:      netip.MustParseAddr("5.6.7.8"),
			srcPort:    49152,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Build inner packet.
			pkt, err := netio.BuildInnerPacket(tt.bfdPayload, tt.srcIP, tt.dstIP, tt.srcPort)
			if err != nil {
				t.Fatalf("BuildInnerPacket: %v", err)
			}

			// Verify total length.
			wantLen := netio.InnerOverheadIPv4 + len(tt.bfdPayload)
			if len(pkt) != wantLen {
				t.Fatalf("packet length = %d, want %d", len(pkt), wantLen)
			}

			// Strip inner packet.
			gotPayload, gotSrc, gotDst, err := netio.StripInnerPacket(pkt)
			if err != nil {
				t.Fatalf("StripInnerPacket: %v", err)
			}

			// Verify source IP.
			if gotSrc != tt.srcIP {
				t.Errorf("srcIP = %s, want %s", gotSrc, tt.srcIP)
			}

			// Verify destination IP.
			if gotDst != tt.dstIP {
				t.Errorf("dstIP = %s, want %s", gotDst, tt.dstIP)
			}

			// Verify BFD payload.
			if len(gotPayload) != len(tt.bfdPayload) {
				t.Fatalf("payload length = %d, want %d", len(gotPayload), len(tt.bfdPayload))
			}
			for i := range tt.bfdPayload {
				if gotPayload[i] != tt.bfdPayload[i] {
					t.Errorf("payload[%d] = 0x%02x, want 0x%02x",
						i, gotPayload[i], tt.bfdPayload[i])
					break
				}
			}
		})
	}
}

// -------------------------------------------------------------------------
// Inner Ethernet Header Validation
// -------------------------------------------------------------------------

func TestInnerPacketDstMAC(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	// RFC 8971 Section 3.1: inner dst MAC = 00:52:02:00:00:00.
	wantMAC := [6]byte{0x00, 0x52, 0x02, 0x00, 0x00, 0x00}
	var gotMAC [6]byte
	copy(gotMAC[:], pkt[0:6])
	if gotMAC != wantMAC {
		t.Errorf("dst MAC = %02x:%02x:%02x:%02x:%02x:%02x, want 00:52:02:00:00:00",
			gotMAC[0], gotMAC[1], gotMAC[2], gotMAC[3], gotMAC[4], gotMAC[5])
	}
}

func TestInnerPacketSrcMAC(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	// Src MAC = 02:00:00:00:00:01 (locally administered).
	wantMAC := [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	var gotMAC [6]byte
	copy(gotMAC[:], pkt[6:12])
	if gotMAC != wantMAC {
		t.Errorf("src MAC = %02x:%02x:%02x:%02x:%02x:%02x, want 02:00:00:00:00:01",
			gotMAC[0], gotMAC[1], gotMAC[2], gotMAC[3], gotMAC[4], gotMAC[5])
	}

	// Verify locally administered bit is set (bit 1 of first octet).
	if gotMAC[0]&0x02 == 0 {
		t.Error("locally administered bit not set in src MAC")
	}
}

func TestInnerPacketEtherType(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	// EtherType at bytes 12-13 = 0x0800 (IPv4).
	etherType := binary.BigEndian.Uint16(pkt[12:14])
	if etherType != 0x0800 {
		t.Errorf("EtherType = 0x%04x, want 0x0800", etherType)
	}
}

// -------------------------------------------------------------------------
// Inner IPv4 Header Validation
// -------------------------------------------------------------------------

func TestInnerPacketIPv4Header(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	srcIP := netip.MustParseAddr("10.1.2.3")
	dstIP := netip.MustParseAddr("10.4.5.6")
	pkt, err := netio.BuildInnerPacket(bfd, srcIP, dstIP, 50000)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	ipOff := netio.InnerEthSize // 14

	// Version + IHL = 0x45 (IPv4, 20 bytes).
	if pkt[ipOff] != 0x45 {
		t.Errorf("Version|IHL = 0x%02x, want 0x45", pkt[ipOff])
	}

	// Total Length = 20 + 8 + 24 = 52.
	totalLen := binary.BigEndian.Uint16(pkt[ipOff+2 : ipOff+4])
	wantTotalLen := uint16(netio.InnerIPv4Size + netio.InnerUDPSize + len(bfd))
	if totalLen != wantTotalLen {
		t.Errorf("IP Total Length = %d, want %d", totalLen, wantTotalLen)
	}

	// TTL = 255 (RFC 5881 Section 5, GTSM).
	if pkt[ipOff+8] != 255 {
		t.Errorf("TTL = %d, want 255", pkt[ipOff+8])
	}

	// Protocol = 17 (UDP).
	if pkt[ipOff+9] != 17 {
		t.Errorf("Protocol = %d, want 17 (UDP)", pkt[ipOff+9])
	}

	// Source IP.
	var gotSrc [4]byte
	copy(gotSrc[:], pkt[ipOff+12:ipOff+16])
	if netip.AddrFrom4(gotSrc) != srcIP {
		t.Errorf("src IP = %s, want %s", netip.AddrFrom4(gotSrc), srcIP)
	}

	// Destination IP.
	var gotDst [4]byte
	copy(gotDst[:], pkt[ipOff+16:ipOff+20])
	if netip.AddrFrom4(gotDst) != dstIP {
		t.Errorf("dst IP = %s, want %s", netip.AddrFrom4(gotDst), dstIP)
	}

	// DF bit set (flags byte 6, bits 14-15 in the 16-bit word at offset 6).
	flagsFrag := binary.BigEndian.Uint16(pkt[ipOff+6 : ipOff+8])
	if flagsFrag&0x4000 == 0 {
		t.Error("DF (Don't Fragment) bit not set in inner IPv4 header")
	}
}

func TestInnerPacketIPv4Checksum(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("192.168.0.1"),
		netip.MustParseAddr("192.168.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	ipOff := netio.InnerEthSize
	ipHdr := pkt[ipOff : ipOff+netio.InnerIPv4Size]

	// Verify the checksum by computing over the entire header including
	// the stored checksum. A valid header produces 0x0000.
	if !verifyIPv4Checksum(ipHdr) {
		storedCsum := binary.BigEndian.Uint16(ipHdr[10:12])
		t.Errorf("IPv4 header checksum verification failed (stored=0x%04x)", storedCsum)
	}
}

func TestInnerPacketIPv4ChecksumVariousAddresses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		dst  string
	}{
		{"loopback", "127.0.0.1", "127.0.0.2"},
		{"private_10", "10.255.255.1", "10.255.255.254"},
		{"private_172", "172.31.0.1", "172.31.255.254"},
		{"public", "203.0.113.1", "198.51.100.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bfd := makePayload(24)
			pkt, err := netio.BuildInnerPacket(
				bfd,
				netip.MustParseAddr(tt.src),
				netip.MustParseAddr(tt.dst),
				49152,
			)
			if err != nil {
				t.Fatalf("BuildInnerPacket: %v", err)
			}

			ipOff := netio.InnerEthSize
			ipHdr := pkt[ipOff : ipOff+netio.InnerIPv4Size]
			if !verifyIPv4Checksum(ipHdr) {
				t.Errorf("checksum verification failed for %s -> %s", tt.src, tt.dst)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Inner UDP Header Validation
// -------------------------------------------------------------------------

func TestInnerPacketUDPHeader(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	srcPort := uint16(55555)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		srcPort,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	udpOff := netio.InnerEthSize + netio.InnerIPv4Size

	// Source port.
	gotSrcPort := binary.BigEndian.Uint16(pkt[udpOff : udpOff+2])
	if gotSrcPort != srcPort {
		t.Errorf("UDP src port = %d, want %d", gotSrcPort, srcPort)
	}

	// Destination port = 3784 (RFC 5881 Section 4).
	gotDstPort := binary.BigEndian.Uint16(pkt[udpOff+2 : udpOff+4])
	if gotDstPort != 3784 {
		t.Errorf("UDP dst port = %d, want 3784", gotDstPort)
	}

	// UDP length = 8 + 24 = 32.
	gotLen := binary.BigEndian.Uint16(pkt[udpOff+4 : udpOff+6])
	wantLen := uint16(netio.InnerUDPSize + len(bfd))
	if gotLen != wantLen {
		t.Errorf("UDP length = %d, want %d", gotLen, wantLen)
	}

	// Checksum = 0 (RFC 768: valid for UDP over IPv4).
	gotCsum := binary.BigEndian.Uint16(pkt[udpOff+6 : udpOff+8])
	if gotCsum != 0 {
		t.Errorf("UDP checksum = 0x%04x, want 0x0000", gotCsum)
	}
}

// -------------------------------------------------------------------------
// StripInnerPacket Error Cases
// -------------------------------------------------------------------------

func TestStripInnerPacketBufferTooShort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"eth_only", 14},
		{"eth_plus_ip", 34},
		{"one_byte_short", 41},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := make([]byte, tt.size)
			_, _, _, err := netio.StripInnerPacket(buf)
			if err == nil {
				t.Fatal("expected error for short buffer")
			}
			if !errors.Is(err, netio.ErrInnerPacketTooShort) {
				t.Errorf("error = %v, want ErrInnerPacketTooShort", err)
			}
		})
	}
}

func TestStripInnerPacketBadEtherType(t *testing.T) {
	t.Parallel()

	// Build a valid packet, then corrupt the EtherType.
	bfd := makePayload(24)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	// Set EtherType to 0x86DD (IPv6) instead of 0x0800.
	binary.BigEndian.PutUint16(pkt[12:14], 0x86DD)

	_, _, _, err = netio.StripInnerPacket(pkt)
	if err == nil {
		t.Fatal("expected error for wrong EtherType")
	}
	if !errors.Is(err, netio.ErrInnerBadEtherType) {
		t.Errorf("error = %v, want ErrInnerBadEtherType", err)
	}
}

func TestStripInnerPacketBadIPVersion(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	// Corrupt IP version to 6 (0x65 = version 6, IHL 5).
	pkt[netio.InnerEthSize] = 0x65

	_, _, _, err = netio.StripInnerPacket(pkt)
	if err == nil {
		t.Fatal("expected error for wrong IP version")
	}
	if !errors.Is(err, netio.ErrInnerBadIPVersion) {
		t.Errorf("error = %v, want ErrInnerBadIPVersion", err)
	}
}

func TestStripInnerPacketBadProtocol(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)
	pkt, err := netio.BuildInnerPacket(
		bfd,
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.2"),
		49152,
	)
	if err != nil {
		t.Fatalf("BuildInnerPacket: %v", err)
	}

	// Set protocol to TCP (6) instead of UDP (17).
	pkt[netio.InnerEthSize+9] = 6

	_, _, _, err = netio.StripInnerPacket(pkt)
	if err == nil {
		t.Fatal("expected error for wrong protocol")
	}
	if !errors.Is(err, netio.ErrInnerBadProtocol) {
		t.Errorf("error = %v, want ErrInnerBadProtocol", err)
	}
}

// -------------------------------------------------------------------------
// BuildInnerPacket Error Cases
// -------------------------------------------------------------------------

func TestBuildInnerPacketIPv6Rejected(t *testing.T) {
	t.Parallel()

	bfd := makePayload(24)

	tests := []struct {
		name string
		src  netip.Addr
		dst  netip.Addr
	}{
		{
			"both_ipv6",
			netip.MustParseAddr("2001:db8::1"),
			netip.MustParseAddr("2001:db8::2"),
		},
		{
			"src_ipv6",
			netip.MustParseAddr("2001:db8::1"),
			netip.MustParseAddr("10.0.0.1"),
		},
		{
			"dst_ipv6",
			netip.MustParseAddr("10.0.0.1"),
			netip.MustParseAddr("2001:db8::1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := netio.BuildInnerPacket(bfd, tt.src, tt.dst, 49152)
			if err == nil {
				t.Fatal("expected error for IPv6 addresses")
			}
			if !errors.Is(err, netio.ErrInnerIPv4Only) {
				t.Errorf("error = %v, want ErrInnerIPv4Only", err)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Constants Validation
// -------------------------------------------------------------------------

func TestInnerOverheadConstants(t *testing.T) {
	t.Parallel()

	if netio.InnerEthSize != 14 {
		t.Errorf("InnerEthSize = %d, want 14", netio.InnerEthSize)
	}
	if netio.InnerIPv4Size != 20 {
		t.Errorf("InnerIPv4Size = %d, want 20", netio.InnerIPv4Size)
	}
	if netio.InnerUDPSize != 8 {
		t.Errorf("InnerUDPSize = %d, want 8", netio.InnerUDPSize)
	}
	if netio.InnerOverheadIPv4 != 42 {
		t.Errorf("InnerOverheadIPv4 = %d, want 42", netio.InnerOverheadIPv4)
	}
	if netio.InnerOverheadIPv4 != netio.InnerEthSize+netio.InnerIPv4Size+netio.InnerUDPSize {
		t.Error("InnerOverheadIPv4 != InnerEthSize + InnerIPv4Size + InnerUDPSize")
	}
}

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// makePayload creates a test payload of the given size with sequential bytes.
func makePayload(size int) []byte {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i & 0xFF)
	}
	return buf
}

// verifyIPv4Checksum verifies an IPv4 header checksum by computing over the
// entire 20-byte header including the stored checksum. A valid header
// produces a result of 0xFFFF (RFC 1071).
func verifyIPv4Checksum(hdr []byte) bool {
	var sum uint32
	for i := 0; i < len(hdr)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(hdr[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return uint16(sum) == 0xFFFF
}
