//go:build linux

package netio

import (
	"errors"
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

// A stale inventory must not turn a different live binding into success.
func TestRebindSenderSocketVerifiesExpectedIndex(t *testing.T) {
	t.Parallel()
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		index   int
		wantErr bool
	}{
		{name: "current", index: iface.Index},
		{name: "stale", index: iface.Index + 1, wantErr: true},
		{name: "zero", index: 0, wantErr: true},
		{name: "negative", index: -1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			conn, listenErr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if listenErr != nil {
				t.Fatal(listenErr)
			}
			t.Cleanup(func() {
				if closeErr := conn.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			raw, rawErr := conn.SyscallConn()
			if rawErr != nil {
				t.Fatal(rawErr)
			}
			got, bindErr := rebindSenderSocket(raw, iface.Name, tc.index)
			if tc.wantErr {
				if !errors.Is(bindErr, unix.ENODEV) {
					t.Fatalf("rebind index %d = %d, %v; want ENODEV", tc.index, got, bindErr)
				}
			} else if bindErr != nil || got != iface.Index {
				t.Fatalf("rebind index %d = %d, %v", tc.index, got, bindErr)
			}
			// A rejected index must leave an initially unbound socket alone.
			if tc.index <= 0 {
				var actual int
				var optionErr error
				if controlErr := raw.Control(func(fd uintptr) {
					actual, optionErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX)
				}); controlErr != nil {
					t.Fatal(controlErr)
				}
				if optionErr != nil || actual != 0 {
					t.Fatalf("invalid input changed binding: index=%d error=%v", actual, optionErr)
				}
			}
		})
	}
}
