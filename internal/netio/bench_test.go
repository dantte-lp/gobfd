package netio_test

import (
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// =========================================================================
// Overlay Codec Benchmarks — Sprint 6 (6.4-6.7)
// =========================================================================

// -------------------------------------------------------------------------
// BuildInnerPacket — assembly hot path
// -------------------------------------------------------------------------

// BenchmarkBuildInnerPacket measures the cost of assembling a complete inner
// packet (Ethernet + IPv4 + UDP + BFD payload) for tunnel encapsulation.
// Called on every VXLAN/Geneve BFD TX (RFC 8971 Section 3, RFC 9521 Section 4.1).
func BenchmarkBuildInnerPacket(b *testing.B) {
	bfdPayload := make([]byte, 24) // Standard BFD Control packet.
	srcIP := netip.MustParseAddr("10.0.0.1")
	dstIP := netip.MustParseAddr("10.0.0.2")
	srcPort := uint16(49152)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = netio.BuildInnerPacket(bfdPayload, srcIP, dstIP, srcPort)
	}
}

// -------------------------------------------------------------------------
// StripInnerPacket — disassembly hot path
// -------------------------------------------------------------------------

// BenchmarkStripInnerPacket measures the cost of stripping inner packet
// headers to extract the BFD payload. Called on every received overlay packet.
//
// Target: 0 allocs/op (all parsing is in-place on the buffer).
func BenchmarkStripInnerPacket(b *testing.B) {
	bfdPayload := make([]byte, 24)
	srcIP := netip.MustParseAddr("10.0.0.1")
	dstIP := netip.MustParseAddr("10.0.0.2")
	buf, err := netio.BuildInnerPacket(bfdPayload, srcIP, dstIP, 49152)
	if err != nil {
		b.Fatalf("setup BuildInnerPacket: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _, _, _ = netio.StripInnerPacket(buf)
	}
}

// -------------------------------------------------------------------------
// VXLANHeader Marshal/Unmarshal
// -------------------------------------------------------------------------

// BenchmarkVXLANHeaderMarshal measures VXLAN header serialization.
//
// Target: 0 allocs/op (writes into pre-allocated buffer).
func BenchmarkVXLANHeaderMarshal(b *testing.B) {
	buf := make([]byte, netio.VXLANHeaderSize)
	vni := uint32(100)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = netio.MarshalVXLANHeader(buf, vni)
	}
}

// BenchmarkVXLANHeaderUnmarshal measures VXLAN header parsing.
//
// Target: 0 allocs/op (returns value type).
func BenchmarkVXLANHeaderUnmarshal(b *testing.B) {
	buf := make([]byte, netio.VXLANHeaderSize)
	if _, err := netio.MarshalVXLANHeader(buf, 100); err != nil {
		b.Fatalf("setup MarshalVXLANHeader: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = netio.UnmarshalVXLANHeader(buf)
	}
}

// -------------------------------------------------------------------------
// GeneveHeader Marshal/Unmarshal
// -------------------------------------------------------------------------

// BenchmarkGeneveHeaderMarshal measures Geneve header serialization.
//
// Target: 0 allocs/op (writes into pre-allocated buffer).
func BenchmarkGeneveHeaderMarshal(b *testing.B) {
	buf := make([]byte, netio.GeneveHeaderMinSize)
	hdr := netio.GeneveHeader{
		OBit:         true,
		ProtocolType: netio.GeneveProtocolEthernet,
		VNI:          100,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = netio.MarshalGeneveHeader(buf, hdr)
	}
}

// BenchmarkGeneveHeaderUnmarshal measures Geneve header parsing.
//
// Target: 0 allocs/op (returns value type).
func BenchmarkGeneveHeaderUnmarshal(b *testing.B) {
	buf := make([]byte, netio.GeneveHeaderMinSize)
	if _, err := netio.MarshalGeneveHeader(buf, netio.GeneveHeader{
		OBit:         true,
		ProtocolType: netio.GeneveProtocolEthernet,
		VNI:          100,
	}); err != nil {
		b.Fatalf("setup MarshalGeneveHeader: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = netio.UnmarshalGeneveHeader(buf)
	}
}
