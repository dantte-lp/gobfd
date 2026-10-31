//go:build !linux

package server

import (
	"context"
	"net"
)

func unixConnContext(ctx context.Context, _ *net.UnixConn) context.Context {
	return ctx
}
