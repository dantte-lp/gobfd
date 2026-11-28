package netio_test

import (
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

func loopbackIndex(t *testing.T) int {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("net.Interfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface.Index
		}
	}
	t.Skip("no loopback interface")
	return 0
}

// RFC 5881 Section 3 binds a single-hop session to one interface: the
// interface that owns the local address, or the configured interface.
func TestResolveSessionIfIndex(t *testing.T) {
	t.Parallel()
	lo := loopbackIndex(t)
	for _, tc := range []struct {
		name    string
		local   netip.Addr
		ifName  string
		want    int
		wantErr error
	}{
		{name: "ipv4 owner", local: netip.MustParseAddr("127.0.0.1"), want: lo},
		{name: "ipv4 mapped owner", local: netip.MustParseAddr("::ffff:127.0.0.1"), want: lo},
		{name: "ipv4 not owned", local: netip.MustParseAddr("192.0.2.250"), wantErr: netio.ErrLocalAddrNotOwned},
		{name: "ipv6 not owned", local: netip.MustParseAddr("2001:db8::250"), wantErr: netio.ErrLocalAddrNotOwned},
		{name: "unspecified local", local: netip.Addr{}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := netio.ResolveSessionIfIndex(tc.local, tc.ifName)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("ResolveSessionIfIndex error = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ResolveSessionIfIndex = %d, want %d", got, tc.want)
			}
		})
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("net.Interfaces: %v", err)
	}
	got, err := netio.ResolveSessionIfIndex(netip.MustParseAddr("192.0.2.250"), ifaces[0].Name)
	if err != nil || got != ifaces[0].Index {
		t.Fatalf("ResolveSessionIfIndex(configured %s) = %d, %v; want %d", ifaces[0].Name, got, err, ifaces[0].Index)
	}
	if _, err := netio.ResolveSessionIfIndex(netip.Addr{}, "gobfd-missing0"); err == nil {
		t.Fatal("ResolveSessionIfIndex(missing interface) error = nil")
	}
}
