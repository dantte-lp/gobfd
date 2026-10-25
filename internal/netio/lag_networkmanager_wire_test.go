package netio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestNetworkManagerPropertyRequestsHonorContext(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name, iface, property string
		path                  dbus.ObjectPath
		want                  any
		call                  func(dbusNetworkManager, context.Context) (any, error)
	}{
		{
			"active connection", networkManagerDeviceInterface, "ActiveConnection", fakeDevicePath, fakeActivePath,
			func(client dbusNetworkManager, ctx context.Context) (any, error) {
				return client.DeviceActiveConnection(ctx, fakeDevicePath)
			},
		},
		{
			"available connections", networkManagerDeviceInterface, "AvailableConnections",
			fakeDevicePath,
			[]dbus.ObjectPath{fakeConnectionPath},
			func(client dbusNetworkManager, ctx context.Context) (any, error) {
				return client.DeviceAvailableConnections(ctx, fakeDevicePath)
			},
		},
		{
			"settings connection", networkManagerActiveInterface, "Connection", fakeActivePath, fakeConnectionPath,
			func(client dbusNetworkManager, ctx context.Context) (any, error) {
				return client.ActiveConnectionConnection(ctx, fakeActivePath)
			},
		},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{"reply", "remote error", "wrong type", "cancel", "deadline"} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()

					received := make(chan struct{}, 1)
					client := networkManagerWireClient(t, func(request *dbus.Message) *dbus.Message {
						if request.Type != dbus.TypeMethodCall ||
							request.Headers[dbus.FieldDestination].Value() != networkManagerBusName ||
							request.Headers[dbus.FieldPath].Value() != operation.path ||
							request.Headers[dbus.FieldInterface].Value() != "org.freedesktop.DBus.Properties" ||
							request.Headers[dbus.FieldMember].Value() != "Get" ||
							!reflect.DeepEqual(request.Body, []any{operation.iface, operation.property}) {
							t.Errorf("unexpected property request: %#v", request)
						}
						received <- struct{}{}
						if mode == "cancel" || mode == "deadline" {
							return nil
						}
						if mode == "remote error" {
							return &dbus.Message{
								Type: dbus.TypeError,
								Headers: map[dbus.HeaderField]dbus.Variant{
									dbus.FieldErrorName: dbus.MakeVariant("org.freedesktop.NetworkManager.TestFailure"),
								}, Body: []any{"fixture failure"},
							}
						}
						value := operation.want
						if mode == "wrong type" {
							value = map[string]string{"invalid": "property type"}
						}
						return &dbus.Message{Type: dbus.TypeMethodReply, Body: []any{dbus.MakeVariant(value)}}
					})
					ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
					defer cancel()
					result := make(chan error, 1)
					stopped := make(chan struct{})
					go func() {
						defer close(stopped)
						got, err := operation.call(client, ctx)
						if mode == "reply" && !reflect.DeepEqual(got, operation.want) {
							t.Errorf("property value = %#v, want %#v", got, operation.want)
						}
						result <- err
					}()
					t.Cleanup(func() {
						if err := client.Close(); err != nil {
							t.Error(err)
						}
						select {
						case <-stopped:
						case <-time.After(time.Second):
							t.Error("property caller did not stop after connection close")
						}
					})
					select {
					case <-received:
					case <-time.After(time.Second):
						t.Fatal("property request did not reach private peer")
					}
					if mode == "cancel" {
						cancel()
					}
					select {
					case err := <-result:
						switch mode {
						case "reply":
							if err != nil {
								t.Error(err)
							}
						case "cancel":
							if !errors.Is(err, context.Canceled) {
								t.Errorf("property error = %v, want canceled", err)
							}
						case "deadline":
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Errorf("property error = %v, want deadline", err)
							}
						case "remote error":
							remoteErr, ok := errors.AsType[dbus.Error](err)
							if !ok || remoteErr.Name != "org.freedesktop.NetworkManager.TestFailure" {
								t.Errorf("property error = %v, want wrapped remote error", err)
							}
						case "wrong type":
							if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
								t.Errorf("property error = %v, want type rejection", err)
							}
						}
					case <-time.After(time.Second):
						t.Error("property request ignored caller context")
					}
				})
			}
		})
	}
}

func networkManagerWireClient(t *testing.T, handle func(*dbus.Message) *dbus.Message) dbusNetworkManager {
	t.Helper()

	local, remote := net.Pipe()
	client, err := dbus.NewConn(local)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- serveNetworkManagerWire(remote, handle)
	}()
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
		if err := remote.Close(); err != nil {
			t.Error(err)
		}
		select {
		case err := <-finished:
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) &&
				!errors.Is(err, net.ErrClosed) {
				t.Errorf("private D-Bus peer: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("private D-Bus peer did not stop")
		}
	})
	if err := local.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := client.Auth([]dbus.Auth{dbus.AuthExternal("0")}); err != nil {
		t.Fatalf("authenticate private D-Bus client: %v", err)
	}
	if err := local.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return dbusNetworkManager{conn: client}
}

func serveNetworkManagerWire(remote net.Conn, handle func(*dbus.Message) *dbus.Message) error {
	reader := bufio.NewReader(remote)
	for _, step := range []struct{ request, reply string }{
		{"\x00AUTH\r\n", "REJECTED EXTERNAL\r\n"},
		{"AUTH EXTERNAL\r\n", "OK 0123456789abcdef0123456789abcdef\r\n"},
		{"BEGIN\r\n", ""},
	} {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read private auth: %w", err)
		}
		if line != step.request {
			return fmt.Errorf("private auth request %q, want %q", line, step.request)
		}
		if _, err := io.WriteString(remote, step.reply); err != nil {
			return fmt.Errorf("write private auth: %w", err)
		}
	}
	peer, err := dbus.NewConn(remote)
	if err != nil {
		return fmt.Errorf("create private D-Bus peer: %w", err)
	}
	defer peer.Close()
	for {
		request, err := dbus.DecodeMessage(reader)
		if err != nil {
			return fmt.Errorf("decode private request: %w", err)
		}
		reply := handle(request)
		if reply == nil {
			continue
		}
		if reply.Headers == nil {
			reply.Headers = make(map[dbus.HeaderField]dbus.Variant)
		}
		reply.Headers[dbus.FieldReplySerial] = dbus.MakeVariant(request.Serial())
		reply.Headers[dbus.FieldSignature] = dbus.MakeVariant(dbus.SignatureOf(reply.Body...))
		if err := peer.Send(reply, nil).Err; err != nil {
			return fmt.Errorf("reply to private request: %w", err)
		}
	}
}
