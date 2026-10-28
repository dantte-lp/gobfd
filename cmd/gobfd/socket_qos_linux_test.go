//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/testutil"
)

type socketQoSCase struct {
	name, network, local, peer, yaml string
	trafficClass, priority           int
	discriminators                   [2]uint32
}

func socketQoSCases() []socketQoSCase {
	const override = "socket:\n  ipv4_dscp: 46\n  ipv6_traffic_class: 184\n  priority: 6\n"
	const bestEffort = "socket:\n  ipv4_dscp: 0\n  ipv6_traffic_class: 0\n"
	return []socketQoSCase{
		{"IPv4/default", "udp4", "127.0.45.1", "127.0.45.254", "{}\n", 192, 0, [2]uint32{4101, 4102}},
		{"IPv4/override", "udp4", "127.0.45.1", "127.0.45.254", override, 184, 6, [2]uint32{4201, 4202}},
		{"IPv4/best-effort", "udp4", "127.0.45.1", "127.0.45.254", bestEffort, 0, 0, [2]uint32{4301, 4302}},
		{"IPv6/default", "udp6", "::1", "::1", "{}\n", 192, 0, [2]uint32{6101, 6102}},
		{"IPv6/override", "udp6", "::1", "::1", override, 184, 6, [2]uint32{6201, 6202}},
		{"IPv6/best-effort", "udp6", "::1", "::1", bestEffort, 0, 0, [2]uint32{6301, 6302}},
	}
}

func socketQoSTestFactory(t *testing.T, contents string) *udpSenderFactory {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qos.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("qos_config_sha256=%x yaml_bytes=%d", sha256.Sum256([]byte(contents)), len(contents))
	factory := newUDPSenderFactory(cfg.Socket)
	t.Cleanup(func() { cleanupCandidateTestSenders(t, factory) })
	return factory
}

func TestSocketQoSFactoryReadback(t *testing.T) {
	for _, test := range socketQoSCases() {
		t.Run(test.name, func(t *testing.T) {
			factory := socketQoSTestFactory(t, test.yaml)
			local := netip.MustParseAddr(test.local)
			_, port, err := factory.CreateSender(local, "", bfd.SessionTypeSingleHop, false, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			level, option := unix.IPPROTO_IP, unix.IP_TOS
			if local.Is6() {
				level, option = unix.IPPROTO_IPV6, unix.IPV6_TCLASS
			}
			for _, expected := range []struct{ level, option, value int }{
				{level, option, test.trafficClass},
				{unix.SOL_SOCKET, unix.SO_PRIORITY, test.priority},
				{unix.SOL_SOCKET, unix.SO_MARK, 0},
			} {
				if got := socketOptionAt(t, local, port, expected.level, expected.option); got != expected.value {
					t.Errorf("socket option level=%d option=%d = %d, want %d",
						expected.level, expected.option, got, expected.value)
				}
			}
		})
	}
}

func TestSocketQoSFactoryFailurePolicy(t *testing.T) {
	for _, option := range []struct {
		field, name string
		key, value  int
	}{
		{"priority", "SO_PRIORITY", unix.SO_PRIORITY, 7},
		{"mark", "SO_MARK", unix.SO_MARK, 123},
	} {
		for _, required := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/required=%t", option.field, required), func(t *testing.T) {
				requireSocketQoSPermissionDenied(t, option.key, option.value)
				factory := socketQoSTestFactory(t, fmt.Sprintf("socket:\n  %s: %d\n  %s_required: %t\n",
					option.field, option.value, option.field, required))
				allocator, availablePort := exhaustedOverlayAllocator(t)
				allocator.Release(availablePort)
				factory.portAlloc = allocator
				var logs lockedBuffer
				logger := slog.New(slog.NewTextHandler(&logs, nil))
				sender, _, err := factory.CreateSender(netip.MustParseAddr("127.0.46.1"), "",
					bfd.SessionTypeSingleHop, false, logger)
				if required {
					if sender != nil || !errors.Is(err, unix.EPERM) || len(factory.senders) != 0 {
						t.Fatalf("required %s failure: sender=%v error=%v retained=%d",
							option.name, sender, err, len(factory.senders))
					}
					port, allocateErr := allocator.Allocate()
					if allocateErr != nil || port != availablePort {
						t.Fatalf("failed sender leaked its port: got=%d want=%d error=%v", port, availablePort, allocateErr)
					}
					allocator.Release(port)
					return
				}
				if err != nil || sender == nil {
					t.Fatalf("optional %s failure rejected sender: %v", option.name, err)
				}
				if text := logs.String(); !strings.Contains(text, "level=WARN") ||
					!strings.Contains(text, option.name) || !strings.Contains(text, "error=") {
					t.Fatalf("optional %s failure has no warning: %s", option.name, text)
				}
			})
		}
	}
}

func requireSocketQoSPermissionDenied(t *testing.T, option, value int) {
	t.Helper()
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDP)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := unix.Close(fd); closeErr != nil {
			t.Error(closeErr)
		}
	})
	err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, option, value)
	if err == nil {
		t.Skip("socket option permitted; run this failure profile without network capabilities")
	}
	if !errors.Is(err, unix.EPERM) {
		t.Fatalf("socket permission probe: %v", err)
	}
}

// Run serially: each case owns UDP/3784 in its network namespace.
func TestSocketQoSFactoryPackets(t *testing.T) {
	testutil.LockLoopbackBFD(t)
	for _, test := range socketQoSCases() {
		t.Run(test.name, func(t *testing.T) {
			factory := socketQoSTestFactory(t, test.yaml)
			listener := socketQoSPacketListener(t, test)
			local, peer := netip.MustParseAddr(test.local), netip.MustParseAddr(test.peer)
			var senders [2]bfd.PacketSender
			var ports [2]uint16
			for index := range senders {
				var err error
				senders[index], ports[index], err = factory.CreateSender(local, "", bfd.SessionTypeSingleHop,
					false, slog.New(slog.DiscardHandler))
				if err != nil {
					t.Fatal(err)
				}
			}
			if ports[0] == ports[1] || ports[0] < 49152 || ports[1] < 49152 {
				t.Fatalf("source ports are not unique RFC 5881 ports: %v", ports)
			}
			for index, sender := range senders {
				packet := bfd.ControlPacket{
					Version: bfd.Version, State: bfd.StateDown, DetectMult: 3, Length: bfd.HeaderSize,
					MyDiscriminator:      test.discriminators[index],
					DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
				}
				var wire [bfd.HeaderSize]byte
				n, err := bfd.MarshalControlPacket(&packet, wire[:])
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if sendErr := sender.SendPacket(t.Context(), wire[:n], peer); sendErr != nil {
						t.Fatal(sendErr)
					}
					assertSocketQoSPacket(t, listener, test, netip.AddrPortFrom(local, ports[index]), packet)
				}
				t.Logf("qos_case=%s discriminator=%d source=%s destination=%s:3784 traffic_class=%d priority=%d packets=2",
					test.name, packet.MyDiscriminator, netip.AddrPortFrom(local, ports[index]),
					peer, test.trafficClass, test.priority)
			}
		})
	}
}

func socketQoSPacketListener(t *testing.T, test socketQoSCase) *net.UDPConn {
	t.Helper()
	listener, err := net.ListenUDP(test.network, net.UDPAddrFromAddrPort(
		netip.AddrPortFrom(netip.MustParseAddr(test.peer), 3784)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	raw, err := listener.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	level, traffic, hops := unix.IPPROTO_IP, unix.IP_RECVTOS, unix.IP_RECVTTL
	if test.network == "udp6" {
		level, traffic, hops = unix.IPPROTO_IPV6, unix.IPV6_RECVTCLASS, unix.IPV6_RECVHOPLIMIT
	}
	var optionErr error
	if err := raw.Control(func(fd uintptr) {
		optionErr = unix.SetsockoptInt(int(fd), level, traffic, 1)
		if optionErr == nil {
			optionErr = unix.SetsockoptInt(int(fd), level, hops, 1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if optionErr != nil {
		t.Fatal(optionErr)
	}
	return listener
}

func assertSocketQoSPacket(
	t *testing.T, listener *net.UDPConn, test socketQoSCase, source netip.AddrPort, expected bfd.ControlPacket,
) {
	t.Helper()
	if err := listener.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var payload [bfd.MaxPacketSize]byte
	var ancillary [256]byte
	n, oobn, flags, from, err := listener.ReadMsgUDP(payload[:], ancillary[:])
	if err != nil {
		t.Fatal(err)
	}
	if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || from.AddrPort() != source {
		t.Fatalf("packet tuple/truncation: source=%s want=%s flags=%d", from, source, flags)
	}
	var received bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(payload[:n], &received); err != nil {
		t.Fatal(err)
	}
	if received != expected {
		t.Fatalf("BFD packet changed: got=%+v want=%+v", received, expected)
	}
	traffic, hops := socketQoSAncillary(t, ancillary[:oobn])
	if traffic != test.trafficClass || hops != 255 {
		t.Errorf("received Traffic Class=%d TTL/HopLimit=%d, want %d/255", traffic, hops, test.trafficClass)
	}
}

func socketQoSAncillary(t *testing.T, data []byte) (int, int) {
	t.Helper()
	messages, err := unix.ParseSocketControlMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	traffic, hops := -1, -1
	for _, message := range messages {
		switch {
		case message.Header.Level == unix.IPPROTO_IP && message.Header.Type == unix.IP_TOS && len(message.Data) == 1:
			traffic = int(message.Data[0])
		case message.Header.Level == unix.IPPROTO_IPV6 && message.Header.Type == unix.IPV6_TCLASS && len(message.Data) == 4:
			traffic = int(binary.NativeEndian.Uint32(message.Data))
		case message.Header.Level == unix.IPPROTO_IP && message.Header.Type == unix.IP_TTL && len(message.Data) == 4:
			hops = int(binary.NativeEndian.Uint32(message.Data))
		case message.Header.Level == unix.IPPROTO_IPV6 && message.Header.Type == unix.IPV6_HOPLIMIT && len(message.Data) == 4:
			hops = int(binary.NativeEndian.Uint32(message.Data))
		}
	}
	return traffic, hops
}
