package netio

// overlay_inner.go: Inner packet assembly for tunnel-encapsulated BFD
// (RFC 8971, RFC 9521).
//
// Both VXLAN (RFC 8971) and Geneve (RFC 9521) use Format A (Ethernet payload)
// for BFD encapsulation. The inner packet stack is:
//
//	Inner Ethernet (14B) | Inner IPv4 (20B) | Inner UDP (8B) | BFD Control (24+B)
//
// This file builds and strips the inner layers shared by both tunnel types.
// The outer tunnel header (VXLAN or Geneve) is handled by vxlan_conn.go and
// geneve_conn.go respectively.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// -------------------------------------------------------------------------
// Inner Packet Constants
// -------------------------------------------------------------------------

const (
	// InnerEthSize is the Ethernet II header size: dst(6) + src(6) + type(2).
	InnerEthSize = 14

	// InnerIPv4Size is the fixed IPv4 header size (no options): IHL=5 => 20 bytes.
	InnerIPv4Size = 20

	// InnerUDPSize is the UDP header size: src(2) + dst(2) + len(2) + csum(2).
	InnerUDPSize = 8

	// InnerOverheadIPv4 is the total inner packet overhead for IPv4:
	// Ethernet(14) + IPv4(20) + UDP(8) = 42 bytes.
	InnerOverheadIPv4 = InnerEthSize + InnerIPv4Size + InnerUDPSize

	// innerEtherTypeIPv4 is the EtherType for IPv4 (0x0800).
	innerEtherTypeIPv4 uint16 = 0x0800

	// innerIPv4VersionIHL is the combined Version(4) and IHL(5) byte: 0x45.
	innerIPv4VersionIHL uint8 = 0x45

	// innerIPv4Protocol is the IP protocol number for UDP (17).
	innerIPv4Protocol uint8 = 17

	// innerIPv4TTL is the TTL for inner BFD packets.
	// RFC 5881 Section 5 / RFC 5082 GTSM: MUST be 255.
	innerIPv4TTL uint8 = 255

	// innerBFDDstPort is the BFD destination port for inner UDP.
	// RFC 5881 Section 4: destination port 3784.
	innerBFDDstPort uint16 = 3784
)

// innerDstMAC is the IANA-assigned BFD-for-VXLAN inner destination MAC.
// RFC 8971 Section 3.1: "00-52-02" padded to 6 bytes.
// Also used by Geneve Format A (RFC 9521 Section 4.1).
var innerDstMAC = [6]byte{0x00, 0x52, 0x02, 0x00, 0x00, 0x00}

// innerSrcMAC is a locally administered MAC address for inner Ethernet.
// Bit 1 of the first octet is set (locally administered flag).
var innerSrcMAC = [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

// -------------------------------------------------------------------------
// Inner Packet Errors
// -------------------------------------------------------------------------

var (
	// ErrInnerPacketTooShort indicates the buffer is shorter than the
	// minimum inner overhead (42 bytes for IPv4).
	ErrInnerPacketTooShort = errors.New("inner packet too short")

	// ErrInnerBadEtherType indicates the inner Ethernet EtherType is not IPv4.
	ErrInnerBadEtherType = errors.New("inner packet: unexpected EtherType, expected 0x0800 (IPv4)")

	// ErrInnerBadIPVersion indicates the inner IP header version is not 4.
	ErrInnerBadIPVersion = errors.New("inner packet: IP version is not 4")

	// ErrInnerBadProtocol indicates the inner IP protocol is not UDP (17).
	ErrInnerBadProtocol = errors.New("inner packet: IP protocol is not UDP (17)")

	// ErrInnerIPv4Only indicates that only IPv4 inner addresses are supported.
	ErrInnerIPv4Only = errors.New("inner packet: only IPv4 addresses supported")
)

// -------------------------------------------------------------------------
// BuildInnerPacket — assemble inner Ethernet + IPv4 + UDP + BFD
// -------------------------------------------------------------------------

// BuildInnerPacket assembles the inner packet layers for tunnel-encapsulated
// BFD Control packets. The resulting buffer contains:
//
//	Inner Ethernet (14B) | Inner IPv4 (20B) | Inner UDP (8B) | BFD payload
//
// srcIP and dstIP MUST be IPv4 addresses (IPv6 inner headers not yet supported).
// srcPort is the ephemeral source port for the inner UDP header.
//
// The function allocates a new buffer sized exactly for the complete inner packet.
// This is called once per TX (after BFD payload is serialized), so the allocation
// is acceptable on the encapsulation path.
//
// References:
//   - RFC 8971 Section 3: VXLAN BFD inner packet format
//   - RFC 9521 Section 4.1: Geneve BFD Format A (Ethernet payload)
//   - RFC 5881 Section 5: TTL=255 (GTSM)
//   - RFC 768: UDP checksum may be zero for IPv4
func BuildInnerPacket(bfdPayload []byte, srcIP, dstIP netip.Addr, srcPort uint16) ([]byte, error) {
	if !srcIP.Is4() || !dstIP.Is4() {
		return nil, fmt.Errorf("build inner packet: src=%s dst=%s: %w",
			srcIP, dstIP, ErrInnerIPv4Only)
	}

	totalLen := InnerOverheadIPv4 + len(bfdPayload)
	buf := make([]byte, totalLen)

	// --- Inner Ethernet Header (bytes 0-13) ---
	// Dst MAC (bytes 0-5): IANA BFD-for-VXLAN MAC (RFC 8971 Section 3.1).
	copy(buf[0:6], innerDstMAC[:])
	// Src MAC (bytes 6-11): locally administered.
	copy(buf[6:12], innerSrcMAC[:])
	// EtherType (bytes 12-13): 0x0800 (IPv4).
	binary.BigEndian.PutUint16(buf[12:14], innerEtherTypeIPv4)

	// --- Inner IPv4 Header (bytes 14-33) ---
	ipOff := InnerEthSize
	ipPayloadLen := InnerIPv4Size + InnerUDPSize + len(bfdPayload)

	// Byte 0: Version(4) | IHL(5) = 0x45
	buf[ipOff] = innerIPv4VersionIHL
	// Byte 1: DSCP/ECN = 0 (best effort)
	buf[ipOff+1] = 0
	// Bytes 2-3: Total Length = IPv4 header + UDP header + BFD payload.
	// ipPayloadLen bounded by BFD packet sizes, always fits uint16.
	binary.BigEndian.PutUint16(
		buf[ipOff+2:ipOff+4],
		uint16(ipPayloadLen), //nolint:gosec // G115
	)
	// Bytes 4-5: Identification = 0 (no fragmentation)
	binary.BigEndian.PutUint16(buf[ipOff+4:ipOff+6], 0)
	// Bytes 6-7: Flags(DF=1) | Fragment Offset = 0x4000
	// Set Don't Fragment to prevent fragmentation of BFD packets.
	binary.BigEndian.PutUint16(buf[ipOff+6:ipOff+8], 0x4000)
	// Byte 8: TTL = 255 (RFC 5881 Section 5, GTSM RFC 5082)
	buf[ipOff+8] = innerIPv4TTL
	// Byte 9: Protocol = 17 (UDP)
	buf[ipOff+9] = innerIPv4Protocol
	// Bytes 10-11: Header Checksum (computed below, initially 0)
	buf[ipOff+10] = 0
	buf[ipOff+11] = 0
	// Bytes 12-15: Source Address
	src4 := srcIP.As4()
	copy(buf[ipOff+12:ipOff+16], src4[:])
	// Bytes 16-19: Destination Address
	dst4 := dstIP.As4()
	copy(buf[ipOff+16:ipOff+20], dst4[:])

	// Compute IPv4 header checksum (RFC 1071).
	csum := ipv4HeaderChecksum(buf[ipOff : ipOff+InnerIPv4Size])
	binary.BigEndian.PutUint16(buf[ipOff+10:ipOff+12], csum)

	// --- Inner UDP Header (bytes 34-41) ---
	udpOff := InnerEthSize + InnerIPv4Size
	udpLen := InnerUDPSize + len(bfdPayload)

	// Bytes 0-1: Source Port (ephemeral)
	binary.BigEndian.PutUint16(buf[udpOff:udpOff+2], srcPort)
	// Bytes 2-3: Destination Port = 3784 (RFC 5881 Section 4)
	binary.BigEndian.PutUint16(buf[udpOff+2:udpOff+4], innerBFDDstPort)
	// Bytes 4-5: Length = UDP header + BFD payload.
	// udpLen bounded by BFD packet sizes, always fits uint16.
	binary.BigEndian.PutUint16(
		buf[udpOff+4:udpOff+6],
		uint16(udpLen), //nolint:gosec // G115
	)
	// Bytes 6-7: Checksum = 0 (valid per RFC 768 for UDP over IPv4)
	binary.BigEndian.PutUint16(buf[udpOff+6:udpOff+8], 0)

	// --- BFD Payload (bytes 42+) ---
	copy(buf[InnerOverheadIPv4:], bfdPayload)

	return buf, nil
}

// -------------------------------------------------------------------------
// StripInnerPacket — extract BFD payload from inner packet
// -------------------------------------------------------------------------

// StripInnerPacket strips the inner Ethernet + IPv4 + UDP headers and returns
// the raw BFD payload bytes along with the inner source and destination IPs.
//
// Validates:
//   - Buffer length >= InnerOverheadIPv4 (42 bytes)
//   - EtherType == 0x0800 (IPv4)
//   - IP version == 4
//   - IP protocol == 17 (UDP)
//
// Does NOT validate:
//   - IPv4 header checksum (performance: the packet traversed the tunnel correctly)
//   - UDP checksum (set to 0 by BuildInnerPacket)
//   - TTL (validated as inner TTL=255 by the session layer)
func StripInnerPacket(buf []byte) ([]byte, netip.Addr, netip.Addr, error) {
	if len(buf) < InnerOverheadIPv4 {
		return nil, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"strip inner packet: got %d bytes, need %d: %w",
			len(buf), InnerOverheadIPv4, ErrInnerPacketTooShort)
	}

	// Validate EtherType (bytes 12-13).
	etherType := binary.BigEndian.Uint16(buf[12:14])
	if etherType != innerEtherTypeIPv4 {
		return nil, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"strip inner packet: EtherType=0x%04x: %w",
			etherType, ErrInnerBadEtherType)
	}

	// Validate IP version (high nibble of byte 14).
	ipOff := InnerEthSize
	ipVersion := buf[ipOff] >> 4
	if ipVersion != 4 {
		return nil, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"strip inner packet: IP version=%d: %w",
			ipVersion, ErrInnerBadIPVersion)
	}

	// Validate IP protocol (byte 23 = ipOff + 9).
	ipProto := buf[ipOff+9]
	if ipProto != innerIPv4Protocol {
		return nil, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"strip inner packet: IP protocol=%d: %w",
			ipProto, ErrInnerBadProtocol)
	}

	// Extract source and destination IP addresses.
	var src4, dst4 [4]byte
	copy(src4[:], buf[ipOff+12:ipOff+16])
	copy(dst4[:], buf[ipOff+16:ipOff+20])

	return buf[InnerOverheadIPv4:], netip.AddrFrom4(src4), netip.AddrFrom4(dst4), nil
}

// -------------------------------------------------------------------------
// IPv4 Header Checksum — RFC 1071
// -------------------------------------------------------------------------

// ipv4HeaderChecksum computes the IPv4 header checksum per RFC 1071.
// The header MUST have the checksum field set to zero before calling.
// hdr must be exactly 20 bytes (no options).
func ipv4HeaderChecksum(hdr []byte) uint16 {
	var sum uint32

	// Sum all 16-bit words in the header.
	for i := 0; i < len(hdr)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(hdr[i : i+2]))
	}

	// Handle odd byte (not applicable for 20-byte header, but defensive).
	if len(hdr)%2 != 0 {
		sum += uint32(hdr[len(hdr)-1]) << 8
	}

	// Fold 32-bit sum to 16 bits: add carry bits.
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}

	// One's complement.
	return ^uint16(sum) //nolint:gosec // G115: intentional truncation after fold
}
