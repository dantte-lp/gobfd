// Package testutil provides real owner fixtures shared by consumer integration tests.
package testutil

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type rawLossSender struct{}

func (rawLossSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

const (
	rawQueueCapacity    = 64
	initialRawEvents    = 1
	retirementRawEvents = 1
	noiseAlternation    = 2
)

// RawLossSession starts a real Control owner Up with its original Up still queued.
func RawLossSession(ctx context.Context, tb testing.TB) (*bfd.Manager, *bfd.Session) {
	tb.Helper()
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	tb.Cleanup(mgr.Close)
	session, err := mgr.CreateSession(ctx, rawLossConfig("192.0.2.1"),
		bfd.NonOwningSenderLeaseFactory(rawLossSender{}))
	if err != nil {
		tb.Fatal(err)
	}
	pkt := bfd.ControlPacket{
		Version: 1, State: bfd.StateInit, DetectMult: 3, Length: bfd.MinPacketSizeNoAuth,
		MyDiscriminator: 99, YourDiscriminator: session.LocalDiscriminator(),
		DesiredMinTxInterval: 3600000000, RequiredMinRxInterval: 3600000000,
	}
	if session.RecvPacket(&pkt) != bfd.DeliveryQueued {
		tb.Fatal("initial owner receive refused")
	}
	Wait(ctx, tb, func() bool {
		return DeliveryCount(mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueued) == initialRawEvents
	})
	if session.State() != bfd.StateUp {
		tb.Fatal("initial owner did not commit Up")
	}
	return mgr, session
}

// LoseFinalDown fills raw delivery with a retired owner's events, then loses the target's final Down.
// The caller starts RunDispatch only after its consumer has observed the initial Up.
func LoseFinalDown(ctx context.Context, tb testing.TB, mgr *bfd.Manager, target *bfd.Session) {
	tb.Helper()
	cfg := rawLossConfig("192.0.2.253")
	cfg.DesiredMinTxInterval, cfg.RequiredMinRxInterval = time.Second, time.Second
	noise, err := mgr.CreateSession(ctx, cfg, bfd.NonOwningSenderLeaseFactory(rawLossSender{}))
	if err != nil {
		tb.Fatal(err)
	}
	pkt := bfd.ControlPacket{
		Version: 1, State: bfd.StateAdminDown, DetectMult: 3, Length: bfd.MinPacketSizeNoAuth,
		MyDiscriminator: 100, YourDiscriminator: noise.LocalDiscriminator(),
		DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 1000000,
	}
	for i := range uint64(rawQueueCapacity - initialRawEvents - retirementRawEvents) {
		pkt.ControlPlaneIndependent = i%noiseAlternation == 0
		if noise.RecvPacket(&pkt) != bfd.DeliveryQueued {
			tb.Fatal("noise receive refused")
		}
		Wait(ctx, tb, func() bool {
			return DeliveryCount(mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueued) == i+initialRawEvents+1
		})
	}
	// Retirement publishes the final lawful AdminDown into the reserved raw slot.
	if err := mgr.DestroySession(ctx, noise.LocalDiscriminator()); err != nil {
		tb.Fatal(err)
	}
	if DeliveryCount(mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueued) != rawQueueCapacity ||
		DeliveryCount(mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueueFull) != 0 {
		tb.Fatal("raw loss fixture lost an event before the target's final owner commit")
	}
	if target.SetPathDown() != bfd.DeliveryQueued {
		tb.Fatal("target PathDown refused")
	}
	Wait(ctx, tb, func() bool {
		return DeliveryCount(mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueueFull) == 1
	})
	if target.State() != bfd.StateDown || target.LocalDiag() != bfd.DiagPathDown {
		tb.Fatal("target did not commit final Down before raw loss")
	}
}

func rawLossConfig(peer string) bfd.SessionConfig {
	return bfd.SessionConfig{
		PeerAddr: netip.MustParseAddr(peer), LocalAddr: netip.MustParseAddr("192.0.2.2"),
		Interface: "eth0", Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
		DesiredMinTxInterval: time.Hour, RequiredMinRxInterval: time.Hour, DetectMultiplier: 3,
	}
}

// DeliveryCount samples one public fixed ledger bucket.
func DeliveryCount(mgr *bfd.Manager, source bfd.DeliverySource, outcome bfd.DeliveryOutcome) uint64 {
	for _, row := range mgr.DeliveryCounters() {
		if row.Source == source && row.Outcome == outcome {
			return row.Count
		}
	}
	return 0
}

// Wait bounds real owner/RPC progress without assuming a scheduler delay.
func Wait(ctx context.Context, tb testing.TB, ready func() bool) {
	tb.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			tb.Fatal("owner/consumer did not converge: ", ctx.Err())
		case <-ticker.C:
		}
	}
}
