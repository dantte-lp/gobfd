package bfd

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

func TestEchoCanceledReconcilePreservesExistingClaims(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		keep     int
		released int
	}{{"empty", 0, 2}, {"subset", 1, 1}, {"unchanged", 2, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := newEchoLeaseManager()
			t.Cleanup(mgr.Close)
			first, second := new(echoLeaseCounter), new(echoLeaseCounter)
			desired := []EchoReconcileConfig{
				{EchoSessionConfig: echoLeaseConfig("192.0.2.81"), SenderLeaseFactory: first.factory(nil)},
				{EchoSessionConfig: echoLeaseConfig("192.0.2.82"), SenderLeaseFactory: second.factory(nil)},
			}
			if result := mgr.ReconcileEchoSessionsDetailed(t.Context(), desired); result.Err() != nil || result.Created != 2 {
				t.Fatalf("seed Echo claims: %+v", result)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			for range 2 {
				result := mgr.ReconcileEchoSessionsDetailed(ctx, desired[:tc.keep])
				if !errors.Is(result.Err(), context.Canceled) || result.Created != 0 || result.Released != 0 ||
					len(mgr.EchoSessions()) != 2 || first.closes.Load() != 0 || second.closes.Load() != 0 {
					t.Fatalf("canceled Echo reconcile changed retained claims: result=%+v sessions=%d closes=%d/%d",
						result, len(mgr.EchoSessions()), first.closes.Load(), second.closes.Load())
				}
			}
			result := mgr.ReconcileEchoSessionsDetailed(t.Context(), desired[:tc.keep])
			if result.Err() != nil || result.Created != 0 || result.Released != tc.released ||
				len(mgr.EchoSessions()) != tc.keep || first.opens.Load() != 1 || second.opens.Load() != 1 {
				t.Fatalf("live retry did not reuse retained wires: %+v", result)
			}
		})
	}
}

func TestEchoCancellationBeforeStaleReleasePreservesPublishedClaims(t *testing.T) {
	t.Parallel()
	mgr := newEchoLeaseManager()
	t.Cleanup(mgr.Close)
	old, added := new(echoLeaseCounter), new(echoLeaseCounter)
	if result := mgr.ReconcileEchoSessionsDetailed(t.Context(), []EchoReconcileConfig{{
		EchoSessionConfig: echoLeaseConfig("192.0.2.83"), SenderLeaseFactory: old.factory(nil),
	}}); result.Err() != nil || result.Created != 1 {
		t.Fatalf("seed Echo claim: %+v", result)
	}
	barrier := &lifecycleLogBarrier{
		target: "echo session created", entered: make(chan struct{}), release: make(chan struct{}),
	}
	mgr.logger = slog.New(barrier)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	desired := []EchoReconcileConfig{{
		EchoSessionConfig: echoLeaseConfig("192.0.2.84"), SenderLeaseFactory: added.factory(nil),
	}}
	done := make(chan ReconcileResult, 1)
	go func() { done <- mgr.ReconcileEchoSessionsDetailed(ctx, desired) }()
	<-barrier.entered
	cancel()
	close(barrier.release)
	result := <-done
	if !errors.Is(result.Err(), context.Canceled) || result.Created != 1 || result.Released != 0 ||
		len(mgr.EchoSessions()) != 2 || old.closes.Load() != 0 || added.closes.Load() != 0 {
		t.Fatalf("late cancellation removed old or published claims: result=%+v sessions=%d closes=%d/%d",
			result, len(mgr.EchoSessions()), old.closes.Load(), added.closes.Load())
	}
	result = mgr.ReconcileEchoSessionsDetailed(t.Context(), desired)
	if result.Err() != nil || result.Created != 0 || result.Released != 1 || len(mgr.EchoSessions()) != 1 ||
		old.closes.Load() != 1 || added.opens.Load() != 1 {
		t.Fatalf("live retry did not converge exactly once: %+v", result)
	}
}
