package netio_test

import (
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// FuzzInnerPacket tests BuildInnerPacket/StripInnerPacket round-trip
// with arbitrary BFD payloads. Verifies the inner packet codec never panics
// and preserves the payload through assembly/disassembly.
func FuzzInnerPacket(f *testing.F) {
	// Seed: typical 24-byte BFD Control packet.
	f.Add(make([]byte, 24))

	// Seed: minimum BFD packet.
	f.Add(make([]byte, 1))

	// Seed: padded BFD packet (RFC 9764).
	f.Add(make([]byte, 128))

	// Seed: empty payload.
	f.Add([]byte{})

	srcIP := netip.MustParseAddr("10.0.0.1")
	dstIP := netip.MustParseAddr("10.0.0.2")
	srcPort := uint16(49152)

	f.Fuzz(func(t *testing.T, bfdPayload []byte) {
		built, err := netio.BuildInnerPacket(bfdPayload, srcIP, dstIP, srcPort)
		if err != nil {
			return
		}

		stripped, gotSrc, gotDst, err := netio.StripInnerPacket(built)
		if err != nil {
			t.Fatalf("StripInnerPacket failed after successful build: %v", err)
		}

		if gotSrc != srcIP {
			t.Fatalf("round-trip srcIP mismatch: got %v, want %v", gotSrc, srcIP)
		}
		if gotDst != dstIP {
			t.Fatalf("round-trip dstIP mismatch: got %v, want %v", gotDst, dstIP)
		}

		if len(stripped) != len(bfdPayload) {
			t.Fatalf("round-trip payload length mismatch: got %d, want %d", len(stripped), len(bfdPayload))
		}

		for i := range bfdPayload {
			if stripped[i] != bfdPayload[i] {
				t.Fatalf("round-trip payload byte %d mismatch: got 0x%02x, want 0x%02x",
					i, stripped[i], bfdPayload[i])
			}
		}
	})
}

// FuzzStripInnerPacketRaw tests StripInnerPacket with arbitrary bytes.
func FuzzStripInnerPacketRaw(f *testing.F) {
	// Seed: valid inner packet.
	valid, _ := netio.BuildInnerPacket(make([]byte, 24),
		netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2"), 49152)
	f.Add(valid)

	// Seed: too short.
	f.Add([]byte{0x00, 0x52, 0x02})

	// Seed: wrong EtherType.
	wrong := make([]byte, netio.InnerOverheadIPv4+24)
	wrong[12] = 0x86
	wrong[13] = 0xDD
	f.Add(wrong)

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Must not panic on any input.
		_, _, _, _ = netio.StripInnerPacket(data)
	})
}
