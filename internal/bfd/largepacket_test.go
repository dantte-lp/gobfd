package bfd_test

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// RFC 9764 — BFD Large Packets (Path MTU Verification)
// -------------------------------------------------------------------------

// TestLargePacket_PaddedPduSize verifies that when PaddedPduSize is set,
// transmitted packets are padded with zeros to the configured size.
func TestLargePacket_PaddedPduSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const paddedSize = 128

		cfg := bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
			PaddedPduSize:         paddedSize,
		}

		sender := &mockSender{}
		sess := mustNewSession(t, cfg, 42, sender, nil, slog.Default())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sess.Run(ctx)
		// Wait for at least one TX interval (slow rate = 1s for Down state).
		time.Sleep(2 * time.Second)

		count := sender.packetCount()
		if count == 0 {
			t.Fatal("expected at least one packet sent")
		}

		// Check the raw bytes of the last sent packet.
		sender.mu.Lock()
		raw := sender.packets[len(sender.packets)-1]
		sender.mu.Unlock()

		if len(raw) != paddedSize {
			t.Errorf("packet length = %d, want %d", len(raw), paddedSize)
		}

		// BFD Length field (byte 3) should be the actual BFD PDU size (24),
		// not the padded size.
		bfdLen := int(raw[3])
		if bfdLen != bfd.HeaderSize {
			t.Errorf("BFD Length field = %d, want %d", bfdLen, bfd.HeaderSize)
		}

		// Padding bytes must be zero (RFC 9764 Section 3).
		for i := bfdLen; i < len(raw); i++ {
			if raw[i] != 0 {
				t.Errorf("padding byte %d = %#x, want 0x00", i, raw[i])
				break
			}
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestLargePacket_NoPaddingByDefault verifies that without PaddedPduSize,
// packets are sent at the normal BFD PDU size (no padding).
func TestLargePacket_NoPaddingByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
		}

		sender := &mockSender{}
		sess := mustNewSession(t, cfg, 43, sender, nil, slog.Default())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sess.Run(ctx)
		time.Sleep(2 * time.Second)

		count := sender.packetCount()
		if count == 0 {
			t.Fatal("expected at least one packet sent")
		}

		sender.mu.Lock()
		raw := sender.packets[len(sender.packets)-1]
		sender.mu.Unlock()

		// Without padding, packet length should equal BFD Length field.
		bfdLen := int(raw[3])
		if len(raw) != bfdLen {
			t.Errorf("packet length = %d, want BFD Length %d (no padding)", len(raw), bfdLen)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestLargePacket_Validation verifies PaddedPduSize validation in SessionConfig.
func TestLargePacket_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		size     uint16
		wantErr  bool
		errMatch string
	}{
		{name: "zero (disabled)", size: 0, wantErr: false},
		{name: "minimum (HeaderSize)", size: bfd.HeaderSize, wantErr: false},
		{name: "typical 128 bytes", size: 128, wantErr: false},
		{name: "typical 512 bytes", size: 512, wantErr: false},
		{name: "typical 1500 bytes", size: 1500, wantErr: false},
		{name: "maximum 9000", size: bfd.MaxPaddedPduSize, wantErr: false},
		{name: "too small (1)", size: 1, wantErr: true, errMatch: "padded PDU size"},
		{name: "too small (23)", size: 23, wantErr: true, errMatch: "padded PDU size"},
		{name: "too large (9001)", size: 9001, wantErr: true, errMatch: "padded PDU size"},
		{name: "too large (max uint16)", size: 65535, wantErr: true, errMatch: "padded PDU size"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := defaultSessionConfig()
			cfg.PaddedPduSize = tt.size

			sender := &mockSender{}
			_, err := bfd.NewSession(cfg, 44, sender, nil, slog.Default())

			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewSession() error = nil, want error containing %q", tt.errMatch)
				}
				if tt.errMatch != "" && !strings.Contains(err.Error(), tt.errMatch) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.errMatch)
				}
			} else if err != nil {
				t.Fatalf("NewSession() unexpected error: %v", err)
			}
		})
	}
}

// TestLargePacket_PaddedPduSize_SmallValue verifies that when PaddedPduSize
// equals HeaderSize (24), no extra padding is added.
func TestLargePacket_PaddedPduSize_SmallValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
			PaddedPduSize:         bfd.HeaderSize, // 24 = same as unauthenticated packet
		}

		sender := &mockSender{}
		sess := mustNewSession(t, cfg, 45, sender, nil, slog.Default())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sess.Run(ctx)
		time.Sleep(2 * time.Second)

		count := sender.packetCount()
		if count == 0 {
			t.Fatal("expected at least one packet sent")
		}

		sender.mu.Lock()
		raw := sender.packets[len(sender.packets)-1]
		sender.mu.Unlock()

		// PaddedPduSize = HeaderSize and unauthenticated packet is 24 bytes,
		// so no extra padding should be added.
		if len(raw) != bfd.HeaderSize {
			t.Errorf("packet length = %d, want %d", len(raw), bfd.HeaderSize)
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestLargePacket_PaddedPduSize_JumboFrame verifies large padding (close to MTU).
func TestLargePacket_PaddedPduSize_JumboFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const paddedSize = 9000

		cfg := bfd.SessionConfig{
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  100 * time.Millisecond,
			RequiredMinRxInterval: 100 * time.Millisecond,
			DetectMultiplier:      3,
			PaddedPduSize:         paddedSize,
		}

		sender := &mockSender{}
		sess := mustNewSession(t, cfg, 46, sender, nil, slog.Default())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go sess.Run(ctx)
		time.Sleep(2 * time.Second)

		count := sender.packetCount()
		if count == 0 {
			t.Fatal("expected at least one packet sent")
		}

		sender.mu.Lock()
		raw := sender.packets[len(sender.packets)-1]
		sender.mu.Unlock()

		if len(raw) != paddedSize {
			t.Errorf("packet length = %d, want %d", len(raw), paddedSize)
		}

		// Verify BFD header is intact.
		bfdLen := int(raw[3])
		if bfdLen < bfd.HeaderSize {
			t.Errorf("BFD Length field = %d, want >= %d", bfdLen, bfd.HeaderSize)
		}

		// All padding bytes must be zero.
		for i := bfdLen; i < len(raw); i++ {
			if raw[i] != 0 {
				t.Errorf("padding byte %d = %#x, want 0x00", i, raw[i])
				break
			}
		}

		time.Sleep(10 * time.Millisecond)
	})
}

// TestLargePacket_ReceiverAcceptsOversized verifies that the receiver
// (UnmarshalControlPacket) accepts packets larger than the BFD Length field.
// RFC 9764: receivers MUST NOT reject packets with trailing data.
func TestLargePacket_ReceiverAcceptsOversized(t *testing.T) {
	t.Parallel()

	// Build a valid 24-byte BFD packet and pad to 256 bytes.
	pkt := bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 bfd.StateDown,
		DetectMult:            3,
		MyDiscriminator:       100,
		YourDiscriminator:     0,
		DesiredMinTxInterval:  100000,
		RequiredMinRxInterval: 100000,
	}

	buf := make([]byte, 256)
	n, err := bfd.MarshalControlPacket(&pkt, buf)
	if err != nil {
		t.Fatalf("MarshalControlPacket: %v", err)
	}
	// Zero-fill padding (already zero from make).
	_ = n

	// Unmarshal should succeed despite buffer being larger than BFD Length.
	var decoded bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(buf, &decoded); err != nil {
		t.Fatalf("UnmarshalControlPacket on padded packet: %v", err)
	}

	if decoded.MyDiscriminator != 100 {
		t.Errorf("MyDiscriminator = %d, want 100", decoded.MyDiscriminator)
	}
}

// TestLargePacket_PaddedPduSizeAccessor verifies that the PaddedPduSize
// accessor correctly reports the configured value.
func TestLargePacket_PaddedPduSizeAccessor(t *testing.T) {
	t.Parallel()

	// Session with padding.
	cfg := defaultSessionConfig()
	cfg.PaddedPduSize = 512

	sender := &mockSender{}
	sess, err := bfd.NewSession(cfg, 47, sender, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if sess.PaddedPduSize() != 512 {
		t.Errorf("PaddedPduSize() = %d, want 512", sess.PaddedPduSize())
	}

	// Session without padding.
	cfg2 := defaultSessionConfig()
	sess2, err := bfd.NewSession(cfg2, 48, sender, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if sess2.PaddedPduSize() != 0 {
		t.Errorf("PaddedPduSize() = %d, want 0", sess2.PaddedPduSize())
	}
}
