//go:build linux

package netio

import (
	"errors"
	"net/netip"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func boundIfIndex(t *testing.T, ln *Listener) int {
	t.Helper()
	conn, ok := ln.conn.(*LinuxPacketConn)
	if !ok {
		t.Fatalf("listener conn = %T, want *LinuxPacketConn", ln.conn)
	}
	raw, err := conn.conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var index int
	var sockErr error
	if ctrlErr := raw.Control(func(fd uintptr) {
		index, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX)
	}); ctrlErr != nil {
		t.Fatal(ctrlErr)
	}
	if sockErr != nil {
		t.Fatal(sockErr)
	}
	return index
}

// RebindDevice re-applies SO_BINDTODEVICE on the same socket. Linux requires
// CAP_NET_RAW to change an existing device binding, so an unprivileged run
// skips; the recreated-interface packet proof is a live qualification step.
func TestListenerRebindDeviceKeepsSocketAndReceives(t *testing.T) {
	lo := loopbackInterface(t)
	// 127.0.53.2 is routed to lo and used by no other test.
	family := loopbackFamily{"IPv4", netip.MustParseAddr("127.0.53.2"), unix.IPPROTO_IP, unix.IP_TTL}
	ln, err := NewListener(ListenerConfig{Addr: family.addr, IfName: lo.Name, Port: PortMicroBFD})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ln.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	before := ln.conn
	if err := ln.RebindDevice(); errors.Is(err, syscall.EPERM) {
		t.Skipf("CAP_NET_RAW required to change a device binding: %v", err)
	} else if err != nil {
		t.Fatalf("RebindDevice: %v", err)
	}
	if ln.conn != before {
		t.Fatal("RebindDevice replaced the socket")
	}
	if got := boundIfIndex(t, ln); got != lo.Index {
		t.Fatalf("SO_BINDTOIFINDEX = %d, want %d", got, lo.Index)
	}
	sendLoopback(t, family, PortMicroBFD, 255, make([]byte, bfd.HeaderSize))
	if meta := recvLoopback(t, ln).Meta; meta.IfIndex != lo.Index {
		t.Fatalf("meta = %+v, want IfIndex %d", meta, lo.Index)
	}
}

func TestListenerRebindDeviceRejectsUnboundListeners(t *testing.T) {
	for _, family := range loopbackFamilies()[:1] {
		multiHop := newLoopbackListener(t, family, PortMultiHop, true)
		if err := multiHop.RebindDevice(); !errors.Is(err, ErrRebindUnsupported) {
			t.Fatalf("multi-hop RebindDevice() = %v, want %v", err, ErrRebindUnsupported)
		}
		unbound := newLoopbackListener(t, family, PortEcho, false)
		if err := unbound.RebindDevice(); !errors.Is(err, ErrRebindUnsupported) {
			t.Fatalf("unbound RebindDevice() = %v, want %v", err, ErrRebindUnsupported)
		}
	}
}
