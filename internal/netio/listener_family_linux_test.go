//go:build linux

package netio

import (
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSingleHopWildcardSocketFamilies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		addr   netip.Addr
		family int
	}{
		{"IPv4", netip.IPv4Unspecified(), unix.AF_INET},
		{"IPv6", netip.IPv6Unspecified(), unix.AF_INET6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := listenUDP(t.Context(), netip.AddrPortFrom(tc.addr, 0), "lo", false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := conn.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			raw, err := conn.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			if err := raw.Control(func(fd uintptr) {
				family, sockErr := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_DOMAIN)
				if sockErr != nil {
					t.Error(sockErr)
				}
				if family != tc.family {
					t.Errorf("socket family=%d, want %d", family, tc.family)
				}
				device, sockErr := unix.GetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE)
				if sockErr != nil {
					t.Error(sockErr)
				}
				if device != "lo" {
					t.Errorf("bound device=%q, want lo", device)
				}
				if family == unix.AF_INET6 {
					v6only, sockErr := unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY)
					if sockErr != nil {
						t.Error(sockErr)
					}
					if v6only != 1 {
						t.Errorf("IPV6_V6ONLY=%d, want 1", v6only)
					}
					t.Logf("SO_DOMAIN=%d SO_BINDTODEVICE=%s IPV6_V6ONLY=%d", family, device, v6only)
				}
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
