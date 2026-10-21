//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	bfdv1connect "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// Observe the actual sockets without adding production accessors for their FDs.
// Each test owns a distinct loopback address and holds its sockets open.
func socketOptionAt(t *testing.T, local netip.Addr, port uint16, level, option int) int {
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
	return socketOptionAt(t, local, port, unix.SOL_SOCKET, unix.SO_RCVBUF),
		socketOptionAt(t, local, port, unix.SOL_SOCKET, unix.SO_SNDBUF)
}

func TestAPIPaddedPDURequiresDF(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	referenceLocal := netip.MustParseAddr("127.0.28.250")
	reference, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(referenceLocal, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reference.Close(); err != nil {
			t.Error(err)
		}
	})
	referencePort := reference.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	ordinary := socketOptionAt(t, referenceLocal, referencePort, unix.IPPROTO_IP, unix.IP_MTU_DISCOVER)
	for index, target := range []uint32{0, 128} {
		t.Run(strconv.FormatUint(uint64(target), 10), func(t *testing.T) {
			local := netip.MustParseAddr(fmt.Sprintf("127.0.28.%d", index+1))
			sf := newUDPSenderFactory(0)
			t.Cleanup(func() { cleanupCandidateTestSenders(t, sf) })
			mgr := bfd.NewManager(logger)
			t.Cleanup(mgr.Close)
			srv := newGRPCServer(config.GRPCConfig{UnsafeLoopbackMutations: true},
				mgr, sf, newDaemonHealthChecker(), nil, logger)
			ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			serveOn(t, srv, ln)
			client := bfdv1connect.NewBfdServiceClient(connect.NewClient(
				connecthttp.NewTransport(http.DefaultClient, "http://"+ln.Addr().String())))
			req := addSessionRequest("127.0.28.254")
			req.LocalAddress = local.String()
			req.PaddedPduSize = target
			if _, err := client.AddSession(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			sf.mu.Lock()
			defer sf.mu.Unlock()
			if len(sf.senders) != 1 {
				t.Fatalf("API sender count = %d, want 1", len(sf.senders))
			}
			want := ordinary
			if target != 0 {
				want = unix.IP_PMTUDISC_DO
			}
			for port := range sf.senders {
				if got := socketOptionAt(t, local, port, unix.IPPROTO_IP, unix.IP_MTU_DISCOVER); got != want {
					t.Errorf("API target %d IP_MTU_DISCOVER = %d, want %d", target, got, want)
				}
			}
		})
	}
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
			cfg.Echo.Enabled = true
			cfg.Echo.Peers = []config.EchoPeerConfig{{Peer: "192.0.2.3", Local: local.String()}}
			cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
				LAGInterface: "test-bond", LocalAddr: local.String(), MemberLinks: []string{"lo"},
			}}
			openTestListeners(t, controlListenerConfigs(cfg))
			openTestListeners(t, echoListenerConfigs(cfg))
			openTestListeners(t, microListenerConfigs(cfg))
			for _, port := range []uint16{netio.PortSingleHop, netio.PortMultiHop, netio.PortEcho, netio.PortMicroBFD} {
				if got := socketOptionAt(t, local, port, unix.SOL_SOCKET, unix.SO_RCVBUF); got != wantRead {
					t.Errorf("listener %d SO_RCVBUF=%d, want %d", port, got, wantRead)
				}
			}
			for _, kind := range []string{"api", "echo", "micro", "unsolicited"} {
				t.Run(kind, func(t *testing.T) {
					sf := newUDPSenderFactory(cfg.Socket.WriteBufferSize)
					var err error
					switch kind {
					case "api":
						_, _, err = sf.CreateSender(local, "", false, false, logger)
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
						if got := socketOptionAt(t, local, port, unix.SOL_SOCKET, unix.SO_SNDBUF); got != wantWrite {
							t.Errorf("sender SO_SNDBUF=%d, want %d", got, wantWrite)
						}
					}
				})
			}
		})
	}
}

// Run serially: the unsolicited listeners own the wildcard single-hop port.
func TestDaemonSocketBufferPolicyUnsolicitedListeners(t *testing.T) {
	for _, size := range []int{0, 8192} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			wantRead, _ := referenceSocketBuffers(t, size)
			cfg := config.DefaultConfig()
			cfg.Socket = config.SocketConfig{ReadBufferSize: size, WriteBufferSize: size}
			cfg.Sessions = nil
			cfg.Unsolicited.Enabled = true
			cfg.Unsolicited.Interfaces = map[string]config.UnsolicitedInterfaceConfig{"lo": {Enabled: true}}
			control := openTestListeners(t, controlListenerConfigs(cfg))
			if len(control) != 2 {
				t.Fatalf("unsolicited listeners = %d, want 2", len(control))
			}
			for _, wildcard := range []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()} {
				if got := socketOptionAt(t, wildcard, netio.PortSingleHop, unix.SOL_SOCKET, unix.SO_RCVBUF); got != wantRead {
					t.Errorf("unsolicited listener %s SO_RCVBUF=%d, want %d", wildcard, got, wantRead)
				}
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
					if got := socketOptionAt(t, local, port, unix.SOL_SOCKET, option.key); got != option.want {
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

// openTestListeners opens configs with the listener owner's constructor and
// closes them at cleanup.
func openTestListeners(t *testing.T, configs []netio.ListenerConfig) []*netio.Listener {
	t.Helper()
	listeners := make([]*netio.Listener, 0, len(configs))
	t.Cleanup(func() {
		for _, ln := range listeners {
			if err := ln.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	for _, cfg := range configs {
		ln, err := netio.NewListener(cfg)
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, ln)
	}
	return listeners
}
