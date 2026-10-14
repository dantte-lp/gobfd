package server

import (
	"context"
	"net"
	"strconv"

	"golang.org/x/sys/unix"
)

type principalKey struct{}

// ConnContext is an http.Server.ConnContext hook. For a connection accepted
// on a Unix socket it records the peer UID that the kernel captured at
// connect(2) (SO_PEERCRED) as the request principal "uid:<n>". Other
// connections carry no principal (gobfd-330).
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return ctx
	}
	raw, err := uc.SyscallConn()
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
	return context.WithValue(ctx, principalKey{}, "uid:"+strconv.FormatUint(uint64(cred.Uid), 10))
}

// PrincipalFromContext returns the authenticated request principal.
func PrincipalFromContext(ctx context.Context) (string, bool) {
	principal, ok := ctx.Value(principalKey{}).(string)
	return principal, ok
}
