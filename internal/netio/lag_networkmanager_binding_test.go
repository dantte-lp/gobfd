package netio

import (
	"context"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestDBusNetworkManagerControllerBinding(t *testing.T) {
	t.Parallel()
	const uuid = "d9cbb60e-8c3d-4d01-8f22-111111111111"
	const parent = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/2")
	for _, test := range []struct {
		name string
		want bool
	}{
		{"modern", true},
		{"legacy", true},
		{"active unbound", true},
		{"inactive unbound", false},
		{"foreign active", false},
		{"uppercase UUID", true},
		{"matching aliases", true},
		{"modern priority", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			nm := &networkManagerBindingDBus{
				fakeNetworkManagerDBus: newFakeNetworkManagerDBus(),
				parentActive:           dbusNoObjectPath, parentProfile: parent,
			}
			nm.activeConnection = dbusNoObjectPath
			port := nm.settings[fakeConnectionPath]["connection"]
			port["controller"] = dbus.MakeVariant(uuid)
			nm.settings[parent] = networkManagerConnectionSettings{
				"connection": {
					"uuid": dbus.MakeVariant(uuid), "type": dbus.MakeVariant("bond"),
					"interface-name": dbus.MakeVariant("bond0"),
				},
				"bond": {"options": dbus.MakeVariant(map[string]string{"mode": "active-backup"})},
			}
			switch test.name {
			case "legacy":
				delete(nm.settings[parent]["connection"], "interface-name")
				nm.settings[parent]["bond"]["interface-name"] = dbus.MakeVariant("bond0")
			case "active unbound", "inactive unbound", "foreign active":
				delete(nm.settings[parent]["connection"], "interface-name")
				if test.name != "inactive unbound" {
					nm.parentActive = "/org/freedesktop/NetworkManager/ActiveConnection/2"
				}
				if test.name == "foreign active" {
					nm.parentProfile = "/org/freedesktop/NetworkManager/Settings/3"
				}
			case "uppercase UUID":
				port["controller"] = dbus.MakeVariant(strings.ToUpper(uuid))
			case "matching aliases":
				port["master"] = dbus.MakeVariant(uuid)
				port["slave-type"] = dbus.MakeVariant("bond")
			case "modern priority":
				nm.settings[parent]["bond"]["interface-name"] = dbus.MakeVariant("bond9")
			}
			client := &dbusNetworkManagerLAGClient{
				newDBus: func(context.Context) (networkManagerDBus, error) { return nm, nil },
				cache:   make(map[networkManagerMemberKey]dbus.ObjectPath),
			}
			client.rememberConnection("bond0", "eth0", fakeConnectionPath)
			err := client.AddBondInterface(t.Context(), "bond0", "eth0")
			if (err == nil) != test.want {
				t.Errorf("controller binding returned %v, want accepted=%t", err, test.want)
			}
			if test.want && (nm.activatedConnection != fakeConnectionPath || nm.activatedDevice != fakeDevicePath) {
				t.Errorf("valid binding activation profile=%q device=%q", nm.activatedConnection, nm.activatedDevice)
			}
			if !test.want && nm.activatedConnection != "" {
				t.Error("unproven controller binding changed the member")
			}
		})
	}
}

type networkManagerBindingDBus struct {
	*fakeNetworkManagerDBus

	parentActive, parentProfile dbus.ObjectPath
}

func (n *networkManagerBindingDBus) DeviceByIPInterface(ctx context.Context, iface string) (dbus.ObjectPath, error) {
	if iface == "bond0" {
		return "/org/freedesktop/NetworkManager/Devices/20", nil
	}
	return n.fakeNetworkManagerDBus.DeviceByIPInterface(ctx, iface)
}

func (n *networkManagerBindingDBus) DeviceActiveConnection(
	ctx context.Context, device dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	if device == "/org/freedesktop/NetworkManager/Devices/20" {
		return n.parentActive, nil
	}
	return n.fakeNetworkManagerDBus.DeviceActiveConnection(ctx, device)
}

func (n *networkManagerBindingDBus) ActiveConnectionConnection(
	ctx context.Context, active dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	if active == "/org/freedesktop/NetworkManager/ActiveConnection/2" {
		return n.parentProfile, nil
	}
	return n.fakeNetworkManagerDBus.ActiveConnectionConnection(ctx, active)
}
