package netio_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Mock Demuxer
// -------------------------------------------------------------------------

type mockDemuxer struct {
	mu    sync.Mutex
	calls []demuxCall
	err   error
}

type demuxCall struct {
	MyDiscr   uint32
	YourDiscr uint32
	SrcAddr   netip.Addr
	WireLen   int
}

func (m *mockDemuxer) DemuxWithWire(pkt *bfd.ControlPacket, meta bfd.PacketMeta, wire []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, demuxCall{
		MyDiscr:   pkt.MyDiscriminator,
		YourDiscr: pkt.YourDiscriminator,
		SrcAddr:   meta.SrcAddr,
		WireLen:   len(wire),
	})
	return m.err
}

// -------------------------------------------------------------------------
// Receiver Tests
// -------------------------------------------------------------------------

func TestReceiver_RunNoListeners(t *testing.T) {
	t.Parallel()

	dmux := &mockDemuxer{}
	r := netio.NewReceiver(dmux, slog.Default())

	err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for no listeners")
	}
	if !errors.Is(err, netio.ErrNoListeners) {
		t.Errorf("error should wrap ErrNoListeners: %v", err)
	}
}

func TestReceiver_RunContextCancelled(t *testing.T) {
	t.Parallel()

	dmux := &mockDemuxer{}
	r := netio.NewReceiver(dmux, slog.Default())

	addr := netip.MustParseAddrPort("192.168.1.1:3784")
	mock := NewMockPacketConn(addr)

	// ReadFunc blocks until context is cancelled (simulates real socket).
	mock.ReadFunc = func(_ []byte) (int, netio.PacketMeta, error) {
		return 0, netio.PacketMeta{}, errors.New("context cancelled")
	}

	listener := netio.NewListenerFromConn(mock, false)
	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	// Run should return quickly without error.
	err := r.Run(ctx, listener)
	if err != nil {
		t.Errorf("Run should return nil on context cancel: %v", err)
	}
}

func TestReceiver_RunDemuxesValidPacket(t *testing.T) {
	t.Parallel()

	dmux := &mockDemuxer{}
	r := netio.NewReceiver(dmux, slog.Default())

	addr := netip.MustParseAddrPort("192.168.1.1:3784")
	mock := NewMockPacketConn(addr)

	// Build a minimal valid BFD Control packet (version 1, length 24).
	// RFC 5880 Section 4.1:
	//   Byte 0: Ver(3 bits)=1, Diag(5 bits)=0  → 0x20
	//   Byte 1: Sta(2 bits)=3(Up), Flags(6 bits)=0 → 0xC0
	//   Byte 2: Detect Mult = 3
	//   Byte 3: Length = 24
	//   Bytes 4-7: My Discriminator = 0x00000042
	//   Bytes 8-11: Your Discriminator = 0x00000043
	//   Bytes 12-15: Desired Min TX = 100000 (100ms in microseconds)
	//   Bytes 16-19: Required Min RX = 100000
	//   Bytes 20-23: Required Min Echo RX = 0
	validBFD := []byte{
		0x20,                   // Version=1, Diag=0
		0xC0,                   // State=Up(3), flags=0
		0x03,                   // Detect Mult=3
		0x18,                   // Length=24
		0x00, 0x00, 0x00, 0x42, // My Discriminator=0x42
		0x00, 0x00, 0x00, 0x43, // Your Discriminator=0x43
		0x00, 0x01, 0x86, 0xA0, // Desired Min TX=100000
		0x00, 0x01, 0x86, 0xA0, // Required Min RX=100000
		0x00, 0x00, 0x00, 0x00, // Required Min Echo RX=0
	}

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		callCount++
		if callCount == 1 {
			n := copy(buf, validBFD)
			return n, netio.PacketMeta{
				SrcAddr: netip.MustParseAddr("10.0.0.2"),
				TTL:     255,
			}, nil
		}
		// After first packet, cancel context.
		cancel()
		return 0, netio.PacketMeta{}, errors.New("stopped")
	}

	listener := netio.NewListenerFromConn(mock, false)
	defer listener.Close()

	err := r.Run(ctx, listener)
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
	if dmux.calls[0].YourDiscr != 0x43 {
		t.Errorf("YourDiscr = 0x%x, want 0x43", dmux.calls[0].YourDiscr)
	}
	if dmux.calls[0].SrcAddr != netip.MustParseAddr("10.0.0.2") {
		t.Errorf("SrcAddr = %s, want 10.0.0.2", dmux.calls[0].SrcAddr)
	}
	if dmux.calls[0].WireLen != 0 {
		t.Errorf("WireLen = %d, want 0 for unauthenticated packet", dmux.calls[0].WireLen)
	}
}

func TestReceiver_RunDropsInvalidPacket(t *testing.T) {
	t.Parallel()

	dmux := &mockDemuxer{}
	r := netio.NewReceiver(dmux, slog.Default())

	addr := netip.MustParseAddrPort("192.168.1.1:3784")
	mock := NewMockPacketConn(addr)

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		callCount++
		if callCount == 1 {
			// Invalid: too short to be a BFD packet.
			n := copy(buf, []byte{0x20, 0xC0})
			return n, netio.PacketMeta{
				SrcAddr: netip.MustParseAddr("10.0.0.2"),
				TTL:     255,
			}, nil
		}
		cancel()
		return 0, netio.PacketMeta{}, errors.New("stopped")
	}

	listener := netio.NewListenerFromConn(mock, false)
	defer listener.Close()

	err := r.Run(ctx, listener)
	if err != nil {
		t.Errorf("Run returned error: %v", err)
	}

	dmux.mu.Lock()
	defer dmux.mu.Unlock()

	if len(dmux.calls) != 0 {
		t.Errorf("expected 0 demux calls for invalid packet, got %d", len(dmux.calls))
	}
}

func TestReceiver_RunMultipleListeners(t *testing.T) {
	t.Parallel()

	dmux := &mockDemuxer{}
	r := netio.NewReceiver(dmux, slog.Default())

	validBFD := []byte{
		0x20, 0xC0, 0x03, 0x18,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x02,
		0x00, 0x01, 0x86, 0xA0,
		0x00, 0x01, 0x86, 0xA0,
		0x00, 0x00, 0x00, 0x00,
	}

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once

	makeListener := func(addr netip.AddrPort) *netio.Listener {
		mock := NewMockPacketConn(addr)
		callCount := 0
		mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
			callCount++
			if callCount == 1 {
				n := copy(buf, validBFD)
				return n, netio.PacketMeta{
					SrcAddr: netip.MustParseAddr("10.0.0.2"),
					TTL:     255,
				}, nil
			}
			once.Do(cancel)
			return 0, netio.PacketMeta{}, errors.New("stopped")
		}
		return netio.NewListenerFromConn(mock, false)
	}

	l1 := makeListener(netip.MustParseAddrPort("0.0.0.0:3784"))
	l2 := makeListener(netip.MustParseAddrPort("0.0.0.0:4784"))
	defer l1.Close()
	defer l2.Close()

	err := r.Run(ctx, l1, l2)
	if err != nil {
		t.Errorf("Run returned error: %v", err)
	}

	dmux.mu.Lock()
	defer dmux.mu.Unlock()

	// At least one listener should have delivered a packet.
	if len(dmux.calls) == 0 {
		t.Error("expected at least one demux call from multiple listeners")
	}
}
