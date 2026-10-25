//go:build e2e_linux

package linux_test

import (
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"
)

const pendingLink = "gobfdq0"

func TestDaemonPendingResourceLifecycle(t *testing.T) {
	for _, local := range []string{"10.247.0.1", "fd00:247::1"} {
		t.Run(local, func(t *testing.T) { testDaemonPending(t, netip.MustParseAddr(local)) })
	}
}

func testDaemonPending(t *testing.T, local netip.Addr) {
	t.Helper()
	assertNetworkNoneIsolation(t)
	nl := newRouteSocket(t)
	defer nl.close(t)
	defer func() {
		nl.deleteLinkByName(pendingLink)
		nl.deleteLinkByName("gobfdp0")
		assertNetworkNoneIsolation(t)
	}()
	if err := nl.setLinkUp("lo", true); err != nil {
		t.Fatal(err)
	}
	create := func(name, peer string) int {
		t.Helper()
		if err := nl.addVethPair(name, peer); err != nil {
			t.Fatal(err)
		}
		for _, link := range []string{name, peer} {
			if err := nl.setLinkUp(link, true); err != nil {
				t.Fatal(err)
			}
		}
		iface, err := net.InterfaceByName(name)
		if err != nil {
			t.Fatal(err)
		}
		return iface.Index
	}
	assign := func(index int, addr netip.Addr) {
		t.Helper()
		prefix := uint8(24)
		if addr.Is6() {
			prefix = 64
		}
		if err := nl.addIPAddress(index, addr, prefix); err != nil {
			t.Fatal(err)
		}
	}
	assign(create("gobfdp0", "gobfdp1"), local)
	pending := netip.MustParseAddr("10.248.0.1")
	conflict := netip.MustParseAddr("10.248.0.3")
	if local.Is6() {
		pending, conflict = netip.MustParseAddr("fd00:248::1"), netip.MustParseAddr("fd00:248::3")
	}
	d := startPendingDaemon(t, local)
	defer func() { d.stop(t); d.artifacts(t, local.Is6()) }()
	initial := d.await(t, "initial", 1, true, func(s daemonObservation) bool {
		return s.Receipt.Desired == s.Receipt.Applied
	})
	d.reload(t, "/metrics", local, pending)
	missing := d.await(t, "interface-pending", 1, false, func(s daemonObservation) bool {
		return s.Receipt.Desired == initial.Receipt.Desired+1 &&
			s.Receipt.Base.InterfacePending && !s.Receipt.Base.AddressPending
	})
	assertDaemonIdentities(t, initial, missing)
	index := create(pendingLink, "gobfdq1")
	address := d.await(t, "address-pending", 1, false, func(s daemonObservation) bool {
		return s.ReceiptCount > missing.ReceiptCount && s.Receipt.Base.AddressPending && !s.Receipt.Base.InterfacePending
	})
	assertDaemonIdentities(t, initial, address)
	assign(index, pending)
	applied := d.await(t, "automatic-convergence", 2, true, func(s daemonObservation) bool {
		return s.Receipt.Desired == missing.Receipt.Desired &&
			s.Receipt.Applied == missing.Receipt.Desired && !s.Receipt.Stale
	})
	assertDaemonIdentities(t, initial, applied)
	prefix := uint8(24)
	if pending.Is6() {
		prefix = 64
	}
	defer func() {
		iface, err := net.InterfaceByName(pendingLink)
		if err != nil {
			return
		}
		for _, addr := range []netip.Addr{pending, conflict} {
			present, checkErr := daemonAddressPresent(iface.Index, addr)
			if checkErr != nil {
				t.Error(checkErr)
				continue
			}
			if !present {
				if err := nl.addIPAddress(iface.Index, addr, prefix); err != nil {
					t.Error(err)
				}
			}
		}
	}()
	if err := nl.deleteIPAddress(index, pending, prefix); err != nil {
		t.Fatal(err)
	}
	lostAddress := d.await(t, "active-address-removed", 2, false, func(s daemonObservation) bool {
		return s.Receipt.Base.AddressPending && s.Receipt.Desired == applied.Receipt.Desired
	})
	assertDaemonIdentities(t, applied, lostAddress)
	assign(index, pending)
	d.await(t, "active-address-restored", 2, true, nil)
	for range 2 {
		if err := nl.setLinkUp(pendingLink, false); err != nil {
			t.Fatal(err)
		}
		d.await(t, "link-down", 2, false, nil)
		if err := nl.setLinkUp(pendingLink, true); err != nil {
			t.Fatal(err)
		}
		present, err := daemonAddressPresent(index, pending)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			assign(index, pending)
		}
		up := d.await(t, "link-up", 2, true, nil)
		assertDaemonIdentities(t, applied, up)
		if !slices.Equal(applied.UDPSockets, up.UDPSockets) {
			t.Fatal("repeated events replaced or duplicated UDP sockets")
		}
	}
	nl.deleteLinkByName(pendingLink)
	gone := d.await(t, "interface-disappeared", 2, false, nil)
	assertDaemonIdentities(t, applied, gone)
	recreated := create(pendingLink, "gobfdq1")
	if recreated == index {
		t.Fatal("recreated interface retained its old index")
	}
	assign(recreated, pending)
	recovered := d.await(t, "interface-recreated", 2, true, func(s daemonObservation) bool {
		return s.Receipt.Desired == applied.Receipt.Desired && s.Receipt.Applied == applied.Receipt.Applied
	})
	assertDaemonIdentities(t, applied, recovered)
	if !slices.Equal(applied.UDPSockets, recovered.UDPSockets) {
		t.Fatal("recreation replaced or duplicated UDP sockets")
	}

	d.reload(t, "/rejected-startup-change", local, pending)
	rejected := d.await(t, "invalid-candidate", 2, true, func(s daemonObservation) bool {
		return s.Rejected > recovered.Rejected && s.Receipt.Desired == recovered.Receipt.Desired
	})
	assertDaemonIdentities(t, applied, rejected)
	assign(recreated, conflict)
	network := "udp4"
	if local.Is6() {
		network = "udp6"
	}
	occupied, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(conflict, 3784)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if occupied != nil {
			_ = occupied.Close()
		}
	}()
	d.reload(t, "/metrics", local, pending, conflict)
	failed := d.await(t, "permanent-bind-failure", 2, false, func(s daemonObservation) bool {
		return s.Receipt.Desired == applied.Receipt.Desired+1 && s.Receipt.Pending == 0 &&
			s.Receipt.Failed == 1 && s.Receipt.Base.Errors.Create == 1
	})
	assertDaemonIdentities(t, applied, failed)
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	occupied = nil
	if err := nl.setLinkUp(pendingLink, false); err != nil {
		t.Fatal(err)
	}
	if err := nl.setLinkUp(pendingLink, true); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []netip.Addr{pending, conflict} {
		present, err := daemonAddressPresent(recreated, addr)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			assign(recreated, addr)
		}
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		s := d.await(t, "failed-not-retried", 2, false, nil)
		if s.Receipt != failed.Receipt || s.ReceiptCount != failed.ReceiptCount {
			t.Fatal("availability event retried a permanent failure")
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.reload(t, "/metrics", local, pending, conflict)
	final := d.await(t, "explicit-reload-recovery", 3, true, func(s daemonObservation) bool {
		return s.Receipt.Desired == failed.Receipt.Desired+1 && s.Receipt.Applied == s.Receipt.Desired && !s.Receipt.Stale
	})
	assertDaemonIdentities(t, applied, final)
	d.stop(t)
}

func assertDaemonIdentities(t *testing.T, before, after daemonObservation) {
	t.Helper()
	for peer, discriminator := range before.Sessions {
		if after.Sessions[peer] != discriminator {
			t.Fatalf("session %s identity changed from %d to %d", peer, discriminator, after.Sessions[peer])
		}
	}
	if before.Receipt.Applied > after.Receipt.Applied {
		t.Fatal("applied generation moved backward")
	}
}
