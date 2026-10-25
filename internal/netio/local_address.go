package netio

import "net/netip"

// LocalAddress is a local bind dependency. Interface scopes unzoned IPv6
// link-local addresses; global addresses may belong to another interface.
type LocalAddress struct {
	Addr      netip.Addr
	Interface string
}
