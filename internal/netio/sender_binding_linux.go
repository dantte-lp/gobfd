//go:build linux

package netio

import (
	"context"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// RebindInterface verifies the expected incarnation while retaining the socket.
func (s *UDPSender) RebindInterface(ctx context.Context, ifName string, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSocketClosed
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("begin sender binding: %w", err)
	}
	if ifName == "" || s.bindDevice != ifName || s.multiHop || index <= 0 {
		return ErrRebindUnsupported
	}
	local, ok := s.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return fmt.Errorf("sender local address: %w", ErrRebindUnsupported)
	}
	addr := local.AddrPort().Addr()
	if addr.IsLinkLocalUnicast() {
		addr = addr.WithZone(ifName)
	}
	claim := []LocalAddress{{Addr: addr, Interface: ifName}}
	if err := verifySenderBinding(ctx, ifName, index, claim); err != nil {
		return err
	}
	raw, err := s.conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("sender binding socket: %w", err)
	}
	if _, err := rebindSenderSocket(raw, ifName, index); err != nil {
		return err
	}
	if err := verifySenderBinding(ctx, ifName, index, claim); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("finish sender binding: %w", err)
	}
	// Named IPv6 zones must use the verified socket rather than a stale Go cache.
	s.deviceScope = true
	return nil
}

func verifySenderBinding(ctx context.Context, ifName string, index int, claim []LocalAddress) error {
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return fmt.Errorf("resolve sender binding: %w", err)
	}
	if iface.Index != index {
		return fmt.Errorf("sender interface index %d, expected %d: %w", iface.Index, index, unix.ENODEV)
	}
	_, err = CheckLocalAddressesAvailableContext(ctx, claim)
	return err
}
