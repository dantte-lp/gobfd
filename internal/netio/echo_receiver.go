// Package netio provides the echo receiver for RFC 9747 BFD echo packets.
//
// The EchoReceiver reads BFD packets from port 3785 listeners, unmarshals
// the BFD Control packet header, and routes returned echo packets to the
// originating echo session via EchoDemuxer.DemuxEcho.
//
// RFC 9747 Section 3: echo packets are standard BFD Control packets sent
// to the remote system on port 3785. The remote forwards them back via
// normal IP routing. On return, MyDiscriminator identifies the originating
// echo session.
package netio

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// ErrNoEchoListeners indicates that EchoReceiver.Run was called without
// any listeners.
var ErrNoEchoListeners = fmt.Errorf("echo receiver run: %w", ErrNoListeners)

// -------------------------------------------------------------------------
// EchoDemuxer — interface for echo packet routing
// -------------------------------------------------------------------------

// EchoDemuxer routes returned echo packets to the originating echo session.
// This interface decouples the echo receiver from the bfd.Manager.
type EchoDemuxer interface {
	// DemuxEcho routes a returned echo packet by MyDiscriminator.
	// RFC 9747: echo packets are self-originated, so MyDiscriminator
	// in the returned packet identifies the local echo session.
	DemuxEcho(myDiscr uint32) error
}

// -------------------------------------------------------------------------
// EchoReceiver — reads BFD echo packets from port 3785
// -------------------------------------------------------------------------

// EchoReceiver reads returned BFD echo packets from one or more Listeners
// and routes them to echo sessions via an EchoDemuxer.
//
// The receiver handles:
//   - Buffer management via bfd.PacketPool
//   - Packet unmarshaling to extract MyDiscriminator
//   - Context-aware graceful shutdown
//
// Unlike the control Receiver, the EchoReceiver does not perform full
// packet validation (RFC 5880 Section 6.3) because echo packets are
// self-originated. Only MyDiscriminator extraction is needed for demux.
type EchoReceiver struct {
	demuxer EchoDemuxer
	logger  *slog.Logger
}

// NewEchoReceiver creates an EchoReceiver that routes echo packets to
// the given EchoDemuxer.
func NewEchoReceiver(demuxer EchoDemuxer, logger *slog.Logger) *EchoReceiver {
	return &EchoReceiver{
		demuxer: demuxer,
		logger:  logger.With(slog.String("component", "netio.echo_receiver")),
	}
}

// Run reads from all echo listeners concurrently until ctx is cancelled.
// Each listener gets its own goroutine. Run blocks until all listener
// goroutines complete.
//
// Errors from individual packet reads are logged but do not stop the
// receiver. Only context cancellation terminates the loop.
func (r *EchoReceiver) Run(ctx context.Context, listeners ...*Listener) error {
	if len(listeners) == 0 {
		return ErrNoEchoListeners
	}

	done := make(chan struct{}, len(listeners))

	for _, ln := range listeners {
		go func(l *Listener) {
			r.recvLoop(ctx, l)
			done <- struct{}{}
		}(ln)
	}

	for range listeners {
		<-done
	}

	return nil
}

// recvLoop reads echo packets from a single Listener until ctx is cancelled.
func (r *EchoReceiver) recvLoop(ctx context.Context, ln *Listener) {
	for {
		if ctx.Err() != nil {
			return
		}

		if err := r.recvOne(ctx, ln); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.logger.Warn("echo recv error", slog.String("error", err.Error()))
		}
	}
}

// recvOne performs a single receive-unmarshal-demux cycle for an echo packet.
func (r *EchoReceiver) recvOne(ctx context.Context, ln *Listener) error {
	raw, _, err := ln.Recv(ctx)
	if err != nil {
		return fmt.Errorf("echo recv: %w", err)
	}

	// Unmarshal just enough to extract MyDiscriminator.
	// RFC 9747: echo packets use standard BFD Control format.
	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(raw, &pkt); err != nil {
		r.logger.Debug("invalid echo packet",
			slog.String("error", err.Error()),
		)
		return nil // Drop invalid packets silently.
	}

	// RFC 9747: MyDiscriminator identifies the originating echo session.
	if pkt.MyDiscriminator == 0 {
		r.logger.Debug("echo packet with zero MyDiscriminator, dropping")
		return nil
	}

	if err := r.demuxer.DemuxEcho(pkt.MyDiscriminator); err != nil {
		r.logger.Debug("echo demux failed",
			slog.Uint64("my_discr", uint64(pkt.MyDiscriminator)),
			slog.String("error", err.Error()),
		)
	}

	return nil
}
