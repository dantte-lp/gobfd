package netio

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestNetworkManagerStrictReplyContracts(t *testing.T) {
	t.Parallel()
	const manager = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	const device = dbus.ObjectPath("/org/freedesktop/NetworkManager/Devices/2")
	const active = dbus.ObjectPath("/org/freedesktop/NetworkManager/ActiveConnection/1")
	const profile = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/1")
	settings := networkManagerConnectionSettings{"connection": {
		"id": dbus.MakeVariant("fixture"), "type": dbus.MakeVariant("802-3-ethernet"),
		"uuid": dbus.MakeVariant("d9cbb60e-8c3d-4d01-8f22-111111111111"),
	}, "802-3-ethernet": {}}
	for _, rpc := range []struct {
		name, iface, member string
		path                dbus.ObjectPath
		args, reply, coerce []any
		root                []any
		allowRoot, property bool
		want                any
		call                func(dbusNetworkManager, context.Context) (any, error)
	}{
		{
			name: "device lookup", path: manager, iface: "org.freedesktop.NetworkManager", member: "GetDeviceByIpIface",
			args: []any{"eth0"}, reply: []any{device}, coerce: []any{string(device)}, root: []any{dbus.ObjectPath("/")},
			want: device, call: func(c dbusNetworkManager, ctx context.Context) (any, error) {
				return c.DeviceByIPInterface(ctx, "eth0")
			},
		},
		{
			name: "activate", path: manager, iface: "org.freedesktop.NetworkManager", member: "ActivateConnection",
			args: []any{profile, device, dbus.ObjectPath("/")}, reply: []any{active}, coerce: []any{string(active)},
			root: []any{dbus.ObjectPath("/")}, want: active,
			call: func(c dbusNetworkManager, ctx context.Context) (any, error) {
				return c.ActivateConnection(ctx, profile, device)
			},
		},
		{
			name: "active property", path: device, iface: "org.freedesktop.DBus.Properties", member: "Get",
			args:  []any{"org.freedesktop.NetworkManager.Device", "ActiveConnection"},
			reply: []any{dbus.MakeVariant(active)}, coerce: []any{dbus.MakeVariant(string(active))},
			root: []any{dbus.MakeVariant(dbus.ObjectPath("/"))}, allowRoot: true, property: true, want: active,
			call: func(c dbusNetworkManager, ctx context.Context) (any, error) {
				return c.DeviceActiveConnection(ctx, device)
			},
		},
		{
			name: "settings property", path: active, iface: "org.freedesktop.DBus.Properties", member: "Get",
			args:  []any{"org.freedesktop.NetworkManager.Connection.Active", "Connection"},
			reply: []any{dbus.MakeVariant(profile)}, coerce: []any{dbus.MakeVariant(string(profile))},
			property: true, want: profile,
			call: func(c dbusNetworkManager, ctx context.Context) (any, error) {
				return c.ActiveConnectionConnection(ctx, active)
			},
		},
		{
			name: "available property", path: device, iface: "org.freedesktop.DBus.Properties", member: "Get",
			args:   []any{"org.freedesktop.NetworkManager.Device", "AvailableConnections"},
			reply:  []any{dbus.MakeVariant([]dbus.ObjectPath{profile})},
			coerce: []any{dbus.MakeVariant([]string{string(profile)})},
			root:   []any{dbus.MakeVariant([]dbus.ObjectPath{"/"})}, property: true, want: []dbus.ObjectPath{profile},
			call: func(c dbusNetworkManager, ctx context.Context) (any, error) {
				return c.DeviceAvailableConnections(ctx, device)
			},
		},
		{
			name: "settings", path: profile, iface: "org.freedesktop.NetworkManager.Settings.Connection", member: "GetSettings",
			reply: []any{settings}, want: settings,
			coerce: []any{map[string]map[string]string{"connection": {
				"id": "fixture", "type": "802-3-ethernet", "uuid": "d9cbb60e-8c3d-4d01-8f22-111111111111",
			}, "802-3-ethernet": {}}},
			call: func(c dbusNetworkManager, ctx context.Context) (any, error) {
				return c.ConnectionSettings(ctx, profile)
			},
		},
		{
			name: "deactivate", path: manager, iface: "org.freedesktop.NetworkManager", member: "DeactivateConnection",
			args: []any{active}, coerce: []any{true},
			call: func(c dbusNetworkManager, ctx context.Context) (any, error) {
				return nil, c.DeactivateConnection(ctx, active)
			},
		},
	} {
		for _, mode := range []string{"valid", "coercible", "arity", "remote", "raw property", "root", "empty list"} {
			if (mode == "raw property" && !rpc.property) || (mode == "root" && rpc.root == nil) ||
				(mode == "empty list" && rpc.name != "available property") {
				continue
			}
			t.Run(rpc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				client := networkManagerWireClient(t, func(request *dbus.Message) *dbus.Message {
					if request.Headers[dbus.FieldDestination].Value() != "org.freedesktop.NetworkManager" ||
						request.Headers[dbus.FieldPath].Value() != rpc.path ||
						request.Headers[dbus.FieldInterface].Value() != rpc.iface ||
						request.Headers[dbus.FieldMember].Value() != rpc.member ||
						len(request.Body) != len(rpc.args) ||
						(len(rpc.args) != 0 && !reflect.DeepEqual(request.Body, rpc.args)) {
						t.Errorf("unexpected request: %#v", request)
					}
					body := rpc.reply
					switch mode {
					case "coercible":
						body = rpc.coerce
					case "arity":
						body = []any{true, true}
					case "raw property":
						body = []any{rpc.reply[0].(dbus.Variant).Value()}
					case "root":
						body = rpc.root
					case "empty list":
						body = []any{dbus.MakeVariant([]dbus.ObjectPath{})}
					case "remote":
						return &dbus.Message{
							Type: dbus.TypeError,
							Headers: map[dbus.HeaderField]dbus.Variant{
								dbus.FieldErrorName: dbus.MakeVariant("org.freedesktop.NetworkManager.Settings.PermissionDenied"),
							}, Body: []any{"private rejection"},
						}
					}
					return &dbus.Message{Type: dbus.TypeMethodReply, Body: body}
				})
				got, err := rpc.call(client, t.Context())
				switch {
				case mode == "valid":
					if err != nil || !reflect.DeepEqual(got, rpc.want) {
						t.Errorf("valid reply returned %#v, %v, want %#v", got, err, rpc.want)
					}
				case mode == "root" && rpc.allowRoot:
					if err != nil || got != dbus.ObjectPath("/") {
						t.Errorf("inactive root sentinel returned %#v, %v", got, err)
					}
				case mode == "empty list":
					paths, valid := got.([]dbus.ObjectPath)
					if err != nil || !valid || len(paths) != 0 {
						t.Errorf("empty available list returned %#v, %v", got, err)
					}
				case mode == "remote":
					remote, ok := errors.AsType[dbus.Error](err)
					if !ok || remote.Name != "org.freedesktop.NetworkManager.Settings.PermissionDenied" {
						t.Errorf("remote error was lost: %v", err)
					}
				default:
					if err == nil {
						t.Errorf("malformed %s reply accepted as %#v", mode, got)
					}
				}
			})
		}
	}
}
