//go:build !linux

package netio

// NewListener rejects the Linux-specific BFD transport on non-Linux platforms.
func NewListener(cfg ListenerConfig) (*Listener, error) {
	return nil, unsupportedPlatform(
		"create BFD listener on %s port %d",
		cfg.Addr,
		cfg.Port,
	)
}
