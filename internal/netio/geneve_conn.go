package netio

// geneve_conn.go: Geneve tunnel connection for BFD (RFC 9521).
//
// GeneveConn implements OverlayConn for Geneve-encapsulated BFD sessions.
// It manages a UDP socket bound to port 6081 and handles the full
// encapsulation/decapsulation stack:
//
//	Outer UDP (dst 6081) | Geneve Header (8B+) | Inner Ethernet (14B) |
//	Inner IPv4 (20B) | Inner UDP (dst 3784) | BFD Control (24+B)
//
// Key RFC 9521 requirements:
//   - Geneve O bit (control) MUST be set to 1 (Section 4)
//   - Geneve C bit (critical) MUST be set to 0 (Section 4)
//   - Protocol Type 0x6558 for Format A (Ethernet payload)
//   - VNI integral to session demultiplexing
//   - Inner TTL=255 (RFC 5881 Section 5, GTSM)

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
)

// geneveBufSize is the receive buffer size for Geneve packets.
// Sized for jumbo frames (9000 bytes) to avoid truncation.
const geneveBufSize = 9000

// Geneve BFD validation errors.
var (
	// ErrGeneveOBitNotSet indicates the O bit (control) is not set in a
	// received Geneve header. RFC 9521 Section 4: O bit MUST be 1 for BFD.
	ErrGeneveOBitNotSet = errors.New("geneve: O bit (control) not set, required by RFC 9521")

	// ErrGeneveCBitSet indicates the C bit (critical) is set in a received
	// Geneve header. RFC 9521 Section 4: C bit MUST be 0 for BFD.
	ErrGeneveCBitSet = errors.New("geneve: C bit (critical) set, must be 0 per RFC 9521")

	// ErrGeneveUnexpectedProto indicates the Geneve Protocol Type is not
	// 0x6558 (Transparent Ethernet Bridging) for Format A.
	ErrGeneveUnexpectedProto = errors.New("geneve: unexpected protocol type, expected 0x6558")

	// ErrGeneveVAPIdentityUnavailable indicates that normative local VAP MAC/IP
	// identity is not yet represented by the Geneve configuration.
	ErrGeneveVAPIdentityUnavailable = errors.New("geneve: VAP MAC/IP identity is not configured")
)

// GeneveConn implements OverlayConn for BFD over Geneve tunnels (RFC 9521).
//
// The connection binds a UDP socket to localAddr:6081 and uses Geneve
// Format A (Ethernet payload, Protocol Type 0x6558) for BFD encapsulation.
//
// Thread safety: same model as VXLANConn. SendEncapsulated and
// RecvDecapsulated may be called concurrently. RecvDecapsulated is
// intended to be called by one receive loop at a time and reuses readBuf
// to avoid per-packet jumbo-buffer allocations. The mu mutex protects
// only the closed flag.
type GeneveConn struct {
	conn      *net.UDPConn
	vni       uint32
	localAddr netip.Addr
	srcPort   uint16 // Ephemeral source port for inner UDP header
	readBuf   []byte
	sendBuf   []byte
	sendMu    sync.Mutex
	logger    *slog.Logger
	mu        sync.Mutex
	closed    bool
}

// NewGeneveConn creates a Geneve tunnel connection for BFD.
//
// Parameters:
//   - localAddr: local NVE IP address to bind to
//   - vni: the VNI for BFD session demultiplexing (RFC 9521)
//   - srcPort: ephemeral source port for the inner UDP header
//   - logger: structured logger for Geneve operations
//
// The socket binds to localAddr:6081 (RFC 8926 Section 3.3).
func NewGeneveConn(
	localAddr netip.Addr,
	vni uint32,
	srcPort uint16,
	logger *slog.Logger,
) (*GeneveConn, error) {
	laddr := &net.UDPAddr{
		IP:   localAddr.AsSlice(),
		Port: int(GenevePort),
	}

	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		return nil, fmt.Errorf("geneve: bind %s:%d: %w", localAddr, GenevePort, err)
	}

	return &GeneveConn{
		conn:      conn,
		vni:       vni,
		localAddr: localAddr,
		srcPort:   srcPort,
		readBuf:   make([]byte, geneveBufSize),
		sendBuf:   make([]byte, geneveBufSize),
		logger: logger.With(
			slog.String("component", "netio.geneve_conn"),
			slog.String("local", localAddr.String()),
			slog.Uint64("vni", uint64(vni)),
		),
	}, nil
}

// SendEncapsulated wraps a BFD Control payload in Geneve encapsulation
// and sends it to the remote NVE.
//
// Packet stack built (outer to inner):
//  1. Geneve header (O=1, C=0, Protocol=0x6558, VNI)
//  2. Inner Ethernet + IPv4 + UDP + BFD payload (via BuildInnerPacket)
//
// The outer UDP packet is sent to dstAddr:6081.
//
// RFC 9521 Section 4: O bit MUST be set, C bit MUST be clear.
func (c *GeneveConn) SendEncapsulated(
	_ context.Context,
	bfdPayload []byte,
	dstAddr netip.Addr,
) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("geneve send to %s: %w", dstAddr, ErrOverlayRecvClosed)
	}
	c.mu.Unlock()
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if len(bfdPayload)+GeneveHeaderMinSize+InnerOverheadIPv4 > len(c.sendBuf) {
		return fmt.Errorf("geneve build packet: payload=%d: %w",
			len(bfdPayload), ErrInnerPacketBufferTooShort)
	}
	buf := c.sendBuf
	// Build inner packet: Ethernet + IPv4 + UDP + BFD in the owned buffer.
	innerPkt, err := BuildInnerPacketInto(
		buf[GeneveHeaderMinSize:], bfdPayload, c.localAddr, dstAddr, c.srcPort,
	)
	if err != nil {
		return fmt.Errorf("geneve build inner: %w", err)
	}

	// Build complete Geneve packet: Geneve header + inner packet.
	totalLen := GeneveHeaderMinSize + len(innerPkt)

	// Marshal Geneve header per RFC 9521 Section 4.
	geneveHdr := GeneveHeader{
		Version:      0,
		OptLen:       0,                      // No options for BFD.
		OBit:         true,                   // RFC 9521: O bit MUST be 1.
		CBit:         false,                  // RFC 9521: C bit MUST be 0.
		ProtocolType: GeneveProtocolEthernet, // Format A: Ethernet payload.
		VNI:          c.vni,
	}
	if _, err := MarshalGeneveHeader(buf[:GeneveHeaderMinSize], geneveHdr); err != nil {
		return fmt.Errorf("geneve marshal header: %w", err)
	}

	// Send to remote NVE on port 6081.
	dst := &net.UDPAddr{
		IP:   dstAddr.AsSlice(),
		Port: int(GenevePort),
	}
	if _, err := c.conn.WriteToUDP(buf[:totalLen], dst); err != nil {
		return fmt.Errorf("geneve send to %s:%d: %w", dstAddr, GenevePort, err)
	}

	return nil
}

// RecvDecapsulated detects datagram truncation, then fails closed until
// normative local VAP MAC/IP identity is represented by the Geneve
// configuration. The existing codec validation remains unreachable from this
// receive path until that identity is available.
func (c *GeneveConn) RecvDecapsulated(_ context.Context) ([]byte, OverlayMeta, error) {
	n, remoteAddr, err := readOverlayDatagram(c.conn, c.readBuf)
	if err != nil {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return nil, OverlayMeta{}, fmt.Errorf("geneve recv: %w", ErrOverlayRecvClosed)
		}
		return nil, OverlayMeta{}, fmt.Errorf("geneve recv: %w", err)
	}
	if err := c.validateVAPIdentity(); err != nil {
		return nil, OverlayMeta{}, fmt.Errorf("geneve recv: %w", err)
	}

	bfdPayload, geneveHdr, innerDst, ttl, err := c.decapGenevePacket(c.readBuf[:n])
	if err != nil {
		return nil, OverlayMeta{}, err
	}
	if err := validateInnerDestination(innerDst, c.localAddr); err != nil {
		return nil, OverlayMeta{}, fmt.Errorf("geneve recv: %w", err)
	}

	// Build overlay metadata from outer UDP source address.
	srcAddr, ok := netip.AddrFromSlice(remoteAddr.IP)
	if !ok {
		return nil, OverlayMeta{}, fmt.Errorf(
			"geneve recv: remote address %s: %w",
			remoteAddr.IP, ErrOverlayInvalidAddr)
	}

	meta := OverlayMeta{
		SrcAddr: srcAddr.Unmap(),
		DstAddr: c.localAddr,
		VNI:     geneveHdr.VNI,
		TTL:     ttl,
	}

	return bfdPayload, meta, nil
}

func (*GeneveConn) validateVAPIdentity() error {
	return ErrGeneveVAPIdentityUnavailable
}

// decapGenevePacket validates and strips Geneve + inner headers from a
// received packet, returning the BFD payload, parsed Geneve header, inner
// destination, and wire TTL.
func (c *GeneveConn) decapGenevePacket(data []byte) ([]byte, GeneveHeader, netip.Addr, uint8, error) {
	// Need at least Geneve min header to parse.
	if len(data) < GeneveHeaderMinSize {
		return nil, GeneveHeader{}, netip.Addr{}, 0, fmt.Errorf(
			"geneve recv: packet %d bytes, need at least %d: %w",
			len(data), GeneveHeaderMinSize, ErrGeneveHeaderTooShort)
	}

	// Parse Geneve header (validates version).
	geneveHdr, err := UnmarshalGeneveHeader(data[:GeneveHeaderMinSize])
	if err != nil {
		return nil, GeneveHeader{}, netip.Addr{}, 0, fmt.Errorf("geneve recv: %w", err)
	}

	// Total Geneve header size including options.
	geneveTotal := geneveHdr.TotalHeaderSize()
	if vErr := c.validateGeneveHeader(geneveHdr, len(data), geneveTotal); vErr != nil {
		return nil, GeneveHeader{}, netip.Addr{}, 0, vErr
	}

	// Strip inner packet headers and extract BFD payload.
	bfdPayload, _, innerDst, ttl, err := stripInnerPacket(data[geneveTotal:])
	if err != nil {
		return nil, GeneveHeader{}, netip.Addr{}, 0, fmt.Errorf("geneve recv: %w", err)
	}

	return bfdPayload, geneveHdr, innerDst, ttl, nil
}

// validateGeneveHeader checks RFC 9521 Section 4 requirements on a parsed
// Geneve header: packet length, O/C bits, protocol type, and VNI match.
func (c *GeneveConn) validateGeneveHeader(hdr GeneveHeader, pktLen, geneveTotal int) error {
	minSize := geneveTotal + InnerOverheadIPv4
	if pktLen < minSize {
		return fmt.Errorf(
			"geneve recv: packet %d bytes, need at least %d (hdr=%d + inner=%d): %w",
			pktLen, minSize, geneveTotal, InnerOverheadIPv4, ErrInnerPacketTooShort)
	}

	// RFC 9521 Section 4: O bit MUST be set for BFD.
	if !hdr.OBit {
		return fmt.Errorf("geneve recv: %w", ErrGeneveOBitNotSet)
	}

	// RFC 9521 Section 4: C bit MUST be clear for BFD.
	if hdr.CBit {
		return fmt.Errorf("geneve recv: %w", ErrGeneveCBitSet)
	}

	// Validate Protocol Type for Format A.
	if hdr.ProtocolType != GeneveProtocolEthernet {
		return fmt.Errorf(
			"geneve recv: protocol type 0x%04x: %w",
			hdr.ProtocolType, ErrGeneveUnexpectedProto)
	}

	// Validate VNI.
	if hdr.VNI != c.vni {
		return fmt.Errorf(
			"geneve recv: VNI %d, expected %d: %w",
			hdr.VNI, c.vni, ErrOverlayVNIMismatch)
	}

	return nil
}

// Close releases the underlying UDP socket.
func (c *GeneveConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("geneve close: %w", err)
	}
	return nil
}

// -------------------------------------------------------------------------
// Geneve encap/decap helpers (exported for testing)
// -------------------------------------------------------------------------

// BuildGenevePacket assembles a complete Geneve-encapsulated BFD packet.
// This is a convenience function that combines Geneve header marshaling
// and inner packet assembly. Exported for unit testing the encapsulation
// logic without requiring a real socket.
//
// Returns the complete packet: Geneve header + inner Ethernet + IPv4 + UDP + BFD.
//
// RFC 9521 Section 4: O=1, C=0, Protocol=0x6558 (Format A).
func BuildGenevePacket(
	bfdPayload []byte,
	vni uint32,
	srcIP, dstIP netip.Addr,
	srcPort uint16,
) ([]byte, error) {
	innerPkt, err := BuildInnerPacket(bfdPayload, srcIP, dstIP, srcPort)
	if err != nil {
		return nil, fmt.Errorf("build geneve packet: inner: %w", err)
	}

	totalLen := GeneveHeaderMinSize + len(innerPkt)
	buf := make([]byte, totalLen)

	geneveHdr := GeneveHeader{
		Version:      0,
		OptLen:       0,
		OBit:         true,
		CBit:         false,
		ProtocolType: GeneveProtocolEthernet,
		VNI:          vni,
	}

	if _, err := MarshalGeneveHeader(buf[:GeneveHeaderMinSize], geneveHdr); err != nil {
		return nil, fmt.Errorf("build geneve packet: header: %w", err)
	}

	copy(buf[GeneveHeaderMinSize:], innerPkt)
	return buf, nil
}

// ParseGenevePacket decapsulates a complete Geneve packet, returning the
// BFD payload, Geneve header fields, and inner IPs. Exported for unit testing.
func ParseGenevePacket(buf []byte) ([]byte, GeneveHeader, netip.Addr, netip.Addr, error) {
	if len(buf) < GeneveHeaderMinSize {
		return nil, GeneveHeader{}, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"parse geneve packet: %d bytes too short: %w",
			len(buf), ErrGeneveHeaderTooShort)
	}

	hdr, err := UnmarshalGeneveHeader(buf[:GeneveHeaderMinSize])
	if err != nil {
		return nil, GeneveHeader{}, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"parse geneve packet: %w", err)
	}

	geneveTotal := hdr.TotalHeaderSize()
	if len(buf) < geneveTotal+InnerOverheadIPv4 {
		return nil, GeneveHeader{}, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"parse geneve packet: %d bytes, need %d: %w",
			len(buf), geneveTotal+InnerOverheadIPv4, ErrInnerPacketTooShort)
	}

	bfdPayload, srcIP, dstIP, err := StripInnerPacket(buf[geneveTotal:])
	if err != nil {
		return nil, GeneveHeader{}, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"parse geneve packet: %w", err)
	}

	return bfdPayload, hdr, srcIP, dstIP, nil
}
