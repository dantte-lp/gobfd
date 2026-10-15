package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"

	"connectrpc.com/connect/v2/connecthttp"
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

// PrincipalFromContext returns the authenticated request principal: the
// Unix peer UID, or for an mTLS request the verified client certificate's
// first URI SAN (for example a SPIFFE ID), else its first DNS SAN as
// "dns:<name>".
func PrincipalFromContext(ctx context.Context) (string, bool) {
	if principal, ok := ctx.Value(principalKey{}).(string); ok {
		return principal, true
	}
	info, ok := connecthttp.ServerInfoForContext(ctx)
	if !ok {
		return "", false
	}
	return tlsPrincipal(info.TLS())
}

// PrincipalFromRequest returns the principal of a plain HTTP request on the
// management server: the Unix peer UID or the verified mTLS identity.
func PrincipalFromRequest(r *http.Request) (string, bool) {
	if principal, ok := r.Context().Value(principalKey{}).(string); ok {
		return principal, true
	}
	return tlsPrincipal(r.TLS)
}

func tlsPrincipal(state *tls.ConnectionState) (string, bool) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return "", false
	}
	leaf := state.VerifiedChains[0][0]
	switch {
	case len(leaf.URIs) > 0:
		return leaf.URIs[0].String(), true
	case len(leaf.DNSNames) > 0:
		return "dns:" + leaf.DNSNames[0], true
	default:
		return "", false
	}
}
