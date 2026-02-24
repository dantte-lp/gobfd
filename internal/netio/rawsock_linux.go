//go:build linux

package netio

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// -------------------------------------------------------------------------
// LinuxPacketConn — RFC 5881 Section 5 socket requirements
// -------------------------------------------------------------------------

// LinuxPacketConn implements PacketConn using platform-specific raw sockets.
//
// For IPv4 (RFC 5881 Section 5):
//  1. IP_TTL = 255 on TX (GTSM, RFC 5082)
//  2. IP_RECVTTL for TTL in ancillary data
//  3. IP_PKTINFO for destination address and interface
//  4. SO_BINDTODEVICE for interface binding (single-hop only)
//  5. SO_REUSEADDR for multiple listeners on the same port
//
// For IPv6 (RFC 5881 Section 5, applied to IPv6):
//  1. IPV6_UNICAST_HOPS = 255 on TX (GTSM, RFC 5082)
//  2. IPV6_RECVHOPLIMIT for hop limit in ancillary data
//  3. IPV6_RECVPKTINFO for destination address and interface
//  4. SO_BINDTODEVICE for interface binding (single-hop only)
//  5. SO_REUSEADDR for multiple listeners on the same port
type LinuxPacketConn struct {
	conn      *net.UDPConn
	localAddr netip.AddrPort
	ifName    string
	multiHop  bool
	closed    bool
	mu        sync.Mutex
}

// ReadPacket reads a single BFD Control packet from the UDP socket.
// Returns the number of bytes read and transport metadata extracted
// from ancillary data (TTL, source/destination addresses, interface).
func (c *LinuxPacketConn) ReadPacket(buf []byte) (int, PacketMeta, error) {
	oob := make([]byte, oobSize)

	n, oobn, _, src, err := c.conn.ReadMsgUDP(buf, oob)
	if err != nil {
		return 0, PacketMeta{}, fmt.Errorf("read BFD packet: %w", err)
	}

	meta := parseMeta(src, oob[:oobn])
	meta.IfName = c.ifName

	return n, meta, nil
}

// WritePacket sends a BFD Control packet to the given destination address.
// TTL is set to 255 at socket creation time per RFC 5881 Section 5.
func (c *LinuxPacketConn) WritePacket(buf []byte, dst netip.Addr) error {
	port := PortSingleHop
	if c.multiHop {
		port = PortMultiHop
	}

	udpAddr := net.UDPAddrFromAddrPort(netip.AddrPortFrom(dst, port))

	_, err := c.conn.WriteToUDP(buf, udpAddr)
	if err != nil {
		return fmt.Errorf("write BFD packet to %s: %w", dst, err)
	}

	return nil
}

// Close releases the underlying socket.
func (c *LinuxPacketConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("close BFD socket: %w", err)
	}
	return nil
}

// LocalAddr returns the local address and port the socket is bound to.
func (c *LinuxPacketConn) LocalAddr() netip.AddrPort {
	return c.localAddr
}

// -------------------------------------------------------------------------
// Constructors — RFC 5881 Section 4-5
// -------------------------------------------------------------------------

// NewSingleHopListener creates a PacketConn for single-hop BFD (RFC 5881).
// Supports both IPv4 and IPv6 addresses; the address family is auto-detected.
//
// Socket configuration per RFC 5881 Section 4-5:
//   - Binds to UDP port 3784 on the specified address
//   - IPv4: IP_TTL = 255, IP_RECVTTL, IP_PKTINFO (RFC 5082 GTSM)
//   - IPv6: IPV6_UNICAST_HOPS = 255, IPV6_RECVHOPLIMIT, IPV6_RECVPKTINFO
//   - SO_BINDTODEVICE set to ifName for interface-specific binding
//   - SO_REUSEADDR for multiple listeners
func NewSingleHopListener(
	ctx context.Context,
	addr netip.Addr,
	ifName string,
) (*LinuxPacketConn, error) {
	laddr := netip.AddrPortFrom(addr, PortSingleHop)

	conn, err := listenUDP(ctx, laddr, ifName, false)
	if err != nil {
		return nil, fmt.Errorf("single-hop listener on %s%%%s: %w", laddr, ifName, err)
	}

	return &LinuxPacketConn{
		conn:      conn,
		localAddr: laddr,
		ifName:    ifName,
		multiHop:  false,
	}, nil
}

// NewMultiHopListener creates a PacketConn for multi-hop BFD (RFC 5883).
// Supports both IPv4 and IPv6 addresses; the address family is auto-detected.
//
// Socket configuration per RFC 5883 Section 2:
//   - Binds to UDP port 4784 on the specified address
//   - IPv4: IP_TTL = 255, IP_RECVTTL, IP_PKTINFO
//   - IPv6: IPV6_UNICAST_HOPS = 255, IPV6_RECVHOPLIMIT, IPV6_RECVPKTINFO
//   - No SO_BINDTODEVICE (multi-hop is not interface-specific)
func NewMultiHopListener(
	ctx context.Context,
	addr netip.Addr,
) (*LinuxPacketConn, error) {
	laddr := netip.AddrPortFrom(addr, PortMultiHop)

	conn, err := listenUDP(ctx, laddr, "", true)
	if err != nil {
		return nil, fmt.Errorf("multi-hop listener on %s: %w", laddr, err)
	}

	return &LinuxPacketConn{
		conn:      conn,
		localAddr: laddr,
		multiHop:  true,
	}, nil
}

// NewGenericListener creates a PacketConn for BFD on a specified port.
// Supports both IPv4 and IPv6 addresses; the address family is auto-detected.
//
// This is used for non-standard BFD port listeners:
//   - RFC 7130 micro-BFD (port 6784): per-member-link sessions
//   - RFC 9747 echo (port 3785): echo packet reception
//
// Socket configuration matches single-hop (RFC 5881 Section 5):
//   - IPv4: IP_TTL = 255, IP_RECVTTL, IP_PKTINFO (RFC 5082 GTSM)
//   - IPv6: IPV6_UNICAST_HOPS = 255, IPV6_RECVHOPLIMIT, IPV6_RECVPKTINFO
//   - SO_BINDTODEVICE set to ifName when non-empty
//   - SO_REUSEADDR for multiple listeners
func NewGenericListener(
	ctx context.Context,
	addr netip.Addr,
	ifName string,
	port uint16,
) (*LinuxPacketConn, error) {
	laddr := netip.AddrPortFrom(addr, port)

	conn, err := listenUDP(ctx, laddr, ifName, false)
	if err != nil {
		return nil, fmt.Errorf("generic listener on %s%%%s port %d: %w",
			laddr, ifName, port, err)
	}

	return &LinuxPacketConn{
		conn:      conn,
		localAddr: laddr,
		ifName:    ifName,
		multiHop:  false,
	}, nil
}

// -------------------------------------------------------------------------
// Socket creation helpers
// -------------------------------------------------------------------------

// oobSize is the buffer size for ancillary (out-of-band) data.
// Must accommodate the largest control message set:
// IPv4: IP_PKTINFO (28 bytes) + IP_TTL (16 bytes) = 44 bytes
// IPv6: IPV6_PKTINFO (36 bytes) + IPV6_HOPLIMIT (16 bytes) = 52 bytes
// Rounded up to 64 for alignment safety.
const oobSize = 64

// ErrUnexpectedConnType indicates the net.ListenPacket returned an
// unexpected connection type instead of *net.UDPConn.
var ErrUnexpectedConnType = errors.New("unexpected connection type from ListenPacket")

// listenUDP creates and configures a UDP socket with BFD-required options.
// Auto-detects IPv4 vs IPv6 from the bind address and applies the
// appropriate socket options (IP_TTL vs IPV6_UNICAST_HOPS, etc.).
func listenUDP(ctx context.Context, laddr netip.AddrPort, ifName string, multiHop bool) (*net.UDPConn, error) {
	isIPv6 := laddr.Addr().Is6() && !laddr.Addr().Is4In6()

	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			return setSocketOpts(c, ifName, multiHop, isIPv6)
		},
	}

	// Use explicit network to prevent dual-stack ambiguity.
	// RFC 5881 Section 4: separate listeners for IPv4 and IPv6.
	network := "udp4"
	if isIPv6 {
		network = "udp6"
	}

	pc, err := lc.ListenPacket(ctx, network, laddr.String())
	if err != nil {
		return nil, fmt.Errorf("listen UDP %s: %w", laddr, err)
	}

	conn, ok := pc.(*net.UDPConn)
	if !ok {
		closeErr := pc.Close()
		return nil, errors.Join(
			fmt.Errorf("listen UDP %s: %w", laddr, ErrUnexpectedConnType),
			closeErr,
		)
	}

	return conn, nil
}

// setSocketOpts configures BFD-required socket options via the Control callback.
//
// For IPv4 (RFC 5881 Section 5, RFC 5082):
//   - SO_REUSEADDR, IP_TTL = 255, IP_RECVTTL, IP_PKTINFO
//   - SO_BINDTODEVICE (single-hop only)
//
// For IPv6 (RFC 5881 Section 5, applied to IPv6):
//   - SO_REUSEADDR, IPV6_UNICAST_HOPS = 255, IPV6_RECVHOPLIMIT, IPV6_RECVPKTINFO
//   - SO_BINDTODEVICE (single-hop only)
func setSocketOpts(c syscall.RawConn, ifName string, multiHop, isIPv6 bool) error {
	var sockErr error

	err := c.Control(func(fd uintptr) {
		//nolint:gosec // G115: fd uintptr->int is safe; kernel FDs are always small positive integers.
		intFD := int(fd)
		if isIPv6 {
			sockErr = applySockOptsV6(intFD, ifName, multiHop)
		} else {
			sockErr = applySockOptsV4(intFD, ifName, multiHop)
		}
	})
	if err != nil {
		return fmt.Errorf("raw conn control: %w", err)
	}

	return sockErr
}

// applySockOptsCommon sets socket options shared by IPv4 and IPv6.
func applySockOptsCommon(fd int, ifName string, multiHop bool) error {
	// SO_REUSEADDR: allow address reuse for multiple BFD listeners.
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		return fmt.Errorf("set SO_REUSEADDR: %w", err)
	}

	// SO_BINDTODEVICE: bind to specific interface (single-hop only).
	// RFC 5881 Section 4 requires interface-specific binding.
	if !multiHop && ifName != "" {
		if err := unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, ifName); err != nil {
			return fmt.Errorf("set SO_BINDTODEVICE(%s): %w", ifName, err)
		}
	}

	return nil
}

// applySockOptsV4 sets IPv4-specific socket options on the file descriptor.
// RFC 5881 Section 5 / RFC 5082 GTSM for IPv4.
func applySockOptsV4(fd int, ifName string, multiHop bool) error {
	if err := applySockOptsCommon(fd, ifName, multiHop); err != nil {
		return err
	}

	// IP_TTL = 255: RFC 5881 Section 5 / RFC 5082 GTSM.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_TTL, int(ttlRequired)); err != nil {
		return fmt.Errorf("set IP_TTL: %w", err)
	}

	// IP_RECVTTL: receive TTL in ancillary data for GTSM validation.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_RECVTTL, 1); err != nil {
		return fmt.Errorf("set IP_RECVTTL: %w", err)
	}

	// IP_PKTINFO: receive destination address and interface index.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_PKTINFO, 1); err != nil {
		return fmt.Errorf("set IP_PKTINFO: %w", err)
	}

	return nil
}

// applySockOptsV6 sets IPv6-specific socket options on the file descriptor.
// RFC 5881 Section 5 applied to IPv6: IPV6_UNICAST_HOPS replaces IP_TTL,
// IPV6_RECVHOPLIMIT replaces IP_RECVTTL, IPV6_RECVPKTINFO replaces IP_PKTINFO.
func applySockOptsV6(fd int, ifName string, multiHop bool) error {
	if err := applySockOptsCommon(fd, ifName, multiHop); err != nil {
		return err
	}

	// IPV6_UNICAST_HOPS = 255: RFC 5881 Section 5 / RFC 5082 GTSM.
	// Equivalent to IP_TTL for IPv6 — sets the Hop Limit on outgoing packets.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, int(ttlRequired)); err != nil {
		return fmt.Errorf("set IPV6_UNICAST_HOPS: %w", err)
	}

	// IPV6_RECVHOPLIMIT: receive hop limit in ancillary data for GTSM validation.
	// Equivalent to IP_RECVTTL for IPv6.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_RECVHOPLIMIT, 1); err != nil {
		return fmt.Errorf("set IPV6_RECVHOPLIMIT: %w", err)
	}

	// IPV6_RECVPKTINFO: receive destination address and interface index.
	// Equivalent to IP_PKTINFO for IPv6. Returns struct in6_pktinfo.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, 1); err != nil {
		return fmt.Errorf("set IPV6_RECVPKTINFO: %w", err)
	}

	return nil
}

// parseMeta extracts transport metadata from the source address and
// out-of-band ancillary data. Handles both IPv4 (IP_RECVTTL, IP_PKTINFO)
// and IPv6 (IPV6_HOPLIMIT, IPV6_PKTINFO) control messages.
func parseMeta(src *net.UDPAddr, oob []byte) PacketMeta {
	meta := PacketMeta{}

	if src != nil {
		srcAddr, ok := netip.AddrFromSlice(src.IP)
		if ok {
			meta.SrcAddr = srcAddr.Unmap()
		}
	}

	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return meta
	}

	parseControlMessages(msgs, &meta)

	return meta
}

// parseControlMessages extracts TTL/HopLimit and PKTINFO from socket
// control messages. Handles both IPv4 and IPv6 ancillary data:
//   - IPv4: IP_TTL (IPPROTO_IP) + IP_PKTINFO (struct in_pktinfo)
//   - IPv6: IPV6_HOPLIMIT (IPPROTO_IPV6) + IPV6_PKTINFO (struct in6_pktinfo)
func parseControlMessages(msgs []unix.SocketControlMessage, meta *PacketMeta) {
	for i := range msgs {
		switch {
		// IPv4 TTL.
		case msgs[i].Header.Level == unix.IPPROTO_IP && msgs[i].Header.Type == unix.IP_TTL:
			parseTTLMessage(msgs[i].Data, meta)
		// IPv4 PKTINFO.
		case msgs[i].Header.Level == unix.IPPROTO_IP && msgs[i].Header.Type == unix.IP_PKTINFO:
			parsePktInfoMessage(msgs[i].Data, meta)
		// IPv6 Hop Limit — equivalent to IPv4 TTL for GTSM (RFC 5082).
		case msgs[i].Header.Level == unix.IPPROTO_IPV6 && msgs[i].Header.Type == unix.IPV6_HOPLIMIT:
			parseHopLimitMessage(msgs[i].Data, meta)
		// IPv6 PKTINFO — equivalent to IPv4 IP_PKTINFO (struct in6_pktinfo).
		case msgs[i].Header.Level == unix.IPPROTO_IPV6 && msgs[i].Header.Type == unix.IPV6_PKTINFO:
			parsePktInfo6Message(msgs[i].Data, meta)
		}
	}
}

// parseTTLMessage extracts the TTL value from an IP_TTL control message.
// The kernel returns TTL as a 4-byte int in native byte order.
func parseTTLMessage(data []byte, meta *PacketMeta) {
	if len(data) >= 1 {
		meta.TTL = data[0]
	}
}

// parsePktInfoMessage extracts destination address and interface index from
// an IP_PKTINFO control message (struct in_pktinfo).
func parsePktInfoMessage(data []byte, meta *PacketMeta) {
	// struct in_pktinfo is 12 bytes:
	//   int       ipi_ifindex  (4 bytes, little-endian on x86)
	//   in_addr   ipi_spec_dst (4 bytes)
	//   in_addr   ipi_addr     (4 bytes)
	const pktInfoSize = 12
	if len(data) < pktInfoSize {
		return
	}

	// ipi_ifindex is a C int (4 bytes, native endian). On Linux x86/x86_64
	// this is little-endian. Values are always small positive integers.
	ifIdx := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	meta.IfIndex = int(ifIdx)

	// ipi_addr is the destination address (bytes 8-11, network byte order).
	var ip4 [4]byte
	copy(ip4[:], data[8:12])
	meta.DstAddr = netip.AddrFrom4(ip4)
}

// parseHopLimitMessage extracts the IPv6 Hop Limit from an IPV6_HOPLIMIT
// control message. The kernel returns hop limit as a 4-byte int in native
// byte order. The hop limit is stored in PacketMeta.TTL — the field serves
// as TTL for IPv4 and Hop Limit for IPv6 (semantically equivalent per
// RFC 5082 GTSM).
func parseHopLimitMessage(data []byte, meta *PacketMeta) {
	if len(data) >= 1 {
		meta.TTL = data[0]
	}
}

// parsePktInfo6Message extracts destination address and interface index from
// an IPV6_PKTINFO control message (struct in6_pktinfo).
func parsePktInfo6Message(data []byte, meta *PacketMeta) {
	// struct in6_pktinfo is 20 bytes:
	//   struct in6_addr ipi6_addr    (16 bytes, network byte order)
	//   unsigned int    ipi6_ifindex (4 bytes, native endian)
	const pktInfo6Size = 20
	if len(data) < pktInfo6Size {
		return
	}

	// ipi6_addr: 16-byte IPv6 address at offset 0.
	var ip6 [16]byte
	copy(ip6[:], data[0:16])
	meta.DstAddr = netip.AddrFrom16(ip6)

	// ipi6_ifindex at offset 16 (4 bytes, native endian / little-endian on x86).
	ifIdx := uint32(data[16]) | uint32(data[17])<<8 | uint32(data[18])<<16 | uint32(data[19])<<24
	meta.IfIndex = int(ifIdx)
}

// -------------------------------------------------------------------------
// SourcePortAllocator — RFC 5881 Section 4
// -------------------------------------------------------------------------

// SourcePortAllocator manages ephemeral source ports for BFD sessions.
//
// RFC 5881 Section 4: "The source port MUST be in the range 49152
// through 65535." This allocator tracks which ports are in use and
// provides thread-safe allocation and release.
type SourcePortAllocator struct {
	mu       sync.Mutex
	inUse    map[uint16]struct{}
	portSpan int
}

// NewSourcePortAllocator creates a new source port allocator covering the
// RFC 5881 Section 4 ephemeral range (49152-65535).
func NewSourcePortAllocator() *SourcePortAllocator {
	return &SourcePortAllocator{
		inUse:    make(map[uint16]struct{}),
		portSpan: int(sourcePortMax) - int(sourcePortMin) + 1,
	}
}

// Allocate returns an unused port in the range [49152, 65535].
// Returns ErrPortExhausted if all ports are in use.
//
// Uses random probing to avoid predictable port sequences, which is
// a security best practice for network protocols.
func (a *SourcePortAllocator) Allocate() (uint16, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.inUse) >= a.portSpan {
		return 0, fmt.Errorf("all %d ports allocated: %w", a.portSpan, ErrPortExhausted)
	}

	// Random starting offset for probe, then linear scan.
	//nolint:gosec // G404: port selection does not require cryptographic randomness.
	offset := rand.IntN(a.portSpan)

	for i := range a.portSpan {
		//nolint:gosec // G115: (offset+i)%portSpan is always in [0, 16383], fits uint16 after adding sourcePortMin.
		port := sourcePortMin + uint16((offset+i)%a.portSpan)
		if _, used := a.inUse[port]; !used {
			a.inUse[port] = struct{}{}
			return port, nil
		}
	}

	return 0, fmt.Errorf("all %d ports allocated: %w", a.portSpan, ErrPortExhausted)
}

// Release returns a port to the available pool.
// Releasing an unallocated port is a no-op.
func (a *SourcePortAllocator) Release(port uint16) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.inUse, port)
}
