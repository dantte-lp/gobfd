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
// Supports both IPv4 and IPv6 addresses; the address family is
// auto-detected from Addr.
//
// For single-hop (RFC 5881): Port = 3784, IfName is required.
// For multi-hop (RFC 5883): Port = 4784, IfName is empty.
// For micro-BFD (RFC 7130): Port = 6784, IfName is required (member link).
type ListenerConfig struct {
	// Addr is the local IP address to bind to (IPv4 or IPv6).
	Addr netip.Addr

	// IfName is the network interface name for SO_BINDTODEVICE.
	// Required for single-hop sessions (RFC 5881 Section 4)
	// and micro-BFD per-member sessions (RFC 7130 Section 2).
	// Empty for multi-hop sessions.
	IfName string

	// Port is the destination UDP port: 3784 (single-hop), 4784 (multi-hop),
	// 3785 (echo), or 6784 (micro-BFD).
	Port uint16

	// MultiHop indicates whether this is a multi-hop listener (RFC 5883).
	MultiHop bool

	// ReadBufferSize is the SO_RCVBUF size in bytes.
	// When nonzero, SetReadBuffer is called on the underlying socket.
	// This reduces packet loss under high session counts.
	ReadBufferSize int
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

// ReceivedPacket owns one buffer borrowed from bfd.PacketPool.
// Call Release exactly once after the packet has been parsed or copied.
type ReceivedPacket struct {
	Data []byte
	Meta PacketMeta

	bufp *[]byte
}

// Release returns the packet buffer to bfd.PacketPool.
// It is safe to call multiple times.
func (p *ReceivedPacket) Release() {
	if p == nil || p.bufp == nil {
		return
	}
	bfd.PacketPool.Put(p.bufp)
	p.bufp = nil
	p.Data = nil
}

// NewListener creates a Listener from the given configuration.
// Returns an error if the underlying socket cannot be created.
// When ReadBufferSize is set, SO_RCVBUF is applied to reduce packet loss.
func NewListener(cfg ListenerConfig) (*Listener, error) {
	conn, err := createConn(cfg)
	if err != nil {
		return nil, err
	}

	// Apply read buffer tuning if configured.
	if cfg.ReadBufferSize > 0 {
		if setter, ok := conn.(interface{ SetReadBuffer(bytes int) error }); ok {
			if err := setter.SetReadBuffer(cfg.ReadBufferSize); err != nil {
				_ = conn.Close() //nolint:gosec // G104: already returning primary error
				return nil, fmt.Errorf("set read buffer to %d: %w", cfg.ReadBufferSize, err)
			}
		}
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
// It returns a copied packet payload for callers that do not manage pooled
// buffers directly. Hot-path receivers should use RecvPacket instead.
func (l *Listener) Recv(ctx context.Context) ([]byte, PacketMeta, error) {
	pkt, err := l.RecvPacket(ctx)
	if err != nil {
		return nil, PacketMeta{}, err
	}
	defer pkt.Release()

	data := make([]byte, len(pkt.Data))
	copy(data, pkt.Data)

	return data, pkt.Meta, nil
}

// RecvPacket blocks until a BFD Control packet is received or ctx is cancelled.
// Returns a packet backed by bfd.PacketPool. The caller must call Release.
//
// RecvPacket validates the received TTL (IPv4) or Hop Limit (IPv6) per GTSM:
//   - Single-hop (RFC 5881 Section 5): TTL/HopLimit must be 255
//   - Multi-hop (RFC 5883 Section 2): TTL/HopLimit must be >= 254
func (l *Listener) RecvPacket(ctx context.Context) (*ReceivedPacket, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("listener recv: %w", err)
		}

		pkt, err := l.recvOne()
		if err != nil {
			return nil, err
		}

		// Validate GTSM TTL before returning to caller.
		if err := ValidateTTL(pkt.Meta, l.multiHop); err != nil {
			pkt.Release()
			continue // Drop packets with invalid TTL silently.
		}

		return pkt, nil
	}
}

// recvOne performs a single read from the underlying connection using
// a pooled buffer. Returns the buffer slice, metadata, and any error.
func (l *Listener) recvOne() (*ReceivedPacket, error) {
	bufp, ok := bfd.PacketPool.Get().(*[]byte)
	if !ok {
		return nil, fmt.Errorf("listener recv: %w", ErrPoolType)
	}

	n, meta, err := l.conn.ReadPacket(*bufp)
	if err != nil {
		bfd.PacketPool.Put(bufp)
		return nil, fmt.Errorf("listener read: %w", err)
	}

	return &ReceivedPacket{
		Data: (*bufp)[:n],
		Meta: meta,
		bufp: bufp,
	}, nil
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
