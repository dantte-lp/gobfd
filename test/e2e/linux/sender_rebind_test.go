//go:build e2e_linux

package linux_test

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// The same physical sender must recover its device binding after recreation.
func TestSenderFollowsRecreatedVeth(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr netip.Addr
		deny bool
	}{
		{"IPv6LinkLocal", netip.MustParseAddr("fe80::51").WithZone(rebindLinkA), false},
		{"IPv4", rebindLocal(), false},
		{"IPv6", netip.MustParseAddr("fd00:250::51"), false},
		{"IPv4NoNetRaw", rebindLocal(), true},
	} {
		t.Run(tc.name, func(t *testing.T) { testSenderRecreation(t, tc.addr, tc.name, tc.deny) })
	}
}

func testSenderRecreation(t *testing.T, peer netip.Addr, family string, deny bool) {
	t.Helper()
	assertNetworkNoneIsolation(t)
	nl := newRouteSocket(t)
	defer nl.close(t)
	defer nl.deleteLinkByName(rebindLinkA)

	createPair := func() int {
		t.Helper()
		index := createAddressedVeth(t, nl)
		if peer.Is6() {
			if addrErr := nl.addIPAddress(index, peer.WithZone(""), 64); addrErr != nil {
				t.Fatal(addrErr)
			}
		}
		return index
	}
	localAt := func(index int) netip.Addr {
		if peer.Zone() != "" {
			return peer.WithZone(strconv.Itoa(index))
		}
		return peer
	}
	first := createPair()
	network := "udp4"
	if peer.Is6() {
		network = "udp6"
	}
	openReceiver := func(index int, port uint16) *net.UDPConn {
		t.Helper()
		listenAddr := localAt(index)
		if peer.Zone() != "" {
			// An unscoped receiver must not refresh the sender's stale Go zone cache.
			listenAddr = netip.IPv6Unspecified()
		}
		conn, openErr := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(listenAddr, port)))
		if openErr != nil {
			t.Fatal(openErr)
		}
		return conn
	}
	conn := openReceiver(first, 0)
	closeReceiver := func() {
		t.Helper()
		if conn != nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			conn = nil
		}
	}
	defer closeReceiver()
	port := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
	sender, err := netio.NewUDPSender(localAt(first), 49152, false, slog.New(slog.DiscardHandler),
		netio.WithBindDevice(rebindLinkA), netio.WithDstPort(port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	received := 0
	checkPacket := func(phase string) {
		t.Helper()
		payload := []byte(phase)
		if sendErr := sender.SendPacket(t.Context(), payload, peer); sendErr != nil {
			t.Fatalf("%s send: %v", phase, sendErr)
		}
		if deadlineErr := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
			t.Fatal(deadlineErr)
		}
		var buf [64]byte
		n, remote, readErr := conn.ReadFromUDPAddrPort(buf[:])
		if readErr != nil {
			t.Fatalf("%s receive: %v", phase, readErr)
		}
		if string(buf[:n]) != phase || remote.Addr().WithZone("") != peer.WithZone("") || remote.Port() != 49152 {
			t.Fatalf("%s packet = %q from %s", phase, buf[:n], remote)
		}
		received++
	}
	checkPacket("before")
	measure := func() float64 {
		t.Helper()
		return testing.AllocsPerRun(10, func() { checkPacket("allocation-sample") })
	}
	baselineAllocs := 0.0
	var packetAllocs []float64
	if peer.Zone() != "" {
		baselineAllocs = measure()
		packetAllocs = make([]float64, 1, 3)
		packetAllocs[0] = baselineAllocs
	}
	indexes := make([]int, 1, 3)
	indexes[0] = first
	for range 2 {
		closeReceiver()
		nl.deleteLinkByName(rebindLinkA)
		if absentErr := sender.SendPacket(t.Context(), []byte("absent"), peer); absentErr == nil {
			t.Fatal("sender succeeded with its device absent")
		}
		index := createPair()
		if index == indexes[len(indexes)-1] {
			t.Fatalf("recreated interface kept ifindex %d", index)
		}
		indexes = append(indexes, index)
		conn = openReceiver(index, port)
		if deny {
			withoutNetRaw(t, func() {
				if sendErr := sender.SendPacket(t.Context(), []byte("denied"), peer); !errors.Is(sendErr, unix.EPERM) {
					t.Errorf("rebind without CAP_NET_RAW = %v, want EPERM", sendErr)
				}
			})
		}
		checkPacket("after")
		checkPacket("after-again")
		if peer.Zone() != "" {
			afterAllocs := measure()
			packetAllocs = append(packetAllocs, afterAllocs)
			if afterAllocs > baselineAllocs {
				t.Errorf("per-packet allocations after rebind = %g, before = %g", afterAllocs, baselineAllocs)
			}
		}
	}
	raw, rawErr := sender.SyscallConn()
	if rawErr != nil {
		t.Fatal(rawErr)
	}
	var bound int
	var optionErr error
	if controlErr := raw.Control(func(fd uintptr) {
		bound, optionErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX)
	}); controlErr != nil {
		t.Fatal(controlErr)
	}
	if optionErr != nil || bound != indexes[len(indexes)-1] {
		t.Fatalf("sender device index = %d, want %v: %v", bound, indexes, optionErr)
	}
	writeJSONArtifact(t, "sender-rebind-"+family+".json", map[string]any{
		"if_indexes": indexes, "source_port": sender.SrcPort(), "received_packets": received,
		"packet_loop_allocations": packetAllocs,
	})
}

func withoutNetRaw(t *testing.T, call func()) {
	t.Helper()
	runtime.LockOSThread()
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var original [2]unix.CapUserData
	if err := unix.Capget(&header, &original[0]); err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Capset(&header, &original[0]); err != nil {
			// A failed restore leaves the thread locked until its test goroutine exits.
			t.Fatalf("restore effective capabilities: %v", err)
		}
		runtime.UnlockOSThread()
	}()
	limited := original
	limited[0].Effective &^= 1 << unix.CAP_NET_RAW
	if err := unix.Capset(&header, &limited[0]); err != nil {
		t.Fatal(err)
	}
	call()
}
