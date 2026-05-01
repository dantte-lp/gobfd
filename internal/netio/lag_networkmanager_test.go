package netio

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestNetworkManagerLAGBackendDelegatesBondInterfaceOperations(t *testing.T) {
	t.Parallel()

	client := &recordingNetworkManagerLAGClient{}
	backend := NewNetworkManagerLAGBackend(NetworkManagerLAGBackendConfig{Client: client})

	if err := backend.RemoveMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if err := backend.AddMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	want := []networkManagerClientCall{
		{op: "remove", bond: "bond0", iface: "eth0"},
		{op: "add", bond: "bond0", iface: "eth0"},
	}
	if !reflect.DeepEqual(client.calls, want) {
		t.Fatalf("NetworkManager client calls = %#v, want %#v", client.calls, want)
	}
}

func TestNetworkManagerLAGBackendRejectsUnsafeInterfaceNames(t *testing.T) {
	t.Parallel()

	backend := NewNetworkManagerLAGBackend(NetworkManagerLAGBackendConfig{
		Client: &recordingNetworkManagerLAGClient{},
	})

	err := backend.RemoveMember(context.Background(), "bond0", "eth0/../../x")
	if !errors.Is(err, ErrInvalidLAGInterfaceName) {
		t.Fatalf("RemoveMember error = %v, want %v", err, ErrInvalidLAGInterfaceName)
	}
}

func TestDBusNetworkManagerLAGClientRemoveDeactivatesActiveConnection(t *testing.T) {
	t.Parallel()

	nm := newFakeNetworkManagerDBus()
	client := testDBusNetworkManagerLAGClient(nm)

	if err := client.RemoveBondInterface(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveBondInterface: %v", err)
	}

	if nm.deactivated != fakeActivePath {
		t.Fatalf("deactivated = %q, want %q", nm.deactivated, fakeActivePath)
	}
	if !nm.closed {
		t.Fatal("RemoveBondInterface did not close NetworkManager D-Bus client")
	}
}

func TestDBusNetworkManagerLAGClientAddUsesRememberedConnection(t *testing.T) {
	t.Parallel()

	nm := newFakeNetworkManagerDBus()
	client := testDBusNetworkManagerLAGClient(nm)

	if err := client.RemoveBondInterface(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveBondInterface: %v", err)
	}
	nm.activeConnection = dbusNoObjectPath
	nm.closed = false

	if err := client.AddBondInterface(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddBondInterface: %v", err)
	}

	if nm.activatedConnection != fakeConnectionPath {
		t.Fatalf("activated connection = %q, want %q", nm.activatedConnection, fakeConnectionPath)
	}
	if nm.activatedDevice != fakeDevicePath {
		t.Fatalf("activated device = %q, want %q", nm.activatedDevice, fakeDevicePath)
	}
	if !nm.closed {
		t.Fatal("AddBondInterface did not close NetworkManager D-Bus client")
	}
}

func TestDBusNetworkManagerLAGClientAddDiscoversAvailableBondPortConnection(t *testing.T) {
	t.Parallel()

	nm := newFakeNetworkManagerDBus()
	nm.activeConnection = dbusNoObjectPath
	client := testDBusNetworkManagerLAGClient(nm)

	if err := client.AddBondInterface(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddBondInterface: %v", err)
	}

	if nm.activatedConnection != fakeConnectionPath {
		t.Fatalf("activated connection = %q, want %q", nm.activatedConnection, fakeConnectionPath)
	}
}

func TestDBusNetworkManagerLAGClientAddNoopsWhenAlreadyActive(t *testing.T) {
	t.Parallel()

	nm := newFakeNetworkManagerDBus()
	client := testDBusNetworkManagerLAGClient(nm)

	if err := client.AddBondInterface(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddBondInterface: %v", err)
	}

	if nm.activatedConnection != "" {
		t.Fatalf("activated connection = %q, want no activation", nm.activatedConnection)
	}
}

func TestDBusNetworkManagerLAGClientAddErrorsWhenConnectionCannotBeFound(t *testing.T) {
	t.Parallel()

	nm := newFakeNetworkManagerDBus()
	nm.activeConnection = dbusNoObjectPath
	nm.availableConnections = nil
	client := testDBusNetworkManagerLAGClient(nm)

	err := client.AddBondInterface(context.Background(), "bond0", "eth0")
	if !errors.Is(err, ErrNetworkManagerConnectionNotFound) {
		t.Fatalf("AddBondInterface error = %v, want %v", err, ErrNetworkManagerConnectionNotFound)
	}
}

func TestNetworkManagerSettingsMatchBondPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings networkManagerConnectionSettings
		want     bool
	}{
		{
			name: "modern controller port type",
			settings: networkManagerConnectionSettings{
				"connection": {
					"interface-name": dbus.MakeVariant("eth0"),
					"controller":     dbus.MakeVariant("bond0"),
					"port-type":      dbus.MakeVariant("bond"),
				},
			},
			want: true,
		},
		{
			name: "legacy master slave type",
			settings: networkManagerConnectionSettings{
				"connection": {
					"interface-name": dbus.MakeVariant("eth0"),
					"master":         dbus.MakeVariant("bond0"),
					"slave-type":     dbus.MakeVariant("bond"),
				},
			},
			want: true,
		},
		{
			name: "wrong interface",
			settings: networkManagerConnectionSettings{
				"connection": {
					"interface-name": dbus.MakeVariant("eth1"),
					"controller":     dbus.MakeVariant("bond0"),
					"port-type":      dbus.MakeVariant("bond"),
				},
			},
			want: false,
		},
		{
			name: "wrong controller",
			settings: networkManagerConnectionSettings{
				"connection": {
					"interface-name": dbus.MakeVariant("eth0"),
					"controller":     dbus.MakeVariant("bond1"),
					"port-type":      dbus.MakeVariant("bond"),
				},
			},
			want: false,
		},
		{
			name: "wrong port type",
			settings: networkManagerConnectionSettings{
				"connection": {
					"interface-name": dbus.MakeVariant("eth0"),
					"controller":     dbus.MakeVariant("bond0"),
					"port-type":      dbus.MakeVariant("bridge"),
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := networkManagerSettingsMatchBondPort(tt.settings, "bond0", "eth0")
			if got != tt.want {
				t.Fatalf("networkManagerSettingsMatchBondPort = %v, want %v", got, tt.want)
			}
		})
	}
}

type networkManagerClientCall struct {
	op    string
	bond  string
	iface string
}

type recordingNetworkManagerLAGClient struct {
	calls []networkManagerClientCall
}

func (r *recordingNetworkManagerLAGClient) RemoveBondInterface(
	_ context.Context,
	bond string,
	iface string,
) error {
	r.calls = append(r.calls, networkManagerClientCall{op: "remove", bond: bond, iface: iface})
	return nil
}

func (r *recordingNetworkManagerLAGClient) AddBondInterface(
	_ context.Context,
	bond string,
	iface string,
) error {
	r.calls = append(r.calls, networkManagerClientCall{op: "add", bond: bond, iface: iface})
	return nil
}

const (
	fakeDevicePath     = dbus.ObjectPath("/org/freedesktop/NetworkManager/Devices/2")
	fakeActivePath     = dbus.ObjectPath("/org/freedesktop/NetworkManager/ActiveConnection/1")
	fakeConnectionPath = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/1")
)

type fakeNetworkManagerDBus struct {
	activeConnection     dbus.ObjectPath
	availableConnections []dbus.ObjectPath
	settings             map[dbus.ObjectPath]networkManagerConnectionSettings
	deactivated          dbus.ObjectPath
	activatedConnection  dbus.ObjectPath
	activatedDevice      dbus.ObjectPath
	closed               bool
}

func newFakeNetworkManagerDBus() *fakeNetworkManagerDBus {
	return &fakeNetworkManagerDBus{
		activeConnection:     fakeActivePath,
		availableConnections: []dbus.ObjectPath{fakeConnectionPath},
		settings: map[dbus.ObjectPath]networkManagerConnectionSettings{
			fakeConnectionPath: {
				"connection": {
					"interface-name": dbus.MakeVariant("eth0"),
					"controller":     dbus.MakeVariant("bond0"),
					"port-type":      dbus.MakeVariant("bond"),
				},
			},
		},
	}
}

func (f *fakeNetworkManagerDBus) DeviceByIPInterface(
	_ context.Context,
	iface string,
) (dbus.ObjectPath, error) {
	if iface != "eth0" {
		return "", errors.New("unexpected iface")
	}
	return fakeDevicePath, nil
}

func (f *fakeNetworkManagerDBus) DeviceActiveConnection(
	context.Context,
	dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	return f.activeConnection, nil
}

func (f *fakeNetworkManagerDBus) DeviceAvailableConnections(
	context.Context,
	dbus.ObjectPath,
) ([]dbus.ObjectPath, error) {
	return append([]dbus.ObjectPath(nil), f.availableConnections...), nil
}

func (f *fakeNetworkManagerDBus) ActiveConnectionConnection(
	context.Context,
	dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	return fakeConnectionPath, nil
}

func (f *fakeNetworkManagerDBus) DeactivateConnection(
	_ context.Context,
	active dbus.ObjectPath,
) error {
	f.deactivated = active
	return nil
}

func (f *fakeNetworkManagerDBus) ActivateConnection(
	_ context.Context,
	connection dbus.ObjectPath,
	device dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	f.activatedConnection = connection
	f.activatedDevice = device
	f.activeConnection = fakeActivePath
	return fakeActivePath, nil
}

func (f *fakeNetworkManagerDBus) ConnectionSettings(
	_ context.Context,
	connection dbus.ObjectPath,
) (networkManagerConnectionSettings, error) {
	settings, ok := f.settings[connection]
	if !ok {
		return nil, ErrNetworkManagerConnectionNotFound
	}
	return settings, nil
}

func (f *fakeNetworkManagerDBus) Close() error {
	f.closed = true
	return nil
}

func testDBusNetworkManagerLAGClient(nm *fakeNetworkManagerDBus) *dbusNetworkManagerLAGClient {
	return &dbusNetworkManagerLAGClient{
		newDBus: func(context.Context) (networkManagerDBus, error) {
			return nm, nil
		},
		cache: make(map[networkManagerMemberKey]dbus.ObjectPath),
	}
}
