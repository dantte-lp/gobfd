package bfd_test

import (
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// -------------------------------------------------------------------------
// BenchmarkControlPacketMarshal — hot path: serialize BFD Control packet
// -------------------------------------------------------------------------

// BenchmarkControlPacketMarshal measures the performance of marshaling a
// BFD Control packet into a pre-allocated buffer. This is the critical
// hot path executed on every TX interval (RFC 5880 Section 6.8.7).
//
// Target: zero allocations per operation.
func BenchmarkControlPacketMarshal(b *testing.B) {
	pkt := &bfd.ControlPacket{
		Version:                   bfd.Version,
		Diag:                      bfd.DiagNone,
		State:                     bfd.StateUp,
		Poll:                      false,
		Final:                     false,
		ControlPlaneIndependent:   false,
		AuthPresent:               false,
		Demand:                    false,
		Multipoint:                false,
		DetectMult:                3,
		MyDiscriminator:           0xDEADBEEF,
		YourDiscriminator:         0xCAFEBABE,
		DesiredMinTxInterval:      100000, // 100ms in microseconds
		RequiredMinRxInterval:     100000, // 100ms in microseconds
		RequiredMinEchoRxInterval: 0,
	}
	buf := make([]byte, bfd.MaxPacketSize)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = bfd.MarshalControlPacket(pkt, buf)
	}
}

// -------------------------------------------------------------------------
// BenchmarkControlPacketMarshalWithAuth — marshal with SHA1 auth section
// -------------------------------------------------------------------------

// BenchmarkControlPacketMarshalWithAuth measures marshaling a packet that
// includes a Keyed SHA1 authentication section (RFC 5880 Section 4.4).
// This is the worst-case marshal path: 24-byte header + 28-byte auth = 52 bytes.
func BenchmarkControlPacketMarshalWithAuth(b *testing.B) {
	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		Diag:                  bfd.DiagNone,
		State:                 bfd.StateUp,
		AuthPresent:           true,
		DetectMult:            3,
		MyDiscriminator:       0xDEADBEEF,
		YourDiscriminator:     0xCAFEBABE,
		DesiredMinTxInterval:  100000,
		RequiredMinRxInterval: 100000,
		Auth: &bfd.AuthSection{
			Type:           bfd.AuthTypeKeyedSHA1,
			Len:            28,
			KeyID:          1,
			SequenceNumber: 42,
			Digest:         make([]byte, 20),
		},
	}
	buf := make([]byte, bfd.MaxPacketSize)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = bfd.MarshalControlPacket(pkt, buf)
	}
}

// -------------------------------------------------------------------------
// BenchmarkControlPacketUnmarshal — hot path: parse BFD Control packet
// -------------------------------------------------------------------------

// BenchmarkControlPacketUnmarshal measures the performance of unmarshaling
// a BFD Control packet from a wire-format buffer. This is the critical
// hot path executed on every RX packet (RFC 5880 Section 6.8.6).
//
// Target: zero allocations per operation (no auth section).
func BenchmarkControlPacketUnmarshal(b *testing.B) {
	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		Diag:                  bfd.DiagNone,
		State:                 bfd.StateUp,
		DetectMult:            3,
		MyDiscriminator:       0xDEADBEEF,
		YourDiscriminator:     0xCAFEBABE,
		DesiredMinTxInterval:  100000,
		RequiredMinRxInterval: 100000,
	}
	buf := make([]byte, bfd.MaxPacketSize)
	n, err := bfd.MarshalControlPacket(pkt, buf)
	if err != nil {
		b.Fatalf("setup marshal: %v", err)
	}
	wire := buf[:n]

	var dst bfd.ControlPacket

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.UnmarshalControlPacket(wire, &dst)
	}
}

// -------------------------------------------------------------------------
// BenchmarkControlPacketUnmarshalWithAuth — unmarshal with SHA1 auth
// -------------------------------------------------------------------------

// BenchmarkControlPacketUnmarshalWithAuth measures unmarshaling a packet
// with a Keyed SHA1 auth section. This exercises the auth section parsing
// code path (RFC 5880 Sections 4.4, 6.8.6).
func BenchmarkControlPacketUnmarshalWithAuth(b *testing.B) {
	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		Diag:                  bfd.DiagNone,
		State:                 bfd.StateUp,
		AuthPresent:           true,
		DetectMult:            3,
		MyDiscriminator:       0xDEADBEEF,
		YourDiscriminator:     0xCAFEBABE,
		DesiredMinTxInterval:  100000,
		RequiredMinRxInterval: 100000,
		Auth: &bfd.AuthSection{
			Type:           bfd.AuthTypeKeyedSHA1,
			Len:            28,
			KeyID:          1,
			SequenceNumber: 42,
			Digest:         make([]byte, 20),
		},
	}
	buf := make([]byte, bfd.MaxPacketSize)
	n, err := bfd.MarshalControlPacket(pkt, buf)
	if err != nil {
		b.Fatalf("setup marshal: %v", err)
	}
	wire := buf[:n]

	var dst bfd.ControlPacket

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.UnmarshalControlPacket(wire, &dst)
	}
}

// -------------------------------------------------------------------------
// BenchmarkControlPacketRoundTrip — marshal + unmarshal combined
// -------------------------------------------------------------------------

// BenchmarkControlPacketRoundTrip measures the combined marshal-unmarshal
// round trip. This represents the full codec cost per BFD packet exchange.
func BenchmarkControlPacketRoundTrip(b *testing.B) {
	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		Diag:                  bfd.DiagNone,
		State:                 bfd.StateUp,
		DetectMult:            3,
		MyDiscriminator:       0xDEADBEEF,
		YourDiscriminator:     0xCAFEBABE,
		DesiredMinTxInterval:  100000,
		RequiredMinRxInterval: 100000,
	}
	buf := make([]byte, bfd.MaxPacketSize)
	var dst bfd.ControlPacket

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		n, _ := bfd.MarshalControlPacket(pkt, buf)
		_ = bfd.UnmarshalControlPacket(buf[:n], &dst)
	}
}

// -------------------------------------------------------------------------
// BenchmarkFSMTransition — FSM table lookup for common transitions
// -------------------------------------------------------------------------

// BenchmarkFSMTransitionUpRecvUp measures the most frequent FSM transition:
// Up + RecvUp (keepalive self-loop). This is the steady-state hot path
// executed on every received packet when the session is established.
func BenchmarkFSMTransitionUpRecvUp(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.ApplyEvent(bfd.StateUp, bfd.EventRecvUp)
	}
}

// BenchmarkFSMTransitionDownRecvDown measures the Down + RecvDown -> Init
// transition, the first step of the three-way handshake
// (RFC 5880 Section 6.8.6).
func BenchmarkFSMTransitionDownRecvDown(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.ApplyEvent(bfd.StateDown, bfd.EventRecvDown)
	}
}

// BenchmarkFSMTransitionUpTimerExpired measures Up + TimerExpired -> Down,
// the detection timeout path (RFC 5880 Section 6.8.4).
func BenchmarkFSMTransitionUpTimerExpired(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.ApplyEvent(bfd.StateUp, bfd.EventTimerExpired)
	}
}

// BenchmarkFSMTransitionIgnored measures the cost of an ignored event
// (no entry in transition table). This verifies the map miss path is cheap.
func BenchmarkFSMTransitionIgnored(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.ApplyEvent(bfd.StateAdminDown, bfd.EventRecvUp)
	}
}

// -------------------------------------------------------------------------
// BenchmarkApplyJitter — jitter calculation on TX interval
// -------------------------------------------------------------------------

// BenchmarkApplyJitter measures the jitter function applied to every TX
// interval (RFC 5880 Section 6.8.7). This is called on every packet
// transmission to randomize the sending interval.
func BenchmarkApplyJitter(b *testing.B) {
	interval := 100 * time.Millisecond

	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.ApplyJitter(interval, 3)
	}
}

// BenchmarkApplyJitterDetectMultOne measures jitter with DetectMult=1,
// which uses the stricter 75%-90% range (RFC 5880 Section 6.8.7).
func BenchmarkApplyJitterDetectMultOne(b *testing.B) {
	interval := 100 * time.Millisecond

	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.ApplyJitter(interval, 1)
	}
}

// -------------------------------------------------------------------------
// BenchmarkPacketPool — sync.Pool Get/Put cycle
// -------------------------------------------------------------------------

// BenchmarkPacketPool measures the sync.Pool overhead for packet buffer
// reuse. The pool is used on every packet receive to avoid heap allocation.
func BenchmarkPacketPool(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		bufp := bfd.PacketPool.Get().(*[]byte)
		bfd.PacketPool.Put(bufp)
	}
}

// -------------------------------------------------------------------------
// BenchmarkRecvStateToEvent — state-to-event mapping
// -------------------------------------------------------------------------

// BenchmarkRecvStateToEvent measures the switch-based mapping from received
// BFD State field to FSM Event (RFC 5880 Section 6.8.6).
func BenchmarkRecvStateToEvent(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = bfd.RecvStateToEvent(bfd.StateUp)
	}
}
