// Package netio provides raw socket abstractions for BFD packet I/O.
//
// Linux-specific implementation uses golang.org/x/net and golang.org/x/sys/unix
// for UDP listeners on ports 3784 (single-hop, RFC 5881) and 4784 (multi-hop, RFC 5883).
package netio
