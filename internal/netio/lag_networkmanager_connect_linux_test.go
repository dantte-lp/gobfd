//go:build linux

package netio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestNetworkManagerConnectHonorsContext(t *testing.T) {
	for _, phase := range []string{"auth", "hello"} {
		for _, mode := range []string{"cancel", "deadline"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				listener, address := networkManagerTestBus(t, "unix")
				t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address)
				reached := make(chan struct{}, 1)
				ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
				defer cancel()
				result := networkManagerConnectResult(ctx, t)
				networkManagerAcceptBus(t, listener, func(remote net.Conn) error {
					if phase == "auth" {
						if _, err := bufio.NewReader(remote).ReadString('\n'); err != nil {
							return err
						}
						reached <- struct{}{}
						_, err := io.Copy(io.Discard, remote)
						return err
					}
					return serveNetworkManagerWire(remote, func(request *dbus.Message) *dbus.Message {
						if request.Headers[dbus.FieldMember].Value() != "Hello" {
							t.Errorf("startup request = %#v, want Hello", request)
						}
						reached <- struct{}{}
						return nil
					})
				})
				select {
				case <-reached:
				case <-time.After(time.Second):
					t.Fatal("connection did not reach the private stalled phase")
				}
				if mode == "cancel" {
					cancel()
				}
				select {
				case err := <-result:
					want := context.DeadlineExceeded
					if mode == "cancel" {
						want = context.Canceled
					}
					if !errors.Is(err, want) {
						t.Errorf("connect error = %v, want %v", err, want)
					}
				case <-time.After(time.Second):
					t.Error("system-bus connection ignored caller context")
				}
			})
		}
	}
}

func TestNetworkManagerConnectPreservesBusAddressTransports(t *testing.T) {
	testNetworkManagerBusTransports(t, []string{"unix", "abstract"})
}

func testNetworkManagerBusTransports(t *testing.T, transports []string) {
	t.Helper()
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			listener, address := networkManagerTestBus(t, transport)
			nonce := []byte("0123456789abcdef")
			if transport == "nonce-tcp" {
				path := filepath.Join(t.TempDir(), "nonce")
				if err := os.WriteFile(path, nonce, 0o600); err != nil {
					t.Fatal(err)
				}
				address += ",noncefile=" + dbus.EscapeBusAddressValue(path)
			}
			t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unsupported:;unix:path=/missing-gobfd-nm-bus;"+address)
			hello := make(chan struct{}, 1)
			networkManagerAcceptBus(t, listener, func(remote net.Conn) error {
				if transport == "nonce-tcp" {
					got := make([]byte, len(nonce))
					if _, err := io.ReadFull(remote, got); err != nil {
						return err
					}
					if string(got) != string(nonce) {
						t.Error("nonce prelude changed")
					}
				}
				return serveNetworkManagerWire(remote, func(request *dbus.Message) *dbus.Message {
					if request.Headers[dbus.FieldMember].Value() != "Hello" {
						t.Errorf("startup request = %#v, want Hello", request)
					}
					hello <- struct{}{}
					return &dbus.Message{Type: dbus.TypeMethodReply, Body: []any{":1.42"}}
				})
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			client, err := newDBusNetworkManagerLAGClient().newDBus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-hello:
			default:
				t.Error("factory returned before the bus registration")
			}
			if err := client.Close(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestNetworkManagerConnectCanceledDialAndMissingBus(t *testing.T) {
	t.Run("already canceled", func(t *testing.T) {
		listener, address := networkManagerTestBus(t, "unix")
		t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		result := networkManagerConnectResult(ctx, t)
		networkManagerAcceptBus(t, listener, func(remote net.Conn) error {
			_, err := io.Copy(io.Discard, remote)
			return err
		})
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("connect error = %v, want canceled", err)
			}
		case <-time.After(time.Second):
			t.Error("already canceled connect attempted blocking I/O")
		}
	})
	t.Run("missing bus", func(t *testing.T) {
		t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "absent"))
		client, err := newDBusNetworkManagerLAGClient().newDBus(t.Context())
		if client != nil || err == nil {
			t.Errorf("missing bus returned %v, %v", client, err)
		}
	})
}

func TestNetworkManagerBusAddressOptions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		address string
		want    networkManagerBusEndpoint
	}{
		{"unix:path=%2Ftmp%2Fbus%3Ba%2Cb,unknown=x", networkManagerBusEndpoint{network: "unix", target: "/tmp/bus;a,b"}},
		{"unix:path=/first,path=/second", networkManagerBusEndpoint{network: "unix", target: "/first"}},
		{"unix:abstract=private", networkManagerBusEndpoint{network: "unix", target: "@private"}},
		{"missing-transport", networkManagerBusEndpoint{}},
		{"unknown:path=/bus", networkManagerBusEndpoint{}},
		{"unix:", networkManagerBusEndpoint{}},
		{"unix:path=/bus,abstract=private", networkManagerBusEndpoint{}},
		{"unix:path=,path=/second", networkManagerBusEndpoint{}},
		{"unix:path=%GG", networkManagerBusEndpoint{}},
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

func TestNetworkManagerConnectUnixDescriptorNegotiation(t *testing.T) {
	for _, acceptFDs := range []bool{false, true} {
		t.Run(fmt.Sprintf("accept=%t", acceptFDs), func(t *testing.T) {
			listener, address := networkManagerTestBus(t, "unix")
			t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address)
			networkManagerAcceptBus(t, listener, func(remote net.Conn) error {
				return serveNetworkManagerWireFDs(remote, func(request *dbus.Message) *dbus.Message {
					if request.Headers[dbus.FieldMember].Value() != "Hello" {
						t.Errorf("startup request = %#v, want Hello", request)
					}
					return &dbus.Message{Type: dbus.TypeMethodReply, Body: []any{":1.42"}}
				}, acceptFDs)
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			client, err := newDBusNetworkManagerLAGClient().newDBus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if closeErr := client.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
	}
}

func TestNetworkManagerConnectDoesNotRetryAuthenticationPeers(t *testing.T) {
	for _, phase := range []string{"auth", "hello"} {
		t.Run(phase, func(t *testing.T) {
			first, firstAddress := networkManagerTestBus(t, "unix")
			second, secondAddress := networkManagerTestBus(t, "unix")
			t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", firstAddress+";"+secondAddress)
			networkManagerAcceptBus(t, first, func(remote net.Conn) error {
				if phase == "auth" {
					if _, err := bufio.NewReader(remote).ReadString('\n'); err != nil {
						return err
					}
					_, err := io.WriteString(remote, "REJECTED UNKNOWN\r\n")
					return err
				}
				return serveNetworkManagerWire(remote, func(*dbus.Message) *dbus.Message {
					return &dbus.Message{
						Type: dbus.TypeError,
						Headers: map[dbus.HeaderField]dbus.Variant{
							dbus.FieldErrorName: dbus.MakeVariant("org.freedesktop.DBus.Error.AccessDenied"),
						}, Body: []any{"private rejection"},
					}
				})
			})
			var retries atomic.Int32
			networkManagerAcceptBus(t, second, func(remote net.Conn) error {
				retries.Add(1)
				return serveNetworkManagerWire(remote, func(*dbus.Message) *dbus.Message {
					return &dbus.Message{Type: dbus.TypeMethodReply, Body: []any{":1.43"}}
				})
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			client, err := newDBusNetworkManagerLAGClient().newDBus(ctx)
			if client != nil || err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("rejected peer returned client %v, error %v", client, err)
			}
			if retries.Load() != 0 {
				t.Error("authentication failure dialed the next peer")
			}
		})
	}
}

func networkManagerTestBus(t *testing.T, transport string) (net.Listener, string) {
	t.Helper()

	network, endpoint := "unix", filepath.Join(t.TempDir(), "bus")
	if transport == "abstract" {
		endpoint = "@" + filepath.Base(filepath.Dir(filepath.Dir(endpoint)))
	}
	if transport == "tcp" || transport == "nonce-tcp" {
		network, endpoint = "tcp4", "127.0.0.1:0"
	}
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), network, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if network == "tcp4" {
		_, port, splitErr := net.SplitHostPort(listener.Addr().String())
		if splitErr != nil {
			t.Fatal(splitErr)
		}
		return listener, transport + ":host=127.0.0.1,port=" + port + ",family=ipv4"
	}
	if transport == "abstract" {
		return listener, "unix:abstract=" + dbus.EscapeBusAddressValue(endpoint[1:])
	}
	return listener, "unix:path=" + dbus.EscapeBusAddressValue(endpoint)
}

func networkManagerAcceptBus(t *testing.T, listener net.Listener, serve func(net.Conn) error) {
	t.Helper()

	accepted := make(chan net.Conn, 1)
	finished := make(chan error, 1)
	go func() {
		remote, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		accepted <- remote
		finished <- serve(remote)
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
		select {
		case remote := <-accepted:
			_ = remote.Close()
		default:
		}
		select {
		case err := <-finished:
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("private bus peer did not stop")
		}
	})
}

func networkManagerConnectResult(ctx context.Context, t *testing.T) <-chan error {
	t.Helper()

	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		client, err := newDBusNetworkManagerLAGClient().newDBus(ctx)
		if client != nil {
			err = errors.Join(err, client.Close())
		}
		result <- err
	}()
	t.Cleanup(func() {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("private bus connector did not stop")
		}
	})
	return result
}
