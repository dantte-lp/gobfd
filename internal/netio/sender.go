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
	conn       *net.UDPConn
	dstPort    uint16
	logger     *slog.Logger
	mu         sync.Mutex
	closed     bool
	srcPort    uint16
	multiHop   bool
	dfBit      bool   // RFC 9764: Don't Fragment bit for path MTU verification
	bindDevice string // SO_BINDTODEVICE interface name (RFC 7130 micro-BFD per-member)
}

// SenderOption configures optional UDPSender parameters.
type SenderOption func(*UDPSender)

// WithDFBit enables the Don't Fragment bit on transmitted packets (RFC 9764).
// For IPv4: sets IP_MTU_DISCOVER = IP_PMTUDISC_DO.
// For IPv6: sets IPV6_DONTFRAG = 1.
func WithDFBit() SenderOption {
	return func(s *UDPSender) {
		s.dfBit = true
	}
}

// WithDstPort overrides the default destination port.
// Use this for echo sessions (port 3785) or micro-BFD (port 6784).
func WithDstPort(port uint16) SenderOption {
	return func(s *UDPSender) {
		s.dstPort = port
	}
}

// WithBindDevice sets SO_BINDTODEVICE on the sender socket, binding it
// to a specific network interface. Required for RFC 7130 micro-BFD
// per-member-link sessions where each BFD session MUST be bound to its
// individual LAG member interface.
func WithBindDevice(ifName string) SenderOption {
	return func(s *UDPSender) {
		s.bindDevice = ifName
	}
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
	opts ...SenderOption,
) (*UDPSender, error) {
	dstPort := PortSingleHop
	if multiHop {
		dstPort = PortMultiHop
	}

	s := &UDPSender{
		dstPort:  dstPort,
		srcPort:  srcPort,
		multiHop: multiHop,
		logger: logger.With(
			slog.String("component", "netio.sender"),
			slog.String("local", localAddr.String()),
			slog.Uint64("src_port", uint64(srcPort)),
		),
	}
	for _, opt := range opts {
		opt(s)
	}

	isIPv6 := localAddr.Is6() && !localAddr.Is4In6()

	conn, err := dialSenderSocket(localAddr, srcPort, isIPv6, s.dfBit, s.bindDevice)
	if err != nil {
		return nil, fmt.Errorf("create UDP sender %s:%d: %w",
			localAddr, srcPort, err)
	}

	s.conn = conn
	return s, nil
}

// dialSenderSocket creates and configures a UDP socket for BFD TX.
func dialSenderSocket(
	localAddr netip.Addr,
	srcPort uint16,
	isIPv6 bool,
	dfBit bool,
	bindDevice string,
) (*net.UDPConn, error) {
	laddr := netip.AddrPortFrom(localAddr, srcPort)

	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			return setSenderOpts(c, isIPv6, dfBit, bindDevice)
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
// When dfBit is true, sets IP_PMTUDISC_DO (IPv4) or IPV6_DONTFRAG (IPv6)
// for RFC 9764 path MTU verification.
// When bindDevice is non-empty, sets SO_BINDTODEVICE for per-interface
// binding (RFC 7130 micro-BFD per-member-link sessions).
func setSenderOpts(c syscall.RawConn, isIPv6 bool, dfBit bool, bindDevice string) error {
	var sockErr error

	err := c.Control(func(fd uintptr) {
		//nolint:gosec // G115: fd uintptr->int is safe; kernel FDs are always small positive integers.
		intFD := int(fd)

		sockErr = setSenderSockOpts(intFD, isIPv6, dfBit, bindDevice)
	})
	if err != nil {
		return fmt.Errorf("raw conn control: %w", err)
	}

	return sockErr
}

// setSenderSockOpts applies socket-level and IP-level options for a BFD sender FD.
func setSenderSockOpts(fd int, isIPv6 bool, dfBit bool, bindDevice string) error {
	// SO_REUSEADDR: allow address reuse.
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		return fmt.Errorf("set SO_REUSEADDR: %w", err)
	}

	// SO_BINDTODEVICE: bind to specific interface.
	// RFC 7130 Section 2: micro-BFD sessions MUST be bound per member link.
	if bindDevice != "" {
		if err := unix.SetsockoptString(
			fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, bindDevice,
		); err != nil {
			return fmt.Errorf("set SO_BINDTODEVICE(%s): %w", bindDevice, err)
		}
	}

	if isIPv6 {
		return setSenderOptsIPv6(fd, dfBit)
	}

	return setSenderOptsIPv4(fd, dfBit)
}

// setSenderOptsIPv4 configures IPv4-specific socket options for BFD TX.
func setSenderOptsIPv4(fd int, dfBit bool) error {
	// IP_TTL = 255: RFC 5881 Section 5 / RFC 5082 GTSM.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_TTL, int(ttlRequired)); err != nil {
		return fmt.Errorf("set IP_TTL: %w", err)
	}

	// RFC 9764: set DF bit for path MTU verification.
	if dfBit {
		if err := unix.SetsockoptInt(
			fd, unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO,
		); err != nil {
			return fmt.Errorf("set IP_PMTUDISC_DO: %w", err)
		}
	}

	return nil
}

// setSenderOptsIPv6 configures IPv6-specific socket options for BFD TX.
func setSenderOptsIPv6(fd int, dfBit bool) error {
	// IPV6_UNICAST_HOPS = 255: RFC 5881 Section 5 / RFC 5082 GTSM.
	if err := unix.SetsockoptInt(
		fd, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, int(ttlRequired),
	); err != nil {
		return fmt.Errorf("set IPV6_UNICAST_HOPS: %w", err)
	}

	// RFC 9764: set DF bit for path MTU verification.
	if dfBit {
		if err := unix.SetsockoptInt(
			fd, unix.IPPROTO_IPV6, unix.IPV6_DONTFRAG, 1,
		); err != nil {
			return fmt.Errorf("set IPV6_DONTFRAG: %w", err)
		}
	}

	return nil
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
