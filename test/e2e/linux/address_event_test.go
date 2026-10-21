//go:build e2e_linux

package linux_test

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

	"github.com/dantte-lp/gobfd/internal/netio"
)

const (
	addressLinkA = "gobfda0"
	addressLinkB = "gobfda1"
)

type addressObservation struct {
	Phase   string `json:"phase"`
	IfIndex int    `json:"if_index"`
	Bound   bool   `json:"bound"`
	Errno   string `json:"errno,omitempty"`
}

// TestAddressEventFollowsUnassignedBind proves the premise of address
// pending: binding an unassigned local address fails with EADDRNOTAVAIL, the
// interface monitor reports the RTM_NEWADDR that assigns it, and the same
// bind then succeeds.
func TestAddressEventFollowsUnassignedBind(t *testing.T) {
	assertNetworkNoneIsolation(t)
	local := netip.AddrFrom4([4]byte{10, 252, 0, 1})

	mon, monErr := netio.NewLinuxInterfaceMonitor(slog.New(slog.DiscardHandler))
	if monErr != nil {
		t.Fatalf("NewLinuxInterfaceMonitor: %v", monErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- mon.Run(ctx) }()
	defer func() {
		cancel()
		if err := mon.Close(); err != nil && !errors.Is(err, unix.EBADF) {
			t.Errorf("close interface monitor: %v", err)
		}
		<-errc
	}()

	nl := newRouteSocket(t)
	defer nl.close(t)
	defer nl.deleteLinkByName(addressLinkA)
	if err := nl.addVethPair(addressLinkA, addressLinkB); err != nil {
		t.Fatalf("add veth pair: %v", err)
	}
	for _, name := range []string{addressLinkA, addressLinkB} {
		if err := nl.setLinkUp(name, true); err != nil {
			t.Fatalf("set %s up: %v", name, err)
		}
	}
	iface, ifaceErr := net.InterfaceByName(addressLinkA)
	if ifaceErr != nil {
		t.Fatal(ifaceErr)
	}

	var observed []addressObservation
	bind := func(phase string) error {
		conn, bindErr := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, 0)))
		row := addressObservation{Phase: phase, IfIndex: iface.Index, Bound: bindErr == nil}
		if bindErr == nil {
			_ = conn.Close()
		} else if errors.Is(bindErr, syscall.EADDRNOTAVAIL) {
			row.Errno = "EADDRNOTAVAIL"
		}
		observed = append(observed, row)
		return bindErr
	}

	if err := bind("unassigned"); !errors.Is(err, syscall.EADDRNOTAVAIL) {
		t.Fatalf("bind before assignment = %v, want EADDRNOTAVAIL", err)
	}
	if err := nl.addIPv4Address(iface.Index, local, 30); err != nil {
		t.Fatalf("assign %s: %v", local, err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-mon.Events():
			if !ok {
				t.Fatal("interface event channel closed before the address event")
			}
			if !ev.Address || ev.IfIndex != iface.Index {
				continue
			}
		case <-deadline:
			t.Fatalf("no address event for ifindex %d", iface.Index)
		}
		break
	}
	if err := bind("assigned"); err != nil {
		t.Fatalf("bind after address event: %v", err)
	}
	writeJSONArtifact(t, "address-event.json", observed)
}
