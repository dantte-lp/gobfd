package netio

import (
	"context"
	"errors"
	"fmt"
	"net"
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

	// ExpectedTTL overrides the default BFD GTSM validation with an exact
	// TTL/Hop Limit requirement. It is used for RFC 9747 Echo loopback
	// packets, which MUST be received with TTL/Hop Limit 254.
	ExpectedTTL uint8

	// Delivery records fixed receive stages; PortEcho selects separate Echo rows.
	Delivery bfd.DeliveryReporter
}

// -------------------------------------------------------------------------
// Listener — High-level BFD packet receive loop
// -------------------------------------------------------------------------

// Listener wraps a PacketConn and provides a high-level, context-aware
// receive loop for BFD Control packets. It handles buffer management
// using bfd.PacketPool and returns validated packet metadata.
type Listener struct {
	conn        PacketConn
	multiHop    bool
	expectedTTL uint8
	delivery    bfd.DeliveryReporter
	echo        bool
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

// NewListenerFromConn creates a Listener from an existing PacketConn.
// This is useful for testing with mock connections or custom transports.
func NewListenerFromConn(conn PacketConn, multiHop bool, delivery ...bfd.DeliveryReporter) *Listener {
	var reporter bfd.DeliveryReporter
	if len(delivery) != 0 {
		reporter = delivery[0]
	}
	return &Listener{
		conn:     conn,
		multiHop: multiHop,
		delivery: reporter,
	}
}

// NewListenerFromConnWithExpectedTTL creates a Listener with an exact TTL
// override. This is primarily used by tests for RFC 9747 echo loopback
// behavior.
func NewListenerFromConnWithExpectedTTL(
	conn PacketConn, multiHop bool, expectedTTL uint8, delivery ...bfd.DeliveryReporter,
) *Listener {
	var reporter bfd.DeliveryReporter
	if len(delivery) != 0 {
		reporter = delivery[0]
	}
	return &Listener{
		conn:        conn,
		multiHop:    multiHop,
		expectedTTL: expectedTTL,
		delivery:    reporter,
		echo:        true,
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
//   - Multi-hop: no listener check; RFC 5883 has no receive TTL rule, so
//     the demultiplexed session applies its hop policy (RFC 5082 Section 3)
func (l *Listener) RecvPacket(ctx context.Context) (*ReceivedPacket, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("listener recv: %w", err)
		}

		pkt, err := l.recvOne(ctx)
		if err != nil {
			return nil, err
		}

		// Validate GTSM TTL before returning to caller.
		if err := l.validateTTL(pkt.Meta); err != nil {
			l.recordTTL(bfd.DeliveryTTLRejected)
			pkt.Release()
			continue // Drop packets with invalid TTL silently.
		}
		l.recordTTL(bfd.DeliveryAccepted)
		pkt.Meta.MultiHop = l.multiHop

		return pkt, nil
	}
}

func (l *Listener) recordTTL(outcome bfd.DeliveryOutcome) {
	if l.delivery != nil {
		source := bfd.DeliveryBaseTTL
		if l.echo {
			source = bfd.DeliveryEchoTTL
		}
		l.delivery.RecordDelivery(source, outcome)
	}
}

func (l *Listener) recordRead(ctx context.Context, err error) {
	if !l.echo || l.delivery == nil {
		return
	}
	outcome := bfd.DeliveryAccepted
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrSocketClosed) || errors.Is(err, net.ErrClosed) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		outcome = bfd.DeliveryReadError
	}
	l.delivery.RecordDelivery(bfd.DeliveryEchoRead, outcome)
}

func (l *Listener) validateTTL(meta PacketMeta) error {
	if l.expectedTTL != 0 {
		return ValidateExpectedTTL(meta, l.expectedTTL, "listener")
	}
	if l.multiHop {
		return nil
	}
	return ValidateTTL(meta)
}

// recvOne performs a single read from the underlying connection using
// a pooled buffer. Returns the buffer slice, metadata, and any error.
func (l *Listener) recvOne(ctx context.Context) (*ReceivedPacket, error) {
	bufp, ok := bfd.PacketPool.Get().(*[]byte)
	if !ok {
		return nil, fmt.Errorf("listener recv: %w", ErrPoolType)
	}

	n, meta, err := l.conn.ReadPacket(*bufp)
	if err != nil {
		bfd.PacketPool.Put(bufp)
		l.recordRead(ctx, err)
		return nil, fmt.Errorf("listener read: %w", err)
	}

	bufferSize := len(*bufp)
	if n < 0 || n > bufferSize {
		bfd.PacketPool.Put(bufp)
		l.recordRead(ctx, ErrInvalidReadSize)
		return nil, fmt.Errorf("listener read size %d outside buffer %d: %w", n, bufferSize, ErrInvalidReadSize)
	}
	l.recordRead(ctx, nil)

	return &ReceivedPacket{
		Data: (*bufp)[:n],
		Meta: meta,
		bufp: bufp,
	}, nil
}

// ErrRebindUnsupported reports a listener without a kernel device binding.
var ErrRebindUnsupported = errors.New("listener has no device binding to rebind")

// RebindDevice re-applies SO_BINDTODEVICE by interface name on the same
// socket. The kernel resolves the name again, so a listener follows an
// interface deleted and recreated under that name with a new ifindex. The
// socket, its port binding and its receive loop stay in place. Linux requires
// CAP_NET_RAW to change an existing device binding.
func (l *Listener) RebindDevice() error {
	conn, ok := l.conn.(interface{ rebindDevice() error })
	if !ok {
		return ErrRebindUnsupported
	}
	return conn.rebindDevice()
}

// Close closes the underlying PacketConn.
func (l *Listener) Close() error {
	if err := l.conn.Close(); err != nil {
		return fmt.Errorf("close listener: %w", err)
	}
	return nil
}
