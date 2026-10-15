//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// Observe the actual sockets without adding production accessors for their FDs.
// Each test owns a distinct loopback address and holds its sockets open.
func socketBufferAt(t *testing.T, local netip.Addr, port uint16, option int) int {
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
		ipv4, ok := addr.(*unix.SockaddrInet4)
		if !ok || ipv4.Port != int(port) || netip.AddrFrom4(ipv4.Addr) != local {
			continue
		}
		value, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, option)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	t.Fatalf("socket %s:%d not found", local, port)
	return 0
}

func referenceSocketBuffers(t *testing.T, size int) (int, int) {
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
	if size > 0 {
		if err := conn.SetReadBuffer(size); err != nil {
			t.Fatal(err)
		}
		if err := conn.SetWriteBuffer(size); err != nil {
			t.Fatal(err)
		}
	}
	port := conn.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	local := netip.MustParseAddr("127.0.27.250")
	return socketBufferAt(t, local, port, unix.SO_RCVBUF), socketBufferAt(t, local, port, unix.SO_SNDBUF)
}

func TestDaemonSocketBufferPolicy(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	for index, size := range []int{0, 8192} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			wantRead, wantWrite := referenceSocketBuffers(t, size)
			local := netip.MustParseAddr(fmt.Sprintf("127.0.27.%d", index+1))
			cfg := config.DefaultConfig()
			cfg.Socket = config.SocketConfig{ReadBufferSize: size, WriteBufferSize: size}
			cfg.Sessions = []config.SessionConfig{
				{Peer: "192.0.2.1", Local: local.String()},
				{Peer: "192.0.2.2", Local: local.String(), Type: "multi_hop"},
			}
			cfg.Echo.Peers = []config.EchoPeerConfig{{Peer: "192.0.2.3", Local: local.String()}}
			cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
				LAGInterface: "test-bond", LocalAddr: local.String(), MemberLinks: []string{"lo"},
			}}
			control, err := createControlListeners(cfg, logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeListeners(control, logger) })
			echo, err := createEchoListeners(cfg, logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeListeners(echo, logger) })
			micro, err := createMicroBFDListeners(cfg, logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeListeners(micro, logger) })
			for _, port := range []uint16{netio.PortSingleHop, netio.PortMultiHop, netio.PortEcho, netio.PortMicroBFD} {
				if got := socketBufferAt(t, local, port, unix.SO_RCVBUF); got != wantRead {
					t.Errorf("listener %d SO_RCVBUF=%d, want %d", port, got, wantRead)
				}
			}
			for _, kind := range []string{"api", "echo", "micro", "unsolicited"} {
				t.Run(kind, func(t *testing.T) {
					sf := newUDPSenderFactory(cfg.Socket.WriteBufferSize)
					var err error
					switch kind {
					case "api":
						_, _, err = sf.CreateSender(local, false, logger)
					case "echo":
						_, _, err = sf.createSenderForSession(local, false, logger, netio.WithDstPort(netio.PortEcho))
					case "micro":
						_, _, err = sf.createSenderForSession(local, false, logger,
							netio.WithDstPort(netio.PortMicroBFD), netio.WithBindDevice("lo"))
					case "unsolicited":
						var lease *bfd.SenderLease
						lease, err = unsolicitedSessionSender(sf, logger)(bfd.PacketMeta{DstAddr: local, IfName: "lo"})
						if err == nil {
							t.Cleanup(func() {
								if closeErr := lease.Close(); closeErr != nil {
									t.Error(closeErr)
								}
							})
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { cleanupCandidateTestSenders(t, sf) })
					if len(sf.senders) != 1 {
						t.Fatalf("senders = %d, want 1", len(sf.senders))
					}
					for port := range sf.senders {
						if got := socketBufferAt(t, local, port, unix.SO_SNDBUF); got != wantWrite {
							t.Errorf("sender SO_SNDBUF=%d, want %d", got, wantWrite)
						}
					}
				})
			}
		})
	}
}

func TestDaemonOverlaySocketBufferPolicy(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	for _, size := range []int{0, 8192} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			wantRead, wantWrite := referenceSocketBuffers(t, size)
			local := overlayTestLocalIPv4(t)
			cfg := config.DefaultConfig()
			cfg.Socket = config.SocketConfig{ReadBufferSize: size, WriteBufferSize: size}
			cfg.VXLAN.Peers = []config.VXLANPeerConfig{{
				Peer: "192.0.2.1", Local: local.String(),
				PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:02",
			}}
			cfg.Geneve.Peers = []config.GenevePeerConfig{{
				Peer: "192.0.2.1", Local: local.String(), VNI: 100,
				InnerPeer: "192.0.2.1", InnerLocal: "192.0.2.2",
				PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:02",
			}}
			sf := newUDPSenderFactory(cfg.Socket.WriteBufferSize)
			_, vxlan := createVXLANConns(cfg, sf, logger)
			_, geneve := createGeneveConns(cfg, sf, logger)
			conns := slices.Concat(vxlan, geneve)
			t.Cleanup(func() {
				for _, conn := range conns {
					if err := conn.Close(); err != nil {
						t.Error(err)
					}
				}
			})
			if len(vxlan) != 1 || len(geneve) != 1 {
				t.Fatalf("overlay sockets = %d/%d, want 1/1", len(vxlan), len(geneve))
			}
			for _, port := range []uint16{netio.VXLANPort, netio.GenevePort} {
				for _, option := range []struct{ key, want int }{{unix.SO_RCVBUF, wantRead}, {unix.SO_SNDBUF, wantWrite}} {
					if got := socketBufferAt(t, local, port, option.key); got != option.want {
						t.Errorf("overlay %d option %d = %d, want %d", port, option.key, got, option.want)
					}
				}
			}
		})
	}
}

func overlayTestLocalIPv4(t *testing.T) netip.Addr {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		if err == nil && prefix.Addr().Is4() && prefix.Addr().IsGlobalUnicast() {
			return prefix.Addr()
		}
	}
	t.Skip("scoped VXLAN requires an assigned non-loopback IPv4 address")
	return netip.Addr{}
}
