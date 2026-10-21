//go:build linux

package netio

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type loopbackFamily struct {
	name  string
	addr  netip.Addr
	level int
	opt   int
}

func loopbackFamilies() []loopbackFamily {
	return []loopbackFamily{
		// 127.0.53.1 is routed to lo but used by no other test package, so
		// parallel packages binding 127.0.0.1:3784 with SO_REUSEADDR cannot
		// take its datagrams.
		{"IPv4", netip.MustParseAddr("127.0.53.1"), unix.IPPROTO_IP, unix.IP_TTL},
		{"IPv6", netip.IPv6Loopback(), unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS},
	}
}

func loopbackIndex(t *testing.T) int {
	t.Helper()
	return loopbackInterface(t).Index
}

func loopbackInterface(t *testing.T) net.Interface {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface
		}
	}
	t.Skip("no loopback interface in this network namespace")
	return net.Interface{}
}

// newLoopbackListener opens a real listener on the fixed BFD port. go test
// runs packages in parallel in one network namespace, and other packages bind
// 127.0.0.1:3784 with SO_REUSEADDR, so IPv4 uses the otherwise unused
// 127.0.53.1. ::1 has no unused alternative: a parallel package binding
// [::1]:3784 or [::1]:4784 could take the datagram and fail this test with a
// receive timeout. No package binds those today.
func newLoopbackListener(t *testing.T, family loopbackFamily, port uint16, multiHop bool) *Listener {
	t.Helper()
	ln, err := NewListener(ListenerConfig{Addr: family.addr, Port: port, MultiHop: multiHop})
	if family.addr.Is6() && (errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT)) {
		t.Skipf("IPv6 loopback disabled in this network namespace: %v", err)
	}
	if err != nil {
		t.Fatalf("NewListener(%s:%d): %v", family.addr, port, err)
	}
	t.Cleanup(func() {
		if closeErr := ln.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return ln
}

func sendLoopback(t *testing.T, family loopbackFamily, port uint16, ttl int, payload []byte) {
	t.Helper()
	// Bind the source too: the kernel would pick 127.0.0.1 for 127.0.53.1.
	client, err := net.DialUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(family.addr, 0)),
		net.UDPAddrFromAddrPort(netip.AddrPortFrom(family.addr, port)))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = client.Close() }()
	raw, err := client.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if ctrlErr := raw.Control(func(fd uintptr) {
		if sockErr := unix.SetsockoptInt(int(fd), family.level, family.opt, ttl); sockErr != nil {
			t.Error(sockErr)
		}
	}); ctrlErr != nil {
		t.Fatal(ctrlErr)
	}
	if _, writeErr := client.Write(payload); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
}

func recvLoopback(t *testing.T, ln *Listener) *ReceivedPacket {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	pkt, err := ln.RecvPacket(ctx)
	if err != nil {
		t.Fatalf("RecvPacket: %v", err)
	}
	t.Cleanup(pkt.Release)
	return pkt
}

// A real RFC 5883 Section 5 listener on UDP 4784 passes a TTL/Hop Limit 250
// packet to the session hop policy and reports the PKTINFO interface.
func TestMultiHopListenerPort4784Families(t *testing.T) {
	lo := loopbackIndex(t)
	for _, family := range loopbackFamilies() {
		t.Run(family.name, func(t *testing.T) {
			ln := newLoopbackListener(t, family, PortMultiHop, true)
			sendLoopback(t, family, PortMultiHop, 250, make([]byte, bfd.HeaderSize))
			meta := recvLoopback(t, ln).Meta
			if meta.TTL != 250 || !meta.MultiHop || meta.DstAddr != family.addr || meta.IfIndex != lo {
				t.Fatalf("meta = %+v, want TTL 250, MultiHop, DstAddr %s, IfIndex %d", meta, family.addr, lo)
			}
		})
	}
}

// A real RFC 5881 Section 4 listener on UDP 3784 without a device binding
// reports the loopback PKTINFO interface, and the daemon resolver binds the
// session to it, so the packet is queued. ::1 is owned by lo, so its session
// has no interface; 127.0.53.1 is routed to lo but not assigned, so its
// session names lo explicitly.
func TestSingleHopListenerPort3784DeliversToSession(t *testing.T) {
	lo := loopbackInterface(t)
	for _, family := range loopbackFamilies() {
		t.Run(family.name, func(t *testing.T) {
			ln := newLoopbackListener(t, family, PortSingleHop, false)
			var ifName string
			if family.addr.Is4() {
				ifName = lo.Name
			}
			mgr := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithSessionInterfaceResolver(ResolveSessionIfIndex))
			t.Cleanup(mgr.Close)
			sess, err := mgr.CreateSession(t.Context(), bfd.SessionConfig{
				PeerAddr: family.addr, LocalAddr: family.addr, Interface: ifName, Type: bfd.SessionTypeSingleHop,
				Role: bfd.RolePassive, DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second,
				DetectMultiplier: 3,
			}, bfd.NonOwningSenderLeaseFactory(discardTestSender{}))
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}

			ctl := bfd.ControlPacket{
				Version: bfd.Version, State: bfd.StateDown, DetectMult: 3, MyDiscriminator: 77,
				DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
			}
			// The unbound listener reports no device, so a session naming lo is
			// reached by Your Discriminator; ::1 exercises the peer-key path.
			if ifName != "" {
				ctl.YourDiscriminator = sess.LocalDiscriminator()
			}
			wire := make([]byte, bfd.HeaderSize)
			if _, err := bfd.MarshalControlPacket(&ctl, wire); err != nil {
				t.Fatal(err)
			}
			sendLoopback(t, family, PortSingleHop, 255, wire)
			received := recvLoopback(t, ln)
			if received.Meta.TTL != 255 || received.Meta.MultiHop || received.Meta.IfIndex != lo.Index {
				t.Fatalf("meta = %+v, want TTL 255, single-hop, IfIndex %d", received.Meta, lo.Index)
			}
			var pkt bfd.ControlPacket
			if err := bfd.UnmarshalControlPacket(received.Data, &pkt); err != nil {
				t.Fatal(err)
			}
			if outcome, err := mgr.DemuxWithWire(&pkt, convertMeta(received.Meta), nil); outcome != bfd.DeliveryQueued {
				t.Fatalf("DemuxWithWire = %s, %v; want queued", outcome, err)
			}
		})
	}
}

type discardTestSender struct{}

func (discardTestSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }
