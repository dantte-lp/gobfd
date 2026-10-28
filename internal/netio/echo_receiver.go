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
	// This preview path lacks RFC 9747's bootstrap/YourDiscriminator procedure.
	DemuxEcho(myDiscr uint32) (bfd.DeliveryOutcome, error)
	bfd.DeliveryReporter
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
// The preview omits full return validation and authentication. Self-originated
// packets remain spoofable; MyDiscriminator-only routing is an implementation
// gap, not an exemption from RFC 9747 Section 2 or RFC 5880 Section 6.3.
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
// receiver. A listener that closes while ctx is live returns
// ErrListenerClosed, as in Receiver.Run.
func (r *EchoReceiver) Run(ctx context.Context, listeners ...*Listener) error {
	if len(listeners) == 0 {
		return ErrNoEchoListeners
	}
	return runListenerLoops(ctx, listeners, r.recvLoop)
}

// recvLoop reads echo packets from a single Listener until ctx is cancelled.
func (r *EchoReceiver) recvLoop(ctx context.Context, ln *Listener) error {
	for {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Context cancellation is expected; return nil to signal clean shutdown.
		}

		if err := r.recvOne(ctx, ln); err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // Context cancellation during recv is expected at shutdown.
			}
			if listenerClosed(err) {
				return fmt.Errorf("echo %w: %w", ErrListenerClosed, err)
			}
			r.logger.Warn("echo recv error", slog.String("error", err.Error()))
		}
	}
}

// recvOne performs a single receive-unmarshal-demux cycle for an echo packet.
func (r *EchoReceiver) recvOne(ctx context.Context, ln *Listener) error {
	received, err := ln.RecvPacket(ctx)
	if err != nil {
		return fmt.Errorf("echo recv: %w", err)
	}
	defer received.Release()

	// Unmarshal just enough to extract MyDiscriminator.
	// RFC 9747: echo packets use standard BFD Control format.
	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(received.Data, &pkt); err != nil {
		r.demuxer.RecordDelivery(bfd.DeliveryEchoParse, bfd.DeliveryParserRejected)
		r.logger.Debug("invalid echo packet",
			slog.String("error", err.Error()),
		)
		return nil // Drop invalid packets silently.
	}

	r.demuxer.RecordDelivery(bfd.DeliveryEchoParse, bfd.DeliveryAccepted)

	if outcome, err := r.demuxer.DemuxEcho(pkt.MyDiscriminator); err != nil {
		r.logger.Debug("echo demux failed",
			slog.String("outcome", outcome.String()),
			slog.Uint64("my_discr", uint64(pkt.MyDiscriminator)),
			slog.String("error", err.Error()),
		)
	}

	return nil
}
