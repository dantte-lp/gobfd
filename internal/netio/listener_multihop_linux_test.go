//go:build linux

package netio_test

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// A real IPv4 and IPv6 RFC 5883 Section 5 listener on UDP 4784 passes a packet
// with TTL/Hop Limit 250 to the session hop policy and reports the receive
// interface from PKTINFO (RFC 5881 Section 3 binding input).
func TestMultiHopListenerPort4784Families(t *testing.T) {
	for _, tc := range []struct {
		name  string
		addr  netip.Addr
		level int
		opt   int
	}{
		{"IPv4", netip.MustParseAddr("127.0.0.1"), unix.IPPROTO_IP, unix.IP_TTL},
		{"IPv6", netip.IPv6Loopback(), unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := netio.NewListener(netio.ListenerConfig{Addr: tc.addr, Port: netio.PortMultiHop, MultiHop: true})
			if err != nil {
				t.Fatalf("NewListener(%s:4784): %v", tc.addr, err)
			}
			t.Cleanup(func() {
				if closeErr := ln.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})

			client, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(tc.addr, netio.PortMultiHop)))
			if err != nil {
				t.Fatalf("DialUDP: %v", err)
			}
			t.Cleanup(func() { _ = client.Close() })
			raw, err := client.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			if ctrlErr := raw.Control(func(fd uintptr) {
				if sockErr := unix.SetsockoptInt(int(fd), tc.level, tc.opt, 250); sockErr != nil {
					t.Error(sockErr)
				}
			}); ctrlErr != nil {
				t.Fatal(ctrlErr)
			}
			if _, writeErr := client.Write(make([]byte, 24)); writeErr != nil {
				t.Fatalf("write: %v", writeErr)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			pkt, err := ln.RecvPacket(ctx)
			if err != nil {
				t.Fatalf("RecvPacket: %v", err)
			}
			defer pkt.Release()
			lo, err := net.InterfaceByName("lo")
			if err != nil {
				t.Fatal(err)
			}
			if pkt.Meta.TTL != 250 || !pkt.Meta.MultiHop || pkt.Meta.DstAddr != tc.addr || pkt.Meta.IfIndex != lo.Index {
				t.Fatalf("meta = %+v, want TTL 250, MultiHop, DstAddr %s, IfIndex %d", pkt.Meta, tc.addr, lo.Index)
			}
		})
	}
}
