//go:build linux

package netio

import (
	"context"
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// Recovery stays off the successful send path and never clears device binding.
func (s *UDPSender) rebindDevice(ctx context.Context, dst *net.UDPAddr, namedZone bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("rebind sender device: %w", ErrSocketClosed)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rebind sender device: %w", err)
	}
	iface, err := net.InterfaceByName(s.bindDevice)
	if err != nil {
		return fmt.Errorf("resolve sender device %s: %w", s.bindDevice, err)
	}
	raw, err := s.conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("sender device socket: %w", err)
	}
	index, err := rebindSenderSocket(raw, s.bindDevice, iface.Index)
	if err != nil {
		return err
	}
	if index <= 0 {
		return fmt.Errorf("rebind sender device %s: %w", s.bindDevice, unix.ENODEV)
	}
	if namedZone {
		// A numeric zone forces Go interface-table refresh; the verified socket supplies scope.
		s.deviceScope = true
		dst.Zone = ""
	}
	return nil
}

func rebindSenderSocket(raw syscall.RawConn, device string, currentIndex int) (int, error) {
	var boundIndex int
	var optionErr error
	if err := raw.Control(func(fd uintptr) {
		boundIndex, optionErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX)
		if optionErr != nil || boundIndex == currentIndex {
			return
		}
		optionErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, device)
		if optionErr == nil {
			boundIndex, optionErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX)
		}
	}); err != nil {
		return 0, fmt.Errorf("control sender device socket: %w", err)
	}
	if optionErr != nil {
		return 0, fmt.Errorf("rebind sender device %s: %w", device, optionErr)
	}
	return boundIndex, nil
}
