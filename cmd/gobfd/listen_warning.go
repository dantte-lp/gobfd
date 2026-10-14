package main

import (
	"log/slog"
	"net"
	"net/netip"
)

// exposedBeyondLoopback reports whether a listen address is reachable from
// other hosts: a wildcard, a non-loopback address or an unparsable value.
func exposedBeyondLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if host == "localhost" {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err != nil || !ip.IsLoopback()
}

// warnUnauthenticatedBinds reports API and metrics listeners that accept
// unauthenticated requests beyond loopback (GitHub #87).
func warnUnauthenticatedBinds(logger *slog.Logger, listeners map[string]string) {
	for name, addr := range listeners {
		if addr != "" && exposedBeyondLoopback(addr) {
			logger.Warn("unauthenticated listener is reachable beyond loopback",
				slog.String("listener", name),
				slog.String("addr", addr),
			)
		}
	}
}
