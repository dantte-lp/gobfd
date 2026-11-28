//go:build linux && !godbus_no_tcp

package netio

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

func TestNetworkManagerConnectTCPTransports(t *testing.T) {
	testNetworkManagerBusTransports(t, []string{"tcp", "nonce-tcp"})
}

func TestNetworkManagerTCPBusAddressOptions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		address string
		want    networkManagerBusEndpoint
	}{
		{"tcp:host=localhost,port=1234", networkManagerBusEndpoint{network: "tcp", target: "localhost:1234"}},
		{"tcp:host=127.0.0.1,port=1234,family=ipv4", networkManagerBusEndpoint{network: "tcp4", target: "127.0.0.1:1234"}},
		{"tcp:host=%3A%3A1,port=1234,family=ipv6", networkManagerBusEndpoint{network: "tcp6", target: "[::1]:1234"}},
		{
			"nonce-tcp:host=localhost,port=1234,noncefile=%2Ftmp%2Fnonce",
			networkManagerBusEndpoint{network: "tcp", target: "localhost:1234", nonceFile: "/tmp/nonce"},
		},
		{"tcp:host=localhost", networkManagerBusEndpoint{}},
		{"tcp:host=localhost,port=1234,family=invalid", networkManagerBusEndpoint{}},
		{"nonce-tcp:host=localhost,port=1234", networkManagerBusEndpoint{}},
	} {
		t.Run(test.address, func(t *testing.T) {
			t.Parallel()
			got, err := parseNetworkManagerBusEndpoint(test.address)
			if test.want.network == "" {
				if !errors.Is(err, errNetworkManagerBusAddress) {
					t.Errorf("invalid address returned %v, %v", got, err)
				}
			} else if err != nil || got != test.want {
				t.Errorf("address returned %v, %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestNetworkManagerConnectCancelsDNSLookup(t *testing.T) {
	previous := net.DefaultResolver
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, &net.DNSError{Err: "private DNS fixture stopped", IsNotFound: true}
		}
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "tcp:host=gobfd-context.invalid.,port=12345,family=ipv4")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := networkManagerConnectResult(ctx, t)
	t.Cleanup(func() { close(release) })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("bus Dial did not reach the private resolver")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Dial error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Error("system-bus Dial ignored caller cancellation")
	}
}

func TestNetworkManagerConnectRejectsUnsafeNonceFiles(t *testing.T) {
	for _, kind := range []string{"empty", "short", "long", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nonce")
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "fifo":
				err = unix.Mkfifo(path, 0o600)
			default:
				size := map[string]int{"empty": 0, "short": 15, "long": 17}[kind]
				err = os.WriteFile(path, make([]byte, size), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			listener, address := networkManagerTestBus(t, "nonce-tcp")
			t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address+",noncefile="+dbus.EscapeBusAddressValue(path))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result := networkManagerConnectResult(ctx, t)
			networkManagerAcceptBus(t, listener, func(remote net.Conn) error {
				written, copyErr := io.Copy(io.Discard, remote)
				if written != 0 {
					t.Errorf("invalid nonce sent %d bytes to peer", written)
				}
				return copyErr
			})
			if kind == "fifo" {
				t.Cleanup(func() {
					writer, openErr := os.OpenFile(path, os.O_WRONLY|unix.O_NONBLOCK, 0)
					if openErr == nil {
						if closeErr := writer.Close(); closeErr != nil {
							t.Error(closeErr)
						}
					} else if !errors.Is(openErr, unix.ENXIO) {
						t.Error(openErr)
					}
				})
			}
			select {
			case err := <-result:
				if !errors.Is(err, errNetworkManagerBusAddress) {
					t.Errorf("invalid nonce error = %v, want address rejection", err)
				}
			case <-time.After(time.Second):
				t.Error("invalid nonce file blocked system-bus startup")
			}
		})
	}
}
