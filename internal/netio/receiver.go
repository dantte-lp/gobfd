package netio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// ErrNoListeners indicates that Run was called without any listeners.
var ErrNoListeners = errors.New("receiver run: no listeners provided")

// Demuxer routes parsed BFD Control packets to the appropriate session.
// This interface decouples the receiver from the bfd.Manager to avoid
// tight coupling between netio and bfd packages.
type Demuxer interface {
	// DemuxWithWire routes a packet to the matching session, passing
	// raw wire bytes for authentication verification.
	DemuxWithWire(pkt *bfd.ControlPacket, meta bfd.PacketMeta, wire []byte) error
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
// Each listener gets its own goroutine. Run blocks until all listener
// goroutines complete (i.e., until ctx is cancelled and all reads
// return).
//
// Errors from individual packet reads are logged but do not stop the
// receiver. Only context cancellation terminates the loop.
func (r *Receiver) Run(ctx context.Context, listeners ...*Listener) error {
	if len(listeners) == 0 {
		return fmt.Errorf("receiver: %w", ErrNoListeners)
	}

	done := make(chan struct{}, len(listeners))

	for _, ln := range listeners {
		go func(l *Listener) {
			r.recvLoop(ctx, l)
			done <- struct{}{}
		}(ln)
	}

	// Wait for all goroutines to finish.
	for range len(listeners) {
		<-done
	}

	return nil
}

// recvLoop reads packets from a single Listener in a loop until ctx
// is cancelled. Each received packet is unmarshaled and routed to the
// Demuxer. Errors from individual reads are logged but do not stop the
// loop; only context cancellation terminates it.
func (r *Receiver) recvLoop(ctx context.Context, ln *Listener) {
	for {
		if ctx.Err() != nil {
			return
		}

		if err := r.recvOne(ctx, ln); err != nil {
			// Context cancellation during read is expected at shutdown.
			if ctx.Err() != nil {
				return
			}
			r.logger.Warn("recv error", slog.String("error", err.Error()))
		}
	}
}

// recvOne performs a single receive-unmarshal-demux cycle. The buffer
// from PacketPool is returned after demux regardless of outcome.
func (r *Receiver) recvOne(ctx context.Context, ln *Listener) error {
	raw, netMeta, err := ln.Recv(ctx)
	if err != nil {
		return fmt.Errorf("recv: %w", err)
	}

	// Convert netio.PacketMeta -> bfd.PacketMeta to avoid import cycles.
	bfdMeta := convertMeta(netMeta)

	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(raw, &pkt); err != nil {
		r.logger.Debug("invalid BFD packet",
			slog.String("src", bfdMeta.SrcAddr.String()),
			slog.String("error", err.Error()),
		)
		return nil // Drop invalid packets silently per RFC 5880 Section 6.8.6.
	}

	// Copy raw bytes for auth verification before buffer is reused.
	wire := make([]byte, len(raw))
	copy(wire, raw)

	if err := r.demuxer.DemuxWithWire(&pkt, bfdMeta, wire); err != nil {
		r.logger.Debug("demux failed",
			slog.String("src", bfdMeta.SrcAddr.String()),
			slog.String("error", err.Error()),
		)
	}

	return nil
}

// convertMeta converts netio.PacketMeta to bfd.PacketMeta.
func convertMeta(nm PacketMeta) bfd.PacketMeta {
	return bfd.PacketMeta{
		SrcAddr: nm.SrcAddr,
		DstAddr: nm.DstAddr,
		TTL:     nm.TTL,
		IfName:  nm.IfName,
	}
}
