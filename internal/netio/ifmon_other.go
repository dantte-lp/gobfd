//go:build !linux

package netio

import "log/slog"

// NewInterfaceMonitor creates the platform default interface monitor.
func NewInterfaceMonitor(logger *slog.Logger) (InterfaceMonitor, error) {
	return NewStubInterfaceMonitor(logger), nil
}
