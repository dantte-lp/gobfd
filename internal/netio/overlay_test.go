package netio_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Mock OverlayConn for overlay_test
// -------------------------------------------------------------------------

type testOverlayConn struct {
	mu       sync.Mutex
	sends    []overlaySendRecord
	recvFunc func(ctx context.Context) ([]byte, netio.OverlayMeta, error)
	sendErr  error
	closed   bool
}

type overlaySendRecord struct {
	payload []byte
	dst     netip.Addr
}

type countWarnHandler struct {
	warnings atomic.Int64
}

type overlayConnConstructor func() (netio.OverlayConn, error)

func testOverlayConnLoopbackLifecycle(
	t *testing.T,
	backend string,
	newConn overlayConnConstructor,
) {
	t.Helper()

	conn, err := newConn()
	if err != nil {
		t.Skipf("%s loopback socket unavailable: %v", backend, err)
	}

	loopback := netip.MustParseAddr("127.0.0.1")
	payload := makePayload(24)
	if sendErr := conn.SendEncapsulated(context.Background(), payload, loopback); sendErr != nil {
		t.Fatalf("SendEncapsulated: %v", sendErr)
	}

	gotPayload, meta, err := conn.RecvDecapsulated(context.Background())
	if err != nil {
		t.Fatalf("RecvDecapsulated: %v", err)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("payload = %x, want %x", gotPayload, payload)
	}
	if meta.VNI != 100 {
		t.Fatalf("VNI = %d, want 100", meta.VNI)
	}
	if meta.TTL != 255 {
		t.Fatalf("TTL = %d, want inner wire TTL 255", meta.TTL)
	}

	err = conn.SendEncapsulated(context.Background(), makePayload(9000), loopback)
	if !errors.Is(err, netio.ErrInnerPacketBufferTooShort) {
		t.Fatalf("oversized SendEncapsulated error = %v, want ErrInnerPacketBufferTooShort", err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := conn.SendEncapsulated(context.Background(), makePayload(24), loopback); !errors.Is(
		err,
		netio.ErrOverlayRecvClosed,
	) {
		t.Fatalf("SendEncapsulated after Close error = %v, want ErrOverlayRecvClosed", err)
	}
}

func TestOverlayRecvRejectsWrongInnerDestination(t *testing.T) {
	local := netip.MustParseAddr("127.0.0.1")
	wrong := netip.MustParseAddr("127.0.0.2")
	conn, err := netio.NewVXLANConn(local, 100, 49152, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Skipf("VXLAN loopback socket unavailable: %v", err)
	}
	defer conn.Close()

	packet, err := netio.BuildVXLANPacket(makePayload(24), 100, wrong, wrong, 49152)
	if err != nil {
		t.Fatalf("build packet: %v", err)
	}
	sender, err := net.ListenUDP("udp4", nil)
	if err != nil {
		t.Fatalf("open sender: %v", err)
	}
	defer sender.Close()
	if _, writeErr := sender.WriteToUDP(
		packet,
		net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, netio.VXLANPort)),
	); writeErr != nil {
		t.Fatalf("send packet: %v", writeErr)
	}

	if _, _, err := conn.RecvDecapsulated(t.Context()); !errors.Is(err, netio.ErrOverlayInnerDstMismatch) {
		t.Fatalf("RecvDecapsulated error = %v, want ErrOverlayInnerDstMismatch", err)
	}
}

func TestOverlayRecvRejectsTruncatedDatagram(t *testing.T) {
	local := netip.MustParseAddr("127.0.0.1")
	payload := makePayload(9000 - netio.VXLANHeaderSize - netio.InnerOverheadIPv4)
	tests := []struct {
		name    string
		port    uint16
		newConn func() (netio.OverlayConn, error)
		packet  func() ([]byte, error)
	}{
		{
			name: "vxlan",
			port: netio.VXLANPort,
			newConn: func() (netio.OverlayConn, error) {
				return netio.NewVXLANConn(local, 100, 49152, slog.New(slog.DiscardHandler))
			},
			packet: func() ([]byte, error) {
				return netio.BuildVXLANPacket(payload, 100, local, local, 49152)
			},
		},
		{
			name: "geneve",
			port: netio.GenevePort,
			newConn: func() (netio.OverlayConn, error) {
				return netio.NewGeneveConn(local, 100, 49152, slog.New(slog.DiscardHandler))
			},
			packet: func() ([]byte, error) {
				return netio.BuildGenevePacket(payload, 100, local, local, 49152)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := tt.newConn()
			if err != nil {
				t.Skipf("%s loopback socket unavailable: %v", tt.name, err)
			}
			defer conn.Close()

			packet, err := tt.packet()
			if err != nil {
				t.Fatalf("build packet: %v", err)
			}
			packet = append(packet, 0)
			sender, err := net.ListenUDP("udp4", nil)
			if err != nil {
				t.Fatalf("open sender: %v", err)
			}
			defer sender.Close()
			if _, writeErr := sender.WriteToUDP(
				packet,
				net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, tt.port)),
			); writeErr != nil {
				t.Fatalf("send packet: %v", writeErr)
			}

			got, _, err := conn.RecvDecapsulated(t.Context())
			if !errors.Is(err, netio.ErrOverlayPacketTruncated) || got != nil {
				t.Fatalf("RecvDecapsulated payload length=%d error=%v, want ErrOverlayPacketTruncated", len(got), err)
			}
		})
	}
}

func (h *countWarnHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *countWarnHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.warnings.Add(1)
	}
	return nil
}

func (h *countWarnHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *countWarnHandler) WithGroup(string) slog.Handler {
	return h
}

func (m *testOverlayConn) SendEncapsulated(_ context.Context, bfdPayload []byte, dstAddr netip.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := make([]byte, len(bfdPayload))
	copy(data, bfdPayload)
	m.sends = append(m.sends, overlaySendRecord{payload: data, dst: dstAddr})
	return m.sendErr
}

func (m *testOverlayConn) RecvDecapsulated(ctx context.Context) ([]byte, netio.OverlayMeta, error) {
	if m.recvFunc != nil {
		return m.recvFunc(ctx)
	}
	return nil, netio.OverlayMeta{}, errors.New("mock: recvFunc not set")
}

func (m *testOverlayConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// -------------------------------------------------------------------------
// OverlaySender Tests
// -------------------------------------------------------------------------

func TestOverlaySender_SendPacket(t *testing.T) {
	t.Parallel()

	conn := &testOverlayConn{}
	sender := netio.NewOverlaySender(conn)

	payload := []byte{0x20, 0xC0, 0x03, 0x18, 0x00, 0x00, 0x00, 0x01}
	dst := netip.MustParseAddr("10.0.0.1")

	err := sender.SendPacket(context.Background(), payload, dst)
	if err != nil {
		t.Fatalf("SendPacket: unexpected error: %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	if len(conn.sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(conn.sends))
	}
	if conn.sends[0].dst != dst {
		t.Errorf("dst = %s, want %s", conn.sends[0].dst, dst)
	}
	if len(conn.sends[0].payload) != len(payload) {
		t.Errorf("payload len = %d, want %d", len(conn.sends[0].payload), len(payload))
	}
}

func TestOverlaySender_SendPacketError(t *testing.T) {
	t.Parallel()

	conn := &testOverlayConn{sendErr: errors.New("network unreachable")}
	sender := netio.NewOverlaySender(conn)

	err := sender.SendPacket(context.Background(), []byte{0x01}, netip.MustParseAddr("10.0.0.1"))
	if err == nil {
		t.Fatal("expected error on send failure")
	}
}

func TestOverlaySender_SendPacketIPv6(t *testing.T) {
	t.Parallel()

	conn := &testOverlayConn{}
	sender := netio.NewOverlaySender(conn)

	dst := netip.MustParseAddr("2001:db8::1")
	err := sender.SendPacket(context.Background(), []byte{0x20}, dst)
	if err != nil {
		t.Fatalf("SendPacket IPv6: %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	if !conn.sends[0].dst.Is6() {
		t.Error("expected IPv6 destination")
	}
}

// -------------------------------------------------------------------------
// OverlayReceiver Tests
// -------------------------------------------------------------------------

func TestOverlayReceiver_RunContextCancelled(t *testing.T) {
	t.Parallel()

	conn := &testOverlayConn{
		recvFunc: func(_ context.Context) ([]byte, netio.OverlayMeta, error) {
			return nil, netio.OverlayMeta{}, errors.New("recv failed")
		},
	}
	dmux := &mockDemuxer{}

	recv := netio.NewOverlayReceiver(conn, dmux, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := recv.Run(ctx)
	if err != nil {
		t.Errorf("Run should return nil on context cancel: %v", err)
	}
}

func TestOverlayReceiver_RunDemuxesValidPacket(t *testing.T) {
	t.Parallel()

	// Valid BFD Control packet.
	validBFD := []byte{
		0x20, 0xC0, 0x03, 0x18,
		0x00, 0x00, 0x00, 0x42,
		0x00, 0x00, 0x00, 0x43,
		0x00, 0x01, 0x86, 0xA0,
		0x00, 0x01, 0x86, 0xA0,
		0x00, 0x00, 0x00, 0x00,
	}

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	conn := &testOverlayConn{
		recvFunc: func(_ context.Context) ([]byte, netio.OverlayMeta, error) {
			callCount++
			if callCount == 1 {
				return validBFD, netio.OverlayMeta{
					SrcAddr: netip.MustParseAddr("10.0.0.2"),
					DstAddr: netip.MustParseAddr("10.0.0.1"),
					VNI:     100,
					TTL:     254,
				}, nil
			}
			cancel()
			return nil, netio.OverlayMeta{}, errors.New("stopped")
		},
	}

	dmux := &mockDemuxer{}
	recv := netio.NewOverlayReceiver(conn, dmux, slog.Default())

	err := recv.Run(ctx)
	if err != nil {
		t.Errorf("Run returned error: %v", err)
	}

	dmux.mu.Lock()
	defer dmux.mu.Unlock()

	if len(dmux.calls) != 1 {
		t.Fatalf("expected 1 demux call, got %d", len(dmux.calls))
	}
	if dmux.calls[0].MyDiscr != 0x42 {
		t.Errorf("MyDiscr = 0x%x, want 0x42", dmux.calls[0].MyDiscr)
	}
	if dmux.calls[0].SrcAddr != netip.MustParseAddr("10.0.0.2") {
		t.Errorf("SrcAddr = %s, want 10.0.0.2", dmux.calls[0].SrcAddr)
	}
	if dmux.calls[0].TTL != 254 {
		t.Errorf("TTL = %d, want wire metadata value 254", dmux.calls[0].TTL)
	}
	if dmux.calls[0].WireLen != 0 {
		t.Errorf("WireLen = %d, want 0 for unauthenticated overlay packet", dmux.calls[0].WireLen)
	}
}

func TestOverlayReceiver_DropsExpectedOverlayErrorsWithoutWarn(t *testing.T) {
	t.Parallel()

	tests := []error{
		netio.ErrOverlayVNIMismatch,
		netio.ErrInnerBadUDPSourcePort,
		netio.ErrOverlayPacketTruncated,
		netio.ErrGeneveVAPIdentityUnavailable,
	}
	for _, dropErr := range tests {
		t.Run(dropErr.Error(), func(t *testing.T) {
			t.Parallel()
			callCount := 0
			ctx, cancel := context.WithCancel(context.Background())
			conn := &testOverlayConn{
				recvFunc: func(_ context.Context) ([]byte, netio.OverlayMeta, error) {
					callCount++
					if callCount == 1 {
						return nil, netio.OverlayMeta{}, fmt.Errorf("wrapped: %w", dropErr)
					}
					cancel()
					return nil, netio.OverlayMeta{}, errors.New("stopped")
				},
			}
			handler := &countWarnHandler{}
			dmux := &mockDemuxer{}
			recv := netio.NewOverlayReceiver(conn, dmux, slog.New(handler))

			if err := recv.Run(ctx); err != nil {
				t.Errorf("Run returned error: %v", err)
			}
			if warnings := handler.warnings.Load(); warnings != 0 {
				t.Errorf("warnings = %d, want 0 for expected overlay drop errors", warnings)
			}
			dmux.mu.Lock()
			defer dmux.mu.Unlock()
			if len(dmux.calls) != 0 {
				t.Errorf("demux calls = %d, want 0 for rejected overlay packet", len(dmux.calls))
			}
		})
	}
}

func TestOverlayReceiver_RunDropsInvalidPacket(t *testing.T) {
	t.Parallel()

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	conn := &testOverlayConn{
		recvFunc: func(_ context.Context) ([]byte, netio.OverlayMeta, error) {
			callCount++
			if callCount == 1 {
				// Invalid: too short.
				return []byte{0x20}, netio.OverlayMeta{
					SrcAddr: netip.MustParseAddr("10.0.0.2"),
				}, nil
			}
			cancel()
			return nil, netio.OverlayMeta{}, errors.New("stopped")
		},
	}

	dmux := &mockDemuxer{}
	recv := netio.NewOverlayReceiver(conn, dmux, slog.Default())

	err := recv.Run(ctx)
	if err != nil {
		t.Errorf("Run returned error: %v", err)
	}

	dmux.mu.Lock()
	defer dmux.mu.Unlock()

	if len(dmux.calls) != 0 {
		t.Errorf("expected 0 demux calls for invalid packet, got %d", len(dmux.calls))
	}
}

func TestOverlayReceiver_DemuxError(t *testing.T) {
	t.Parallel()

	validBFD := []byte{
		0x20, 0xC0, 0x03, 0x18,
		0x00, 0x00, 0x00, 0x42,
		0x00, 0x00, 0x00, 0x43,
		0x00, 0x01, 0x86, 0xA0,
		0x00, 0x01, 0x86, 0xA0,
		0x00, 0x00, 0x00, 0x00,
	}

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	conn := &testOverlayConn{
		recvFunc: func(_ context.Context) ([]byte, netio.OverlayMeta, error) {
			callCount++
			if callCount == 1 {
				return validBFD, netio.OverlayMeta{
					SrcAddr: netip.MustParseAddr("10.0.0.2"),
				}, nil
			}
			cancel()
			return nil, netio.OverlayMeta{}, errors.New("stopped")
		},
	}

	dmux := &mockDemuxer{err: errors.New("no matching session")}
	recv := netio.NewOverlayReceiver(conn, dmux, slog.Default())

	// Should not return error — demux errors are logged, not propagated.
	err := recv.Run(ctx)
	if err != nil {
		t.Errorf("Run should not propagate demux error: %v", err)
	}
}

// -------------------------------------------------------------------------
// OverlayMeta Tests
// -------------------------------------------------------------------------

func TestOverlayMeta_Fields(t *testing.T) {
	t.Parallel()

	meta := netio.OverlayMeta{
		SrcAddr: netip.MustParseAddr("10.0.0.1"),
		DstAddr: netip.MustParseAddr("10.0.0.2"),
		VNI:     42,
	}

	if meta.SrcAddr != netip.MustParseAddr("10.0.0.1") {
		t.Errorf("SrcAddr = %s, want 10.0.0.1", meta.SrcAddr)
	}
	if meta.DstAddr != netip.MustParseAddr("10.0.0.2") {
		t.Errorf("DstAddr = %s, want 10.0.0.2", meta.DstAddr)
	}
	if meta.VNI != 42 {
		t.Errorf("VNI = %d, want 42", meta.VNI)
	}
}

// -------------------------------------------------------------------------
// Overlay Errors Tests
// -------------------------------------------------------------------------

func TestOverlayErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		msg  string
	}{
		{"VNIMismatch", netio.ErrOverlayVNIMismatch, "overlay: VNI mismatch"},
		{"RecvClosed", netio.ErrOverlayRecvClosed, "overlay: connection closed"},
		{"InvalidAddr", netio.ErrOverlayInvalidAddr, "overlay: invalid remote address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.msg {
				t.Errorf("error = %q, want %q", tt.err.Error(), tt.msg)
			}
		})
	}
}
