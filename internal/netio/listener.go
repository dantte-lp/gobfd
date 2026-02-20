package netio

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// ListenerConfig — BFD packet listener configuration
// -------------------------------------------------------------------------

// ListenerConfig holds configuration for a BFD packet listener.
//
// For single-hop (RFC 5881): Port = 3784, IfName is required.
// For multi-hop (RFC 5883): Port = 4784, IfName is empty.
type ListenerConfig struct {
	// Addr is the local IP address to bind to.
	Addr netip.Addr

	// IfName is the network interface name for SO_BINDTODEVICE.
	// Required for single-hop sessions (RFC 5881 Section 4).
	// Empty for multi-hop sessions.
	IfName string

	// Port is the destination UDP port: 3784 (single-hop) or 4784 (multi-hop).
	Port uint16

	// MultiHop indicates whether this is a multi-hop listener (RFC 5883).
	MultiHop bool
}

// -------------------------------------------------------------------------
// Listener — High-level BFD packet receive loop
// -------------------------------------------------------------------------

// Listener wraps a PacketConn and provides a high-level, context-aware
// receive loop for BFD Control packets. It handles buffer management
// using bfd.PacketPool and returns validated packet metadata.
type Listener struct {
	conn     PacketConn
	multiHop bool
}

// NewListener creates a Listener from the given configuration.
// Returns an error if the underlying socket cannot be created.
func NewListener(cfg ListenerConfig) (*Listener, error) {
	conn, err := createConn(cfg)
	if err != nil {
		return nil, err
	}

	return &Listener{
		conn:     conn,
		multiHop: cfg.MultiHop,
	}, nil
}

// NewListenerFromConn creates a Listener from an existing PacketConn.
// This is useful for testing with mock connections or custom transports.
func NewListenerFromConn(conn PacketConn, multiHop bool) *Listener {
	return &Listener{
		conn:     conn,
		multiHop: multiHop,
	}
}

// Recv blocks until a BFD Control packet is received or ctx is cancelled.
// Returns the raw packet bytes (from bfd.PacketPool), transport metadata,
// and any error. The caller is responsible for returning the buffer to
// bfd.PacketPool after processing.
//
// Recv validates the received TTL per GTSM requirements:
//   - Single-hop (RFC 5881 Section 5): TTL must be 255
//   - Multi-hop (RFC 5883 Section 2): TTL must be >= 254
func (l *Listener) Recv(ctx context.Context) ([]byte, PacketMeta, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, PacketMeta{}, fmt.Errorf("listener recv: %w", err)
		}

		buf, meta, err := l.recvOne()
		if err != nil {
			return nil, PacketMeta{}, err
		}

		// Validate GTSM TTL before returning to caller.
		if ttlErr := ValidateTTL(meta, l.multiHop); ttlErr != nil {
			continue // Drop packets with invalid TTL silently.
		}

		return buf, meta, nil
	}
}

// recvOne performs a single read from the underlying connection using
// a pooled buffer. Returns the buffer slice, metadata, and any error.
func (l *Listener) recvOne() ([]byte, PacketMeta, error) {
	bufp, ok := bfd.PacketPool.Get().(*[]byte)
	if !ok {
		return nil, PacketMeta{}, fmt.Errorf("listener recv: %w", ErrPoolType)
	}

	n, meta, err := l.conn.ReadPacket(*bufp)
	if err != nil {
		bfd.PacketPool.Put(bufp)
		return nil, PacketMeta{}, fmt.Errorf("listener read: %w", err)
	}

	return (*bufp)[:n], meta, nil
}

// Close closes the underlying PacketConn.
func (l *Listener) Close() error {
	if err := l.conn.Close(); err != nil {
		return fmt.Errorf("close listener: %w", err)
	}
	return nil
}

// createConn creates the appropriate PacketConn based on the config.
func createConn(cfg ListenerConfig) (PacketConn, error) {
	if cfg.MultiHop {
		conn, err := NewMultiHopListener(context.Background(), cfg.Addr)
		if err != nil {
			return nil, fmt.Errorf("create multi-hop listener: %w", err)
		}
		return conn, nil
	}

	conn, err := NewSingleHopListener(context.Background(), cfg.Addr, cfg.IfName)
	if err != nil {
		return nil, fmt.Errorf("create single-hop listener: %w", err)
	}
	return conn, nil
}
