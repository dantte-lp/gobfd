package netio_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// A listener without a kernel device binding has nothing to rebind; the
// caller keeps it not ready instead of reporting a false recovery.
func TestListenerRebindDeviceRequiresDeviceBoundSocket(t *testing.T) {
	t.Parallel()

	ln := netio.NewListenerFromConn(NewMockPacketConn(netip.MustParseAddrPort("192.0.2.1:3784")), false)
	if err := ln.RebindDevice(); !errors.Is(err, netio.ErrRebindUnsupported) {
		t.Fatalf("RebindDevice() = %v, want %v", err, netio.ErrRebindUnsupported)
	}
}
