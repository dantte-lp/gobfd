//go:build !linux

package netio

import (
	"context"

	"github.com/godbus/dbus/v5"
)

// shortcut: non-Linux retains upstream dialing; qualify it before supporting that platform.
func connectNetworkManagerBus(ctx context.Context) (*dbus.Conn, error) {
	return dbus.ConnectSystemBus(dbus.WithContext(ctx))
}
