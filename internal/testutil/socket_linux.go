//go:build linux

package testutil

import (
	"net"
	"net/netip"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// SocketOptionAt observes actual sockets without production accessors for their FDs.
// Each test owns a distinct loopback address and holds its sockets open.
func SocketOptionAt(t *testing.T, local netip.Addr, port uint16, level, option int) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		addr, err := unix.Getsockname(fd)
		if err != nil {
			continue
		}
		var boundAddr netip.Addr
		var boundPort int
		switch sa := addr.(type) {
		case *unix.SockaddrInet4:
			boundAddr, boundPort = netip.AddrFrom4(sa.Addr), sa.Port
		case *unix.SockaddrInet6:
			boundAddr, boundPort = netip.AddrFrom16(sa.Addr), sa.Port
		default:
			continue
		}
		if boundPort != int(port) || boundAddr != local {
			continue
		}
		value, err := unix.GetsockoptInt(fd, level, option)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	t.Fatalf("socket %s:%d not found", local, port)
	return 0
}

// ReferenceSocketBuffers measures a UDP reference after independent buffer configuration.
// The returned kernel values account for clamping and platform bookkeeping.
func ReferenceSocketBuffers(t *testing.T, readSize, writeSize int) (int, int) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 27, 250)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if readSize > 0 {
		if err := conn.SetReadBuffer(readSize); err != nil {
			t.Fatal(err)
		}
	}
	if writeSize > 0 {
		if err := conn.SetWriteBuffer(writeSize); err != nil {
			t.Fatal(err)
		}
	}
	binding, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatal("reference socket has no UDP local address")
	}
	port := binding.AddrPort().Port()
	local := netip.MustParseAddr("127.0.27.250")
	return SocketOptionAt(t, local, port, unix.SOL_SOCKET, unix.SO_RCVBUF),
		SocketOptionAt(t, local, port, unix.SOL_SOCKET, unix.SO_SNDBUF)
}
