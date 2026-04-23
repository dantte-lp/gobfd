//go:build linux

package netio

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
)

func TestSenderOptionsApply(t *testing.T) {
	t.Parallel()

	s := &UDPSender{}
	WithDFBit()(s)
	WithDstPort(PortEcho)(s)
	WithBindDevice("lo")(s)
	WithWriteBuffer(4096)(s)

	if !s.dfBit {
		t.Fatal("dfBit = false, want true")
	}
	if s.dstPort != PortEcho {
		t.Fatalf("dstPort = %d, want %d", s.dstPort, PortEcho)
	}
	if s.bindDevice != "lo" {
		t.Fatalf("bindDevice = %q, want lo", s.bindDevice)
	}
	if s.writeBufferSize != 4096 {
		t.Fatalf("writeBufferSize = %d, want 4096", s.writeBufferSize)
	}
}

func TestNewUDPSenderLoopbackLifecycle(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	s, err := NewUDPSender(
		netip.MustParseAddr("127.0.0.1"),
		0,
		false,
		logger,
		WithDFBit(),
		WithDstPort(PortEcho),
		WithWriteBuffer(4096),
	)
	if err != nil {
		t.Fatalf("NewUDPSender: %v", err)
	}

	if s.dstPort != PortEcho {
		t.Fatalf("dstPort = %d, want %d", s.dstPort, PortEcho)
	}
	if err := s.SendPacket(context.Background(), []byte{1, 2, 3}, netip.MustParseAddr("127.0.0.1")); err != nil {
		t.Fatalf("SendPacket: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := s.SendPacket(context.Background(), []byte{1}, netip.MustParseAddr("127.0.0.1")); !errors.Is(err, ErrSocketClosed) {
		t.Fatalf("SendPacket after Close error = %v, want ErrSocketClosed", err)
	}
}
