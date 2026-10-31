//go:build linux

package netio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const networkManagerBusNonceSize = 16

var errNetworkManagerBusAddress = errors.New("invalid NetworkManager system-bus address")

type networkManagerBusEndpoint struct {
	network   string
	target    string
	nonceFile string
}

func connectNetworkManagerBus(ctx context.Context) (*dbus.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start system-bus connection: %w", err)
	}
	address, err := networkManagerSystemBusAddress()
	if err != nil {
		return nil, err
	}
	var connection *dbus.Conn
	for candidate := range strings.SplitSeq(address, ";") {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, fmt.Errorf("select system-bus transport: %w", contextErr)
		}
		connection, err = dialNetworkManagerBus(ctx, candidate)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	// Match godbus: fallback selects a transport, never another authentication peer.
	if authErr := connection.Auth(nil); authErr != nil {
		return nil, errors.Join(fmt.Errorf("authenticate system bus: %w", authErr), connection.Close())
	}
	if helloErr := connection.Hello(); helloErr != nil {
		return nil, errors.Join(fmt.Errorf("register with system bus: %w", helloErr), connection.Close())
	}
	return connection, nil
}

func networkManagerSystemBusAddress() (string, error) {
	if address := os.Getenv("DBUS_SYSTEM_BUS_ADDRESS"); address != "" {
		return address, nil
	}
	for _, path := range []string{"/var/run/dbus/system_bus_socket", "/run/dbus/system_bus_socket"} {
		if _, err := os.Stat(path); err == nil {
			return "unix:path=" + path, nil
		}
	}
	return "", fmt.Errorf("%w: system bus not found", errNetworkManagerBusAddress)
}

func dialNetworkManagerBus(ctx context.Context, candidate string) (*dbus.Conn, error) {
	endpoint, err := parseNetworkManagerBusEndpoint(candidate)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	socket, err := dialer.DialContext(ctx, endpoint.network, endpoint.target)
	if err != nil {
		return nil, fmt.Errorf("dial system bus: %w", err)
	}
	var connection *dbus.Conn
	if unixSocket, ok := socket.(*net.UnixConn); ok {
		connection, err = dbus.DialUnix(unixSocket, dbus.WithContext(ctx))
	} else {
		connection, err = dbus.NewConn(socket, dbus.WithContext(ctx))
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("prepare system bus: %w", err), socket.Close())
	}
	if endpoint.nonceFile != "" {
		if nonceErr := writeNetworkManagerBusNonce(socket, endpoint.nonceFile); nonceErr != nil {
			return nil, errors.Join(nonceErr, connection.Close())
		}
	}
	return connection, nil
}

func parseNetworkManagerBusEndpoint(candidate string) (networkManagerBusEndpoint, error) {
	kind, keys, ok := strings.Cut(candidate, ":")
	if !ok {
		return networkManagerBusEndpoint{}, errNetworkManagerBusAddress
	}
	switch kind {
	case "unix":
		path := networkManagerBusKey(keys, "path")
		abstract := networkManagerBusKey(keys, "abstract")
		if (path == "") == (abstract == "") {
			return networkManagerBusEndpoint{}, errNetworkManagerBusAddress
		}
		if abstract != "" {
			path = "@" + abstract
		}
		return networkManagerBusEndpoint{network: "unix", target: path}, nil
	case "tcp", "nonce-tcp":
		return networkManagerTCPBusEndpoint(kind, keys)
	default:
		return networkManagerBusEndpoint{}, errNetworkManagerBusAddress
	}
}

func networkManagerTCPBusEndpoint(kind, keys string) (networkManagerBusEndpoint, error) {
	host, port := networkManagerBusKey(keys, "host"), networkManagerBusKey(keys, "port")
	if !networkManagerBusTCPEnabled || host == "" || port == "" {
		return networkManagerBusEndpoint{}, errNetworkManagerBusAddress
	}
	var network string
	switch networkManagerBusKey(keys, "family") {
	case "":
		network = "tcp"
	case "ipv4":
		network = "tcp4"
	case "ipv6":
		network = "tcp6"
	default:
		return networkManagerBusEndpoint{}, errNetworkManagerBusAddress
	}
	endpoint := networkManagerBusEndpoint{network: network, target: net.JoinHostPort(host, port)}
	if kind == "nonce-tcp" {
		endpoint.nonceFile = networkManagerBusKey(keys, "noncefile")
		if endpoint.nonceFile == "" {
			return networkManagerBusEndpoint{}, errNetworkManagerBusAddress
		}
	}
	return endpoint, nil
}

// Match godbus's first-key and escaping rules; its transport registry is private.
func networkManagerBusKey(keys, key string) string {
	for entry := range strings.SplitSeq(keys, ",") {
		if encoded, ok := strings.CutPrefix(entry, key+"="); ok {
			value, err := dbus.UnescapeBusAddressValue(encoded)
			if err != nil {
				return ""
			}
			return value
		}
	}
	return ""
}

func writeNetworkManagerBusNonce(socket net.Conn, path string) (returnErr error) {
	// A nonce is a local regular 16-byte file, never a pipe that can stall Open.
	// #nosec G304 -- The operator system-bus address selects this nonce; reads require a regular 16-byte file.
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open system-bus nonce: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat system-bus nonce: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: nonce must be a regular file", errNetworkManagerBusAddress)
	}
	nonce, err := io.ReadAll(io.LimitReader(file, networkManagerBusNonceSize+1))
	defer clear(nonce)
	if err != nil {
		return fmt.Errorf("read system-bus nonce: %w", err)
	}
	if len(nonce) != networkManagerBusNonceSize {
		return fmt.Errorf("%w: nonce must contain 16 bytes", errNetworkManagerBusAddress)
	}
	written, err := socket.Write(nonce)
	if err != nil {
		return fmt.Errorf("write system-bus nonce: %w", err)
	}
	if written != len(nonce) {
		return fmt.Errorf("write system-bus nonce: %w", io.ErrShortWrite)
	}
	return nil
}
