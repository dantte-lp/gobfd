//go:build linux

package server

import (
	"context"
	"net"
	"strconv"

	"golang.org/x/sys/unix"
)

func unixConnContext(ctx context.Context, conn *net.UnixConn) context.Context {
	raw, err := conn.SyscallConn()
	if err != nil {
		return ctx
	}
	var (
		cred    *unix.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil {
		return ctx
	}
	return context.WithValue(ctx, principalKey{}, transportPrincipal{
		kind:     principalUnix,
		identity: "uid:" + strconv.FormatUint(uint64(cred.Uid), 10),
	})
}
