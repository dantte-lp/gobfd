package netio

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	networkManagerBusName           = "org.freedesktop.NetworkManager"
	networkManagerObjectPath        = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	networkManagerInterface         = "org.freedesktop.NetworkManager"
	networkManagerDeviceInterface   = "org.freedesktop.NetworkManager.Device"
	networkManagerActiveInterface   = "org.freedesktop.NetworkManager.Connection.Active"
	networkManagerSettingsInterface = "org.freedesktop.NetworkManager.Settings.Connection"
	dbusNoObjectPath                = dbus.ObjectPath("/")
)

var (
	ErrNetworkManagerConnectionNotFound = errors.New("NetworkManager bond port connection not found")
	ErrNetworkManagerSettingsInvalid    = errors.New("NetworkManager connection settings are invalid")
)

// NetworkManagerLAGBackendConfig configures NetworkManager D-Bus enforcement.
type NetworkManagerLAGBackendConfig struct {
	// Client applies high-level NetworkManager operations. Empty uses D-Bus.
	Client BondLAGClient
}

// NetworkManagerLAGBackend applies member changes through NetworkManager D-Bus.
type NetworkManagerLAGBackend struct {
	client BondLAGClient
}

// NewNetworkManagerLAGBackend creates a NetworkManager-backed LAG backend.
func NewNetworkManagerLAGBackend(cfg NetworkManagerLAGBackendConfig) *NetworkManagerLAGBackend {
	nmClient := cfg.Client
	if nmClient == nil {
		nmClient = newDBusNetworkManagerLAGClient()
	}
	return &NetworkManagerLAGBackend{client: nmClient}
}

// RemoveMember removes memberInterface from lagInterface through NetworkManager.
func (b *NetworkManagerLAGBackend) RemoveMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	if err := validateLAGInterfaceName(lagInterface); err != nil {
		return fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if err := validateLAGInterfaceName(memberInterface); err != nil {
		return fmt.Errorf("member interface %q: %w", memberInterface, err)
	}
	if err := b.client.RemoveBondInterface(ctx, lagInterface, memberInterface); err != nil {
		return fmt.Errorf("networkmanager lag backend: %w", err)
	}
	return nil
}

// AddMember adds memberInterface to lagInterface through NetworkManager.
func (b *NetworkManagerLAGBackend) AddMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	if err := validateLAGInterfaceName(lagInterface); err != nil {
		return fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if err := validateLAGInterfaceName(memberInterface); err != nil {
		return fmt.Errorf("member interface %q: %w", memberInterface, err)
	}
	if err := b.client.AddBondInterface(ctx, lagInterface, memberInterface); err != nil {
		return fmt.Errorf("networkmanager lag backend: %w", err)
	}
	return nil
}

type networkManagerDBus interface {
	DeviceByIPInterface(ctx context.Context, iface string) (dbus.ObjectPath, error)
	DeviceActiveConnection(ctx context.Context, device dbus.ObjectPath) (dbus.ObjectPath, error)
	DeviceAvailableConnections(ctx context.Context, device dbus.ObjectPath) ([]dbus.ObjectPath, error)
	ActiveConnectionConnection(ctx context.Context, active dbus.ObjectPath) (dbus.ObjectPath, error)
	DeactivateConnection(ctx context.Context, active dbus.ObjectPath) error
	ActivateConnection(
		ctx context.Context,
		connection dbus.ObjectPath,
		device dbus.ObjectPath,
	) (dbus.ObjectPath, error)
	ConnectionSettings(
		ctx context.Context,
		connection dbus.ObjectPath,
	) (networkManagerConnectionSettings, error)
	Close() error
}

type networkManagerDBusFactory func(context.Context) (networkManagerDBus, error)

type networkManagerConnectionSettings map[string]map[string]dbus.Variant

type dbusNetworkManagerLAGClient struct {
	newDBus networkManagerDBusFactory
	mu      sync.Mutex
	cache   map[networkManagerMemberKey]dbus.ObjectPath
}

type networkManagerMemberKey struct {
	bond  string
	iface string
}

func newDBusNetworkManagerLAGClient() *dbusNetworkManagerLAGClient {
	return &dbusNetworkManagerLAGClient{
		newDBus: func(context.Context) (networkManagerDBus, error) {
			conn, err := dbus.ConnectSystemBus()
			if err != nil {
				return nil, err
			}
			return dbusNetworkManager{conn: conn}, nil
		},
		cache: make(map[networkManagerMemberKey]dbus.ObjectPath),
	}
}

func (c *dbusNetworkManagerLAGClient) RemoveBondInterface(
	ctx context.Context,
	bond string,
	iface string,
) error {
	nm, err := c.newDBus(ctx)
	if err != nil {
		return err
	}
	defer nm.Close()

	device, err := nm.DeviceByIPInterface(ctx, iface)
	if err != nil {
		return err
	}
	active, err := nm.DeviceActiveConnection(ctx, device)
	if err != nil {
		return err
	}
	if isNoObjectPath(active) {
		return nil
	}
	connection, err := nm.ActiveConnectionConnection(ctx, active)
	if err != nil {
		return err
	}

	c.rememberConnection(bond, iface, connection)
	return nm.DeactivateConnection(ctx, active)
}

func (c *dbusNetworkManagerLAGClient) AddBondInterface(
	ctx context.Context,
	bond string,
	iface string,
) error {
	nm, err := c.newDBus(ctx)
	if err != nil {
		return err
	}
	defer nm.Close()

	device, err := nm.DeviceByIPInterface(ctx, iface)
	if err != nil {
		return err
	}
	active, err := nm.DeviceActiveConnection(ctx, device)
	if err != nil {
		return err
	}
	if !isNoObjectPath(active) {
		return nil
	}

	connection, ok := c.rememberedConnection(bond, iface)
	if !ok {
		connection, err = c.findBondPortConnection(ctx, nm, device, bond, iface)
		if err != nil {
			return err
		}
	}
	_, err = nm.ActivateConnection(ctx, connection, device)
	return err
}

func (c *dbusNetworkManagerLAGClient) rememberConnection(
	bond string,
	iface string,
	connection dbus.ObjectPath,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[networkManagerMemberKey{bond: bond, iface: iface}] = connection
}

func (c *dbusNetworkManagerLAGClient) rememberedConnection(
	bond string,
	iface string,
) (dbus.ObjectPath, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	connection, ok := c.cache[networkManagerMemberKey{bond: bond, iface: iface}]
	return connection, ok
}

func (c *dbusNetworkManagerLAGClient) findBondPortConnection(
	ctx context.Context,
	nm networkManagerDBus,
	device dbus.ObjectPath,
	bond string,
	iface string,
) (dbus.ObjectPath, error) {
	available, err := nm.DeviceAvailableConnections(ctx, device)
	if err != nil {
		return "", err
	}
	for _, connection := range available {
		settings, err := nm.ConnectionSettings(ctx, connection)
		if err != nil {
			return "", err
		}
		if networkManagerSettingsMatchBondPort(settings, bond, iface) {
			c.rememberConnection(bond, iface, connection)
			return connection, nil
		}
	}
	return "", fmt.Errorf("%s/%s: %w", bond, iface, ErrNetworkManagerConnectionNotFound)
}

func networkManagerSettingsMatchBondPort(
	settings networkManagerConnectionSettings,
	bond string,
	iface string,
) bool {
	connection, ok := settings["connection"]
	if !ok {
		return false
	}
	interfaceName, ok := networkManagerSettingString(connection, "interface-name")
	if ok && interfaceName != "" && interfaceName != iface {
		return false
	}
	controller, ok := networkManagerSettingString(connection, "controller")
	if !ok || controller == "" {
		controller, _ = networkManagerSettingString(connection, "master")
	}
	if controller != bond {
		return false
	}
	portType, ok := networkManagerSettingString(connection, "port-type")
	if !ok || portType == "" {
		portType, _ = networkManagerSettingString(connection, "slave-type")
	}
	return portType == "" || portType == "bond"
}

func networkManagerSettingString(settings map[string]dbus.Variant, key string) (string, bool) {
	value, ok := settings[key]
	if !ok {
		return "", false
	}
	str, ok := value.Value().(string)
	return str, ok
}

func isNoObjectPath(path dbus.ObjectPath) bool {
	return path == "" || path == dbusNoObjectPath
}

type dbusNetworkManager struct {
	conn *dbus.Conn
}

func (c dbusNetworkManager) DeviceByIPInterface(
	ctx context.Context,
	iface string,
) (dbus.ObjectPath, error) {
	var device dbus.ObjectPath
	err := c.managerObject().CallWithContext(
		ctx,
		networkManagerInterface+".GetDeviceByIpIface",
		0,
		iface,
	).Store(&device)
	return device, err
}

func (c dbusNetworkManager) DeviceActiveConnection(
	_ context.Context,
	device dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	var active dbus.ObjectPath
	err := c.object(device).StoreProperty(networkManagerDeviceInterface+".ActiveConnection", &active)
	return active, err
}

func (c dbusNetworkManager) DeviceAvailableConnections(
	_ context.Context,
	device dbus.ObjectPath,
) ([]dbus.ObjectPath, error) {
	var connections []dbus.ObjectPath
	err := c.object(device).StoreProperty(networkManagerDeviceInterface+".AvailableConnections", &connections)
	return connections, err
}

func (c dbusNetworkManager) ActiveConnectionConnection(
	_ context.Context,
	active dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	var connection dbus.ObjectPath
	err := c.object(active).StoreProperty(networkManagerActiveInterface+".Connection", &connection)
	return connection, err
}

func (c dbusNetworkManager) DeactivateConnection(
	ctx context.Context,
	active dbus.ObjectPath,
) error {
	return c.managerObject().CallWithContext(
		ctx,
		networkManagerInterface+".DeactivateConnection",
		0,
		active,
	).Err
}

func (c dbusNetworkManager) ActivateConnection(
	ctx context.Context,
	connection dbus.ObjectPath,
	device dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	var active dbus.ObjectPath
	err := c.managerObject().CallWithContext(
		ctx,
		networkManagerInterface+".ActivateConnection",
		0,
		connection,
		device,
		dbusNoObjectPath,
	).Store(&active)
	return active, err
}

func (c dbusNetworkManager) ConnectionSettings(
	ctx context.Context,
	connection dbus.ObjectPath,
) (networkManagerConnectionSettings, error) {
	settings := networkManagerConnectionSettings{}
	err := c.object(connection).CallWithContext(
		ctx,
		networkManagerSettingsInterface+".GetSettings",
		0,
	).Store(&settings)
	if err != nil {
		return nil, err
	}
	if _, ok := settings["connection"]; !ok {
		return nil, ErrNetworkManagerSettingsInvalid
	}
	return settings, nil
}

func (c dbusNetworkManager) Close() error {
	return c.conn.Close()
}

func (c dbusNetworkManager) managerObject() dbus.BusObject {
	return c.object(networkManagerObjectPath)
}

func (c dbusNetworkManager) object(path dbus.ObjectPath) dbus.BusObject {
	return c.conn.Object(networkManagerBusName, path)
}
