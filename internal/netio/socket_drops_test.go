package netio

import (
	"errors"
	"log/slog"
	"math"
	"net"
	"sync"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type socketDropSample struct {
	echo    bool
	outcome bfd.SocketDropOutcome
	delta   uint32
}

type socketDropRecorder struct {
	receiveBenchmarkDemux

	samples []socketDropSample
}

func (r *socketDropRecorder) ObserveSocketDrops(echo bool, outcome bfd.SocketDropOutcome, delta uint32) {
	r.samples = append(r.samples, socketDropSample{echo, outcome, delta})
}

type socketDropRead struct {
	meta PacketMeta
	n    int
	err  error
}

type socketDropConn struct {
	PacketConn

	reads []socketDropRead
}

func (c *socketDropConn) ReadPacket(buf []byte) (int, PacketMeta, error) {
	r := c.reads[0]
	c.reads = c.reads[1:]
	buf[0] = 0
	return r.n, r.meta, r.err
}

func knownSocketDrops(n uint32) PacketMeta {
	return PacketMeta{TTL: 255, SocketDrops: n, SocketDropsKnown: true}
}

func requireSocketDropSamples(t *testing.T, recorder *socketDropRecorder, want []socketDropSample) {
	t.Helper()
	if len(recorder.samples) != len(want) {
		t.Fatalf("socket drop samples=%v, want%v", recorder.samples, want)
	}
	for i := range want {
		if recorder.samples[i] != want[i] {
			t.Fatalf("socket drop sample%d=%v, want%v", i, recorder.samples[i], want[i])
		}
	}
}

func TestSocketDropListenerSamples(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		meta []PacketMeta
		want []socketDropSample
	}{
		{
			"cumulative",
			[]PacketMeta{knownSocketDrops(10), knownSocketDrops(12), knownSocketDrops(12), knownSocketDrops(15)},
			[]socketDropSample{
				{false, bfd.SocketDropInitial, 0},
				{false, bfd.SocketDropSample, 2},
				{false, bfd.SocketDropSample, 0},
				{false, bfd.SocketDropSample, 3},
			},
		},
		{
			"wrap",
			[]PacketMeta{knownSocketDrops(math.MaxUint32 - 2), knownSocketDrops(3)},
			[]socketDropSample{{false, bfd.SocketDropInitial, 0}, {false, bfd.SocketDropSample, 6}},
		},
		{
			"stale",
			[]PacketMeta{knownSocketDrops(10), knownSocketDrops(9), knownSocketDrops(11)},
			[]socketDropSample{
				{false, bfd.SocketDropInitial, 0}, {false, bfd.SocketDropAmbiguous, 0}, {false, bfd.SocketDropSample, 1},
			},
		},
		{
			"halfRange",
			[]PacketMeta{knownSocketDrops(1), knownSocketDrops(2147483649), knownSocketDrops(2)},
			[]socketDropSample{
				{false, bfd.SocketDropInitial, 0}, {false, bfd.SocketDropAmbiguous, 0}, {false, bfd.SocketDropSample, 1},
			},
		},
		{
			"missing",
			[]PacketMeta{knownSocketDrops(10), {TTL: 255}, knownSocketDrops(12)},
			[]socketDropSample{
				{false, bfd.SocketDropInitial, 0}, {false, bfd.SocketDropUnavailable, 0}, {false, bfd.SocketDropSample, 2},
			},
		},
		{
			"invalid",
			[]PacketMeta{
				knownSocketDrops(10),
				{TTL: 255, SocketDropsKnown: true, SocketDrops: 12, SocketDropsInvalid: true},
				knownSocketDrops(13),
			},
			[]socketDropSample{
				{false, bfd.SocketDropInitial, 0}, {false, bfd.SocketDropInvalid, 0}, {false, bfd.SocketDropSample, 3},
			},
		},
		{
			"unknownInitial",
			[]PacketMeta{{TTL: 255}, knownSocketDrops(7)},
			[]socketDropSample{{false, bfd.SocketDropUnavailable, 0}, {false, bfd.SocketDropInitial, 0}},
		},
		{
			"largestDelta",
			[]PacketMeta{knownSocketDrops(0), knownSocketDrops(2147483647)},
			[]socketDropSample{{false, bfd.SocketDropInitial, 0}, {false, bfd.SocketDropSample, 2147483647}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := &socketDropRecorder{}
			conn := &socketDropConn{}
			for _, meta := range tc.meta {
				conn.reads = append(conn.reads, socketDropRead{meta: meta, n: 1})
			}
			ln := NewListenerFromConn(conn, false, recorder)
			for range tc.meta {
				packet, err := ln.RecvPacket(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				packet.Release()
			}
			requireSocketDropSamples(t, recorder, tc.want)
		})
	}
}

func TestSocketDropOptionalReporter(t *testing.T) {
	t.Parallel()
	manager := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(manager.Close)
	manager.ObserveSocketDrops(false, bfd.SocketDropSample, 3)
}

func TestSocketDropListenerEpoch(t *testing.T) {
	t.Parallel()
	recorder := &socketDropRecorder{}
	conn := &socketDropConn{reads: []socketDropRead{
		{meta: knownSocketDrops(10), n: 1}, {meta: knownSocketDrops(12), n: 1}, {meta: knownSocketDrops(14), n: 1},
	}}
	ln := NewListenerFromConn(conn, false, recorder)
	for range 3 {
		if err := NewReceiver(recorder, slog.New(slog.DiscardHandler)).recvOne(t.Context(), ln); err != nil {
			t.Fatal(err)
		}
	}
	other := NewListenerFromConn(&socketDropConn{reads: []socketDropRead{
		{meta: knownSocketDrops(100), n: 1}, {meta: knownSocketDrops(103), n: 1},
	}}, false, recorder)
	for range 2 {
		if err := NewReceiver(recorder, slog.New(slog.DiscardHandler)).recvOne(t.Context(), other); err != nil {
			t.Fatal(err)
		}
	}
	requireSocketDropSamples(t, recorder, []socketDropSample{
		{false, bfd.SocketDropInitial, 0},
		{false, bfd.SocketDropSample, 2},
		{false, bfd.SocketDropSample, 2},
		{false, bfd.SocketDropInitial, 0},
		{false, bfd.SocketDropSample, 3},
	})
}

func TestSocketDropsBeforeRejection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		read    socketDropRead
		wantErr error
		want    bfd.SocketDropOutcome
		delta   uint32
	}{
		{"parser", socketDropRead{meta: knownSocketDrops(12), n: 1}, nil, bfd.SocketDropSample, 2},
		{"size", socketDropRead{meta: knownSocketDrops(12), n: -1}, ErrInvalidReadSize, bfd.SocketDropSample, 2},
		{
			"payloadTruncated",
			socketDropRead{meta: knownSocketDrops(12), err: ErrPacketTruncated},
			ErrPacketTruncated, bfd.SocketDropSample, 2,
		},
		{
			"ancillaryTruncated",
			socketDropRead{meta: PacketMeta{SocketDropsInvalid: true}, err: ErrAncillaryTruncated},
			ErrAncillaryTruncated, bfd.SocketDropInvalid, 0,
		},
		{
			"closed",
			socketDropRead{meta: knownSocketDrops(12), err: net.ErrClosed},
			net.ErrClosed, bfd.SocketDropOutcomeCount, 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := &socketDropRecorder{}
			conn := &socketDropConn{reads: []socketDropRead{{meta: knownSocketDrops(10), n: 1}, tc.read}}
			ln := NewListenerFromConn(conn, false, recorder)
			r := NewReceiver(recorder, slog.New(slog.DiscardHandler))
			if err := r.recvOne(t.Context(), ln); err != nil {
				t.Fatal(err)
			}
			if err := r.recvOne(t.Context(), ln); !errors.Is(err, tc.wantErr) {
				t.Fatalf("error=%v,want%v", err, tc.wantErr)
			}
			want := []socketDropSample{{false, bfd.SocketDropInitial, 0}}
			if tc.want != bfd.SocketDropOutcomeCount {
				want = append(want, socketDropSample{false, tc.want, tc.delta})
			}
			requireSocketDropSamples(t, recorder, want)
		})
	}
}

func TestSocketDropsBeforeTTLAndEcho(t *testing.T) {
	t.Parallel()
	for _, echo := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "echo"}[echo], func(t *testing.T) {
			t.Parallel()
			recorder := &socketDropRecorder{}
			a, b := knownSocketDrops(10), knownSocketDrops(12)
			a.TTL = 253
			conn := &socketDropConn{reads: []socketDropRead{{meta: a, n: 1}, {meta: b, n: 1}}}
			ln := NewListenerFromConn(conn, false, recorder)
			if echo {
				b.TTL = 254
				conn.reads[1].meta = b
				ln = NewListenerFromConnWithExpectedTTL(conn, false, 254, recorder)
			}
			packet, err := ln.RecvPacket(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			packet.Release()
			requireSocketDropSamples(t, recorder, []socketDropSample{
				{echo, bfd.SocketDropInitial, 0}, {echo, bfd.SocketDropSample, 2},
			})
		})
	}
}

type constantSocketDropConn struct {
	PacketConn

	meta PacketMeta
}

func (c constantSocketDropConn) ReadPacket(buf []byte) (int, PacketMeta, error) {
	buf[0] = 0
	return 1, c.meta, nil
}

func TestSocketDropConcurrentReads(t *testing.T) {
	t.Parallel()
	recorder := &socketDropRecorder{}
	ln := NewListenerFromConn(constantSocketDropConn{meta: knownSocketDrops(10)}, false, recorder)
	p, err := ln.recvOne(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p.Release()
	ln.conn = constantSocketDropConn{meta: knownSocketDrops(12)}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			packet, readErr := ln.recvOne(t.Context())
			if readErr != nil {
				t.Error(readErr)
				return
			}
			packet.Release()
		})
	}
	wg.Wait()
	var delta uint32
	for _, sample := range recorder.samples {
		delta += sample.delta
	}
	if len(recorder.samples) != 33 || delta != 2 {
		t.Fatalf("samples=%v total=%d", recorder.samples, delta)
	}
}
