package netio

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type echoDeliveryRecorder struct {
	controlDeliveryRecorder

	calls int
}

func (r *echoDeliveryRecorder) DemuxEcho(uint32) (bfd.DeliveryOutcome, error) {
	r.calls++
	return bfd.DeliveryQueued, nil
}

type echoDeliveryConn struct {
	sizedReceiveConn

	reads       int
	badFirstTTL bool
	err         error
}

func (c *echoDeliveryConn) ReadPacket(buf []byte) (int, PacketMeta, error) {
	if c.err != nil {
		return 0, PacketMeta{}, c.err
	}
	n, meta, err := c.sizedReceiveConn.ReadPacket(buf)
	c.reads++
	meta.TTL = TTLEchoLoopback
	if c.badFirstTTL && c.reads == 1 {
		meta.TTL++
	}
	return n, meta, err
}

func TestEchoReceiverSeparatesReadTTLAndParserDecisions(t *testing.T) {
	for _, name := range []string{"valid", "malformed", "zero discriminator"} {
		t.Run(name, func(t *testing.T) {
			payload := make([]byte, bfd.HeaderSize)
			_, err := bfd.MarshalControlPacket(&bfd.ControlPacket{
				Version: bfd.Version, State: bfd.StateDown, DetectMult: 3, MyDiscriminator: 42,
				DesiredMinTxInterval: 100000, RequiredMinRxInterval: 100000,
			}, payload)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "malformed":
				payload = payload[:1]
			case "zero discriminator":
				clear(payload[4:8])
			}
			recorder := &echoDeliveryRecorder{}
			recorder.rows = make(map[bfd.DeliveryCounter]uint64)
			conn := &echoDeliveryConn{badFirstTTL: true}
			conn.payload = payload
			ln := NewListenerFromConnWithExpectedTTL(conn, false, TTLEchoLoopback, recorder)
			if err := NewEchoReceiver(recorder, slog.New(slog.DiscardHandler)).recvOne(t.Context(), ln); err != nil {
				t.Fatal(err)
			}
			parser, calls := bfd.DeliveryParserRejected, 0
			if name == "valid" {
				parser, calls = bfd.DeliveryAccepted, 1
			}
			if conn.reads != 2 || recorder.calls != calls || len(recorder.rows) != 4 ||
				recorder.count(bfd.DeliveryEchoRead, bfd.DeliveryAccepted) != 2 ||
				recorder.count(bfd.DeliveryEchoTTL, bfd.DeliveryTTLRejected) != 1 ||
				recorder.count(bfd.DeliveryEchoTTL, bfd.DeliveryAccepted) != 1 ||
				recorder.count(bfd.DeliveryEchoParse, parser) != 1 {
				t.Fatalf("Echo stage accounting=%+v reads=%d demux attempts=%d", recorder.rows, conn.reads, recorder.calls)
			}
		})
	}
}

func TestEchoReceiverReadErrorsExcludeShutdown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		cancel bool
		want   uint64
	}{
		{"read error", errors.New("read failed"), false, 1},
		{"socket closed", ErrSocketClosed, false, 0},
		{"network closed", net.ErrClosed, false, 0},
		{"context canceled", context.Canceled, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &echoDeliveryRecorder{}
			recorder.rows = make(map[bfd.DeliveryCounter]uint64)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			ln := NewListenerFromConnWithExpectedTTL(&echoDeliveryConn{err: tc.err}, false, TTLEchoLoopback, recorder)
			err := NewEchoReceiver(recorder, slog.New(slog.DiscardHandler)).recvOne(ctx, ln)
			if !errors.Is(err, tc.err) || recorder.count(bfd.DeliveryEchoRead, bfd.DeliveryReadError) != tc.want ||
				recorder.calls != 0 || len(recorder.rows) != int(tc.want) {
				t.Fatalf("Echo read error=%v decisions=%+v", err, recorder.rows)
			}
		})
	}
}
