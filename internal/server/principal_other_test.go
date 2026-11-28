//go:build !linux

package server

import (
	"net"
	"testing"
)

func TestUnixPeerUIDRejectsUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	got := unixConnContext(ctx, new(net.UnixConn))
	if got != ctx {
		t.Fatal("unsupported Unix peer authentication changed the context")
	}
	if principal, ok := PrincipalFromContext(got); ok || principal != "" {
		t.Fatalf("unsupported Unix peer principal = %q, %t; want none", principal, ok)
	}
}
