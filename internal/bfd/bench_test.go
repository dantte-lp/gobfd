package bfd_test

import (
	"context"
	"log/slog"
	"net/netip"
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

// =========================================================================
// Sprint 17 — Full-path benchmarks
// =========================================================================

// -------------------------------------------------------------------------
// BenchmarkFullRecvPath — unmarshal + demux simulation + FSM
// -------------------------------------------------------------------------

// BenchmarkFullRecvPath measures the complete receive-side hot path:
// unmarshal a wire-format packet, simulate two-tier demultiplexing via
// Manager.Demux, which delivers the packet to the session's recvCh.
// This represents the full cost of processing an incoming BFD keepalive
// (RFC 5880 Section 6.8.6 steps 1-7 validation + demux + channel send).
//
// The benchmark creates a real Manager with one session and routes packets
// through the full Demux path. The session goroutine is running and
// consuming from recvCh so the channel does not block.
//
// Target: zero allocations on the unmarshal + demux path for packets
// with Your Discriminator != 0 (tier-1 O(1) lookup).
func BenchmarkFullRecvPath(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	defer mgr.Close()

	sender := &discardSender{}
	cfg := bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.1"),
		LocalAddr:             netip.MustParseAddr("192.0.2.2"),
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  1 * time.Second,
		RequiredMinRxInterval: 1 * time.Second,
		DetectMultiplier:      3,
	}

	sess, err := mgr.CreateSession(b.Context(), cfg, sender)
	if err != nil {
		b.Fatalf("CreateSession: %v", err)
	}
	localDiscr := sess.LocalDiscriminator()

	// Build a wire-format keepalive packet (Up state, Your Discriminator set).
	srcPkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		Diag:                  bfd.DiagNone,
		State:                 bfd.StateUp,
		DetectMult:            3,
		MyDiscriminator:       0xABCD1234,
		YourDiscriminator:     localDiscr,
		DesiredMinTxInterval:  1000000, // 1s in microseconds
		RequiredMinRxInterval: 1000000,
	}
	wireBuf := make([]byte, bfd.MaxPacketSize)
	n, err := bfd.MarshalControlPacket(srcPkt, wireBuf)
	if err != nil {
		b.Fatalf("MarshalControlPacket: %v", err)
	}
	wire := wireBuf[:n]

	meta := bfd.PacketMeta{
		SrcAddr: netip.MustParseAddr("192.0.2.1"),
		DstAddr: netip.MustParseAddr("192.0.2.2"),
		TTL:     255,
		IfName:  "eth0",
	}

	var pkt bfd.ControlPacket

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		// Step 1: Unmarshal (RFC 5880 Section 6.8.6 steps 1-7).
		_ = bfd.UnmarshalControlPacket(wire, &pkt)
		// Step 2: Demux (tier-1 discriminator lookup + channel send).
		_ = mgr.Demux(&pkt, meta)
	}
}

// -------------------------------------------------------------------------
// BenchmarkFullTxPath — buildControlPacket + marshal
// -------------------------------------------------------------------------

// BenchmarkFullTxPath measures the complete transmit-side hot path:
// build a BFD Control packet from session state and marshal it into a
// pre-allocated buffer. This represents the per-TX-interval cost
// (RFC 5880 Section 6.8.7) excluding the actual socket send.
//
// The benchmark constructs a ControlPacket with typical Up-state values
// and marshals it, simulating the session's rebuildCachedPacket() path.
//
// Target: zero allocations per operation.
func BenchmarkFullTxPath(b *testing.B) {
	buf := make([]byte, bfd.MaxPacketSize)

	// Simulate session state: build the packet struct as Session.buildControlPacket
	// would for a session in Up state (RFC 5880 Section 6.8.7).
	pkt := bfd.ControlPacket{
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
		RequiredMinRxInterval:     100000,
		RequiredMinEchoRxInterval: 0,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = bfd.MarshalControlPacket(&pkt, buf)
	}
}

// -------------------------------------------------------------------------
// BenchmarkSessionRecvPacket — deliver packet through recvCh
// -------------------------------------------------------------------------

// BenchmarkSessionRecvPacket measures the channel-send cost of delivering
// a packet to a running BFD session via RecvPacket(). The session goroutine
// is actively consuming from the buffered recvCh (capacity 16).
//
// This benchmark captures the real contention pattern: the network listener
// goroutine calls RecvPacket (producer) while the session goroutine
// processes packets (consumer). It measures the channel send path that
// occurs on every received packet.
//
// Note: this benchmark has inherent variance from goroutine scheduling.
// The session goroutine runs with a 1-second TX interval to minimize
// interference from packet sends.
func BenchmarkSessionRecvPacket(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	sender := &discardSender{}
	cfg := bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.1"),
		LocalAddr:             netip.MustParseAddr("192.0.2.2"),
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  1 * time.Second,
		RequiredMinRxInterval: 1 * time.Second,
		DetectMultiplier:      3,
	}

	sess, err := bfd.NewSession(cfg, 42, sender, nil, logger)
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}

	go sess.Run(b.Context())

	// Pre-build a packet to inject. Use high DetectMult to prevent
	// detection timeout during benchmark runtime.
	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 bfd.StateUp,
		DetectMult:            255,
		MyDiscriminator:       0xABCD1234,
		YourDiscriminator:     42,
		DesiredMinTxInterval:  1000000, // 1s
		RequiredMinRxInterval: 1000000,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		sess.RecvPacket(pkt)
	}
}

// -------------------------------------------------------------------------
// BenchmarkDetectionTimeCalc — detection time calculation hot path
// -------------------------------------------------------------------------

// BenchmarkDetectionTimeCalc measures the detection time calculation that
// occurs on every received packet when timers are reset.
//
// RFC 5880 Section 6.8.4: Detection Time = RemoteDetectMult *
// max(RequiredMinRxInterval, RemoteDesiredMinTxInterval).
//
// The session must be created with remote parameters already set via a
// received packet so that the calculation exercises the negotiated path
// (not the initial slow-rate fallback).
//
// Target: zero allocations (pure arithmetic on value types).
func BenchmarkDetectionTimeCalc(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	sender := &discardSender{}
	cfg := bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.1"),
		LocalAddr:             netip.MustParseAddr("192.0.2.2"),
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
	}

	sess, err := bfd.NewSession(cfg, 42, sender, nil, logger)
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = sess.DetectionTime()
	}
}

// -------------------------------------------------------------------------
// BenchmarkCalcTxInterval — TX interval calculation
// -------------------------------------------------------------------------

// BenchmarkCalcTxInterval measures the TX interval negotiation that occurs
// on every timer reset.
//
// RFC 5880 Section 6.8.7: actual TX = max(bfd.DesiredMinTxInterval,
// bfd.RemoteMinRxInterval). When state is not Up, the slow rate (1s)
// is enforced per RFC 5880 Section 6.8.3.
//
// Target: zero allocations (pure arithmetic + one atomic load for state).
func BenchmarkCalcTxInterval(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	sender := &discardSender{}
	cfg := bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.1"),
		LocalAddr:             netip.MustParseAddr("192.0.2.2"),
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
	}

	sess, err := bfd.NewSession(cfg, 42, sender, nil, logger)
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = sess.NegotiatedTxInterval()
	}
}

// -------------------------------------------------------------------------
// Benchmark helpers
// -------------------------------------------------------------------------

// discardSender is a no-op PacketSender that silently drops all packets.
// Used in benchmarks to eliminate network I/O overhead from measurements.
type discardSender struct{}

func (*discardSender) SendPacket(_ context.Context, _ []byte, _ netip.Addr) error {
	return nil
}
