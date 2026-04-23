package bfd_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// =========================================================================
// Sprint 8 — Session Scaling Benchmarks
// =========================================================================

// -------------------------------------------------------------------------
// BenchmarkManagerCreate100Sessions — create + destroy 100 sessions
// -------------------------------------------------------------------------

// BenchmarkManagerCreate100Sessions measures the throughput of creating
// and destroying 100 BFD sessions via the Manager. This benchmarks the
// session allocation, discriminator management, and map operations that
// occur during initial configuration reconciliation.
func BenchmarkManagerCreate100Sessions(b *testing.B) {
	benchmarkManagerCreateDestroy(b, 100)
}

// -------------------------------------------------------------------------
// BenchmarkManagerCreate1000Sessions — create + destroy 1000 sessions
// -------------------------------------------------------------------------

// BenchmarkManagerCreate1000Sessions measures the throughput of creating
// and destroying 1000 BFD sessions. This is the "headline claim" benchmark
// for session scaling capacity.
func BenchmarkManagerCreate1000Sessions(b *testing.B) {
	benchmarkManagerCreateDestroy(b, 1000)
}

func benchmarkManagerCreateDestroy(b *testing.B, count int) {
	b.Helper()

	logger := slog.New(slog.DiscardHandler)
	sender := &discardSender{}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		mgr := bfd.NewManager(logger)
		ctx := context.Background()

		for i := range count {
			ip := netip.AddrFrom4([4]byte{
				10,
				byte((i >> 16) & 0xFF),
				byte((i >> 8) & 0xFF),
				byte(i & 0xFF),
			})

			cfg := bfd.SessionConfig{
				PeerAddr:              ip,
				LocalAddr:             netip.MustParseAddr("192.0.2.2"),
				Type:                  bfd.SessionTypeSingleHop,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  1 * time.Second,
				RequiredMinRxInterval: 1 * time.Second,
				DetectMultiplier:      3,
			}

			if _, err := mgr.CreateSession(ctx, cfg, sender); err != nil {
				b.Fatalf("CreateSession %d: %v", i, err)
			}
		}

		mgr.Close()
	}
}

// -------------------------------------------------------------------------
// BenchmarkManagerDemux1000Sessions — demux by discriminator with 1000 active sessions
// -------------------------------------------------------------------------

// BenchmarkManagerDemux1000Sessions measures the per-packet demux cost
// when 1000 sessions are active. The demux uses tier-1 discriminator
// lookup (map access), which should be O(1) regardless of session count.
//
// Verification: ns/op should be approximately equal to BenchmarkFullRecvPath
// (which uses 1 session), confirming O(1) demux.
func BenchmarkManagerDemux1000Sessions(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	sender := &discardSender{}
	mgr := bfd.NewManager(logger)
	defer mgr.Close()

	ctx := context.Background()
	const sessionCount = 1000

	// Create 1000 sessions and record their discriminators.
	discrs := make([]uint32, 0, sessionCount)
	for i := range sessionCount {
		ip := netip.AddrFrom4([4]byte{
			10,
			byte((i >> 16) & 0xFF),
			byte((i >> 8) & 0xFF),
			byte(i & 0xFF),
		})

		cfg := bfd.SessionConfig{
			PeerAddr:              ip,
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  1 * time.Second,
			RequiredMinRxInterval: 1 * time.Second,
			DetectMultiplier:      3,
		}

		sess, err := mgr.CreateSession(ctx, cfg, sender)
		if err != nil {
			b.Fatalf("CreateSession %d: %v", i, err)
		}
		discrs = append(discrs, sess.LocalDiscriminator())
	}

	// Target the last session for demux (worst case if linear scan).
	targetDiscr := discrs[sessionCount-1]
	targetPeer := netip.AddrFrom4([4]byte{
		10, 0,
		byte(((sessionCount - 1) >> 8) & 0xFF),
		byte((sessionCount - 1) & 0xFF),
	})

	// Build a wire-format packet targeting the last session.
	pkt := &bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 bfd.StateUp,
		DetectMult:            3,
		MyDiscriminator:       0xABCD0000,
		YourDiscriminator:     targetDiscr,
		DesiredMinTxInterval:  1000000, // 1s in microseconds
		RequiredMinRxInterval: 1000000,
	}

	meta := bfd.PacketMeta{
		SrcAddr: targetPeer,
		DstAddr: netip.MustParseAddr("192.0.2.2"),
		TTL:     255,
		IfName:  "eth0",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = mgr.Demux(pkt, meta)
	}
}

// -------------------------------------------------------------------------
// BenchmarkManagerLookup1000Sessions — pure map lookup without channel send
// -------------------------------------------------------------------------

// BenchmarkManagerLookup1000Sessions measures the pure discriminator
// lookup cost (RWMutex + map) with 1000 active sessions, WITHOUT the
// channel send that Demux includes. This is the FAIR comparison with
// C benchmarks, which measure only hashmap lookup without IPC.
//
// Compare with BenchmarkManagerDemux1000Sessions to isolate the channel
// send overhead: demux_ns - lookup_ns = channel_send_ns.
func BenchmarkManagerLookup1000Sessions(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	sender := &discardSender{}
	mgr := bfd.NewManager(logger)
	defer mgr.Close()

	ctx := context.Background()
	const sessionCount = 1000

	// Create 1000 sessions and record their discriminators.
	discrs := make([]uint32, 0, sessionCount)
	for i := range sessionCount {
		ip := netip.AddrFrom4([4]byte{
			10,
			byte((i >> 16) & 0xFF),
			byte((i >> 8) & 0xFF),
			byte(i & 0xFF),
		})

		cfg := bfd.SessionConfig{
			PeerAddr:              ip,
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			Type:                  bfd.SessionTypeSingleHop,
			Role:                  bfd.RoleActive,
			DesiredMinTxInterval:  1 * time.Second,
			RequiredMinRxInterval: 1 * time.Second,
			DetectMultiplier:      3,
		}

		sess, err := mgr.CreateSession(ctx, cfg, sender)
		if err != nil {
			b.Fatalf("CreateSession %d: %v", i, err)
		}
		discrs = append(discrs, sess.LocalDiscriminator())
	}

	// Cycle through all discriminators to avoid branch prediction bias.
	b.ResetTimer()
	b.ReportAllocs()
	idx := 0
	for b.Loop() {
		_, _ = mgr.LookupByDiscriminator(discrs[idx])
		idx++
		if idx >= sessionCount {
			idx = 0
		}
	}
}

// -------------------------------------------------------------------------
// BenchmarkManagerReconcile — reconcile diff on 100-session baseline
// -------------------------------------------------------------------------

// BenchmarkManagerReconcile measures the cost of a reconciliation pass
// that adds 10 new sessions and removes 5 from a 100-session baseline.
// This simulates a SIGHUP config reload scenario.
func BenchmarkManagerReconcile(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	sender := &discardSender{}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		b.StopTimer()

		mgr := bfd.NewManager(logger)
		ctx := context.Background()

		// Build initial 100-session baseline via reconcile.
		baselineConfigs := make([]bfd.ReconcileConfig, 100)
		for i := range 100 {
			ip := netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})
			baselineConfigs[i] = bfd.ReconcileConfig{
				Key: fmt.Sprintf("10.0.0.%d|192.0.2.2|", i+1),
				SessionConfig: bfd.SessionConfig{
					PeerAddr:              ip,
					LocalAddr:             netip.MustParseAddr("192.0.2.2"),
					Type:                  bfd.SessionTypeSingleHop,
					Role:                  bfd.RoleActive,
					DesiredMinTxInterval:  1 * time.Second,
					RequiredMinRxInterval: 1 * time.Second,
					DetectMultiplier:      3,
				},
				Sender: sender,
			}
		}

		if _, _, err := mgr.ReconcileSessions(ctx, baselineConfigs); err != nil {
			b.Fatalf("initial reconcile: %v", err)
		}

		// Build target: keep 95 sessions, remove first 5, add 10 new.
		newConfigs := make([]bfd.ReconcileConfig, 0, 105)

		// Keep sessions 6-100 (remove 1-5).
		newConfigs = append(newConfigs, baselineConfigs[5:]...)

		// Add 10 new sessions (101-110).
		for i := range 10 {
			ip := netip.AddrFrom4([4]byte{10, 0, 1, byte(i + 1)})
			newConfigs = append(newConfigs, bfd.ReconcileConfig{
				Key: fmt.Sprintf("10.0.1.%d|192.0.2.2|", i+1),
				SessionConfig: bfd.SessionConfig{
					PeerAddr:              ip,
					LocalAddr:             netip.MustParseAddr("192.0.2.2"),
					Type:                  bfd.SessionTypeSingleHop,
					Role:                  bfd.RoleActive,
					DesiredMinTxInterval:  1 * time.Second,
					RequiredMinRxInterval: 1 * time.Second,
					DetectMultiplier:      3,
				},
				Sender: sender,
			})
		}

		b.StartTimer()

		// Measure: reconcile with diff (add 10, remove 5).
		_, _, _ = mgr.ReconcileSessions(ctx, newConfigs)

		b.StopTimer()
		mgr.Close()
		b.StartTimer()
	}
}
