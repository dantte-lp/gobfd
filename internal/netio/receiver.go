package netio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// ErrNoListeners indicates that Run was called without any listeners.
var ErrNoListeners = errors.New("receiver run: no listeners provided")

// ErrListenerClosed indicates that a listener closed while its receiver
// context was still live.
var ErrListenerClosed = errors.New("listener closed while receiver is running")

// Demuxer routes parsed BFD Control packets to the appropriate session.
// This interface decouples the receiver from the bfd.Manager to avoid
// tight coupling between netio and bfd packages.
type Demuxer interface {
	// DemuxWithWire routes a packet to the matching session, passing
	// raw wire bytes for authentication verification.
	DemuxWithWire(pkt *bfd.ControlPacket, meta bfd.PacketMeta, wire []byte) (bfd.DeliveryOutcome, error)
	bfd.DeliveryReporter
}

// Receiver reads BFD Control packets from one or more Listeners and
// routes them to sessions via a Demuxer.
//
// The Receiver handles:
//   - Buffer management via bfd.PacketPool
//   - Packet unmarshaling via bfd.UnmarshalControlPacket
//   - Metadata conversion from netio.PacketMeta to bfd.PacketMeta
//   - Context-aware graceful shutdown
type Receiver struct {
	demuxer Demuxer
	logger  *slog.Logger
}

// NewReceiver creates a Receiver that routes packets to the given Demuxer.
func NewReceiver(demuxer Demuxer, logger *slog.Logger) *Receiver {
	return &Receiver{
		demuxer: demuxer,
		logger:  logger.With(slog.String("component", "netio.receiver")),
	}
}

// Run reads from all listeners concurrently until ctx is cancelled.
// Each listener gets its own goroutine. After cancellation Run returns nil
// once every read returns; a blocked read returns when its listener closes.
//
// Errors from individual packet reads are logged but do not stop the
// receiver. A listener that closes while ctx is live is a lost receive
// path: Run returns ErrListenerClosed at once, without waiting for the
// remaining listeners, which stop on cancellation and close.
func (r *Receiver) Run(ctx context.Context, listeners ...*Listener) error {
	if len(listeners) == 0 {
		return fmt.Errorf("receiver: %w", ErrNoListeners)
	}
	return runListenerLoops(ctx, listeners, r.recvLoop)
}

// runListenerLoops runs one loop per listener and returns the first loop
// error, or nil after every loop stops on cancellation.
func runListenerLoops(
	ctx context.Context, listeners []*Listener, loop func(context.Context, *Listener) error,
) error {
	done := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(l *Listener) { done <- loop(ctx, l) }(ln)
	}
	for range listeners {
		if err := <-done; err != nil {
			return err
		}
	}
	return nil
}

// recvLoop reads packets from a single Listener in a loop until ctx
// is cancelled. Each received packet is unmarshaled and routed to the
// Demuxer. Errors from individual reads are logged but do not stop the
// loop; a closed listener with a live ctx returns ErrListenerClosed.
func (r *Receiver) recvLoop(ctx context.Context, ln *Listener) error {
	for {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Context cancellation is expected; return nil to signal clean shutdown.
		}

		if err := r.recvOne(ctx, ln); err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // Context cancellation during recv is expected at shutdown.
			}
			if listenerClosed(err) {
				return fmt.Errorf("%w: %w", ErrListenerClosed, err)
			}
			r.logger.Warn("recv error", slog.String("error", err.Error()))
		}
	}
}

func listenerClosed(err error) bool {
	return errors.Is(err, ErrSocketClosed) || errors.Is(err, net.ErrClosed)
}

// recvOne performs a single receive-unmarshal-demux cycle. The buffer
// from PacketPool is returned after demux regardless of outcome.
func (r *Receiver) recvOne(ctx context.Context, ln *Listener) error {
	received, err := ln.RecvPacket(ctx)
	if err != nil {
		return fmt.Errorf("recv: %w", err)
	}
	defer received.Release()

	raw := received.Data

	// Convert netio.PacketMeta -> bfd.PacketMeta to avoid import cycles.
	bfdMeta := convertMeta(received.Meta)

	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(raw, &pkt); err != nil {
		r.demuxer.RecordDelivery(bfd.DeliveryWireParse, bfd.DeliveryParserRejected)
		r.logger.Debug("invalid BFD packet",
			slog.String("src", bfdMeta.SrcAddr.String()),
			slog.String("error", err.Error()),
		)
		return nil // Drop invalid packets silently per RFC 5880 Section 6.8.6.
	}
	r.demuxer.RecordDelivery(bfd.DeliveryWireParse, bfd.DeliveryAccepted)

	// Auth fields are zero-copy slices into raw. If auth is present, copy and
	// re-parse from stable wire bytes before returning raw to PacketPool.
	var wire []byte
	if pkt.Auth != nil {
		wire = make([]byte, len(raw))
		copy(wire, raw)
		if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
			return fmt.Errorf("reparse auth packet from wire copy: %w", err)
		}
	}

	if outcome, err := r.demuxer.DemuxWithWire(&pkt, bfdMeta, wire); err != nil {
		r.logger.Debug("demux failed",
			slog.String("outcome", outcome.String()),
			slog.String("src", bfdMeta.SrcAddr.String()),
			slog.String("error", err.Error()),
		)
	}

	return nil
}

// convertMeta converts netio.PacketMeta to bfd.PacketMeta.
func convertMeta(nm PacketMeta) bfd.PacketMeta {
	return bfd.PacketMeta{
		SrcAddr:  nm.SrcAddr,
		DstAddr:  nm.DstAddr,
		TTL:      nm.TTL,
		IfName:   nm.IfName,
		IfIndex:  nm.IfIndex,
		MultiHop: nm.MultiHop,
	}
}
