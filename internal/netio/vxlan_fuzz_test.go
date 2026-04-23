package netio_test

import (
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// FuzzVXLANHeader tests MarshalVXLANHeader/UnmarshalVXLANHeader round-trip
// with arbitrary VNI values, plus UnmarshalVXLANHeader with random input bytes.
//
// This covers the untrusted network input path: a malicious or buggy VTEP
// may send arbitrary bytes where we expect a VXLAN header.
func FuzzVXLANHeader(f *testing.F) {
	// Seed corpus: valid VNIs.
	f.Add(uint32(0))
	f.Add(uint32(1))
	f.Add(uint32(0x00FFFFFF)) // Max valid VNI (24-bit).
	f.Add(uint32(0x01000000)) // Overflow (>24 bits).
	f.Add(uint32(100))

	f.Fuzz(func(t *testing.T, vni uint32) {
		buf := make([]byte, netio.VXLANHeaderSize)

		// Round-trip: marshal then unmarshal.
		n, err := netio.MarshalVXLANHeader(buf, vni)
		if err != nil {
			// Expected for VNI > 24 bits or invalid input.
			return
		}
		if n != netio.VXLANHeaderSize {
			t.Fatalf("MarshalVXLANHeader wrote %d bytes, want %d", n, netio.VXLANHeaderSize)
		}

		hdr, err := netio.UnmarshalVXLANHeader(buf[:n])
		if err != nil {
			t.Fatalf("UnmarshalVXLANHeader failed after successful marshal: %v", err)
		}
		if hdr.VNI != vni {
			t.Fatalf("round-trip VNI mismatch: got %d, want %d", hdr.VNI, vni)
		}
	})
}

// FuzzVXLANHeaderUnmarshalRaw tests UnmarshalVXLANHeader with arbitrary bytes.
func FuzzVXLANHeaderUnmarshalRaw(f *testing.F) {
	// Seed: valid VXLAN header with I flag set and VNI=100.
	validBuf := make([]byte, netio.VXLANHeaderSize)
	netio.MarshalVXLANHeader(validBuf, 100) //nolint:errcheck // seed corpus; error not relevant for fuzzing
	f.Add(validBuf)

	// Seed: too short.
	f.Add([]byte{0x08, 0x00, 0x00})

	// Seed: I flag not set.
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x64, 0x00})

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Must not panic on any input.
		_, _ = netio.UnmarshalVXLANHeader(data)
	})
}
