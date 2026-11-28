package netio

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestDBusNetworkManagerRejectsForeignOwnership(t *testing.T) {
	t.Parallel()
	for _, invalid := range []struct {
		name, key string
		value     any
	}{
		{"foreign controller", "controller", "bond9"},
		{"foreign interface", "interface-name", "eth9"},
		{"foreign port type", "port-type", "bridge"},
		{"missing port type", "port-type", ""},
		{"malformed interface", "interface-name", true},
		{"malformed controller", "controller", true},
		{"malformed port type", "port-type", true},
		{"conflicting controller alias", "master", "bond9"},
		{"conflicting port alias", "slave-type", "bridge"},
	} {
		for _, operation := range []string{"remove", "active add", "cached add"} {
			t.Run(invalid.name+"/"+operation, func(t *testing.T) {
				t.Parallel()
				nm := newFakeNetworkManagerDBus()
				nm.settings[fakeConnectionPath]["connection"][invalid.key] = dbus.MakeVariant(invalid.value)
				client := testDBusNetworkManagerLAGClient(nm)
				if operation == "cached add" {
					nm.activeConnection = dbusNoObjectPath
					client.rememberConnection("bond0", "eth0", fakeConnectionPath)
				}
				var err error
				if operation == "remove" {
					err = client.RemoveBondInterface(t.Context(), "bond0", "eth0")
				} else {
					err = client.AddBondInterface(t.Context(), "bond0", "eth0")
				}
				if err == nil {
					t.Error("incompatible profile accepted as a bond0/eth0 port")
				}
				if nm.deactivated != "" || nm.activatedConnection != "" {
					t.Errorf("incompatible profile changed state: deactivate=%q activate=%q", nm.deactivated, nm.activatedConnection)
				}
			})
		}
	}
}

func TestDBusNetworkManagerRejectsDeletedCachedProfile(t *testing.T) {
	t.Parallel()
	nm := newFakeNetworkManagerDBus()
	nm.activeConnection = dbusNoObjectPath
	client := testDBusNetworkManagerLAGClient(nm)
	client.rememberConnection("bond0", "eth0", fakeConnectionPath)
	delete(nm.settings, fakeConnectionPath)
	err := client.AddBondInterface(t.Context(), "bond0", "eth0")
	if !errors.Is(err, ErrNetworkManagerConnectionNotFound) {
		t.Errorf("deleted cached profile returned %v", err)
	}
	if nm.activatedConnection != "" {
		t.Error("deleted cached profile was activated")
	}
}

func TestDBusNetworkManagerValidOwnerIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, legacy := range []bool{false, true} {
		name := "modern"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			nm := &networkManagerOwnerRecorder{fakeNetworkManagerDBus: newFakeNetworkManagerDBus()}
			settings := nm.settings[fakeConnectionPath]["connection"]
			delete(settings, "interface-name") // An unbound port uses the explicitly resolved device.
			if legacy {
				delete(settings, "controller")
				delete(settings, "port-type")
				settings["master"] = dbus.MakeVariant("bond0")
				settings["slave-type"] = dbus.MakeVariant("bond")
			}
			client := &dbusNetworkManagerLAGClient{
				newDBus: func(context.Context) (networkManagerDBus, error) { return nm, nil },
				cache:   make(map[networkManagerMemberKey]dbus.ObjectPath),
			}
			for range 2 {
				if err := client.RemoveBondInterface(t.Context(), "bond0", "eth0"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := client.AddBondInterface(t.Context(), "bond0", "eth0"); err != nil {
					t.Fatal(err)
				}
			}
			if nm.removes != 1 || nm.adds != 1 || nm.activatedDevice != fakeDevicePath {
				t.Errorf("repeated operations: remove=%d add=%d device=%q", nm.removes, nm.adds, nm.activatedDevice)
			}
		})
	}
}

func TestDBusNetworkManagerResolvesControllerUUID(t *testing.T) {
	t.Parallel()
	const controllerUUID = "d9cbb60e-8c3d-4d01-8f22-111111111111"
	const controllerPath = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/2")
	for _, operation := range []string{"remove", "active add", "cached add", "discovered add"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			nm := newFakeNetworkManagerDBus()
			nm.settings[fakeConnectionPath]["connection"]["controller"] = dbus.MakeVariant(controllerUUID)
			nm.settings[controllerPath] = networkManagerConnectionSettings{"connection": {
				"uuid": dbus.MakeVariant(controllerUUID), "type": dbus.MakeVariant("bond"),
				"interface-name": dbus.MakeVariant("bond0"),
			}}
			client := testDBusNetworkManagerLAGClient(nm)
			if operation == "cached add" || operation == "discovered add" {
				nm.activeConnection = dbusNoObjectPath
			}
			if operation == "cached add" {
				client.rememberConnection("bond0", "eth0", fakeConnectionPath)
			}
			var err error
			if operation == "remove" {
				err = client.RemoveBondInterface(t.Context(), "bond0", "eth0")
			} else {
				err = client.AddBondInterface(t.Context(), "bond0", "eth0")
			}
			if err != nil {
				t.Fatal(err)
			}
			if operation == "remove" && nm.deactivated != fakeActivePath {
				t.Error("valid UUID-owned port was not deactivated")
			}
			if (operation == "cached add" || operation == "discovered add") && nm.activatedConnection != fakeConnectionPath {
				t.Error("valid UUID-owned port was not activated")
			}
		})
	}
}

func TestDBusNetworkManagerRejectsForeignControllerUUID(t *testing.T) {
	t.Parallel()
	const uuid = "d9cbb60e-8c3d-4d01-8f22-111111111111"
	const parent = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/2")
	for _, kind := range []string{"wrong name", "wrong type", "deleted", "malformed name", "permission", "canceled"} {
		for _, operation := range []string{"remove", "active add", "cached add", "discovered add"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				t.Parallel()
				nm := newFakeNetworkManagerDBus()
				nm.settings[fakeConnectionPath]["connection"]["controller"] = dbus.MakeVariant(uuid)
				nm.settings[parent] = networkManagerConnectionSettings{"connection": {
					"uuid": dbus.MakeVariant(uuid), "type": dbus.MakeVariant("bond"), "interface-name": dbus.MakeVariant("bond0"),
				}}
				switch kind {
				case "wrong name":
					nm.settings[parent]["connection"]["interface-name"] = dbus.MakeVariant("bond9")
				case "wrong type":
					nm.settings[parent]["connection"]["type"] = dbus.MakeVariant("bridge")
				case "deleted":
					delete(nm.settings, parent)
				case "malformed name":
					nm.settings[parent]["connection"]["interface-name"] = dbus.MakeVariant(true)
				case "permission":
					nm.uuidError = dbus.Error{Name: "org.freedesktop.NetworkManager.Settings.PermissionDenied"}
				case "canceled":
					nm.uuidError = context.Canceled
				}
				client := testDBusNetworkManagerLAGClient(nm)
				if operation == "cached add" || operation == "discovered add" {
					nm.activeConnection = dbusNoObjectPath
				}
				if operation == "cached add" {
					client.rememberConnection("bond0", "eth0", fakeConnectionPath)
				}
				var err error
				if operation == "remove" {
					err = client.RemoveBondInterface(t.Context(), "bond0", "eth0")
				} else {
					err = client.AddBondInterface(t.Context(), "bond0", "eth0")
				}
				if err == nil || (kind == "canceled" && !errors.Is(err, context.Canceled)) {
					t.Errorf("controller validation returned %v, expected rejection with original error", err)
				}
				if kind == "permission" {
					remote, ok := errors.AsType[dbus.Error](err)
					if !ok || remote.Name != "org.freedesktop.NetworkManager.Settings.PermissionDenied" {
						t.Errorf("permission error was lost: %v", err)
					}
				}
				if nm.deactivated != "" || nm.activatedConnection != "" {
					t.Errorf("foreign controller changed state: deactivate=%q activate=%q", nm.deactivated, nm.activatedConnection)
				}
			})
		}
	}
}

type networkManagerOwnerRecorder struct {
	*fakeNetworkManagerDBus

	removes, adds int
}

func (r *networkManagerOwnerRecorder) DeactivateConnection(ctx context.Context, active dbus.ObjectPath) error {
	r.removes++
	r.activeConnection = dbusNoObjectPath
	return r.fakeNetworkManagerDBus.DeactivateConnection(ctx, active)
}

func (r *networkManagerOwnerRecorder) ActivateConnection(
	ctx context.Context, connection, device dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	r.adds++
	return r.fakeNetworkManagerDBus.ActivateConnection(ctx, connection, device)
}
