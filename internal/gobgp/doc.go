// Package gobgp integrates GoBFD with GoBGP via its gRPC API.
//
// When a BFD session transitions to Down, the handler either disables the
// corresponding BGP peer or withdraws its routes through GoBGP. When the
// BFD session returns to Up, the peer is re-enabled or routes are restored.
//
// This package implements RFC 5882 Section 3.2 flap dampening to prevent
// rapid BFD state oscillations from causing BGP route churn.
package gobgp
