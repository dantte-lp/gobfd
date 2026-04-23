package netio_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// FuzzGeneveHeader tests MarshalGeneveHeader/UnmarshalGeneveHeader round-trip
// with arbitrary header values, plus UnmarshalGeneveHeader with random input.
//
// This covers the untrusted network input path: a remote NVE may send
// arbitrary bytes where we expect a Geneve header.
func FuzzGeneveHeader(f *testing.F) {
	// Seed corpus with representative headers.
	f.Add(uint32(0), true, false, netio.GeneveProtocolEthernet)
	f.Add(uint32(100), true, false, netio.GeneveProtocolEthernet)
	f.Add(uint32(0x00FFFFFF), false, false, netio.GeneveProtocolIPv4)
	f.Add(uint32(0x01000000), true, false, netio.GeneveProtocolIPv6) // VNI overflow.

	f.Fuzz(func(t *testing.T, vni uint32, oBit, cBit bool, protoType uint16) {
		hdr := netio.GeneveHeader{
			Version:      0, // Only version 0 is valid.
			OptLen:       0, // No options.
			OBit:         oBit,
			CBit:         cBit,
			ProtocolType: protoType,
			VNI:          vni,
		}

		buf := make([]byte, netio.GeneveHeaderMinSize)

		n, err := netio.MarshalGeneveHeader(buf, hdr)
		if err != nil {
			// Expected for VNI > 24 bits.
			return
		}
		if n != netio.GeneveHeaderMinSize {
			t.Fatalf("MarshalGeneveHeader wrote %d bytes, want %d", n, netio.GeneveHeaderMinSize)
		}

		got, err := netio.UnmarshalGeneveHeader(buf[:n])
		if err != nil {
			t.Fatalf("UnmarshalGeneveHeader failed after successful marshal: %v", err)
		}
		if got.VNI != vni {
			t.Fatalf("round-trip VNI mismatch: got %d, want %d", got.VNI, vni)
		}
		if got.OBit != oBit {
			t.Fatalf("round-trip OBit mismatch: got %v, want %v", got.OBit, oBit)
		}
		if got.CBit != cBit {
			t.Fatalf("round-trip CBit mismatch: got %v, want %v", got.CBit, cBit)
		}
		if got.ProtocolType != protoType {
			t.Fatalf("round-trip ProtocolType mismatch: got 0x%04x, want 0x%04x", got.ProtocolType, protoType)
		}
	})
}

// FuzzGeneveHeaderUnmarshalRaw tests UnmarshalGeneveHeader with arbitrary bytes.
func FuzzGeneveHeaderUnmarshalRaw(f *testing.F) {
	// Seed: valid Geneve header.
	validBuf := make([]byte, netio.GeneveHeaderMinSize)
	//nolint:errcheck // seed corpus; error not relevant for fuzzing
	netio.MarshalGeneveHeader(validBuf, netio.GeneveHeader{
		OBit:         true,
		ProtocolType: netio.GeneveProtocolEthernet,
		VNI:          100,
	})
	f.Add(validBuf)

	// Seed: too short.
	f.Add([]byte{0x00, 0x80, 0x65})

	// Seed: invalid version (non-zero).
	f.Add([]byte{0x40, 0x80, 0x65, 0x58, 0x00, 0x00, 0x64, 0x00})

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Must not panic on any input.
		_, _ = netio.UnmarshalGeneveHeader(data)
	})
}
