//go:build !linux

package netio

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
)

// UDPSender is the non-Linux API placeholder for the Linux BFD sender.
// NewUDPSender always returns ErrUnsupportedPlatform on this platform.
type UDPSender struct{}

// SenderOption preserves the sender configuration API on non-Linux builds.
type SenderOption func(*UDPSender)

func unsupportedPlatform(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrUnsupportedPlatform)
}

// WithDFBit preserves the Linux sender option API on non-Linux builds.
func WithDFBit() SenderOption {
	return func(*UDPSender) {
		// No-op: non-Linux transports cannot set the IPv4 DF bit.
	}
}

// WithDstPort preserves the Linux sender option API on non-Linux builds.
func WithDstPort(_ uint16) SenderOption {
	return func(*UDPSender) {
		// No-op: non-Linux transports cannot create a BFD socket.
	}
}

// WithBindDevice preserves the Linux sender option API on non-Linux builds.
func WithBindDevice(_ string) SenderOption {
	return func(*UDPSender) {
		// No-op: device binding is unavailable without Linux sockets.
	}
}

// WithWriteBuffer preserves the Linux sender option API on non-Linux builds.
func WithWriteBuffer(_ int) SenderOption {
	return func(*UDPSender) {
		// No-op: no socket exists whose write buffer could be configured.
	}
}

// NewUDPSender rejects the Linux-specific BFD sender on non-Linux platforms.
func NewUDPSender(
	localAddr netip.Addr,
	srcPort uint16,
	_ bool,
	_ *slog.Logger,
	_ ...SenderOption,
) (*UDPSender, error) {
	return nil, unsupportedPlatform(
		"create UDP sender %s:%d",
		localAddr,
		srcPort,
	)
}

// SendPacket rejects BFD transmission on non-Linux platforms.
func (*UDPSender) SendPacket(_ context.Context, _ []byte, addr netip.Addr) error {
	return unsupportedPlatform("send BFD packet to %s", addr)
}

// Close rejects the non-Linux placeholder because it never owns a socket.
func (*UDPSender) Close() error {
	return unsupportedPlatform("close UDP sender")
}

// SrcPort returns zero because a non-Linux sender cannot be created.
func (*UDPSender) SrcPort() uint16 {
	return 0
}

// SourcePortAllocator is the non-Linux API placeholder for the Linux allocator.
type SourcePortAllocator struct{}

// NewSourcePortAllocator preserves the allocator API on non-Linux builds.
func NewSourcePortAllocator() *SourcePortAllocator {
	return &SourcePortAllocator{}
}

// Allocate rejects source-port allocation for the unsupported transport.
func (*SourcePortAllocator) Allocate() (uint16, error) {
	return 0, unsupportedPlatform(
		"allocate BFD source port in range %d-%d",
		sourcePortMin,
		sourcePortMax,
	)
}

// Release is a no-op because Allocate cannot reserve a port on this platform.
func (*SourcePortAllocator) Release(_ uint16) {
	// No-op: the non-Linux allocator never reserves ports.
}

// NewSingleHopListener rejects Linux-specific single-hop sockets on non-Linux platforms.
func NewSingleHopListener(_ context.Context, addr netip.Addr, ifName string) (PacketConn, error) {
	return nil, unsupportedPlatform(
		"single-hop listener on %s%%%s",
		addr,
		ifName,
	)
}

// NewMultiHopListener rejects Linux-specific multi-hop sockets on non-Linux platforms.
func NewMultiHopListener(_ context.Context, addr netip.Addr) (PacketConn, error) {
	return nil, unsupportedPlatform("multi-hop listener on %s", addr)
}

// NewGenericListener rejects Linux-specific generic BFD sockets on non-Linux platforms.
func NewGenericListener(
	_ context.Context,
	addr netip.Addr,
	ifName string,
	port uint16,
) (PacketConn, error) {
	return nil, unsupportedPlatform(
		"generic listener on %s%%%s port %d",
		addr,
		ifName,
		port,
	)
}
