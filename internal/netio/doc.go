// Package netio provides raw socket abstractions for BFD packet I/O.
//
// Supports both IPv4 and IPv6 address families. The address family is
// auto-detected from the bind address:
//   - IPv4: IP_TTL, IP_RECVTTL, IP_PKTINFO
//   - IPv6: IPV6_UNICAST_HOPS, IPV6_RECVHOPLIMIT, IPV6_RECVPKTINFO
//
// Linux-specific implementation uses golang.org/x/net and golang.org/x/sys/unix
// for UDP listeners on ports 3784 (single-hop, RFC 5881) and 4784 (multi-hop, RFC 5883).
package netio
