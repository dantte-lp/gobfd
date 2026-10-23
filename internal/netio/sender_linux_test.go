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

func TestUDPSenderPaddingPreparation(t *testing.T) {
	t.Parallel()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "prepared"}[prepared], func(t *testing.T) {
			t.Parallel()
			var opts []SenderOption
			if prepared {
				opts = append(opts, WithDFBit())
			}
			s, err := NewUDPSender(netip.MustParseAddr("127.0.0.1"), 0, false,
				slog.New(slog.DiscardHandler), opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := s.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			proof, ok := any(s).(interface{ PaddingPrepared() bool })
			if !ok || proof.PaddingPrepared() != prepared {
				t.Fatal("sender lacks truthful padding preparation proof")
			}
			raw, err := s.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var actual int
			var optionErr error
			if err = raw.Control(func(fd uintptr) {
				actual, optionErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER)
			}); err != nil {
				t.Fatal(err)
			}
			if optionErr != nil {
				t.Fatal(optionErr)
			}
			if prepared && actual != unix.IP_PMTUDISC_DO {
				t.Fatalf("prepared sender MTU policy = %d, want IP_PMTUDISC_DO", actual)
			}
			WithDFBit()(s)
			if proof.PaddingPrepared() != prepared {
				t.Fatal("requested option was mistaken for installed socket policy")
			}
		})
	}
}

func TestSenderOptionsApply(t *testing.T) {
	t.Parallel()

	s := &UDPSender{}
	WithDFBit()(s)
	WithDstPort(PortEcho)(s)
	WithBindDevice("lo")(s)
	WithWriteBuffer(4096)(s)

	if !s.dfBit {
		t.Fatal("dfBit = false, want true")
	}
	if s.dstPort != PortEcho {
		t.Fatalf("dstPort = %d, want %d", s.dstPort, PortEcho)
	}
	if s.bindDevice != "lo" {
		t.Fatalf("bindDevice = %q, want lo", s.bindDevice)
	}
	if s.writeBufferSize != 4096 {
		t.Fatalf("writeBufferSize = %d, want 4096", s.writeBufferSize)
	}
}

func TestNewUDPSenderLoopbackLifecycle(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	s, err := NewUDPSender(
		netip.MustParseAddr("127.0.0.1"),
		0,
		false,
		logger,
		WithDFBit(),
		WithDstPort(PortEcho),
		WithWriteBuffer(4096),
	)
	if err != nil {
		t.Fatalf("NewUDPSender: %v", err)
	}

	if s.dstPort != PortEcho {
		t.Fatalf("dstPort = %d, want %d", s.dstPort, PortEcho)
	}
	if err := s.SendPacket(context.Background(), []byte{1, 2, 3}, netip.MustParseAddr("127.0.0.1")); err != nil {
		t.Fatalf("SendPacket: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	sendErr := s.SendPacket(context.Background(), []byte{1}, netip.MustParseAddr("127.0.0.1"))
	if !errors.Is(sendErr, ErrSocketClosed) {
		t.Fatalf("SendPacket after Close error = %v, want ErrSocketClosed", sendErr)
	}
}

// C-07 evidence: a sender bound to a local address emits from that address
// and its RFC 5881 source port. 127.0.0.2 differs from the kernel's default
// loopback source, so the bind, not routing, chooses the source.
func TestUDPSenderEmitsFromBoundLocalTuple(t *testing.T) {
	t.Parallel()

	for _, local := range []string{"127.0.0.2", "::1"} {
		t.Run(local, func(t *testing.T) {
			t.Parallel()

			localAddr := netip.MustParseAddr(local)
			network := "udp4"
			if localAddr.Is6() {
				network = "udp6"
			}
			listener, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(localAddr, 0)))
			if err != nil {
				t.Skipf("no %s loopback: %v", network, err)
			}
			defer listener.Close()
			dstPort := netip.MustParseAddrPort(listener.LocalAddr().String()).Port()

			alloc := NewSourcePortAllocator()
			srcPort, err := alloc.Allocate()
			if err != nil {
				t.Fatalf("allocate source port: %v", err)
			}
			s, err := NewUDPSender(localAddr, srcPort, false, slog.New(slog.DiscardHandler), WithDstPort(dstPort))
			if err != nil {
				t.Fatalf("NewUDPSender: %v", err)
			}
			defer s.Close()

			if sendErr := s.SendPacket(t.Context(), []byte{1}, localAddr); sendErr != nil {
				t.Fatalf("SendPacket: %v", sendErr)
			}
			if deadlineErr := listener.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
				t.Fatalf("set deadline: %v", deadlineErr)
			}
			_, from, err := listener.ReadFromUDPAddrPort(make([]byte, 16))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if want := netip.AddrPortFrom(localAddr, srcPort); from != want {
				t.Fatalf("packet source = %s, want %s", from, want)
			}
		})
	}
}
