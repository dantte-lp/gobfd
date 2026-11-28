package netio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type controlDeliveryRecorder struct {
	receiveBenchmarkDemux

	rows map[bfd.DeliveryCounter]uint64
}

func (r *controlDeliveryRecorder) RecordDelivery(source bfd.DeliverySource, outcome bfd.DeliveryOutcome) {
	r.rows[bfd.DeliveryCounter{Source: source, Outcome: outcome}]++
}

func (r *controlDeliveryRecorder) count(source bfd.DeliverySource, outcome bfd.DeliveryOutcome) uint64 {
	return r.rows[bfd.DeliveryCounter{Source: source, Outcome: outcome}]
}

type ttlDeliveryConn struct {
	sizedReceiveConn

	reads int
}

func (c *ttlDeliveryConn) ReadPacket(buf []byte) (int, PacketMeta, error) {
	n, meta, err := c.sizedReceiveConn.ReadPacket(buf)
	c.reads++
	if c.reads == 1 {
		meta.TTL = 254
	}
	return n, meta, err
}

func TestControlReceiverRecordsTTLBeforeParsing(t *testing.T) {
	t.Parallel()
	recorder := &controlDeliveryRecorder{rows: make(map[bfd.DeliveryCounter]uint64)}
	conn := &ttlDeliveryConn{}
	conn.payload = []byte{0}
	ln := NewListenerFromConn(conn, false, recorder)
	if err := NewReceiver(recorder, slog.New(slog.DiscardHandler)).recvOne(t.Context(), ln); err != nil {
		t.Fatal(err)
	}
	if conn.reads != 2 || recorder.count(bfd.DeliveryBaseTTL, bfd.DeliveryTTLRejected) != 1 ||
		recorder.count(bfd.DeliveryBaseTTL, bfd.DeliveryAccepted) != 1 ||
		recorder.count(bfd.DeliveryWireParse, bfd.DeliveryParserRejected) != 1 || len(recorder.rows) != 3 {
		t.Fatalf("TTL/parser decisions = %+v, reads=%d", recorder.rows, conn.reads)
	}
}

func TestControlReceiverAuthenticatedReparseCountsOnce(t *testing.T) {
	t.Parallel()
	payload := make([]byte, bfd.MaxPacketSize)
	pkt := &bfd.ControlPacket{
		Version: bfd.Version, State: bfd.StateDown, DetectMult: 3, MyDiscriminator: 1,
		DesiredMinTxInterval: 100000, RequiredMinRxInterval: 100000, AuthPresent: true,
		Auth: bfd.AuthSection{Type: bfd.AuthTypeSimplePassword, Len: 6, KeyID: 1, DataLen: 3, Data: [20]byte{'k', 'e', 'y'}},
	}
	n, err := bfd.MarshalControlPacket(pkt, payload)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &controlDeliveryRecorder{rows: make(map[bfd.DeliveryCounter]uint64)}
	ln := NewListenerFromConn(&sizedReceiveConn{payload: payload[:n]}, false, recorder)
	if err := NewReceiver(recorder, slog.New(slog.DiscardHandler)).recvOne(t.Context(), ln); err != nil {
		t.Fatal(err)
	}
	if recorder.count(bfd.DeliveryBaseTTL, bfd.DeliveryAccepted) != 1 ||
		recorder.count(bfd.DeliveryWireParse, bfd.DeliveryAccepted) != 1 || len(recorder.rows) != 2 {
		t.Fatalf("authenticated reparse counted another packet: %+v", recorder.rows)
	}
}

type deliveryOverlayConn struct {
	OverlayConn

	err     error
	payload []byte
}

func (c deliveryOverlayConn) RecvDecapsulated(context.Context) ([]byte, OverlayMeta, error) {
	return c.payload, OverlayMeta{}, c.err
}

func TestControlOverlayClassifiesAtReceiveBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bfd.DeliveryOutcome
	}{
		{"parser", ErrInnerBadIPLength, bfd.DeliveryParserRejected},
		{"TTL", ErrInnerBadTTL, bfd.DeliveryTTLRejected},
		{"policy", ErrOverlayIdentityMismatch, bfd.DeliveryPolicyRejected},
		{"read", errors.New("socket read failed"), bfd.DeliveryReadError},
		{"closed", ErrOverlayRecvClosed, bfd.DeliveryOutcomeUnspecified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := &controlDeliveryRecorder{rows: make(map[bfd.DeliveryCounter]uint64)}
			conn := deliveryOverlayConn{err: fmt.Errorf("wrapped decapsulation: %w", tc.err)}
			err := NewOverlayReceiver(conn, recorder, slog.New(slog.DiscardHandler)).recvOne(t.Context())
			if !errors.Is(err, tc.err) {
				t.Fatalf("receive error = %v", err)
			}
			if tc.want == bfd.DeliveryOutcomeUnspecified {
				if len(recorder.rows) != 0 {
					t.Fatalf("closed socket counted as packet loss: %+v", recorder.rows)
				}
			} else if recorder.count(bfd.DeliveryOverlayDecap, tc.want) != 1 || len(recorder.rows) != 1 {
				t.Fatalf("overlay decision = %+v", recorder.rows)
			}
		})
	}
}

func TestControlOverlayRecordsDecapAndParserSeparately(t *testing.T) {
	t.Parallel()
	recorder := &controlDeliveryRecorder{rows: make(map[bfd.DeliveryCounter]uint64)}
	conn := deliveryOverlayConn{payload: []byte{0}}
	if err := NewOverlayReceiver(conn, recorder, slog.New(slog.DiscardHandler)).recvOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if recorder.count(bfd.DeliveryOverlayDecap, bfd.DeliveryAccepted) != 1 ||
		recorder.count(bfd.DeliveryWireParse, bfd.DeliveryParserRejected) != 1 || len(recorder.rows) != 2 {
		t.Fatalf("decap/parser decision = %+v", recorder.rows)
	}
}

func TestControlOverlayCancellationIsNotReadLoss(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	recorder := &controlDeliveryRecorder{rows: make(map[bfd.DeliveryCounter]uint64)}
	conn := deliveryOverlayConn{err: context.Canceled}
	err := NewOverlayReceiver(conn, recorder, slog.New(slog.DiscardHandler)).recvOne(ctx)
	if !errors.Is(err, context.Canceled) || len(recorder.rows) != 0 {
		t.Fatalf("shutdown generated packet loss: err=%v rows=%+v", err, recorder.rows)
	}
}
