// Package gobgp integrates GoBFD with GoBGP via its gRPC API.
//
// When a BFD session transitions to Down, the handler either disables the
// corresponding BGP peer or withdraws its routes through GoBGP. When the
// BFD session returns to Up, the peer is re-enabled or routes are restored.
//
// The package includes implementation-defined flap dampening informed by the
// session-state hysteresis permitted by RFC 5882 Section 3.1. RFC 5882 does
// not standardize the penalty algorithm.
package gobgp
