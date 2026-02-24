package netio_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Mock EchoDemuxer
// -------------------------------------------------------------------------

// mockEchoDemuxer records DemuxEcho calls for testing.
type mockEchoDemuxer struct {
	calls     atomic.Uint64
	lastDiscr atomic.Uint32
	err       error // injectable error
}

func (m *mockEchoDemuxer) DemuxEcho(myDiscr uint32) error {
	m.calls.Add(1)
	m.lastDiscr.Store(myDiscr)
	return m.err
}

// -------------------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------------------

// buildEchoPacket creates a serialized BFD Control packet with the given
// MyDiscriminator for echo receiver testing.
func buildEchoPacket(t *testing.T, myDiscr uint32) []byte {
	t.Helper()

	pkt := bfd.ControlPacket{
		Version:              bfd.Version,
		State:                bfd.StateDown,
		DetectMult:           3,
		MyDiscriminator:      myDiscr,
		YourDiscriminator:    0,
		DesiredMinTxInterval: 1000000, // 1s in microseconds
	}
	buf := make([]byte, bfd.MaxPacketSize)
	n, err := bfd.MarshalControlPacket(&pkt, buf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data := make([]byte, n)
	copy(data, buf[:n])
	return data
}

// echoTestConn holds the mock conn and control channels for echo receiver tests.
type echoTestConn struct {
	mock    *MockPacketConn
	blockCh <-chan struct{} // signaled when mock starts blocking (all data delivered)
	stopCh  chan struct{}   // close to unblock the mock
}

// newEchoTestConn creates a MockPacketConn that returns the given packets
// in order, then signals on blockCh and blocks on stopCh.
// Close stopCh to unblock the ReadFunc and allow the receiver to exit.
func newEchoTestConn(
	addr netip.AddrPort,
	packets [][]byte,
	meta netio.PacketMeta,
) echoTestConn {
	mock := NewMockPacketConn(addr)
	blockCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	var callIdx atomic.Int32

	mock.ReadFunc = func(b []byte) (int, netio.PacketMeta, error) {
		idx := int(callIdx.Add(1)) - 1
		if idx < len(packets) {
			n := copy(b, packets[idx])
			return n, meta, nil
		}
		// All packets delivered -- signal and block.
		select {
		case blockCh <- struct{}{}:
		default:
		}
		<-stopCh
		return 0, netio.PacketMeta{}, netio.ErrSocketClosed
	}

	return echoTestConn{mock: mock, blockCh: blockCh, stopCh: stopCh}
}

// -------------------------------------------------------------------------
// Tests -- EchoReceiver
// -------------------------------------------------------------------------

// TestEchoReceiverRunNoListeners verifies that Run returns an error when
// called without any listeners.
func TestEchoReceiverRunNoListeners(t *testing.T) {
	t.Parallel()

	demuxer := &mockEchoDemuxer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recv := netio.NewEchoReceiver(demuxer, logger)

	err := recv.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for no listeners, got nil")
	}
	if !errors.Is(err, netio.ErrNoListeners) {
		t.Errorf("error = %v, want ErrNoListeners", err)
	}
}

// TestEchoReceiverDemuxesPacket verifies that the EchoReceiver unmarshals
// a valid BFD Control packet and calls DemuxEcho with the MyDiscriminator.
func TestEchoReceiverDemuxesPacket(t *testing.T) {
	t.Parallel()

	demuxer := &mockEchoDemuxer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recv := netio.NewEchoReceiver(demuxer, logger)

	pktData := buildEchoPacket(t, 42)
	addr := netip.MustParseAddrPort("10.0.0.2:3785")
	meta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("10.0.0.1"),
		DstAddr: netip.MustParseAddr("10.0.0.2"),
		TTL:     255,
	}

	tc := newEchoTestConn(addr, [][]byte{pktData}, meta)
	listener := netio.NewListenerFromConn(tc.mock, false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = recv.Run(ctx, listener)
		close(done)
	}()

	// Wait until the mock has delivered the packet and starts blocking.
	select {
	case <-tc.blockCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for echo packet delivery")
	}

	// Verify DemuxEcho was called with discriminator 42.
	if got := demuxer.calls.Load(); got < 1 {
		t.Errorf("DemuxEcho calls = %d, want >= 1", got)
	}
	if got := demuxer.lastDiscr.Load(); got != 42 {
		t.Errorf("DemuxEcho discriminator = %d, want 42", got)
	}

	// Cancel context and unblock mock to stop the receiver.
	cancel()
	close(tc.stopCh)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("echo receiver did not stop")
	}
}

// TestEchoReceiverDropsZeroDiscriminator verifies that echo packets with
// MyDiscriminator == 0 are silently dropped.
func TestEchoReceiverDropsZeroDiscriminator(t *testing.T) {
	t.Parallel()

	demuxer := &mockEchoDemuxer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recv := netio.NewEchoReceiver(demuxer, logger)

	pktData := buildEchoPacket(t, 0)
	addr := netip.MustParseAddrPort("10.0.0.2:3785")
	meta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("10.0.0.1"),
		TTL:     255,
	}

	tc := newEchoTestConn(addr, [][]byte{pktData}, meta)
	listener := netio.NewListenerFromConn(tc.mock, false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = recv.Run(ctx, listener)
		close(done)
	}()

	select {
	case <-tc.blockCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for processing")
	}

	// DemuxEcho should NOT have been called (zero discriminator dropped).
	if got := demuxer.calls.Load(); got != 0 {
		t.Errorf("DemuxEcho calls = %d, want 0 (zero discr packet should be dropped)", got)
	}

	cancel()
	close(tc.stopCh)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("echo receiver did not stop")
	}
}

// TestEchoReceiverDropsInvalidPacket verifies that malformed packets are
// silently dropped without calling DemuxEcho.
func TestEchoReceiverDropsInvalidPacket(t *testing.T) {
	t.Parallel()

	demuxer := &mockEchoDemuxer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recv := netio.NewEchoReceiver(demuxer, logger)

	// Garbage data -- not a valid BFD packet.
	garbage := []byte{0xFF, 0xFF, 0xFF}
	addr := netip.MustParseAddrPort("10.0.0.2:3785")
	meta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("10.0.0.1"),
		TTL:     255,
	}

	tc := newEchoTestConn(addr, [][]byte{garbage}, meta)
	listener := netio.NewListenerFromConn(tc.mock, false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = recv.Run(ctx, listener)
		close(done)
	}()

	select {
	case <-tc.blockCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for processing")
	}

	if got := demuxer.calls.Load(); got != 0 {
		t.Errorf("DemuxEcho calls = %d, want 0 (invalid packet should be dropped)", got)
	}

	cancel()
	close(tc.stopCh)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("echo receiver did not stop")
	}
}

// TestEchoReceiverContextCancellation verifies that the EchoReceiver's
// recvLoop stops when the context is cancelled and the mock unblocks.
func TestEchoReceiverContextCancellation(t *testing.T) {
	t.Parallel()

	demuxer := &mockEchoDemuxer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recv := netio.NewEchoReceiver(demuxer, logger)

	addr := netip.MustParseAddrPort("10.0.0.2:3785")
	tc := newEchoTestConn(addr, nil, netio.PacketMeta{})
	listener := netio.NewListenerFromConn(tc.mock, false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		_ = recv.Run(ctx, listener)
		close(done)
	}()

	// Wait for the mock to start blocking (no packets to deliver).
	select {
	case <-tc.blockCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for receiver to start blocking")
	}

	// Cancel context and unblock mock.
	cancel()
	close(tc.stopCh)

	select {
	case <-done:
		// Receiver stopped cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("echo receiver did not stop after context cancellation")
	}
}
