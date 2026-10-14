package main

import (
	"log/slog"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

type tupleRecordingSenderFactory struct {
	*nthFailureDeclarativeSenderFactory

	localAddrs []netip.Addr
	multiHop   []bool
}

func (f *tupleRecordingSenderFactory) createSenderForSession(
	localAddr netip.Addr,
	multiHop bool,
	logger *slog.Logger,
	opts ...netio.SenderOption,
) (bfd.PacketSender, uint16, error) {
	f.localAddrs = append(f.localAddrs, localAddr)
	f.multiHop = append(f.multiHop, multiHop)
	return f.nthFailureDeclarativeSenderFactory.createSenderForSession(localAddr, multiHop, logger, opts...)
}

// C-07: the daemon builds each unsolicited session's sender on the local
// address that received the packet (single-hop), scoping an IPv6
// link-local address to the receiving interface, and closes its port with
// the lease.
func TestUnsolicitedSessionSenderBindsReceivedTuple(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		dst, ifName, wantLocal string
	}{
		{"192.0.2.1", "eth0", "192.0.2.1"},
		{"2001:db8::1", "eth0", "2001:db8::1"},
		{"fe80::1", "eth0", "fe80::1%eth0"},
		{"192.0.2.1", "", "192.0.2.1"},
	} {
		sf := &tupleRecordingSenderFactory{nthFailureDeclarativeSenderFactory: newNthFailureDeclarativeSenderFactory(0)}
		newSender := unsolicitedSessionSender(sf, slog.New(slog.DiscardHandler))

		lease, err := newSender(bfd.PacketMeta{
			SrcAddr: netip.MustParseAddr("192.0.2.10"),
			DstAddr: netip.MustParseAddr(tc.dst),
			IfName:  tc.ifName,
		})
		if err != nil {
			t.Fatalf("%s on %q: %v", tc.dst, tc.ifName, err)
		}
		if len(sf.localAddrs) != 1 || sf.localAddrs[0] != netip.MustParseAddr(tc.wantLocal) || sf.multiHop[0] {
			t.Fatalf("%s on %q: sender local %v multihop %v, want %s single-hop",
				tc.dst, tc.ifName, sf.localAddrs, sf.multiHop, tc.wantLocal)
		}
		if err := lease.Close(); err != nil {
			t.Fatalf("close lease: %v", err)
		}
		if len(sf.closed) != 1 || len(sf.open) != 0 {
			t.Fatalf("closed ports %v, open %v; want one closed", sf.closed, sf.open)
		}
	}
}
