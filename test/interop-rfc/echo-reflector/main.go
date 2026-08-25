// Echo reflector for RFC 9747 BFD echo interop testing.
//
// Listens on UDP port 3785 and reflects every received packet back
// to the sender. This simulates the echo reflection function that a
// remote system performs for BFD echo mode (RFC 5881 Section 4).
//
// In production, echo reflection is done by the remote's IP forwarding
// plane. This standalone reflector provides deterministic behavior for
// containerized interop tests where IP forwarding tricks are unreliable.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		slog.Error("echo reflector stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr, err := net.ResolveUDPAddr("udp4", ":3785")
	if err != nil {
		return fmt.Errorf("resolve address: %w", err)
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("listen UDP :3785: %w", err)
	}

	if err := setUDPTTL(conn, 254); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("set reflected packet TTL: %w", err),
				fmt.Errorf("close UDP socket after TTL setup error: %w", closeErr))
		}
		return fmt.Errorf("set reflected packet TTL: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			slog.Error("close UDP socket", "error", closeErr)
		}
	}()

	slog.Info("echo reflector listening", "address", ":3785")

	buf := make([]byte, 9000)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			slog.Warn("read echo packet", "error", err)
			continue
		}

		// Reflect the packet back to the sender on port 3785.
		dst := &net.UDPAddr{
			IP:   remote.IP,
			Port: 3785,
		}
		if _, err := conn.WriteToUDP(buf[:n], dst); err != nil {
			slog.Warn("write echo packet", "destination", dst, "error", err)
		}
	}
}

func setUDPTTL(conn *net.UDPConn, ttl int) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("get UDP socket control connection: %w", err)
	}

	var sockErr error
	if err := rawConn.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
	}); err != nil {
		return fmt.Errorf("control UDP socket: %w", err)
	}
	if sockErr != nil {
		return fmt.Errorf("set UDP socket TTL to %d: %w", ttl, sockErr)
	}

	return nil
}
