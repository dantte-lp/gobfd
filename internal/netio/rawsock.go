package netio

import (
	"errors"
	"fmt"
	"net/netip"
)

// -------------------------------------------------------------------------
// BFD Port Constants — RFC 5881 Section 4, RFC 5883 Section 2
// -------------------------------------------------------------------------

const (
	// PortSingleHop is the destination UDP port for single-hop BFD sessions
	// (RFC 5881 Section 4: "BFD Control packets MUST be transmitted in UDP
	// packets with destination port 3784").
	PortSingleHop uint16 = 3784

	// PortMultiHop is the destination UDP port for multi-hop BFD sessions
	// (RFC 5883 Section 2: "The destination UDP port MUST be set to 4784").
	PortMultiHop uint16 = 4784

	// sourcePortMin is the minimum ephemeral source port for BFD sessions
	// (RFC 5881 Section 4: "source port MUST be in the range 49152 through
	// 65535").
	sourcePortMin uint16 = 49152

	// sourcePortMax is the maximum ephemeral source port (inclusive).
	sourcePortMax uint16 = 65535

	// ttlRequired is the mandatory TTL/Hop Limit for BFD packets
	// (RFC 5881 Section 5 / RFC 5082: GTSM requires TTL=255).
	ttlRequired uint8 = 255

	// ttlMultiHopMin is the minimum acceptable TTL for multi-hop BFD
	// (RFC 5883 Section 2: "the received TTL MUST be checked to be 254
	// at a minimum").
	ttlMultiHopMin uint8 = 254
)

// -------------------------------------------------------------------------
// Transport Metadata
// -------------------------------------------------------------------------

// PacketMeta contains transport-layer metadata extracted from received
// BFD Control packets via ancillary data (IP_PKTINFO, IP_RECVTTL).
// Used for GTSM validation (RFC 5082) and session demultiplexing.
type PacketMeta struct {
	// SrcAddr is the source IP address from the IP header.
	SrcAddr netip.Addr

	// DstAddr is the destination IP address, obtained from IP_PKTINFO
	// ancillary data. Needed for multi-hop demultiplexing (RFC 5883).
	DstAddr netip.Addr

	// TTL is the Time-to-Live / Hop Limit from the received IP header.
	// Single-hop (RFC 5881 Section 5): MUST be 255.
	// Multi-hop (RFC 5883 Section 2): MUST be >= 254.
	TTL uint8

	// IfIndex is the interface index on which the packet was received.
	// Used for single-hop session binding (SO_BINDTODEVICE).
	IfIndex int

	// IfName is the interface name on which the packet was received.
	// Set by the listener from the interface index when available.
	IfName string
}

// -------------------------------------------------------------------------
// PacketConn Interface
// -------------------------------------------------------------------------

// PacketConn abstracts BFD packet send/receive operations over raw UDP
// sockets. Implementations handle platform-specific socket configuration
// including TTL, PKTINFO, and interface binding per RFC 5881/5883.
//
// The interface is intentionally minimal to enable mock implementations
// for testing without CAP_NET_RAW.
type PacketConn interface {
	// ReadPacket reads a single BFD Control packet into buf.
	// Returns the number of bytes read and transport metadata.
	// The caller provides a buffer from bfd.PacketPool.
	ReadPacket(buf []byte) (n int, meta PacketMeta, err error)

	// WritePacket sends a BFD Control packet to the given destination.
	// The implementation sets TTL=255 per RFC 5881 Section 5.
	WritePacket(buf []byte, dst netip.Addr) error

	// Close releases the underlying socket resources.
	Close() error

	// LocalAddr returns the local address and port the socket is bound to.
	LocalAddr() netip.AddrPort
}

// -------------------------------------------------------------------------
// Sentinel Errors
// -------------------------------------------------------------------------

var (
	// ErrTTLInvalid indicates the received TTL does not meet GTSM requirements.
	// RFC 5881 Section 5 (single-hop: TTL must be 255).
	// RFC 5883 Section 2 (multi-hop: TTL must be >= 254).
	ErrTTLInvalid = errors.New("TTL validation failed")

	// ErrPortExhausted indicates no source ports are available in the
	// RFC 5881 Section 4 range (49152-65535).
	ErrPortExhausted = errors.New("no source ports available in range 49152-65535")

	// ErrSocketClosed indicates an operation on a closed socket.
	ErrSocketClosed = errors.New("socket closed")

	// ErrPoolType indicates the packet pool returned an unexpected type.
	ErrPoolType = errors.New("packet pool returned unexpected type")
)

// -------------------------------------------------------------------------
// GTSM Validation — RFC 5881 Section 5, RFC 5883 Section 2
// -------------------------------------------------------------------------

// ValidateTTL checks the received TTL against GTSM requirements.
//
// For single-hop (RFC 5881 Section 5): "the received TTL MUST be checked
// to be 255." This is the Generalized TTL Security Mechanism (RFC 5082).
//
// For multi-hop (RFC 5883 Section 2): "the received TTL MUST be checked
// to be 254 at a minimum." This allows exactly one intermediate hop.
func ValidateTTL(meta PacketMeta, multiHop bool) error {
	if multiHop {
		if meta.TTL < ttlMultiHopMin {
			return fmt.Errorf(
				"multi-hop TTL %d, minimum %d (RFC 5883 Section 2): %w",
				meta.TTL, ttlMultiHopMin, ErrTTLInvalid,
			)
		}
		return nil
	}

	if meta.TTL != ttlRequired {
		return fmt.Errorf(
			"single-hop TTL %d, required %d (RFC 5881 Section 5): %w",
			meta.TTL, ttlRequired, ErrTTLInvalid,
		)
	}
	return nil
}
