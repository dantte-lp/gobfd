package netio

import "errors"

// ErrInvalidInterfaceName indicates an interface identity that cannot be
// represented by the platform transport boundary.
var ErrInvalidInterfaceName = errors.New("invalid interface name")
