package main

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestInterfaceEventCoalescerBoundsAndGeneration(t *testing.T) {
	t.Parallel()
	queue := newInterfaceEventCoalescer()
	for range 10000 {
		queue.add(3, sourceMask(sourceBase))
		queue.add(3, sourceMask(sourceEcho))
	}
	generation, selected := queue.take()
	if generation != 3 || selected != sourceMask(sourceBase)|sourceMask(sourceEcho) {
		t.Fatalf("coalesced batch = %d/%b, want 3/base+echo", generation, selected)
	}
	queue.add(4, sourceMask(sourceMicro))
	queue.add(3, sourceMask(sourceBase))
	generation, selected = queue.take()
	if generation != 4 || selected != sourceMask(sourceMicro) {
		t.Fatalf("stale event replaced current batch: %d/%b", generation, selected)
	}
	_, selected = queue.take()
	if selected != 0 {
		t.Fatalf("consumed source replayed: %b", selected)
	}
}

func TestInterfaceEventWorkerConvergesAndDoesNotReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		logger := slog.New(slog.DiscardHandler)
		mgr := bfd.NewManager(logger)
		defer mgr.Close()
		checker := newDaemonHealthChecker()
		coordinator := newReconciliationCoordinator(config.DefaultConfig(), logger, checker)
		candidate := compiledControlSessionCandidate{
			base: []baseSessionCandidate{{config: bfd.SessionConfig{Interface: "base0"}}},
			echo: []echoSessionCandidate{{config: bfd.EchoSessionConfig{Interface: "echo0"}}},
		}
		initial := coordinator.applyCandidate(ctx, candidate,
			func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
				return sourceApplyResult{}
			})
		queue := newInterfaceEventCoalescer()
		events := make(chan netio.InterfaceEvent)
		consumerDone := make(chan struct{})
		go func() {
			defer close(consumerDone)
			consumeInterfaceEvents(ctx, events, mgr, coordinator, queue, logger)
		}()
		available := false
		var applied []reconciliationSource
		done := make(chan struct{})
		go func() {
			defer close(done)
			coordinator.runInterfaceReconciliation(ctx, queue,
				func(dependencies []string) (int, error) {
					if !slices.Equal(dependencies, []string{"base0"}) {
						t.Errorf("unrelated source preflight: %v", dependencies)
					}
					if !available {
						return 1, testUnavailableResourceError(t, "base0")
					}
					return 0, nil
				},
				func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
					applied = append(applied, source)
					return sourceApplyResult{Created: 1}
				})
		}()
		events <- netio.InterfaceEvent{IfName: "base0"}
		synctest.Wait()
		pending := coordinator.Snapshot()
		if pending.Pending != 1 || !pending.Stale || len(applied) != 0 {
			t.Fatalf("down event = %+v, applied=%v", pending, applied)
		}
		available = true
		events <- netio.InterfaceEvent{IfName: "base0", Up: true}
		synctest.Wait()
		converged := coordinator.Snapshot()
		if converged.Stale || converged.Pending != 0 || converged.DesiredGeneration != initial.DesiredGeneration ||
			!slices.Equal(applied, []reconciliationSource{sourceBase}) {
			t.Fatalf("up event = %+v, applied=%v", converged, applied)
		}
		events <- netio.InterfaceEvent{IfName: "base0", Up: true}
		synctest.Wait()
		if coordinator.Snapshot() != converged || len(applied) != 1 {
			t.Fatal("duplicate event replayed a converged source")
		}
		cancel()
		<-consumerDone
		<-done
	})
}

func TestInterfaceEventBatchRejectsStaleGeneration(t *testing.T) {
	t.Parallel()
	coordinator := newReconciliationCoordinator(config.DefaultConfig(), slog.New(slog.DiscardHandler), nil)
	candidate := compiledControlSessionCandidate{
		base: []baseSessionCandidate{{config: bfd.SessionConfig{Interface: "base0"}}},
	}
	apply := func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
		return sourceApplyResult{}
	}
	before := coordinator.applyCandidate(context.Background(), candidate, apply)
	after := coordinator.applyCandidate(context.Background(), candidate, apply)
	coordinator.reconcileInterfaceSources(context.Background(), before.DesiredGeneration, sourceMask(sourceBase),
		func([]string) (int, error) { t.Fatal("stale event performed preflight"); return 0, nil },
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("stale event mutated a new candidate")
			return sourceApplyResult{}
		})
	if coordinator.Snapshot() != after {
		t.Fatal("stale event changed current receipt")
	}
	generation, selected := coordinator.interfaceEventSelection(netio.InterfaceEvent{Resync: true})
	if generation != after.DesiredGeneration || selected != sourceMask(sourceBase) {
		t.Fatalf("resync selection = %d/%b, want current generation/base", generation, selected)
	}
	_, selected = coordinator.interfaceEventSelection(netio.InterfaceEvent{IfName: "other0"})
	if selected != 0 {
		t.Fatalf("irrelevant event selected sources: %b", selected)
	}
}

func TestInterfaceEventConsumerClosesWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		logger := slog.New(slog.DiscardHandler)
		mgr := bfd.NewManager(logger)
		defer mgr.Close()
		coordinator := newReconciliationCoordinator(config.DefaultConfig(), logger, nil)
		queue := newInterfaceEventCoalescer()
		events := make(chan netio.InterfaceEvent)
		done := make(chan struct{})
		go func() {
			defer close(done)
			consumeInterfaceEvents(ctx, events, mgr, coordinator, queue, logger)
		}()
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			coordinator.runInterfaceReconciliation(ctx, queue, netio.CheckInterfacesAvailable,
				func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
					t.Error("empty candidate must not be applied")
					return sourceApplyResult{}
				})
		}()
		events <- netio.InterfaceEvent{IfName: "irrelevant0", Up: true}
		close(events)
		<-done
		<-workerDone
	})
}

func TestInterfaceEventConsumerPathDownDoesNotWaitForApply(t *testing.T) {
	for _, test := range []struct {
		name  string
		event netio.InterfaceEvent
	}{
		{name: "named down", event: netio.InterfaceEvent{IfName: "absent0"}},
		{name: "lost notification resync", event: netio.InterfaceEvent{Resync: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			testInterfaceEventPathDown(t, test.event)
		})
	}
}

func testInterfaceEventPathDown(t *testing.T, event netio.InterfaceEvent) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		logger := slog.New(slog.DiscardHandler)
		mgr := bfd.NewManager(logger)
		defer mgr.Close()
		session, err := mgr.CreateSession(ctx, bfd.SessionConfig{
			PeerAddr: netip.MustParseAddr("192.0.2.1"), LocalAddr: netip.MustParseAddr("192.0.2.2"),
			Interface: "absent0", Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
			DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
		}, candidateTestSenderLeaseFactory())
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		session.RecvPacket(&bfd.ControlPacket{
			Version: bfd.Version, State: bfd.StateInit, DetectMult: 3,
			MyDiscriminator: 99, YourDiscriminator: session.LocalDiscriminator(),
			DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 1000000,
		})
		synctest.Wait()
		if session.State() != bfd.StateUp {
			t.Fatalf("initial state = %v, want Up", session.State())
		}
		coordinator := newReconciliationCoordinator(config.DefaultConfig(), logger, nil)
		coordinator.applyMu.Lock()
		queue := newInterfaceEventCoalescer()
		events := make(chan netio.InterfaceEvent)
		done := make(chan struct{})
		go func() {
			defer close(done)
			consumeInterfaceEvents(ctx, events, mgr, coordinator, queue, logger)
		}()
		events <- event
		synctest.Wait()
		if session.State() != bfd.StateDown || session.LocalDiag() != bfd.DiagPathDown {
			t.Errorf("link down blocked behind apply: %v/%v", session.State(), session.LocalDiag())
		}
		coordinator.applyMu.Unlock()
		close(events)
		<-done
	})
}
