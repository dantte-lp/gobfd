//go:build linux

package netio

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSenderBindingRefreshRetainsSocketAndPort(t *testing.T) {
	t.Parallel()
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []string{"127.0.0.1", "::1"} {
		t.Run(local, func(t *testing.T) {
			t.Parallel()
			addr := netip.MustParseAddr(local)
			peer, listenErr := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(addr, 0)))
			if listenErr != nil {
				t.Fatal(listenErr)
			}
			t.Cleanup(func() {
				if closeErr := peer.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			port := uint16(peer.LocalAddr().(*net.UDPAddr).Port)
			sender, createErr := NewUDPSender(addr, 49152, false, slog.New(slog.DiscardHandler),
				WithBindDevice("lo"), WithDstPort(port))
			if createErr != nil {
				t.Fatal(createErr)
			}
			t.Cleanup(func() {
				if closeErr := sender.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			socket := sender.conn
			for range 2 {
				if bindErr := sender.RebindInterface(t.Context(), "lo", iface.Index); bindErr != nil {
					t.Fatal(bindErr)
				}
			}
			if bindErr := sender.RebindInterface(t.Context(), "lo", iface.Index+1); !errors.Is(bindErr, unix.ENODEV) {
				t.Fatalf("stale rebind=%v", bindErr)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if bindErr := sender.RebindInterface(ctx, "lo", iface.Index); !errors.Is(bindErr, context.Canceled) {
				t.Fatalf("canceled rebind=%v", bindErr)
			}
			if bindErr := sender.RebindInterface(t.Context(), "another-device", iface.Index); !errors.Is(
				bindErr, ErrRebindUnsupported) {
				t.Fatalf("different device rebind=%v", bindErr)
			}
			if sender.conn != socket || sender.SrcPort() != 49152 {
				t.Fatal("refresh replaced socket or source port")
			}
			if sendErr := sender.SendPacket(t.Context(), []byte("retained"), addr); sendErr != nil {
				t.Fatal(sendErr)
			}
			if deadlineErr := peer.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
				t.Fatal(deadlineErr)
			}
			var data [64]byte
			n, remote, receiveErr := peer.ReadFromUDPAddrPort(data[:])
			if receiveErr != nil || string(data[:n]) != "retained" || remote.Port() != 49152 {
				t.Fatalf("retained transport RX=%q from=%s error=%v", data[:n], remote, receiveErr)
			}
		})
	}
}
