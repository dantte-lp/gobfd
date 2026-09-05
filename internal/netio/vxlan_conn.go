package netio

// vxlan_conn.go: VXLAN tunnel connection for BFD (RFC 8971).
//
// VXLANConn implements OverlayConn for VXLAN-encapsulated BFD sessions.
// It manages a UDP socket bound to port 4789 and handles the full
// encapsulation/decapsulation stack:
//
//	Outer UDP (dst 4789) | VXLAN Header (8B) | Inner Ethernet (14B) |
//	Inner IPv4 (20B) | Inner UDP (dst 3784) | BFD Control (24+B)
//
// Key RFC 8971 requirements:
//   - BFD packets use a dedicated Management VNI (Section 3)
//   - Inner destination MAC: 00:52:02:00:00:00 (Section 3.1)
//   - Inner TTL=255 (RFC 5881 Section 5, GTSM)
//   - Management VNI packets are processed locally, not forwarded

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// vxlanBufSize is the receive buffer size for VXLAN packets.
// Sized for jumbo frames (9000 bytes) to avoid truncation.
const vxlanBufSize = 9000

// VXLANConn implements OverlayConn for BFD over VXLAN tunnels (RFC 8971).
//
// The connection binds a UDP socket to localAddr:4789 and encapsulates/
// decapsulates BFD Control packets in the VXLAN Format A packet stack.
//
// Thread safety: SendEncapsulated and RecvDecapsulated may be called
// concurrently from separate goroutines (TX from session goroutine,
// RX from OverlayReceiver goroutine). The underlying net.UDPConn is
// safe for concurrent use. RecvDecapsulated is intended to be called by
// one receive loop at a time and reuses readBuf to avoid per-packet
// jumbo-buffer allocations. The mu mutex protects the closed flag only.
type VXLANConn struct {
	conn          *net.UDPConn
	managementVNI uint32
	localAddr     netip.Addr
	srcPort       uint16 // Ephemeral source port for inner UDP header
	readBuf       []byte
	sendBuf       []byte
	sendMu        sync.Mutex
	logger        *slog.Logger
	mu            sync.Mutex
	closed        bool
	scopes        map[overlayReceiveKey]bfd.TransportScope
}

// NewVXLANConn creates a VXLAN tunnel connection for BFD.
//
// Parameters:
//   - localAddr: local VTEP IP address to bind to
//   - managementVNI: the dedicated Management VNI for BFD (RFC 8971 Section 3)
//   - srcPort: ephemeral source port for the inner UDP header
//   - logger: structured logger for VXLAN operations
//
// The socket binds to localAddr:4789 (RFC 7348 Section 5).
func NewVXLANConn(
	localAddr netip.Addr,
	managementVNI uint32,
	srcPort uint16,
	logger *slog.Logger,
) (*VXLANConn, error) {
	return newVXLANConn(localAddr, managementVNI, srcPort, nil, logger)
}

// NewVXLANConnForScopes creates one listener for a local VTEP and binds every
// accepted packet and send operation to an exact configured tunnel identity.
func NewVXLANConnForScopes(
	localAddr netip.Addr,
	srcPort uint16,
	scopes []bfd.TransportScope,
	logger *slog.Logger,
) (*VXLANConn, error) {
	return newVXLANConn(localAddr, 0, srcPort, scopes, logger)
}

func newVXLANConn(
	localAddr netip.Addr,
	managementVNI uint32,
	srcPort uint16,
	scopes []bfd.TransportScope,
	logger *slog.Logger,
) (*VXLANConn, error) {
	conn, scopeMap, err := openScopedOverlayUDP(
		localAddr, VXLANPort, bfd.TransportScopeVXLAN, scopes,
	)
	if err != nil {
		return nil, fmt.Errorf("vxlan: open %s:%d: %w", localAddr, VXLANPort, err)
	}

	c := &VXLANConn{
		conn:          conn,
		managementVNI: managementVNI,
		localAddr:     localAddr,
		srcPort:       srcPort,
		readBuf:       make([]byte, vxlanBufSize),
		sendBuf:       make([]byte, vxlanBufSize),
		logger: logger.With(
			slog.String("component", "netio.vxlan_conn"),
			slog.String("local", localAddr.String()),
			slog.Uint64("mgmt_vni", uint64(managementVNI)),
		),
		scopes: scopeMap,
	}
	return c, nil
}

// SendEncapsulated wraps a BFD Control payload in VXLAN encapsulation
// and sends it to the remote VTEP.
//
// Packet stack built (outer to inner):
//  1. VXLAN header with Management VNI
//  2. Inner Ethernet + IPv4 + UDP + BFD payload (via BuildInnerPacket)
//
// The outer UDP packet is sent to dstAddr:4789.
//
// RFC 8971 Section 3: BFD packets MUST use the Management VNI.
func (c *VXLANConn) SendEncapsulated(
	_ context.Context,
	bfdPayload []byte,
	dstAddr netip.Addr,
) error {
	scope := bfd.TransportScope{
		Kind: bfd.TransportScopeVXLAN, VNI: c.managementVNI,
		OuterPeerAddr: dstAddr, OuterLocalAddr: c.localAddr,
		InnerPeerAddr: dstAddr, InnerLocalAddr: c.localAddr,
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       innerSrcMAC, LocalMAC: innerSrcMAC,
	}
	return c.sendEncapsulated(bfdPayload, scope)
}

// SendEncapsulatedFor sends using an exact configured VXLAN identity.
func (c *VXLANConn) SendEncapsulatedFor(
	_ context.Context,
	bfdPayload []byte,
	scope bfd.TransportScope,
) error {
	if c.scopes == nil {
		return fmt.Errorf("vxlan send: %w", ErrOverlayIdentityMismatch)
	}
	configured, ok := c.scopes[receiveKeyForScope(scope)]
	if !ok || configured != scope {
		return fmt.Errorf("vxlan send: %w", ErrOverlayIdentityMismatch)
	}
	return c.sendEncapsulated(bfdPayload, scope)
}

func (c *VXLANConn) sendEncapsulated(bfdPayload []byte, scope bfd.TransportScope) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("vxlan send to %s: %w", scope.OuterPeerAddr, ErrOverlayRecvClosed)
	}
	c.mu.Unlock()
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if len(bfdPayload)+VXLANHeaderSize+InnerOverheadIPv4 > len(c.sendBuf) {
		return fmt.Errorf("vxlan build packet: payload=%d: %w",
			len(bfdPayload), ErrInnerPacketBufferTooShort)
	}
	buf := c.sendBuf
	// Build inner packet: Ethernet + IPv4 + UDP + BFD in the owned buffer.
	innerPkt, err := buildInnerPacketInto(
		buf[VXLANHeaderSize:], bfdPayload,
		scope.InnerLocalAddr, scope.InnerPeerAddr, c.srcPort,
		scope.LocalMAC, innerDstMAC,
	)
	if err != nil {
		return fmt.Errorf("vxlan build inner: %w", err)
	}

	// Build complete VXLAN packet: VXLAN header + inner packet.
	totalLen := VXLANHeaderSize + len(innerPkt)

	// Marshal VXLAN header with Management VNI.
	if _, err := MarshalVXLANHeader(buf[:VXLANHeaderSize], scope.VNI); err != nil {
		return fmt.Errorf("vxlan marshal header: %w", err)
	}

	// Send to remote VTEP on port 4789.
	dst := &net.UDPAddr{
		IP:   scope.OuterPeerAddr.AsSlice(),
		Port: int(VXLANPort),
	}
	if _, err := c.conn.WriteToUDP(buf[:totalLen], dst); err != nil {
		return fmt.Errorf("vxlan send to %s:%d: %w", scope.OuterPeerAddr, VXLANPort, err)
	}

	return nil
}

// RecvDecapsulated reads a VXLAN packet, strips the VXLAN header and inner
// packet headers, and returns the raw BFD Control payload with overlay metadata.
//
// Validation performed:
//   - VXLAN header I flag (VNI valid)
//   - VNI matches the configured Management VNI (RFC 8971 Section 3)
//   - Complete supported Format A inner Ethernet, IPv4, and UDP framing
//
// Packets with non-matching VNI are silently dropped (they belong to
// data-plane VNIs, not BFD management traffic). Datagram truncation is
// detected before parsing.
func (c *VXLANConn) RecvDecapsulated(_ context.Context) ([]byte, OverlayMeta, error) {
	n, remoteAddr, err := readOverlayDatagram(c.conn, c.readBuf)
	if err != nil {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return nil, OverlayMeta{}, fmt.Errorf("vxlan recv: %w", ErrOverlayRecvClosed)
		}
		return nil, OverlayMeta{}, fmt.Errorf("vxlan recv: %w", err)
	}

	return c.decapVXLANDatagram(c.readBuf[:n], remoteAddr)
}

func (c *VXLANConn) decapVXLANDatagram(
	data []byte,
	remoteAddr *net.UDPAddr,
) ([]byte, OverlayMeta, error) {
	// Need at least VXLAN header + minimum inner overhead.
	minSize := VXLANHeaderSize + InnerOverheadIPv4
	if len(data) < minSize {
		return nil, OverlayMeta{}, fmt.Errorf(
			"vxlan recv: packet %d bytes, need at least %d: %w",
			len(data), minSize, ErrInnerPacketTooShort)
	}

	// Parse VXLAN header.
	vxlanHdr, err := UnmarshalVXLANHeader(data[:VXLANHeaderSize])
	if err != nil {
		return nil, OverlayMeta{}, fmt.Errorf("vxlan recv: %w", err)
	}

	// Validate Management VNI for the legacy single-profile connection.
	if c.scopes == nil && vxlanHdr.VNI != c.managementVNI {
		return nil, OverlayMeta{}, fmt.Errorf(
			"vxlan recv: VNI %d, expected management VNI %d: %w",
			vxlanHdr.VNI, c.managementVNI, ErrOverlayVNIMismatch)
	}

	// Strip inner packet headers and extract BFD payload.
	innerData := data[VXLANHeaderSize:]
	packet, err := parseInnerPacket(innerData)
	if err != nil {
		return nil, OverlayMeta{}, fmt.Errorf("vxlan recv: %w", err)
	}

	// Build overlay metadata from outer UDP source address.
	srcAddr, ok := netip.AddrFromSlice(remoteAddr.IP)
	if !ok {
		return nil, OverlayMeta{}, fmt.Errorf(
			"vxlan recv: remote address %s: %w",
			remoteAddr.IP, ErrOverlayInvalidAddr)
	}

	meta := OverlayMeta{
		SrcAddr: srcAddr.Unmap(),
		DstAddr: c.localAddr,
		VNI:     vxlanHdr.VNI,
		TTL:     packet.ttl,
	}
	if c.scopes == nil {
		if packet.dstMAC != innerDstMAC {
			return nil, OverlayMeta{}, fmt.Errorf("vxlan recv: %w", ErrInnerBadDestinationMAC)
		}
		if err := validateInnerDestination(packet.dstIP, c.localAddr); err != nil {
			return nil, OverlayMeta{}, fmt.Errorf("vxlan recv: %w", err)
		}
		return packet.payload, meta, nil
	}
	key := receiveKeyForPacket(
		bfd.TransportScopeVXLAN, srcAddr, c.localAddr, vxlanHdr.VNI, packet,
	)
	scope, ok := c.scopes[key]
	if !ok {
		return nil, OverlayMeta{}, fmt.Errorf("vxlan recv: %w", ErrOverlayIdentityMismatch)
	}
	meta.TransportScope = scope

	return packet.payload, meta, nil
}

// Close releases the underlying UDP socket.
func (c *VXLANConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("vxlan close: %w", err)
	}
	return nil
}

// -------------------------------------------------------------------------
// VXLAN encap/decap helpers (exported for testing)
// -------------------------------------------------------------------------

// BuildVXLANPacket assembles a complete VXLAN-encapsulated BFD packet.
// This is a convenience function that combines VXLAN header marshaling
// and inner packet assembly. Exported for unit testing the encapsulation
// logic without requiring a real socket.
//
// Returns the complete packet: VXLAN header + inner Ethernet + IPv4 + UDP + BFD.
func BuildVXLANPacket(
	bfdPayload []byte,
	vni uint32,
	srcIP, dstIP netip.Addr,
	srcPort uint16,
) ([]byte, error) {
	innerPkt, err := BuildInnerPacket(bfdPayload, srcIP, dstIP, srcPort)
	if err != nil {
		return nil, fmt.Errorf("build vxlan packet: inner: %w", err)
	}

	totalLen := VXLANHeaderSize + len(innerPkt)
	buf := make([]byte, totalLen)

	if _, err := MarshalVXLANHeader(buf[:VXLANHeaderSize], vni); err != nil {
		return nil, fmt.Errorf("build vxlan packet: header: %w", err)
	}

	copy(buf[VXLANHeaderSize:], innerPkt)
	return buf, nil
}

// ParseVXLANPacket decapsulates a complete VXLAN packet, returning the
// BFD payload, VNI, and inner IPs. Exported for unit testing.
func ParseVXLANPacket(buf []byte) ([]byte, uint32, netip.Addr, netip.Addr, error) {
	if len(buf) < VXLANHeaderSize+InnerOverheadIPv4 {
		return nil, 0, netip.Addr{}, netip.Addr{}, fmt.Errorf(
			"parse vxlan packet: %d bytes too short: %w",
			len(buf), ErrInnerPacketTooShort)
	}

	hdr, err := UnmarshalVXLANHeader(buf[:VXLANHeaderSize])
	if err != nil {
		return nil, 0, netip.Addr{}, netip.Addr{}, fmt.Errorf("parse vxlan packet: %w", err)
	}

	bfdPayload, srcIP, dstIP, err := StripInnerPacket(buf[VXLANHeaderSize:])
	if err != nil {
		return nil, 0, netip.Addr{}, netip.Addr{}, fmt.Errorf("parse vxlan packet: %w", err)
	}

	return bfdPayload, hdr.VNI, srcIP, dstIP, nil
}
