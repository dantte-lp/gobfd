//go:build linux

package bfdload

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type journalShutdownLog struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*journalShutdownLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *journalShutdownLog) WithAttrs([]slog.Attr) slog.Handler     { return h }
func (h *journalShutdownLog) WithGroup(string) slog.Handler          { return h }

func (h *journalShutdownLog) Handle(_ context.Context, record slog.Record) error {
	if record.Message != "echo session state changed" {
		return nil
	}
	down := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "new_state" && attr.Value.String() == "Down" {
			down = true
		}
		return true
	})
	if down {
		h.once.Do(func() { close(h.entered) })
		<-h.release
	}
	return nil
}

func TestDownJournalShutdownRetainsCommittedEchoDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	barrier := &journalShutdownLog{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	unblock := func() { release.Do(func() { close(barrier.release) }) }
	owner, err := StartOwner(ctx, OwnerConfig{
		Echo: true, Interval: 10 * time.Millisecond, DetectMultiplier: 3,
		Sessions: []OwnerEndpoint{{
			Local: netip.MustParseAddrPort("127.0.0.1:3785"), Peer: netip.MustParseAddrPort("127.0.0.2:3785"),
		}},
	}, slog.New(barrier))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		unblock()
		if closeErr := owner.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	journalCtx, cancelJournal := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJournal()
	changes, err := owner.manager.SubscribeStateChanges(journalCtx)
	if err != nil {
		t.Fatal(err)
	}
	journal := startDownJournal(journalCtx, changes, owner)
	started := time.Now()
	session := owner.sessions[0].session
	outcome, receiveErr := owner.manager.DemuxEcho(session.LocalDiscriminator())
	if receiveErr != nil || outcome != bfd.DeliveryQueued {
		t.Fatalf("real Echo owner refused return: %v, %v", outcome, receiveErr)
	}
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal("real Echo expiry did not reach the pre-publication barrier")
	}
	committed := session.Observation()
	if committed.State != bfd.StateDown || committed.LocalDiag != bfd.DiagEchoFailed {
		t.Fatalf("fixture did not commit Echo Down: %+v", committed)
	}
	report := OwnerWorkloadObservation{Config: PeerConfig{Profile: Profile{Duration: time.Hour}}}
	captureErr := report.captureDownJournal(owner, started)
	if captureErr != nil || report.DownJournal.SourceTransitions != 1 {
		t.Fatalf("fixture did not increment the real source counter: %+v, %v", report.DownJournal, captureErr)
	}
	closed := make(chan error, 1)
	go func() { closed <- owner.Close() }()
	owner.workers.Wait()
	unblock()
	select {
	case closeErr := <-closed:
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	case <-ctx.Done():
		t.Fatal("Owner.Close did not join the released producer")
	}
	if joinErr := report.joinDownJournal(ctx, started, journal); joinErr != nil {
		t.Fatal(joinErr)
	}
	report.Final, err = owner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settleErr := report.finalizeDownJournal(); settleErr != nil || len(report.DownWitnesses) != 1 {
		t.Fatalf("committed Echo Down lost across shutdown: journal=%+v witnesses=%d: %v",
			report.DownJournal, len(report.DownWitnesses), settleErr)
	}
	witness := report.DownWitnesses[0]
	if witness.LocalDiscriminator != session.LocalDiscriminator() ||
		witness.ObservationVersion != committed.ObservationVersion ||
		witness.Type != bfd.SessionTypeEcho || witness.Cause != bfd.CauseDetectionTimeout ||
		witness.LocalDiag != bfd.DiagEchoFailed ||
		!witness.Incarnation.Known || witness.Incarnation.Slot != 0 || witness.Incarnation.Generation != 1 ||
		!witness.Timing.CommitKnown || !witness.Timing.DetectionKnown || witness.Timing.Error != "" {
		t.Fatalf("shutdown rewrote or invented the real source witness: %+v", witness)
	}
}
