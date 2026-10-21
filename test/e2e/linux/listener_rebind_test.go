//go:build e2e_linux

package linux_test

import (
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

const (
	rebindLinkA     = "gobfdr0"
	rebindLinkB     = "gobfdr1"
	rebindPrefixLen = 30
)

// rebindLocal is the listener address on the recreated veth.
func rebindLocal() netip.Addr { return netip.AddrFrom4([4]byte{10, 250, 0, 1}) }

type rebindObservation struct {
	Phase    string `json:"phase"`
	IfIndex  int    `json:"if_index"`
	Received bool   `json:"received"`
}

// TestListenerRebindFollowsRecreatedVeth proves at packet level that a
// device-bound UDP 3784 listener stays deaf after its interface is deleted
// and recreated with a new ifindex, and receives again after RebindDevice
// re-applies SO_BINDTODEVICE to the same socket (needs CAP_NET_RAW).
func TestListenerRebindFollowsRecreatedVeth(t *testing.T) {
	assertNetworkNoneIsolation(t)

	nl := newRouteSocket(t)
	defer nl.close(t)
	defer nl.deleteLinkByName(rebindLinkA)

	firstIndex := createAddressedVeth(t, nl)
	ln, err := netio.NewListener(netio.ListenerConfig{
		Addr: rebindLocal(), IfName: rebindLinkA, Port: netio.PortSingleHop,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	received := make(chan struct{}, 8)
	go func() {
		for {
			pkt, recvErr := ln.RecvPacket(t.Context())
			if recvErr != nil {
				return
			}
			pkt.Release()
			received <- struct{}{}
		}
	}()
	defer func() {
		if closeErr := ln.Close(); closeErr != nil {
			t.Errorf("close listener: %v", closeErr)
		}
	}()

	var observed []rebindObservation
	expect := func(phase string, ifIndex int, want bool) {
		t.Helper()
		sendSingleHopDatagram(t, netip.AddrPortFrom(rebindLocal(), netio.PortSingleHop))
		wait := 500 * time.Millisecond
		if want {
			wait = 3 * time.Second
		}
		got := false
		select {
		case <-received:
			got = true
		case <-time.After(wait):
		}
		observed = append(observed, rebindObservation{Phase: phase, IfIndex: ifIndex, Received: got})
		if got != want {
			t.Fatalf("%s: received=%t, want %t; observed=%+v", phase, got, want, observed)
		}
	}

	expect("bound", firstIndex, true)
	nl.deleteLinkByName(rebindLinkA)
	secondIndex := createAddressedVeth(t, nl)
	if secondIndex == firstIndex {
		t.Fatalf("recreated %s kept ifindex %d", rebindLinkA, firstIndex)
	}
	expect("recreated", secondIndex, false)
	if err := ln.RebindDevice(); err != nil {
		t.Fatalf("RebindDevice: %v", err)
	}
	expect("rebound", secondIndex, true)
	writeJSONArtifact(t, "listener-rebind.json", observed)
}

// createAddressedVeth creates the veth pair, brings both ends up and assigns
// rebindLocal to the listener end. It returns the listener end's ifindex.
func createAddressedVeth(t *testing.T, nl *routeSocket) int {
	t.Helper()
	if err := nl.addVethPair(rebindLinkA, rebindLinkB); err != nil {
		t.Fatalf("add veth pair: %v", err)
	}
	for _, name := range []string{rebindLinkA, rebindLinkB} {
		if err := nl.setLinkUp(name, true); err != nil {
			t.Fatalf("set %s up: %v", name, err)
		}
	}
	iface, err := net.InterfaceByName(rebindLinkA)
	if err != nil {
		t.Fatalf("resolve %s: %v", rebindLinkA, err)
	}
	if err := nl.addIPv4Address(iface.Index, rebindLocal(), rebindPrefixLen); err != nil {
		t.Fatalf("add %s to %s: %v", rebindLocal(), rebindLinkA, err)
	}
	return iface.Index
}

func (s *routeSocket) addIPv4Address(index int, addr netip.Addr, prefixLen uint8) error {
	body := make([]byte, unix.SizeofIfAddrmsg)
	body[0] = unix.AF_INET
	body[1] = prefixLen
	binary.NativeEndian.PutUint32(body[4:8], uint32(index))
	raw := addr.As4()
	attrs := append(netlinkAttr(unix.IFA_LOCAL, raw[:], false), netlinkAttr(unix.IFA_ADDRESS, raw[:], false)...)
	return s.request(unix.RTM_NEWADDR, unix.NLM_F_CREATE|unix.NLM_F_EXCL, body, attrs)
}

// sendSingleHopDatagram sends one header-sized datagram with TTL 255, so the
// listener's GTSM check (RFC 5881 Section 5) accepts it.
func sendSingleHopDatagram(t *testing.T, dst netip.AddrPort) {
	t.Helper()
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var sockErr error
		if err := raw.Control(func(fd uintptr) {
			sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TTL, 255)
		}); err != nil {
			return err
		}
		return sockErr
	}}
	pc, err := lc.ListenPacket(t.Context(), "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("open sender: %v", err)
	}
	defer func() { _ = pc.Close() }()
	if _, err := pc.WriteTo(make([]byte, bfd.HeaderSize), net.UDPAddrFromAddrPort(dst)); err != nil {
		t.Fatalf("send to %s: %v", dst, err)
	}
}
