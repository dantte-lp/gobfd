package bfd

import (
	"context"
	"log/slog"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type echoReceiveDelayMetrics struct{ noopMetrics }

func (echoReceiveDelayMetrics) IncPacketsReceived(_, _ netip.Addr) {
	synctest.Sleep(15 * time.Millisecond)
}

func TestEchoDownWitnessSurvivesPublicationDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		changes := make(chan StateChange, 4)
		es, err := NewEchoSession(echoLeaseConfig("192.0.2.61"), 42, echoLeaseSender{}, changes,
			slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("StateChangeBytes=%d EchoSessionBytes=%d",
			reflect.TypeFor[StateChange]().Size(), reflect.TypeFor[EchoSession]().Size())
		detect, tx := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
		defer detect.Stop()
		defer tx.Stop()
		es.handleEchoRecv(t.Context(), detect)
		<-changes
		synctest.Sleep(100 * time.Millisecond)
		acceptedAt := time.Now()
		es.metrics = echoReceiveDelayMetrics{}
		es.handleEchoRecv(t.Context(), detect)
		barrier := &lifecycleLogBarrier{
			target: "echo session state changed", entered: make(chan struct{}), release: make(chan struct{}),
		}
		es.logger = slog.New(barrier)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { es.echoRunLoop(ctx, tx, detect); close(done) }()
		var release sync.Once
		unblock := func() { release.Do(func() { close(barrier.release) }) }
		defer func() { unblock(); cancel(); <-done }()
		synctest.Sleep(es.DetectionTime())
		<-barrier.entered
		if es.State() != StateDown || es.LocalDiag() != DiagEchoFailed {
			t.Fatal("Echo expiry did not commit before blocked publication")
		}
		synctest.Sleep(time.Second)
		unblock()
		change := <-changes
		witness := change.TransitionTiming
		want := acceptedAt.Add(15*time.Millisecond + es.DetectionTime())
		if witness.Version == 0 || witness.Version != change.ObservationVersion ||
			witness.ValidatedAt != acceptedAt || witness.DetectionDue != want ||
			witness.DetectionTime != es.DetectionTime() || witness.CommitStartedAt != want || witness.CommitEndedAt != want {
			t.Fatalf("Echo Down lost its owner timeline: %+v", witness)
		}
		if change.Timestamp.Sub(witness.CommitEndedAt) != time.Second ||
			change.Type != SessionTypeEcho || change.Cause != CauseDetectionTimeout ||
			change.RemoteKnown || change.Status.RemoteDiscr != 0 || witness.ValidatedRemoteDiscriminator != 0 {
			t.Fatalf("publication delay or fictional remote replaced Echo evidence: %+v", change)
		}
	})
}

func TestEchoDownWitnessNativeMonotonic(t *testing.T) {
	t.Parallel()
	changes := make(chan StateChange, 4)
	cfg := echoLeaseConfig("192.0.2.62")
	cfg.TxInterval = time.Millisecond
	es, err := NewEchoSession(cfg, 43, echoLeaseSender{}, changes, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	done := make(chan struct{})
	go func() { es.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	es.RecvEcho()
	for {
		select {
		case change := <-changes:
			if change.NewState != StateDown {
				continue
			}
			witness := change.TransitionTiming
			if witness.Version == 0 || witness.Version != change.ObservationVersion ||
				!hasReceiveMonotonic(witness.ValidatedAt) || !hasReceiveMonotonic(witness.DetectionDue) ||
				!hasReceiveMonotonic(witness.CommitStartedAt) || !hasReceiveMonotonic(witness.CommitEndedAt) ||
				witness.CommitEndedAt.Before(witness.CommitStartedAt) || witness.CommitStartedAt.Before(witness.DetectionDue) ||
				witness.DetectionDue.Before(witness.ValidatedAt) || witness.DetectionTime != 3*time.Millisecond {
				t.Fatalf("native Echo expiry lacks ordered monotonic evidence: %+v", witness)
			}
			return
		case <-ctx.Done():
			t.Fatal("real Echo owner did not publish expiry")
		}
	}
}

func TestEchoDownWitnessFrozenWithoutInventedDeadline(t *testing.T) {
	t.Parallel()
	step := newEchoOwnerDeliveryStep(t, slog.DiscardHandler)
	_, first, _ := step()
	frozen := first.TransitionTiming
	_, second, _ := step()
	if frozen.Version == 0 || first.TransitionTiming != frozen || second.TransitionTiming.Version <= frozen.Version ||
		frozen.Version != first.ObservationVersion || frozen.ValidatedAt.IsZero() ||
		frozen.CommitEndedAt.Before(frozen.CommitStartedAt) {
		t.Fatalf("Echo witness did not retain its own commit version: first=%+v second=%+v", frozen, second.TransitionTiming)
	}
	if !frozen.DetectionDue.IsZero() || frozen.ValidatedRemoteDiscriminator != 0 {
		t.Fatalf("direct handler invocation invented a timer deadline or remote peer: %+v", frozen)
	}
	up, _, _ := step()
	if up.TransitionTiming != (TransitionTiming{}) {
		t.Fatalf("old Down witness was attached to a later Up: %+v", up.TransitionTiming)
	}
}
