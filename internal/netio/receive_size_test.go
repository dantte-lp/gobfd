package netio

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type sizedReceiveConn struct {
	PacketConn

	payload  []byte
	invalid  int
	borrowed []byte
}

func (c *sizedReceiveConn) ReadPacket(buf []byte) (int, PacketMeta, error) {
	c.borrowed = buf
	if c.invalid < 0 {
		return -1, PacketMeta{}, nil
	}
	if c.invalid > 0 {
		return len(buf) + 1, PacketMeta{}, nil
	}
	return copy(buf, c.payload), PacketMeta{TTL: 255}, nil
}

func TestListenerRejectsInvalidReadSize(t *testing.T) {
	t.Parallel()
	for _, n := range []int{-1, 1} {
		t.Run(map[int]string{-1: "negative", 1: "beyond-buffer"}[n], func(t *testing.T) {
			t.Parallel()
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("invalid read size panicked: %v", v)
				}
			}()
			conn := &sizedReceiveConn{invalid: n}
			packet, err := NewListenerFromConn(conn, false).RecvPacket(t.Context())
			if !errors.Is(err, ErrInvalidReadSize) || packet != nil {
				t.Fatalf("packet=%v error=%v, want typed read-size rejection", packet, err)
			}
		})
	}
}

type receiveBenchmarkDemux struct{}

func (receiveBenchmarkDemux) DemuxWithWire(*bfd.ControlPacket, bfd.PacketMeta, []byte) (bfd.DeliveryOutcome, error) {
	return bfd.DeliveryQueued, nil
}

func (receiveBenchmarkDemux) RecordDelivery(bfd.DeliverySource, bfd.DeliveryOutcome) {}

// BenchmarkUnauthenticatedRX measures warmed pool/read/parse/demux allocations
// with an in-memory transport, excluding sockets, session handling and logging.
func BenchmarkUnauthenticatedRX(b *testing.B) {
	payload := make([]byte, bfd.HeaderSize)
	_, err := bfd.MarshalControlPacket(&bfd.ControlPacket{
		Version: bfd.Version, State: bfd.StateDown, DetectMult: 3, MyDiscriminator: 1,
		DesiredMinTxInterval: 100000, RequiredMinRxInterval: 100000,
	}, payload)
	if err != nil {
		b.Fatal(err)
	}
	ln := NewListenerFromConn(&sizedReceiveConn{payload: payload}, false)
	receiver := NewReceiver(receiveBenchmarkDemux{}, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	if err := receiver.recvOne(ctx, ln); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		if err := receiver.recvOne(ctx, ln); err != nil {
			b.Fatal(err)
		}
	}
}

type paddedWireDemux struct {
	wire []byte
	auth *bfd.AuthSection
}

func (d *paddedWireDemux) DemuxWithWire(
	pkt *bfd.ControlPacket, _ bfd.PacketMeta, wire []byte,
) (bfd.DeliveryOutcome, error) {
	d.wire, d.auth = wire, pkt.Auth
	return bfd.DeliveryQueued, nil
}

func (*paddedWireDemux) RecordDelivery(bfd.DeliverySource, bfd.DeliveryOutcome) {}

func TestPaddedAuthenticatedReceiveOwnsCompleteWire(t *testing.T) {
	t.Parallel()
	payload := make([]byte, bfd.MaxPaddedPduSize)
	payload[len(payload)-1] = 0x5a
	pkt := &bfd.ControlPacket{
		Version: bfd.Version, State: bfd.StateDown, DetectMult: 3, MyDiscriminator: 1,
		DesiredMinTxInterval: 100000, RequiredMinRxInterval: 100000, AuthPresent: true,
		Auth: &bfd.AuthSection{Type: bfd.AuthTypeSimplePassword, Len: 6, KeyID: 1, AuthData: []byte("key")},
	}
	if _, err := bfd.MarshalControlPacket(pkt, payload); err != nil {
		t.Fatal(err)
	}
	conn := &sizedReceiveConn{payload: payload}
	dmux := &paddedWireDemux{}
	receiver := NewReceiver(dmux, slog.New(slog.DiscardHandler))
	if err := receiver.recvOne(t.Context(), NewListenerFromConn(conn, false)); err != nil {
		t.Fatal(err)
	}
	if len(dmux.wire) != bfd.MaxPaddedPduSize || dmux.wire[len(dmux.wire)-1] != 0x5a {
		t.Fatalf("owned wire length=%d, want complete 9000 bytes", len(dmux.wire))
	}
	if &dmux.wire[0] == &conn.borrowed[0] {
		t.Fatal("authenticated wire aliases borrowed packet buffer")
	}
	if dmux.auth == nil || string(dmux.auth.AuthData) != "key" {
		t.Fatal("owned authentication fields lost")
	}
}
