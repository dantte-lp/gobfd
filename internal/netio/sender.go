//go:build linux

package netio

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// UDPSender implements bfd.PacketSender by sending BFD Control packets
// over UDP. Each sender is bound to a specific local address and source
// port within the RFC 5881 Section 4 ephemeral range (49152-65535).
//
// Supports both IPv4 and IPv6. The address family is auto-detected from
// the local address:
//   - IPv4: IP_TTL = 255 (RFC 5881 Section 5, GTSM RFC 5082)
//   - IPv6: IPV6_UNICAST_HOPS = 255 (RFC 5881 Section 5, GTSM RFC 5082)
type UDPSender struct {
	conn     *net.UDPConn
	dstPort  uint16
	logger   *slog.Logger
	mu       sync.Mutex
	closed   bool
	srcPort  uint16
	multiHop bool
}

// NewUDPSender creates a sender for BFD packets from localAddr:srcPort.
// Supports both IPv4 and IPv6 addresses; the address family is auto-detected.
//
// The socket is configured with:
//   - IPv4: IP_TTL = 255 (RFC 5881 Section 5 / RFC 5082 GTSM)
//   - IPv6: IPV6_UNICAST_HOPS = 255 (RFC 5881 Section 5 / RFC 5082 GTSM)
//   - SO_REUSEADDR for compatibility with multiple BFD sessions
//
// Destination port is determined by multiHop:
//   - false: port 3784 (RFC 5881 Section 4, single-hop)
//   - true: port 4784 (RFC 5883 Section 2, multi-hop)
func NewUDPSender(
	localAddr netip.Addr,
	srcPort uint16,
	multiHop bool,
	logger *slog.Logger,
) (*UDPSender, error) {
	dstPort := PortSingleHop
	if multiHop {
		dstPort = PortMultiHop
	}

	conn, err := dialSenderSocket(localAddr, srcPort)
	if err != nil {
		return nil, fmt.Errorf("create UDP sender %s:%d: %w",
			localAddr, srcPort, err)
	}

	return &UDPSender{
		conn:     conn,
		dstPort:  dstPort,
		srcPort:  srcPort,
		multiHop: multiHop,
		logger: logger.With(
			slog.String("component", "netio.sender"),
			slog.String("local", localAddr.String()),
			slog.Uint64("src_port", uint64(srcPort)),
		),
	}, nil
}

// dialSenderSocket creates and configures a UDP socket for BFD TX.
// Auto-detects IPv4 vs IPv6 from the local address.
func dialSenderSocket(
	localAddr netip.Addr,
	srcPort uint16,
) (*net.UDPConn, error) {
	laddr := netip.AddrPortFrom(localAddr, srcPort)
	isIPv6 := localAddr.Is6() && !localAddr.Is4In6()

	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			return setSenderOpts(c, isIPv6)
		},
	}

	// Use explicit network to prevent dual-stack ambiguity.
	network := "udp4"
	if isIPv6 {
		network = "udp6"
	}

	pc, err := lc.ListenPacket(context.Background(), network, laddr.String())
	if err != nil {
		return nil, fmt.Errorf("listen UDP %s: %w", laddr, err)
	}

	conn, ok := pc.(*net.UDPConn)
	if !ok {
		closeErr := pc.Close()
		return nil, fmt.Errorf(
			"listen UDP %s: %w: %w",
			laddr, ErrUnexpectedConnType, closeErr,
		)
	}

	return conn, nil
}

// setSenderOpts configures socket options for BFD TX.
// For IPv4: sets IP_TTL = 255. For IPv6: sets IPV6_UNICAST_HOPS = 255.
func setSenderOpts(c syscall.RawConn, isIPv6 bool) error {
	var sockErr error

	err := c.Control(func(fd uintptr) {
		//nolint:gosec // G115: fd uintptr->int is safe; kernel FDs are always small positive integers.
		intFD := int(fd)

		// SO_REUSEADDR: allow address reuse.
		if sockErr = unix.SetsockoptInt(
			intFD, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1,
		); sockErr != nil {
			sockErr = fmt.Errorf("set SO_REUSEADDR: %w", sockErr)
			return
		}

		if isIPv6 {
			// IPV6_UNICAST_HOPS = 255: RFC 5881 Section 5 / RFC 5082 GTSM.
			if sockErr = unix.SetsockoptInt(
				intFD, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, int(ttlRequired),
			); sockErr != nil {
				sockErr = fmt.Errorf("set IPV6_UNICAST_HOPS: %w", sockErr)
			}
		} else {
			// IP_TTL = 255: RFC 5881 Section 5 / RFC 5082 GTSM.
			if sockErr = unix.SetsockoptInt(
				intFD, unix.IPPROTO_IP, unix.IP_TTL, int(ttlRequired),
			); sockErr != nil {
				sockErr = fmt.Errorf("set IP_TTL: %w", sockErr)
			}
		}
	})
	if err != nil {
		return fmt.Errorf("raw conn control: %w", err)
	}

	return sockErr
}

// SendPacket sends buf to the given peer address on the standard BFD
// destination port. For single-hop: port 3784 (RFC 5881 Section 4).
// For multi-hop: port 4784 (RFC 5883 Section 2).
//
// This method satisfies the bfd.PacketSender interface.
func (s *UDPSender) SendPacket(
	_ context.Context,
	buf []byte,
	addr netip.Addr,
) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("send to %s: %w", addr, ErrSocketClosed)
	}
	s.mu.Unlock()

	dst := net.UDPAddrFromAddrPort(netip.AddrPortFrom(addr, s.dstPort))

	if _, err := s.conn.WriteToUDP(buf, dst); err != nil {
		return fmt.Errorf("send BFD packet to %s:%d: %w",
			addr, s.dstPort, err)
	}

	return nil
}

// Close closes the underlying UDP connection.
func (s *UDPSender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("close sender socket: %w", err)
	}

	return nil
}

// SrcPort returns the allocated source port for this sender.
func (s *UDPSender) SrcPort() uint16 {
	return s.srcPort
}
