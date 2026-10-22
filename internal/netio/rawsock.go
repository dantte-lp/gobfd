package netio

import (
	"errors"
	"fmt"
	"net/netip"
)

// -------------------------------------------------------------------------
// BFD Port Constants — RFC 5881 Section 4, RFC 5883 Section 5
// -------------------------------------------------------------------------

const (
	// PortSingleHop is the destination UDP port for single-hop BFD sessions
	// (RFC 5881 Section 4: "BFD Control packets MUST be transmitted in UDP
	// packets with destination port 3784").
	PortSingleHop uint16 = 3784

	// PortMultiHop is the destination UDP port for multi-hop BFD sessions
	// (RFC 5883 Section 5: "the UDP destination port MUST have a value of
	// 4784").
	PortMultiHop uint16 = 4784

	// PortEcho is the destination UDP port for BFD echo packets
	// (RFC 5881 Section 4, RFC 9747: "BFD Echo packets MUST be
	// transmitted in UDP packets with destination port 3785").
	PortEcho uint16 = 3785

	// PortMicroBFD is the destination UDP port for micro-BFD sessions
	// on LAG member links (RFC 7130 Section 2.1: "Micro-BFD sessions
	// use a well-known UDP destination port: 6784").
	PortMicroBFD uint16 = 6784

	// sourcePortMin is the minimum ephemeral source port for BFD sessions
	// (RFC 5881 Section 4: "source port MUST be in the range 49152 through
	// 65535").
	sourcePortMin uint16 = 49152

	// sourcePortMax is the maximum ephemeral source port (inclusive).
	sourcePortMax uint16 = 65535

	// ttlRequired is the mandatory TTL/Hop Limit for BFD packets
	// (RFC 5881 Section 5 / RFC 5082: GTSM requires TTL=255).
	ttlRequired uint8 = 255

	// TTLEchoLoopback is the required TTL/Hop Limit for received RFC 9747
	// Unaffiliated BFD Echo packets after one-hop loopback forwarding.
	TTLEchoLoopback uint8 = 254
)

// -------------------------------------------------------------------------
// Transport Metadata
// -------------------------------------------------------------------------

// PacketMeta contains transport-layer metadata extracted from received
// BFD Control packets via ancillary data. Supports both IPv4 and IPv6:
//   - IPv4: IP_PKTINFO + IP_RECVTTL
//   - IPv6: IPV6_PKTINFO + IPV6_RECVHOPLIMIT
//
// Used for GTSM validation (RFC 5082) and session demultiplexing.
type PacketMeta struct {
	// SrcAddr is the source IP address from the IP header (IPv4 or IPv6).
	SrcAddr netip.Addr

	// DstAddr is the destination IP address, obtained from ancillary data:
	// IP_PKTINFO (IPv4) or IPV6_PKTINFO (IPv6).
	// Needed for multi-hop demultiplexing (RFC 5883).
	DstAddr netip.Addr

	// TTL is the Time-to-Live (IPv4) or Hop Limit (IPv6) from the
	// received IP header. Both are semantically equivalent for GTSM
	// (RFC 5082):
	//   - Single-hop (RFC 5881 Section 5): MUST be 255
	//   - Multi-hop: RFC 5883 has no receive TTL rule; the demultiplexed
	//     session applies its configured hop policy.
	TTL uint8

	// MultiHop is true when the packet arrived on a multi-hop (RFC 5883)
	// listener rather than a single-hop (RFC 5881) one.
	MultiHop bool

	// IfIndex is the interface index on which the packet was received.
	// Used for single-hop session binding (SO_BINDTODEVICE).
	IfIndex int

	// IfName is the interface name on which the packet was received.
	// It is the listener's device binding, empty for an unbound listener.
	IfName string

	// KernelRXUnixNano is a software CLOCK_REALTIME receive timestamp.
	// Zero means unavailable; convert clock domains before measuring latency.
	// It must never set a protocol detection deadline.
	KernelRXUnixNano int64

	// SocketDrops is the cumulative uint32 SO_RXQ_OVFL sample, not a delta.
	// SocketDropsKnown distinguishes an absent cmsg from a reported zero.
	SocketDrops      uint32
	SocketDropsKnown bool
	// SocketDropsInvalid excludes malformed or truncated ancillary data.
	SocketDropsInvalid bool
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
	ErrTTLInvalid = errors.New("TTL validation failed")

	// ErrPortExhausted indicates no source ports are available in the
	// RFC 5881 Section 4 range (49152-65535).
	ErrPortExhausted = errors.New("no source ports available in range 49152-65535")

	// ErrSocketClosed indicates an operation on a closed socket.
	ErrSocketClosed = errors.New("socket closed")

	// ErrUnsupportedPlatform indicates that the Linux-specific BFD transport
	// cannot run on the current operating system.
	ErrUnsupportedPlatform = errors.New("netio transport requires linux")

	// ErrPacketTruncated indicates that the complete UDP datagram did not fit the read buffer.
	ErrPacketTruncated = errors.New("BFD UDP datagram truncated")

	// ErrAncillaryTruncated indicates recvmsg lost required transport metadata.
	ErrAncillaryTruncated = errors.New("BFD UDP ancillary data truncated")

	// ErrInvalidReadSize indicates a transport returned a size outside its caller-owned buffer.
	ErrInvalidReadSize = errors.New("invalid packet read size")

	// ErrPoolType indicates the packet pool returned an unexpected type.
	ErrPoolType = errors.New("packet pool returned unexpected type")
)

// -------------------------------------------------------------------------
// GTSM Validation — RFC 5881 Section 5
// -------------------------------------------------------------------------

// ValidateTTL checks the received single-hop TTL (IPv4) or Hop Limit (IPv6)
// against RFC 5881 Section 5: received packets "MUST be discarded if the
// received TTL or Hop Limit is not equal to 255" (GTSM, RFC 5082). The rule
// is identical for both address families.
//
// RFC 5883 defines no receive TTL rule for multihop; that hop policy is
// applied per demultiplexed session (bfd.SessionConfig.MultiHopMaxHops).
func ValidateTTL(meta PacketMeta) error {
	if meta.TTL != ttlRequired {
		return fmt.Errorf(
			"single-hop TTL %d, required %d (RFC 5881 Section 5): %w",
			meta.TTL, ttlRequired, ErrTTLInvalid,
		)
	}
	return nil
}

// ValidateExpectedTTL checks that a received packet has an exact TTL/Hop Limit.
// RFC 9747 requires looped-back Unaffiliated BFD Echo packets to be received
// with TTL/Hop Limit 254.
func ValidateExpectedTTL(meta PacketMeta, expected uint8, context string) error {
	if meta.TTL != expected {
		return fmt.Errorf(
			"%s TTL %d, required %d: %w",
			context, meta.TTL, expected, ErrTTLInvalid,
		)
	}
	return nil
}
