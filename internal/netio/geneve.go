// RFC 9521 — BFD for Geneve Tunnels.
//
// Geneve encapsulation for BFD Control packets enables forwarding-path
// liveness detection between NVEs (Network Virtualization Edges) at the
// VAP (Virtual Access Point) level.
//
// Packet stack (Format A — Ethernet payload):
//
//	Outer IP | Outer UDP (dst 6081) |
//	Geneve Header (8+ bytes) |
//	Inner Ethernet | Inner IP | Inner UDP (dst 3784) |
//	BFD Control Packet
//
// Key requirements (RFC 9521):
//   - BFD sessions originate/terminate at VAPs, not NVEs
//   - Geneve O bit (control) MUST be set to 1
//   - Geneve C bit (critical) MUST be set to 0
//   - VNI integral to session demultiplexing
//   - Inner TTL=255 per RFC 5881
//   - Two formats: Ethernet payload (0x6558) and IP payload (0x0800/0x86DD)

package netio

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// -------------------------------------------------------------------------
// Geneve Constants — RFC 8926 / RFC 9521
// -------------------------------------------------------------------------

const (
	// GeneveHeaderMinSize is the minimum Geneve header size in bytes.
	// RFC 8926 Section 3.4: 8 bytes fixed header (Opt Len can extend it).
	GeneveHeaderMinSize = 8

	// GenevePort is the standard Geneve UDP destination port.
	// RFC 8926 Section 3.3: "IANA has assigned the value 6081".
	GenevePort uint16 = 6081

	// GeneveProtocolEthernet is the Protocol Type for Ethernet payloads.
	// RFC 9521 Section 4.1 (Format A): "Transparent Ethernet Bridging".
	GeneveProtocolEthernet uint16 = 0x6558

	// GeneveProtocolIPv4 is the Protocol Type for IPv4 payloads.
	// RFC 9521 Section 4.2 (Format B).
	GeneveProtocolIPv4 uint16 = 0x0800

	// GeneveProtocolIPv6 is the Protocol Type for IPv6 payloads.
	// RFC 9521 Section 4.2 (Format B).
	GeneveProtocolIPv6 uint16 = 0x86DD
)

// -------------------------------------------------------------------------
// Geneve Header — RFC 8926 Section 3.4
// -------------------------------------------------------------------------
//
// Geneve Header (fixed portion):
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|Ver|  Opt Len  |O|C|    Rsvd.  |         Protocol Type         |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|        Virtual Network Identifier (VNI)       |    Reserved   |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                    Variable-Length Options                     |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+

// GeneveHeader represents a parsed Geneve header.
type GeneveHeader struct {
	// Version is the Geneve protocol version (2 bits).
	Version uint8

	// OptLen is the length of options in 4-byte multiples.
	OptLen uint8

	// OBit indicates this is a control message (O=1 for BFD per RFC 9521).
	OBit bool

	// CBit indicates critical options are present (must be 0 for BFD).
	CBit bool

	// ProtocolType identifies the payload type (0x6558, 0x0800, 0x86DD).
	ProtocolType uint16

	// VNI is the Virtual Network Identifier (24-bit).
	VNI uint32
}

// Sentinel errors for Geneve operations.
var (
	// ErrGeneveHeaderTooShort indicates the buffer is shorter than 8 bytes.
	ErrGeneveHeaderTooShort = errors.New("geneve header too short: need at least 8 bytes")

	// ErrGeneveVNIOverflow indicates the VNI exceeds 24 bits.
	ErrGeneveVNIOverflow = errors.New("geneve VNI exceeds 24-bit range")

	// ErrGeneveInvalidVersion indicates an unsupported Geneve version.
	ErrGeneveInvalidVersion = errors.New("geneve header: unsupported version")
)

// TotalHeaderSize returns the total Geneve header size including options.
func (h GeneveHeader) TotalHeaderSize() int {
	return GeneveHeaderMinSize + int(h.OptLen)*4
}

// MarshalGeneveHeader encodes a Geneve header into buf.
// Returns the number of bytes written (always 8 for the fixed header;
// options are not marshaled by this function).
func MarshalGeneveHeader(buf []byte, hdr GeneveHeader) (int, error) {
	if len(buf) < GeneveHeaderMinSize {
		return 0, ErrGeneveHeaderTooShort
	}
	if hdr.VNI > 0x00FFFFFF {
		return 0, fmt.Errorf("vni=%d: %w", hdr.VNI, ErrGeneveVNIOverflow)
	}

	// Byte 0: Ver (2 bits) | Opt Len (6 bits).
	buf[0] = (hdr.Version << 6) | (hdr.OptLen & 0x3F)

	// Byte 1: O (1 bit) | C (1 bit) | Rsvd (6 bits).
	buf[1] = 0
	if hdr.OBit {
		buf[1] |= 0x80
	}
	if hdr.CBit {
		buf[1] |= 0x40
	}

	// Bytes 2-3: Protocol Type.
	binary.BigEndian.PutUint16(buf[2:4], hdr.ProtocolType)

	// Bytes 4-7: VNI (24 bits) + Reserved (8 bits).
	binary.BigEndian.PutUint32(buf[4:8], hdr.VNI<<8)

	return GeneveHeaderMinSize, nil
}

// UnmarshalGeneveHeader parses a Geneve header from buf.
func UnmarshalGeneveHeader(buf []byte) (GeneveHeader, error) {
	if len(buf) < GeneveHeaderMinSize {
		return GeneveHeader{}, ErrGeneveHeaderTooShort
	}

	ver := buf[0] >> 6
	if ver != 0 {
		return GeneveHeader{}, fmt.Errorf("version=%d: %w", ver, ErrGeneveInvalidVersion)
	}

	optLen := buf[0] & 0x3F
	oBit := buf[1]&0x80 != 0
	cBit := buf[1]&0x40 != 0
	protoType := binary.BigEndian.Uint16(buf[2:4])
	vni := binary.BigEndian.Uint32(buf[4:8]) >> 8

	return GeneveHeader{
		Version:      ver,
		OptLen:       optLen,
		OBit:         oBit,
		CBit:         cBit,
		ProtocolType: protoType,
		VNI:          vni,
	}, nil
}
