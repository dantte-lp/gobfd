package netio_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
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
