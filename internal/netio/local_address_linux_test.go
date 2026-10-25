//go:build linux

package netio

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestLocalAddressInventoryRejectsUnusableAddresses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		flags       uint32
		headerFlags byte
		local       string
		peer        string
		want        string
	}{
		{name: "IPv4 local rather than point-to-point peer", local: "192.0.2.1", peer: "192.0.2.2", want: "192.0.2.1"},
		{name: "IPv6 ready", peer: "2001:db8::1", want: "2001:db8::1"},
		{name: "IPv6 tentative", peer: "2001:db8::1", flags: unix.IFA_F_TENTATIVE},
		{name: "IPv6 DAD failed", peer: "2001:db8::1", flags: unix.IFA_F_DADFAILED},
		{name: "extended flags override header", peer: "2001:db8::1", headerFlags: unix.IFA_F_TENTATIVE, want: "2001:db8::1"},
		{name: "deprecated remains usable", peer: "2001:db8::1", flags: unix.IFA_F_DEPRECATED, want: "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg := localAddressMessage(t, tc.local, tc.peer, tc.flags)
			msg.Data[2] = tc.headerFlags
			got, err := assignedLocalAddresses([]syscall.NetlinkMessage{msg})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				count, availabilityErr := checkLocalAddresses(
					[]LocalAddress{{Addr: netip.MustParseAddr(tc.peer)}}, got, nil)
				if count != 1 || !errors.Is(availabilityErr, bfd.ErrResourceUnavailable) {
					t.Fatalf("unusable address accepted: %v, %v", got, availabilityErr)
				}
				return
			}
			indices := got[netip.MustParseAddr(tc.want)]
			if len(got) != 1 || len(indices) != 1 || indices[0].index != 7 || !indices[0].usable {
				t.Fatalf("assigned addresses %v", got)
			}
		})
	}
}

func TestLocalAddressInventoryFailsClosed(t *testing.T) {
	t.Parallel()
	valid := localAddressMessage(t, "", "2001:db8::1", 0)
	failedDone := make([]byte, localAddressFlagsSize)
	binary.NativeEndian.PutUint32(failedDone, ^uint32(0))
	malformedAttributes := append([]byte{unix.AF_INET6, 64, 0, 0, 7, 0, 0, 0}, []byte{5, 0, 1, 0, 1, 0, 0, 0}...)
	for _, msg := range []syscall.NetlinkMessage{
		{Header: syscall.NlMsghdr{Type: syscall.RTM_NEWADDR}, Data: []byte{unix.AF_INET6}},
		{Header: syscall.NlMsghdr{Type: syscall.NLMSG_DONE, Flags: unix.NLM_F_DUMP_INTR}},
		{Header: syscall.NlMsghdr{Type: syscall.NLMSG_DONE}, Data: failedDone},
		{Header: syscall.NlMsghdr{Type: syscall.RTM_NEWADDR}, Data: malformedAttributes},
	} {
		if got, err := assignedLocalAddresses([]syscall.NetlinkMessage{valid, msg}); err == nil || got != nil {
			t.Fatalf("incomplete inventory accepted: %v, %v", got, err)
		}
	}
}

func TestLocalAddressAvailabilityKeepsScopeAndBindSemantics(t *testing.T) {
	t.Parallel()
	assigned := map[netip.Addr][]localAddressAssignment{
		netip.MustParseAddr("192.0.2.1"): {{index: 7, usable: true}},
		netip.MustParseAddr("fe80::1"):   {{index: 7, usable: true}},
	}
	interfaces := []net.Interface{{Name: "uplink0", Index: 7}, {Name: "other0", Index: 8}}
	for _, tc := range []struct {
		local, device string
		pending       int
	}{
		{"192.0.2.1", "other0", 0}, // Global source addresses may live on another interface.
		{"::ffff:192.0.2.1", "", 0},
		{"192.0.2.2", "uplink0", 1},
		{"fe80::1", "uplink0", 0},
		{"fe80::1", "other0", 1},
		{"fe80::1%7", "", 0},
		{"fe80::1%8", "", 1},
		{"fe80::1%uplink0", "", 0},
		{"0.0.0.0", "missing0", 0},
	} {
		claim := LocalAddress{Addr: netip.MustParseAddr(tc.local), Interface: tc.device}
		count, err := checkLocalAddresses([]LocalAddress{claim}, assigned, interfaces)
		if count != tc.pending || (err != nil) != (tc.pending != 0) {
			t.Fatalf("%+v: count %d, error %v", claim, count, err)
		}
		if err != nil && !errors.Is(err, bfd.ErrResourceUnavailable) {
			t.Fatal(err)
		}
	}
}

func TestLocalAddressInventoryUsesLegacyHeaderFlags(t *testing.T) {
	t.Parallel()
	msg := localAddressMessage(t, "", "2001:db8::1", 0)
	msg.Data = msg.Data[:len(msg.Data)-8]
	msg.Data[2] = unix.IFA_F_TENTATIVE
	got, err := assignedLocalAddresses([]syscall.NetlinkMessage{msg})
	assignments := got[netip.MustParseAddr("2001:db8::1")]
	if err != nil || len(assignments) != 1 || assignments[0].usable {
		t.Fatalf("tentative header address accepted: %v, %v", got, err)
	}
}

func TestLocalAddressAvailabilityReadsKernelLoopback(t *testing.T) {
	t.Parallel()
	count, err := CheckLocalAddressesAvailable([]LocalAddress{
		{Addr: netip.MustParseAddr("127.0.0.1")}, {Addr: netip.MustParseAddr("127.0.0.92")},
	})
	if err != nil || count != 0 {
		t.Fatalf("loopback bind address: %d, %v", count, err)
	}
}

func localAddressMessage(t *testing.T, local, peer string, flags uint32) syscall.NetlinkMessage {
	t.Helper()
	addr := netip.MustParseAddr(peer)
	family := byte(unix.AF_INET6)
	if addr.Is4() {
		family = unix.AF_INET
	}
	data := []byte{family, 0, 0, 0, 7, 0, 0, 0}
	binary.NativeEndian.PutUint32(data[4:8], 7)
	appendAttr := func(kind uint16, value []byte) {
		attr := make([]byte, (4+len(value)+3)&^3)
		binary.NativeEndian.PutUint16(attr[:2], uint16(4+len(value)))
		binary.NativeEndian.PutUint16(attr[2:4], kind)
		copy(attr[4:], value)
		data = append(data, attr...)
	}
	appendAttr(unix.IFA_ADDRESS, addr.AsSlice())
	if local != "" {
		appendAttr(unix.IFA_LOCAL, netip.MustParseAddr(local).AsSlice())
	}
	var extended [4]byte
	binary.NativeEndian.PutUint32(extended[:], flags)
	appendAttr(unix.IFA_FLAGS, extended[:])
	return syscall.NetlinkMessage{Header: syscall.NlMsghdr{Type: syscall.RTM_NEWADDR}, Data: data}
}
