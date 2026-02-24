// RFC 8971 — BFD for VXLAN Tunnels.
//
// VXLAN encapsulation for BFD Control packets enables forwarding-path
// liveness detection between VTEPs (Virtual Tunnel Endpoints).
//
// Packet stack (outer to inner):
//
//	Outer Ethernet | Outer IP | Outer UDP (dst 4789) |
//	VXLAN Header (8 bytes) |
//	Inner Ethernet | Inner IP | Inner UDP (dst 3784) |
//	BFD Control Packet
//
// Key requirements (RFC 8971):
//   - BFD packets use a dedicated Management VNI
//   - Inner destination port 3784 (standard BFD single-hop)
//   - Inner destination MAC: 00-52-02 (IANA "BFD for VXLAN")
//   - Inner TTL=255 (RFC 5881 GTSM)
//   - Management VNI packets processed locally, not forwarded to tenants

package netio

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// -------------------------------------------------------------------------
// VXLAN Constants — RFC 7348 / RFC 8971
// -------------------------------------------------------------------------

const (
	// VXLANHeaderSize is the fixed VXLAN header size in bytes.
	// RFC 7348 Section 5: 8 bytes (Flags + Reserved + VNI + Reserved).
	VXLANHeaderSize = 8

	// VXLANPort is the standard VXLAN UDP destination port.
	// RFC 7348 Section 5: "IANA has assigned the value 4789".
	VXLANPort uint16 = 4789

	// vxlanFlagVNI is the VXLAN flag indicating a valid VNI.
	// RFC 7348 Section 5: bit 4 (I flag) MUST be set to 1.
	vxlanFlagVNI uint8 = 0x08

	// VXLANBFDInnerMAC is the IANA-assigned inner destination MAC
	// for BFD-over-VXLAN packets.
	// RFC 8971 Section 3.1: "00-52-02".
	VXLANBFDInnerMAC = "00:52:02:00:00:00"
)

// -------------------------------------------------------------------------
// VXLAN Header — RFC 7348 Section 5
// -------------------------------------------------------------------------
//
// VXLAN Header:
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|R|R|R|R|I|R|R|R|            Reserved                           |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                VXLAN Network Identifier (VNI) |   Reserved    |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+

// VXLANHeader represents a parsed VXLAN header.
type VXLANHeader struct {
	// VNI is the VXLAN Network Identifier (24-bit).
	VNI uint32
}

// Sentinel errors for VXLAN operations.
var (
	// ErrVXLANHeaderTooShort indicates the buffer is shorter than 8 bytes.
	ErrVXLANHeaderTooShort = errors.New("vxlan header too short: need 8 bytes")

	// ErrVXLANInvalidFlags indicates the I flag is not set.
	ErrVXLANInvalidFlags = errors.New("vxlan header: I flag (VNI valid) not set")

	// ErrVXLANVNIOverflow indicates the VNI exceeds 24 bits.
	ErrVXLANVNIOverflow = errors.New("vxlan VNI exceeds 24-bit range")
)

// MarshalVXLANHeader encodes a VXLAN header into buf (must be >= 8 bytes).
// Returns the number of bytes written (always 8).
func MarshalVXLANHeader(buf []byte, vni uint32) (int, error) {
	if len(buf) < VXLANHeaderSize {
		return 0, ErrVXLANHeaderTooShort
	}
	if vni > 0x00FFFFFF {
		return 0, fmt.Errorf("vni=%d: %w", vni, ErrVXLANVNIOverflow)
	}

	// Clear the buffer.
	buf[0] = vxlanFlagVNI // Flags: I=1, rest=0
	buf[1] = 0            // Reserved
	buf[2] = 0            // Reserved
	buf[3] = 0            // Reserved

	// VNI occupies bytes 4-6 (24 bits), byte 7 is reserved.
	binary.BigEndian.PutUint32(buf[4:8], vni<<8)

	return VXLANHeaderSize, nil
}

// UnmarshalVXLANHeader parses a VXLAN header from buf (must be >= 8 bytes).
func UnmarshalVXLANHeader(buf []byte) (VXLANHeader, error) {
	if len(buf) < VXLANHeaderSize {
		return VXLANHeader{}, ErrVXLANHeaderTooShort
	}

	// Validate I flag.
	if buf[0]&vxlanFlagVNI == 0 {
		return VXLANHeader{}, ErrVXLANInvalidFlags
	}

	// Extract VNI from bytes 4-6 (top 24 bits of the 32-bit word).
	vni := binary.BigEndian.Uint32(buf[4:8]) >> 8

	return VXLANHeader{VNI: vni}, nil
}
