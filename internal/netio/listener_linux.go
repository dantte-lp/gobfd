//go:build linux

package netio

import (
	"context"
	"errors"
	"fmt"
)

// NewListener creates a Listener from the given configuration.
// Returns an error if the underlying socket cannot be created.
// When ReadBufferSize is set, SO_RCVBUF is applied to reduce packet loss.
func NewListener(cfg ListenerConfig) (*Listener, error) {
	conn, err := createConn(cfg)
	if err != nil {
		return nil, err
	}

	// Apply read buffer tuning if configured.
	if setter, ok := conn.(interface{ SetReadBuffer(bytes int) error }); ok && cfg.ReadBufferSize > 0 {
		if err := setter.SetReadBuffer(cfg.ReadBufferSize); err != nil {
			closeErr := conn.Close()
			if closeErr != nil {
				closeErr = fmt.Errorf("close listener after read buffer failure: %w", closeErr)
			}
			return nil, errors.Join(
				fmt.Errorf("set read buffer to %d: %w", cfg.ReadBufferSize, err),
				closeErr,
			)
		}
	}

	return &Listener{
		conn:        conn,
		multiHop:    cfg.MultiHop,
		expectedTTL: cfg.ExpectedTTL,
	}, nil
}

// createConn creates the appropriate Linux PacketConn based on the config.
func createConn(cfg ListenerConfig) (PacketConn, error) {
	if cfg.MultiHop {
		conn, err := NewMultiHopListener(context.Background(), cfg.Addr)
		if err != nil {
			return nil, fmt.Errorf("create multi-hop listener: %w", err)
		}
		return conn, nil
	}

	// For non-standard ports (micro-BFD 6784, echo 3785), use the generic
	// listener with SO_BINDTODEVICE and GTSM TTL=255 (single-hop semantics).
	if cfg.Port != 0 && cfg.Port != PortSingleHop {
		conn, err := NewGenericListener(context.Background(), cfg.Addr, cfg.IfName, cfg.Port)
		if err != nil {
			return nil, fmt.Errorf("create listener on port %d: %w", cfg.Port, err)
		}
		return conn, nil
	}

	conn, err := NewSingleHopListener(context.Background(), cfg.Addr, cfg.IfName)
	if err != nil {
		return nil, fmt.Errorf("create single-hop listener: %w", err)
	}
	return conn, nil
}
