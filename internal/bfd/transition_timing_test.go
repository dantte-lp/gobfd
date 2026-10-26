package bfd

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestDownTransitionWitnessSurvivesPublicationDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newRemoteTxTestSession(t)
		packet := s.buildControlPacket()
		packet.State = StateUp
		s.handleRecvPacket(t.Context(), recvItem{pkt: packet}, tx, detect)
		validatedAt := time.Now()
		detection := s.calcDetectionTimeHot()
		barrier := &lifecycleLogBarrier{
			target: "session state changed", entered: make(chan struct{}), release: make(chan struct{}),
		}
		changes := make(chan StateChange, 4)
		s.logger, s.notifyCh = slog.New(barrier), changes
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.runLoop(ctx, tx, detect); close(done) }()
		var release sync.Once
		unblock := func() { release.Do(func() { close(barrier.release) }) }
		defer func() { unblock(); cancel(); <-done }()
		synctest.Sleep(detection)
		<-barrier.entered
		if s.State() != StateDown || s.Observation().Cause != CauseDetectionTimeout {
			t.Fatal("owner did not commit timeout before blocked logging")
		}
		synctest.Sleep(time.Second)
		unblock()
		change := <-changes
		t.Logf("StateChangeBytes=%d SessionBytes=%d",
			reflect.TypeFor[StateChange]().Size(), reflect.TypeFor[Session]().Size())
		witness := change.TransitionTiming
		wantCommit := validatedAt.Add(detection)
		if change.OldState != StateUp || change.NewState != StateDown || witness.Version != change.ObservationVersion ||
			witness.ValidatedAt != validatedAt || witness.CommitStartedAt != wantCommit ||
			witness.CommitEndedAt != wantCommit || witness.DetectionDue != wantCommit || witness.DetectionTime != detection {
			t.Fatalf("Down witness lost the actual owner timeline: witness=%+v observation=%+v", witness, change.Status)
		}
		if change.Timestamp.Sub(witness.CommitEndedAt) != time.Second {
			t.Fatalf("publication delay replaced the commit witness: %+v", witness)
		}
	})
}

func TestDownTransitionWitnessRetainsNativeMonotonicValues(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	s.desiredMinTxInterval, s.requiredMinRxInterval = time.Millisecond, time.Millisecond
	packet := s.buildControlPacket()
	packet.State = StateUp
	s.handleRecvPacket(t.Context(), recvItem{pkt: packet}, tx, detect)
	changes := make(chan StateChange, 2)
	s.notifyCh = changes
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	done := make(chan struct{})
	go func() { s.runLoop(ctx, tx, detect); close(done) }()
	defer func() { cancel(); <-done }()
	var change StateChange
	select {
	case change = <-changes:
	case <-ctx.Done():
		t.Fatal("native owner timer did not publish Down")
	}
	witness := change.TransitionTiming
	if witness.Version != change.ObservationVersion || !hasReceiveMonotonic(witness.ValidatedAt) ||
		!hasReceiveMonotonic(witness.CommitStartedAt) || !hasReceiveMonotonic(witness.CommitEndedAt) ||
		!hasReceiveMonotonic(witness.DetectionDue) || witness.CommitEndedAt.Before(witness.CommitStartedAt) ||
		witness.CommitStartedAt.Before(witness.ValidatedAt) {
		t.Fatalf("native owner witness has no ordered monotonic values: %+v", witness)
	}
}

func newDownWitnessStep(tb testing.TB, handler slog.Handler) func() StateChange {
	tb.Helper()
	s := newCachedPacketTestSession(MaxPacketSize)
	s.sender, s.logger = senderLeaseTestSender{}, slog.New(handler)
	changes := make(chan StateChange, 2)
	s.notifyCh = changes
	tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	tb.Cleanup(func() { tx.Stop(); detect.Stop() })
	packet := s.buildControlPacket()
	packet.State, packet.MyDiscriminator, packet.YourDiscriminator = StateInit, 7, s.localDiscr
	ctx := tb.Context()
	return func() StateChange {
		s.handleRecvPacket(ctx, recvItem{pkt: packet}, tx, detect)
		<-changes
		s.timerEvent.begin(s.metrics, TimerControlDetection, s.lastValidRecv.Add(s.calcDetectionTimeHot()))
		s.handleDetectTimer(ctx, tx, detect)
		s.timerEvent.finish(s.metrics)
		return <-changes
	}
}

func TestDownTransitionWitnessIsFrozenAcrossLaterCommits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		step := newDownWitnessStep(t, slog.DiscardHandler)
		first := step()
		frozen := first.TransitionTiming
		synctest.Sleep(time.Millisecond)
		second := step()
		if frozen.Version == 0 || first.TransitionTiming != frozen || second.TransitionTiming.Version <= frozen.Version ||
			second.TransitionTiming.CommitStartedAt.Sub(frozen.CommitStartedAt) != time.Millisecond {
			t.Fatalf("later commit changed the retained witness: first=%+v second=%+v", frozen, second.TransitionTiming)
		}
	})
}

func TestDownTransitionWitnessDoesNotDescribeAnotherVersion(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	changes := make(chan StateChange, 2)
	s.notifyCh = changes
	s.applyFSMEvent(t.Context(), EventTimerExpired, tx, detect)
	first := <-changes
	if first.TransitionTiming.Version == 0 {
		t.Fatal("real Down did not produce its witness")
	}
	observation, _ := s.commitObservation(StateDown, DiagNeighborDown, CauseRemoteDown)
	s.emitNotification(FSMResult{OldState: StateUp, NewState: StateDown}, observation)
	next := <-changes
	if next.TransitionTiming != (TransitionTiming{}) {
		t.Fatalf("stale witness assigned to another observation version: %+v", next.TransitionTiming)
	}
}

func TestDownTransitionWitnessKeepsExpiredDetectionTimeDuringPoll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newRemoteTxTestSession(t)
		s.requiredMinRxInterval = time.Second
		packet := s.buildControlPacket()
		packet.State, packet.MyDiscriminator, packet.YourDiscriminator = StateUp, 7, s.localDiscr
		s.handleRecvPacket(t.Context(), recvItem{pkt: packet}, tx, detect)
		validatedAt := time.Now()
		proposal := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 3, 0, 0}
		if _, err := s.admitTimerUpdate(t.Context(), proposal, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		if !s.activeUpdate.Present || s.requiredMinRxInterval != time.Second || s.calcDetectionTimeHot() != 3*time.Second {
			t.Fatal("RX decrease did not retain the old Detection Time while Up")
		}
		changes := make(chan StateChange, 2)
		s.notifyCh = changes
		tx.Stop()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.runLoop(ctx, tx, detect); close(done) }()
		defer func() { cancel(); <-done }()
		synctest.Sleep(3 * time.Second)
		change := <-changes
		if change.Cause != CauseDetectionTimeout || change.TransitionTiming.DetectionTime != 3*time.Second ||
			change.TransitionTiming.DetectionDue.Sub(validatedAt) != 3*time.Second ||
			change.Status.DetectionTime != 300*time.Millisecond ||
			change.TransitionTiming.Version != change.ObservationVersion {
			t.Fatalf("Poll interruption replaced the expired Detection Time: timing=%+v status=%+v",
				change.TransitionTiming, change.Status)
		}
	})
}

func BenchmarkDownTransitionWitness(b *testing.B) {
	for _, tc := range []struct {
		name    string
		handler slog.Handler
	}{
		{name: "disabled", handler: slog.DiscardHandler},
		//nolint:sloglint // Enabled JSON allocation sampling must retain the handler's normal encoding path.
		{name: "enabled_JSON", handler: slog.NewJSONHandler(io.Discard, nil)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			step := newDownWitnessStep(b, tc.handler)
			if allocs := testing.AllocsPerRun(100, func() { step() }); allocs != 0 {
				b.Fatalf("Down receive/commit/publication = %g allocs/op, want 0", allocs)
			}
			b.ReportAllocs()
			for b.Loop() {
				step()
			}
		})
	}
}
