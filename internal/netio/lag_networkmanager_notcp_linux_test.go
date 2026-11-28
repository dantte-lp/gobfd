//go:build linux && godbus_no_tcp

package netio

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestNetworkManagerConnectTCPDisabledBeforeDNS(t *testing.T) {
	previous := net.DefaultResolver
	var lookups atomic.Int32
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		lookups.Add(1)
		return nil, &net.DNSError{Err: "unexpected private lookup", IsNotFound: true}
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	path := filepath.Join(t.TempDir(), "nonce")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"tcp", "nonce-tcp"} {
		t.Run(kind, func(t *testing.T) {
			lookups.Store(0)
			t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", kind+":host=gobfd-notcp.invalid.,port=12345,family=ipv4,noncefile="+
				dbus.EscapeBusAddressValue(path))
			client, err := newDBusNetworkManagerLAGClient().newDBus(t.Context())
			if client != nil || err == nil {
				t.Errorf("disabled TCP returned client %v, error %v", client, err)
			}
			if lookups.Load() != 0 {
				t.Error("disabled TCP reached DNS")
			}
		})
	}
}
