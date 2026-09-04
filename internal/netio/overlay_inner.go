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
	"bytes"
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

	// maxInnerBFDPayloadSize is bounded by the IPv4 total-length field.
	maxInnerBFDPayloadSize = 1<<16 - 1 - InnerIPv4Size - InnerUDPSize

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

	// ErrInnerBadDestinationMAC indicates the packet is not addressed to the
	// supported Format A BFD destination MAC.
	ErrInnerBadDestinationMAC = errors.New("inner packet: unexpected destination MAC")

	// ErrInnerBadIPVersion indicates the inner IP header version is not 4.
	ErrInnerBadIPVersion = errors.New("inner packet: IP version is not 4")

	// ErrInnerBadIHL indicates an invalid or out-of-bounds IPv4 header length.
	ErrInnerBadIHL = errors.New("inner packet: invalid IPv4 header length")

	// ErrInnerBadIPLength indicates the IPv4 total length does not exactly
	// match the received inner packet.
	ErrInnerBadIPLength = errors.New("inner packet: invalid IPv4 total length")

	// ErrInnerFragmented indicates forbidden IPv4 fragmentation or reserved flags.
	ErrInnerFragmented = errors.New("inner packet: fragmented IPv4 packet")

	// ErrInnerBadIPChecksum indicates an invalid IPv4 header checksum.
	ErrInnerBadIPChecksum = errors.New("inner packet: invalid IPv4 header checksum")

	// ErrInnerBadProtocol indicates the inner IP protocol is not UDP (17).
	ErrInnerBadProtocol = errors.New("inner packet: IP protocol is not UDP (17)")

	// ErrInnerBadTTL indicates the inner IPv4 TTL is not 255.
	ErrInnerBadTTL = errors.New("inner packet: IPv4 TTL is not 255")

	// ErrInnerBadUDPDestinationPort indicates the UDP destination is not BFD.
	ErrInnerBadUDPDestinationPort = errors.New("inner packet: UDP destination port is not 3784")

	// ErrInnerBadUDPSourcePort indicates the UDP source is outside the RFC 5881 range.
	ErrInnerBadUDPSourcePort = errors.New("inner packet: UDP source port is outside 49152-65535")

	// ErrInnerBadUDPLength indicates an invalid or non-exact UDP length.
	ErrInnerBadUDPLength = errors.New("inner packet: invalid UDP length")

	// ErrInnerBadUDPChecksum indicates an invalid non-zero IPv4 UDP checksum.
	ErrInnerBadUDPChecksum = errors.New("inner packet: invalid UDP checksum")

	// ErrInnerIPv4Only indicates that only IPv4 inner addresses are supported.
	ErrInnerIPv4Only = errors.New("inner packet: only IPv4 addresses supported")

	// ErrInnerPacketBufferTooShort indicates that the caller-owned destination
	// buffer cannot hold the complete inner packet.
	ErrInnerPacketBufferTooShort = errors.New("inner packet: destination buffer too short")

	// ErrInnerPacketPayloadTooLarge indicates that the BFD payload cannot be
	// represented by the inner IPv4 and UDP length fields.
	ErrInnerPacketPayloadTooLarge = errors.New("inner packet: BFD payload exceeds IPv4 length limit")
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
// Production tunnel connections use BuildInnerPacketInto with a connection-owned
// buffer; this wrapper remains useful for callers that want ownership of a packet.
//
// References:
//   - RFC 8971 Section 3: VXLAN BFD inner packet format
//   - RFC 9521 Section 4.1: Geneve BFD Format A (Ethernet payload)
//   - RFC 5881 Section 5: TTL=255 (GTSM)
//   - RFC 768: UDP checksum may be zero for IPv4
func BuildInnerPacket(bfdPayload []byte, srcIP, dstIP netip.Addr, srcPort uint16) ([]byte, error) {
	if err := validateInnerBFDPayloadSize(len(bfdPayload)); err != nil {
		return nil, err
	}
	totalLen := InnerOverheadIPv4 + len(bfdPayload)
	buf := make([]byte, totalLen)
	return BuildInnerPacketInto(buf, bfdPayload, srcIP, dstIP, srcPort)
}

// BuildInnerPacketInto assembles an inner packet into dst and returns the exact
// packet slice. The caller owns dst and must provide at least
// InnerOverheadIPv4+len(bfdPayload) bytes. Reusing dst avoids a per-packet
// allocation in the VXLAN and Geneve transmit paths.
func BuildInnerPacketInto(
	dst []byte,
	bfdPayload []byte,
	srcIP, dstIP netip.Addr,
	srcPort uint16,
) ([]byte, error) {
	if !srcIP.Is4() || !dstIP.Is4() {
		return nil, fmt.Errorf("build inner packet: src=%s dst=%s: %w",
			srcIP, dstIP, ErrInnerIPv4Only)
	}
	if err := validateInnerBFDPayloadSize(len(bfdPayload)); err != nil {
		return nil, err
	}

	totalLen := InnerOverheadIPv4 + len(bfdPayload)
	if len(dst) < totalLen {
		return nil, fmt.Errorf("build inner packet: buffer=%d need=%d: %w",
			len(dst), totalLen, ErrInnerPacketBufferTooShort)
	}
	buf := dst[:totalLen]

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
		uint16(ipPayloadLen), // #nosec G115 -- validateInnerBFDPayloadSize bounds the IPv4 total length.
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
		uint16(udpLen), // #nosec G115 -- validateInnerBFDPayloadSize bounds the UDP length.
	)
	// Bytes 6-7: Checksum = 0 (valid per RFC 768 for UDP over IPv4)
	binary.BigEndian.PutUint16(buf[udpOff+6:udpOff+8], 0)

	// --- BFD Payload (bytes 42+) ---
	copy(buf[InnerOverheadIPv4:], bfdPayload)

	return buf, nil
}

func validateInnerBFDPayloadSize(size int) error {
	if size > maxInnerBFDPayloadSize {
		return fmt.Errorf(
			"inner BFD payload size %d exceeds maximum %d: %w",
			size,
			maxInnerBFDPayloadSize,
			ErrInnerPacketPayloadTooLarge,
		)
	}
	return nil
}

// -------------------------------------------------------------------------
// StripInnerPacket — extract BFD payload from inner packet
// -------------------------------------------------------------------------

// StripInnerPacket strips the inner Ethernet + IPv4 + UDP headers and returns
// the raw BFD payload bytes and inner source and destination IPs.
//
// Validates:
//   - Format A destination MAC and IPv4 EtherType
//   - IPv4 version, IHL, exact total length, flags, checksum, protocol, and TTL
//   - UDP destination port, exact length, and any non-zero checksum
func StripInnerPacket(buf []byte) ([]byte, netip.Addr, netip.Addr, error) {
	payload, src, dst, _, err := stripInnerPacket(buf)
	return payload, src, dst, err
}

type innerIPv4Packet struct {
	udp []byte
	src [4]byte
	dst [4]byte
	ttl uint8
}

func stripInnerPacket(buf []byte) ([]byte, netip.Addr, netip.Addr, uint8, error) {
	if len(buf) < InnerOverheadIPv4 {
		return nil, netip.Addr{}, netip.Addr{}, 0, fmt.Errorf(
			"strip inner packet: got %d bytes, need %d: %w",
			len(buf), InnerOverheadIPv4, ErrInnerPacketTooShort)
	}
	if !bytes.Equal(buf[:len(innerDstMAC)], innerDstMAC[:]) {
		return nil, netip.Addr{}, netip.Addr{}, 0, fmt.Errorf(
			"strip inner packet: destination MAC=%02x:%02x:%02x:%02x:%02x:%02x: %w",
			buf[0], buf[1], buf[2], buf[3], buf[4], buf[5], ErrInnerBadDestinationMAC)
	}

	// Validate EtherType (bytes 12-13).
	etherType := binary.BigEndian.Uint16(buf[12:14])
	if etherType != innerEtherTypeIPv4 {
		return nil, netip.Addr{}, netip.Addr{}, 0, fmt.Errorf(
			"strip inner packet: EtherType=0x%04x: %w",
			etherType, ErrInnerBadEtherType)
	}

	ipPacket, err := parseInnerIPv4(buf[InnerEthSize:])
	if err != nil {
		return nil, netip.Addr{}, netip.Addr{}, 0, err
	}
	payload, err := parseInnerUDP(ipPacket)
	if err != nil {
		return nil, netip.Addr{}, netip.Addr{}, 0, err
	}

	return payload, netip.AddrFrom4(ipPacket.src), netip.AddrFrom4(ipPacket.dst), ipPacket.ttl, nil
}

func parseInnerIPv4(ip []byte) (innerIPv4Packet, error) {
	var packet innerIPv4Packet
	if len(ip) < InnerIPv4Size+InnerUDPSize {
		return packet, fmt.Errorf("strip inner packet: IPv4 packet=%d: %w", len(ip), ErrInnerBadIPLength)
	}
	ipVersion := ip[0] >> 4
	if ipVersion != 4 {
		return packet, fmt.Errorf(
			"strip inner packet: IP version=%d: %w",
			ipVersion, ErrInnerBadIPVersion)
	}
	ipHeaderLen := int(ip[0]&0x0f) * 4
	if ipHeaderLen < InnerIPv4Size || ipHeaderLen+InnerUDPSize > len(ip) {
		return packet, fmt.Errorf(
			"strip inner packet: IPv4 header length=%d packet=%d: %w",
			ipHeaderLen, len(ip), ErrInnerBadIHL)
	}
	ipTotalLen := int(binary.BigEndian.Uint16(ip[2:4]))
	if ipTotalLen != len(ip) || ipTotalLen < ipHeaderLen+InnerUDPSize {
		return packet, fmt.Errorf(
			"strip inner packet: IPv4 total length=%d packet=%d header=%d: %w",
			ipTotalLen, len(ip), ipHeaderLen, ErrInnerBadIPLength)
	}

	packet, err := validateInnerIPv4Header(ip[:ipHeaderLen])
	if err != nil {
		return packet, err
	}
	packet.udp = ip[ipHeaderLen:ipTotalLen]
	return packet, nil
}

func validateInnerIPv4Header(header []byte) (innerIPv4Packet, error) {
	var packet innerIPv4Packet
	if len(header) < InnerIPv4Size {
		return packet, fmt.Errorf("strip inner packet: IPv4 header=%d: %w", len(header), ErrInnerBadIHL)
	}
	if flagsFragment := binary.BigEndian.Uint16(header[6:8]); flagsFragment&^uint16(0x4000) != 0 {
		return packet, fmt.Errorf(
			"strip inner packet: IPv4 flags/fragment offset=0x%04x: %w",
			flagsFragment, ErrInnerFragmented)
	}
	if ipv4HeaderChecksum(header) != 0 {
		return packet, fmt.Errorf(
			"strip inner packet: %w", ErrInnerBadIPChecksum)
	}

	ipProto := header[9]
	if ipProto != innerIPv4Protocol {
		return packet, fmt.Errorf(
			"strip inner packet: IP protocol=%d: %w",
			ipProto, ErrInnerBadProtocol)
	}
	packet.ttl = header[8]
	if packet.ttl != innerIPv4TTL {
		return packet, fmt.Errorf(
			"strip inner packet: IPv4 TTL=%d: %w", packet.ttl, ErrInnerBadTTL)
	}
	packet.src = [4]byte{header[12], header[13], header[14], header[15]}
	packet.dst = [4]byte{header[16], header[17], header[18], header[19]}
	return packet, nil
}

func parseInnerUDP(packet innerIPv4Packet) ([]byte, error) {
	udp := packet.udp
	if len(udp) < InnerUDPSize {
		return nil, fmt.Errorf("strip inner packet: UDP length=%d: %w", len(udp), ErrInnerBadUDPLength)
	}
	udpSrcPort := binary.BigEndian.Uint16(udp[:2])
	if udpSrcPort < sourcePortMin || udpSrcPort > sourcePortMax {
		return nil, fmt.Errorf(
			"strip inner packet: UDP source port=%d: %w",
			udpSrcPort, ErrInnerBadUDPSourcePort)
	}
	udpDstPort := binary.BigEndian.Uint16(udp[2:4])
	if udpDstPort != innerBFDDstPort {
		return nil, fmt.Errorf(
			"strip inner packet: UDP destination port=%d: %w",
			udpDstPort, ErrInnerBadUDPDestinationPort)
	}
	udpLen := binary.BigEndian.Uint16(udp[4:6])
	if udpLen < InnerUDPSize || int(udpLen) != len(udp) {
		return nil, fmt.Errorf(
			"strip inner packet: UDP length=%d IP payload=%d: %w",
			udpLen, len(udp), ErrInnerBadUDPLength)
	}
	if binary.BigEndian.Uint16(udp[6:8]) != 0 && !validUDPIPv4Checksum(packet.src, packet.dst, udp, udpLen) {
		return nil, fmt.Errorf(
			"strip inner packet: %w", ErrInnerBadUDPChecksum)
	}

	return udp[InnerUDPSize:], nil
}

// -------------------------------------------------------------------------
// IPv4 Header Checksum — RFC 1071
// -------------------------------------------------------------------------

// ipv4HeaderChecksum computes the complete IPv4 header checksum per RFC 1071.
// The checksum field must be zero when generating a checksum; verifying a
// header with its stored checksum returns zero.
func ipv4HeaderChecksum(hdr []byte) uint16 {
	return internetChecksum(hdr, 0)
}

func validUDPIPv4Checksum(src, dst [4]byte, udp []byte, udpLen uint16) bool {
	sum := (uint32(src[0]) << 8) + uint32(src[1]) +
		(uint32(src[2]) << 8) + uint32(src[3]) +
		(uint32(dst[0]) << 8) + uint32(dst[1]) +
		(uint32(dst[2]) << 8) + uint32(dst[3]) +
		uint32(innerIPv4Protocol) + uint32(udpLen)
	return internetChecksum(udp, sum) == 0
}

func internetChecksum(buf []byte, sum uint32) uint16 {
	for len(buf) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(buf))
		buf = buf[2:]
	}
	if len(buf) == 1 {
		sum += uint32(buf[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}
