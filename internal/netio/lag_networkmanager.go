package netio

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	dbusPropertiesGetMethod         = "org.freedesktop.DBus.Properties.Get"
	dbusNoObjectPath                = dbus.ObjectPath("/")
)

var (
	// ErrNetworkManagerConnectionNotFound indicates that no matching bond-port connection exists.
	ErrNetworkManagerConnectionNotFound = errors.New("NetworkManager bond port connection not found")
	// ErrNetworkManagerSettingsInvalid indicates missing, malformed or incompatible connection settings.
	ErrNetworkManagerSettingsInvalid = errors.New("NetworkManager connection settings are invalid")
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
	ConnectionByUUID(ctx context.Context, uuid string) (dbus.ObjectPath, error)
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
		newDBus: func(ctx context.Context) (networkManagerDBus, error) {
			conn, err := connectNetworkManagerBus(ctx)
			if err != nil {
				return nil, fmt.Errorf("connect NetworkManager system bus: %w", errors.Join(ctx.Err(), err))
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
		return fmt.Errorf("open NetworkManager D-Bus client: %w", err)
	}
	defer nm.Close()

	device, err := nm.DeviceByIPInterface(ctx, iface)
	if err != nil {
		return fmt.Errorf("resolve NetworkManager device for interface %q: %w", iface, err)
	}
	active, err := nm.DeviceActiveConnection(ctx, device)
	if err != nil {
		return fmt.Errorf("read active NetworkManager connection for interface %q: %w", iface, err)
	}
	if isNoObjectPath(active) {
		return nil
	}
	connection, err := nm.ActiveConnectionConnection(ctx, active)
	if err != nil {
		return fmt.Errorf("resolve NetworkManager settings connection for interface %q: %w", iface, err)
	}

	if err := validateNetworkManagerBondPort(ctx, nm, connection, bond, iface); err != nil {
		return err
	}
	if err := nm.DeactivateConnection(ctx, active); err != nil {
		return fmt.Errorf("deactivate NetworkManager connection for interface %q: %w", iface, err)
	}
	c.rememberConnection(bond, iface, connection)

	return nil
}

func (c *dbusNetworkManagerLAGClient) AddBondInterface(
	ctx context.Context,
	bond string,
	iface string,
) error {
	nm, err := c.newDBus(ctx)
	if err != nil {
		return fmt.Errorf("open NetworkManager D-Bus client: %w", err)
	}
	defer nm.Close()

	device, err := nm.DeviceByIPInterface(ctx, iface)
	if err != nil {
		return fmt.Errorf("resolve NetworkManager device for interface %q: %w", iface, err)
	}
	active, err := nm.DeviceActiveConnection(ctx, device)
	if err != nil {
		return fmt.Errorf("read active NetworkManager connection for interface %q: %w", iface, err)
	}
	if !isNoObjectPath(active) {
		connection, lookupErr := nm.ActiveConnectionConnection(ctx, active)
		if lookupErr != nil {
			return fmt.Errorf("resolve active NetworkManager settings for interface %q: %w", iface, lookupErr)
		}
		return validateNetworkManagerBondPort(ctx, nm, connection, bond, iface)
	}

	connection, ok := c.rememberedConnection(bond, iface)
	if !ok {
		connection, err = c.findBondPortConnection(ctx, nm, device, bond, iface)
		if err != nil {
			return err
		}
	} else if validationErr := validateNetworkManagerBondPort(ctx, nm, connection, bond, iface); validationErr != nil {
		return validationErr
	}
	_, err = nm.ActivateConnection(ctx, connection, device)
	if err != nil {
		return fmt.Errorf("activate NetworkManager connection for interface %q: %w", iface, err)
	}

	return nil
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
		return "", fmt.Errorf("list available NetworkManager connections for interface %q: %w", iface, err)
	}
	for _, connection := range available {
		matches, err := networkManagerConnectionMatchesBondPort(ctx, nm, connection, bond, iface)
		if err != nil {
			return "", err
		}
		if matches {
			c.rememberConnection(bond, iface, connection)
			return connection, nil
		}
	}
	return "", fmt.Errorf("%s/%s: %w", bond, iface, ErrNetworkManagerConnectionNotFound)
}

func validateNetworkManagerBondPort(
	ctx context.Context, nm networkManagerDBus, connection dbus.ObjectPath, bond, iface string,
) error {
	matches, err := networkManagerConnectionMatchesBondPort(ctx, nm, connection, bond, iface)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("NetworkManager profile %q does not own %s/%s: %w",
			connection, bond, iface, ErrNetworkManagerSettingsInvalid)
	}
	return nil
}

func networkManagerConnectionMatchesBondPort(
	ctx context.Context, nm networkManagerDBus, connection dbus.ObjectPath, bond, iface string,
) (bool, error) {
	settings, err := nm.ConnectionSettings(ctx, connection)
	if err != nil {
		return false, fmt.Errorf("read NetworkManager settings for interface %q: %w", iface, err)
	}
	controller, valid := networkManagerSettingAlias(settings["connection"], "controller", "master")
	if !valid || !networkManagerSettingsMatchBondPort(settings, controller, iface) {
		return false, nil
	}
	if controller == bond {
		return true, nil
	}
	return networkManagerControllerMatchesBond(ctx, nm, controller, bond, iface)
}

func networkManagerControllerMatchesBond(
	ctx context.Context, nm networkManagerDBus, controller, bond, iface string,
) (bool, error) {
	parent, err := nm.ConnectionByUUID(ctx, strings.ToLower(controller))
	if err != nil {
		remote, remoteOK := errors.AsType[dbus.Error](err)
		if remoteOK && remote.Name == "org.freedesktop.NetworkManager.Settings.InvalidConnection" {
			return false, nil
		}
		return false, fmt.Errorf("resolve NetworkManager controller for %s/%s: %w", bond, iface, err)
	}
	if isNoObjectPath(parent) {
		return false, nil
	}
	parentSettings, err := nm.ConnectionSettings(ctx, parent)
	if err != nil {
		return false, fmt.Errorf("read NetworkManager controller settings for %s/%s: %w", bond, iface, err)
	}
	parentConnection := parentSettings["connection"]
	uuid, uuidOK := networkManagerSettingString(parentConnection, "uuid")
	kind, kindOK := networkManagerSettingString(parentConnection, "type")
	if !uuidOK || !kindOK || !strings.EqualFold(uuid, controller) || kind != "bond" {
		return false, nil
	}
	name, valid := networkManagerControllerInterface(parentSettings)
	if !valid {
		return false, nil
	}
	if name != "" {
		return name == bond, nil
	}
	// An unbound controller profile needs an actual active-device binding.
	return networkManagerControllerDeviceMatches(ctx, nm, parent, bond)
}

func networkManagerControllerDeviceMatches(
	ctx context.Context, nm networkManagerDBus, parent dbus.ObjectPath, bond string,
) (bool, error) {
	device, err := nm.DeviceByIPInterface(ctx, bond)
	if err != nil {
		return false, fmt.Errorf("resolve NetworkManager bond device: %w", err)
	}
	active, err := nm.DeviceActiveConnection(ctx, device)
	if err != nil {
		return false, fmt.Errorf("read NetworkManager bond active connection: %w", err)
	}
	if isNoObjectPath(active) {
		return false, nil
	}
	bound, err := nm.ActiveConnectionConnection(ctx, active)
	if err != nil {
		return false, fmt.Errorf("read active NetworkManager bond profile: %w", err)
	}
	return bound == parent, nil
}

func networkManagerControllerInterface(settings networkManagerConnectionSettings) (string, bool) {
	for _, fields := range []map[string]dbus.Variant{settings["connection"], settings["bond"]} {
		value, exists := fields["interface-name"]
		if !exists {
			continue
		}
		name, valid := value.Value().(string)
		if !valid {
			return "", false
		}
		if name != "" {
			return name, true
		}
	}
	return "", true
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
	if value, exists := connection["interface-name"]; exists {
		name, valid := value.Value().(string)
		if !valid || (name != "" && name != iface) {
			return false
		}
	}
	controller, valid := networkManagerSettingAlias(connection, "controller", "master")
	if !valid || controller != bond {
		return false
	}
	portType, valid := networkManagerSettingAlias(connection, "port-type", "slave-type")
	return valid && portType == "bond"
}

func networkManagerSettingAlias(settings map[string]dbus.Variant, modern, legacy string) (string, bool) {
	var result string
	for _, key := range []string{modern, legacy} {
		value, exists := settings[key]
		if !exists {
			continue
		}
		text, valid := value.Value().(string)
		if !valid || (result != "" && text != "" && text != result) {
			return "", false
		}
		if text != "" {
			result = text
		}
	}
	return result, result != ""
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
	if err != nil {
		return "", fmt.Errorf("call NetworkManager GetDeviceByIpIface for %q: %w", iface, err)
	}

	return device, nil
}

func (c dbusNetworkManager) DeviceActiveConnection(
	ctx context.Context,
	device dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	var active dbus.ObjectPath
	err := c.object(device).CallWithContext(
		ctx, dbusPropertiesGetMethod, 0, networkManagerDeviceInterface, "ActiveConnection",
	).Store(&active)
	if err != nil {
		return "", fmt.Errorf("read NetworkManager device %q active connection: %w", device, err)
	}

	return active, nil
}

func (c dbusNetworkManager) DeviceAvailableConnections(
	ctx context.Context,
	device dbus.ObjectPath,
) ([]dbus.ObjectPath, error) {
	var connections []dbus.ObjectPath
	err := c.object(device).CallWithContext(
		ctx, dbusPropertiesGetMethod, 0, networkManagerDeviceInterface, "AvailableConnections",
	).Store(&connections)
	if err != nil {
		return nil, fmt.Errorf("read NetworkManager device %q available connections: %w", device, err)
	}

	return connections, nil
}

func (c dbusNetworkManager) ActiveConnectionConnection(
	ctx context.Context,
	active dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	var connection dbus.ObjectPath
	err := c.object(active).CallWithContext(
		ctx, dbusPropertiesGetMethod, 0, networkManagerActiveInterface, "Connection",
	).Store(&connection)
	if err != nil {
		return "", fmt.Errorf("read NetworkManager active connection %q settings path: %w", active, err)
	}

	return connection, nil
}

func (c dbusNetworkManager) DeactivateConnection(
	ctx context.Context,
	active dbus.ObjectPath,
) error {
	if err := c.managerObject().CallWithContext(
		ctx,
		networkManagerInterface+".DeactivateConnection",
		0,
		active,
	).Err; err != nil {
		return fmt.Errorf("call NetworkManager DeactivateConnection for %q: %w", active, err)
	}

	return nil
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
	if err != nil {
		return "", fmt.Errorf("call NetworkManager ActivateConnection for %q: %w", connection, err)
	}

	return active, nil
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
		return nil, fmt.Errorf("call NetworkManager GetSettings for %q: %w", connection, err)
	}
	if _, ok := settings["connection"]; !ok {
		return nil, ErrNetworkManagerSettingsInvalid
	}
	return settings, nil
}

func (c dbusNetworkManager) ConnectionByUUID(ctx context.Context, uuid string) (dbus.ObjectPath, error) {
	call := c.object(networkManagerObjectPath+"/Settings").CallWithContext(
		ctx, networkManagerInterface+".Settings.GetConnectionByUuid", 0, uuid,
	)
	if call.Err != nil {
		return "", fmt.Errorf("call NetworkManager GetConnectionByUuid: %w", call.Err)
	}
	if len(call.Body) != 1 {
		return "", fmt.Errorf("read NetworkManager controller path: %w", ErrNetworkManagerSettingsInvalid)
	}
	// Store permits numeric/string coercion; this method requires an object-path reply.
	connection, valid := call.Body[0].(dbus.ObjectPath)
	if !valid || !connection.IsValid() || isNoObjectPath(connection) {
		return "", fmt.Errorf("read NetworkManager controller path: %w", ErrNetworkManagerSettingsInvalid)
	}
	return connection, nil
}

func (c dbusNetworkManager) Close() error {
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("close NetworkManager D-Bus connection: %w", err)
	}

	return nil
}

func (c dbusNetworkManager) managerObject() dbus.BusObject {
	return c.object(networkManagerObjectPath)
}

func (c dbusNetworkManager) object(path dbus.ObjectPath) dbus.BusObject {
	return c.conn.Object(networkManagerBusName, path)
}
