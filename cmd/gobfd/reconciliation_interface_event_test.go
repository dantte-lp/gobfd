package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestCompiledCandidateInterfaceSourceMask(t *testing.T) {
	t.Parallel()

	candidate := compiledControlSessionCandidate{
		base: []baseSessionCandidate{
			{config: bfd.SessionConfig{Interface: "shared0"}},
			{config: bfd.SessionConfig{Interface: "base0"}},
		},
		echo: []echoSessionCandidate{
			{config: bfd.EchoSessionConfig{Interface: "shared0"}},
			{config: bfd.EchoSessionConfig{Interface: "echo0"}},
		},
		microGroups: []bfd.MicroBFDReconcileConfig{
			{Config: bfd.MicroBFDConfig{LAGInterface: "shared0"}},
			{Config: bfd.MicroBFDConfig{LAGInterface: "bond0"}},
		},
		microMembers: []microBFDMemberCandidate{
			{member: "shared0"},
			{member: "member0"},
		},
		overlays: [2]compiledOverlayCandidate{
			{desired: []bfd.ReconcileConfig{{Key: "shared0"}}},
			{desired: []bfd.ReconcileConfig{{Key: "shared0"}}},
		},
	}
	allInterfaceSources := sourceMask(sourceBase) | sourceMask(sourceEcho) |
		sourceMask(sourceMicroGroup) | sourceMask(sourceMicroMember)
	tests := []struct {
		name   string
		ifName string
		want   reconciliationSourceMask
	}{
		{name: "empty"},
		{name: "irrelevant", ifName: "other0"},
		{name: "same name across sources", ifName: "shared0", want: allInterfaceSources},
		{name: "base exact", ifName: "base0", want: sourceMask(sourceBase)},
		{name: "echo exact", ifName: "echo0", want: sourceMask(sourceEcho)},
		{name: "group exact", ifName: "bond0", want: sourceMask(sourceMicroGroup)},
		{name: "member exact", ifName: "member0", want: sourceMask(sourceMicroMember)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := compiledCandidateInterfaceSourceMask(candidate, test.ifName); got != test.want {
				t.Fatalf("interface %q source mask = %08b, want %08b", test.ifName, got, test.want)
			}
		})
	}
}

func TestReconciliationCoordinatorUnavailableInterfaceClassifiesWithoutApply(t *testing.T) {
	t.Parallel()

	typed0 := testUnavailableResourceError(t, "shared0")
	typed1 := testUnavailableResourceError(t, "other0")
	permanent := errors.New("inventory failed")
	tests := []struct {
		name           string
		initial        sourceApplyResult
		pendingClaims  int
		preflightErr   error
		wantPending    int
		wantFailed     int
		wantChanged    bool
		wantCheckCalls int
	}{
		{
			name: "typed unavailable replaces converged receipt", initial: sourceApplyResult{Created: 3, Released: 2},
			pendingClaims: 2, preflightErr: errors.Join(typed0, typed1),
			wantPending: 2, wantChanged: true, wantCheckCalls: 1,
		},
		{
			name: "permanent becomes failed", initial: sourceApplyResult{Created: 3, Released: 2},
			preflightErr: permanent,
			wantFailed:   1, wantChanged: true, wantCheckCalls: 1,
		},
		{
			name: "mixed becomes failed", initial: sourceApplyResult{Created: 3, Released: 2},
			pendingClaims: 1, preflightErr: errors.Join(typed0, permanent),
			wantFailed: 1, wantChanged: true, wantCheckCalls: 1,
		},
		{
			name: "inconsistent becomes failed", initial: sourceApplyResult{Created: 3, Released: 2},
			pendingClaims: 1,
			wantFailed:    1, wantChanged: true, wantCheckCalls: 1,
		},
		{
			name: "successful stale hint is no-op", initial: sourceApplyResult{Created: 3, Released: 2},
			wantCheckCalls: 1,
		},
		{
			name:          "pending count is refreshed",
			initial:       sourceApplyResult{Created: 3, Released: 2, Pending: 1},
			pendingClaims: 2, preflightErr: errors.Join(typed0, typed1),
			wantPending: 2, wantChanged: true, wantCheckCalls: 1,
		},
		{
			name:          "same pending receipt is no-op",
			initial:       sourceApplyResult{Created: 3, Released: 2, Pending: 2},
			pendingClaims: 2, preflightErr: errors.Join(typed0, typed1),
			wantPending: 2, wantCheckCalls: 1,
		},
		{
			name:       "failed source is no-op",
			initial:    sourceApplyResult{Created: 3, Released: 2, Failed: 1},
			wantFailed: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			logs := new(lockedBuffer)
			coordinator := newReconciliationCoordinator(
				config.DefaultConfig(), slog.New(slog.NewTextHandler(logs, nil)), newDaemonHealthChecker(),
			)
			candidate := compiledControlSessionCandidate{base: []baseSessionCandidate{
				{config: bfd.SessionConfig{Interface: "shared0"}},
				{config: bfd.SessionConfig{Interface: "other0"}},
			}}
			before := coordinator.applyCandidate(
				context.Background(), candidate,
				func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
					if source == sourceBase {
						return test.initial
					}
					return sourceApplyResult{}
				},
			)
			beforeLogs := logs.String()
			checkCalls := 0
			applyCalls := 0
			got := coordinator.reconcileInterfaceEvent(
				context.Background(), before.DesiredGeneration, "shared0", interfaceEventUnavailable,
				func(dependencies []string) (int, error) {
					checkCalls++
					if !slices.Equal(dependencies, []string{"shared0", "other0"}) {
						t.Fatalf("full source dependencies = %v, want [shared0 other0]", dependencies)
					}
					return test.pendingClaims, test.preflightErr
				},
				func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
					applyCalls++
					return sourceApplyResult{}
				},
			)
			if checkCalls != test.wantCheckCalls || applyCalls != 0 {
				t.Fatalf("event checker/apply calls = %d/%d, want %d/0", checkCalls, applyCalls, test.wantCheckCalls)
			}
			if got.DesiredGeneration != before.DesiredGeneration ||
				got.LastReceipt.Generation != before.LastReceipt.Generation {
				t.Fatalf("event changed generation: before=%+v after=%+v", before, got)
			}
			base := receiptForSource(t, got.LastReceipt, sourceBase)
			if base.Created != 3 || base.Released != 2 ||
				base.Pending != test.wantPending || base.Failed != test.wantFailed {
				t.Fatalf("base event receipt = %+v, want created=3 released=2 pending=%d failed=%d",
					base, test.wantPending, test.wantFailed)
			}
			if test.wantChanged && test.wantFailed != 0 &&
				base.Errors.Count(bfd.ReconcileErrorCreate) != 1 {
				t.Fatalf("base create error count = %d, want 1", base.Errors.Count(bfd.ReconcileErrorCreate))
			}
			if test.wantChanged {
				if got == before || logs.String() == beforeLogs {
					t.Fatalf("changed event was not published: before=%+v after=%+v", before, got)
				}
			} else if got != before || logs.String() != beforeLogs {
				t.Fatalf("semantic no-op changed snapshot/log: before=%+v after=%+v", before, got)
			}
		})
	}
}

func TestReconciliationCoordinatorAvailableInterfaceRetriesExactPendingSources(t *testing.T) {
	t.Parallel()

	logs := new(lockedBuffer)
	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.NewTextHandler(logs, nil)), newDaemonHealthChecker(),
	)
	candidate := compiledControlSessionCandidate{
		base:         []baseSessionCandidate{{config: bfd.SessionConfig{Interface: "shared0"}}},
		echo:         []echoSessionCandidate{{config: bfd.EchoSessionConfig{Interface: "shared0"}}},
		microGroups:  []bfd.MicroBFDReconcileConfig{{Config: bfd.MicroBFDConfig{LAGInterface: "shared0"}}},
		microMembers: []microBFDMemberCandidate{{member: "shared0"}},
		overlays: [2]compiledOverlayCandidate{
			{desired: []bfd.ReconcileConfig{{Key: "shared0"}}},
			{desired: []bfd.ReconcileConfig{{Key: "shared0"}}},
		},
	}
	initial := coordinator.applyCandidate(
		context.Background(), candidate,
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			switch source {
			case sourceBase, sourceEcho, sourceMicroGroup:
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, source.String()))
			default:
				return sourceApplyResult{}
			}
		},
	)

	order := make([]reconciliationSource, 0, 4)
	first := coordinator.reconcileInterfaceEvent(
		context.Background(), initial.DesiredGeneration, "shared0", interfaceEventAvailable,
		func([]string) (int, error) {
			t.Fatal("available event directly invoked preflight checker")
			return 0, nil
		},
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			order = append(order, source)
			if source == sourceEcho {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "shared0"))
			}
			return sourceApplyResult{Created: 1}
		},
	)
	wantOrder := []reconciliationSource{sourceBase, sourceEcho, sourceMicroGroup, sourceMicroMember}
	if !slices.Equal(order, wantOrder) {
		t.Fatalf("available event order = %v, want %v", order, wantOrder)
	}
	if first.DesiredGeneration != initial.DesiredGeneration || first.AppliedGeneration != 0 ||
		!first.Stale || first.Pending != 1 || first.Failed != 0 {
		t.Fatalf("partially available snapshot = %+v", first)
	}

	order = order[:0]
	converged := coordinator.reconcileInterfaceEvent(
		context.Background(), initial.DesiredGeneration, "shared0", interfaceEventAvailable,
		func([]string) (int, error) {
			t.Fatal("available event directly invoked preflight checker")
			return 0, nil
		},
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			order = append(order, source)
			return sourceApplyResult{Created: 1}
		},
	)
	if !slices.Equal(order, []reconciliationSource{sourceEcho}) {
		t.Fatalf("second available event order = %v, want [echo]", order)
	}
	if converged.Stale || converged.AppliedGeneration != initial.DesiredGeneration {
		t.Fatalf("converged available snapshot = %+v", converged)
	}

	beforeDuplicateLogs := logs.String()
	duplicate := coordinator.reconcileInterfaceEvent(
		context.Background(), initial.DesiredGeneration, "shared0", interfaceEventAvailable,
		func([]string) (int, error) {
			t.Fatal("duplicate event invoked checker")
			return 0, nil
		},
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("duplicate event invoked source apply")
			return sourceApplyResult{}
		},
	)
	if duplicate != converged || logs.String() != beforeDuplicateLogs {
		t.Fatalf("duplicate available event changed snapshot/log: got %+v want %+v", duplicate, converged)
	}
}

func TestReconciliationCoordinatorAvailableInterfaceSemanticNoOpIsNotPublished(t *testing.T) {
	t.Parallel()

	logs := new(lockedBuffer)
	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.NewTextHandler(logs, nil)), newDaemonHealthChecker(),
	)
	candidate := compiledControlSessionCandidate{
		base: []baseSessionCandidate{{config: bfd.SessionConfig{Interface: "pending0"}}},
	}
	initial := coordinator.applyCandidate(
		context.Background(), candidate,
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "pending0"))
			}
			return sourceApplyResult{}
		},
	)
	beforeLogs := logs.String()
	applyCalls := 0
	got := coordinator.reconcileInterfaceEvent(
		context.Background(), initial.DesiredGeneration, "pending0", interfaceEventAvailable,
		func([]string) (int, error) { return 0, nil },
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			applyCalls++
			if source != sourceBase {
				t.Fatalf("semantic no-op source = %s, want base", source)
			}
			return resourceErrorSourceResult(1, testUnavailableResourceError(t, "pending0"))
		},
	)
	if applyCalls != 1 || got != initial || logs.String() != beforeLogs {
		t.Fatalf("semantic no-op calls/snapshot/log = %d/%+v/%q, want 1/unchanged/unchanged",
			applyCalls, got, logs.String())
	}
}

func TestReconciliationCoordinatorInterfaceEventReusesMicroDependencyClosure(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), nil,
	)
	candidate := compiledControlSessionCandidate{
		microGroups:  []bfd.MicroBFDReconcileConfig{{Config: bfd.MicroBFDConfig{LAGInterface: "bond0"}}},
		microMembers: []microBFDMemberCandidate{{member: "member0"}},
	}
	initial := coordinator.applyCandidate(
		context.Background(), candidate,
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceMicroGroup {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "bond0"))
			}
			return sourceApplyResult{}
		},
	)
	order := make([]reconciliationSource, 0, 2)
	got := coordinator.reconcileInterfaceEvent(
		context.Background(), initial.DesiredGeneration, "bond0", interfaceEventAvailable,
		func([]string) (int, error) { return 0, nil },
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			order = append(order, source)
			return sourceApplyResult{}
		},
	)
	if !slices.Equal(order, []reconciliationSource{sourceMicroGroup, sourceMicroMember}) {
		t.Fatalf("Micro-BFD dependency event order = %v, want group then member", order)
	}
	if got.Stale || got.AppliedGeneration != initial.DesiredGeneration {
		t.Fatalf("Micro-BFD dependency event snapshot = %+v, want converged", got)
	}
}

func TestReconciliationCoordinatorInterfaceEventNoOps(t *testing.T) {
	t.Parallel()

	logs := new(lockedBuffer)
	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.NewTextHandler(logs, nil)), nil,
	)
	beforeNoCandidateLogs := logs.String()
	noCandidate := coordinator.reconcileInterfaceEvent(
		context.Background(), 0, "shared0", interfaceEventAvailable,
		func([]string) (int, error) { t.Fatal("no-candidate event invoked checker"); return 0, nil },
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("no-candidate event invoked source apply")
			return sourceApplyResult{}
		},
	)
	if noCandidate != (reconciliationSnapshot{Stale: true}) {
		t.Fatalf("no-candidate event snapshot = %+v", noCandidate)
	}
	if logs.String() != beforeNoCandidateLogs {
		t.Fatalf("no-candidate event emitted a log: %q", logs.String())
	}

	candidate := compiledControlSessionCandidate{
		base: []baseSessionCandidate{{config: bfd.SessionConfig{Interface: "shared0"}}},
	}
	initial := coordinator.applyCandidate(
		context.Background(), candidate,
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "shared0"))
			}
			return sourceApplyResult{}
		},
	)
	for _, test := range []struct {
		name       string
		generation uint64
		ifName     string
	}{
		{name: "stale generation", generation: initial.DesiredGeneration - 1, ifName: "shared0"},
		{name: "empty interface", generation: initial.DesiredGeneration},
		{name: "irrelevant interface", generation: initial.DesiredGeneration, ifName: "other0"},
	} {
		beforeLogs := logs.String()
		got := coordinator.reconcileInterfaceEvent(
			context.Background(), test.generation, test.ifName, interfaceEventAvailable,
			func([]string) (int, error) { t.Fatal("no-op event invoked checker"); return 0, nil },
			func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
				t.Fatal("no-op event invoked source apply")
				return sourceApplyResult{}
			},
		)
		if got != initial {
			t.Fatalf("%s snapshot = %+v, want %+v", test.name, got, initial)
		}
		if logs.String() != beforeLogs {
			t.Fatalf("%s emitted a log: before=%q after=%q", test.name, beforeLogs, logs.String())
		}
	}

	failedCoordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.NewTextHandler(logs, nil)), nil,
	)
	failed := failedCoordinator.applyCandidate(
		context.Background(), candidate,
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return failedSourceResult(bfd.ReconcileErrorCreate, errors.New("permanent"))
			}
			return sourceApplyResult{}
		},
	)
	beforeFailedLogs := logs.String()
	failedUp := failedCoordinator.reconcileInterfaceEvent(
		context.Background(), failed.DesiredGeneration, "shared0", interfaceEventAvailable,
		func([]string) (int, error) { t.Fatal("failed-up event invoked checker"); return 0, nil },
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("failed-up event invoked source apply")
			return sourceApplyResult{}
		},
	)
	if failedUp != failed || logs.String() != beforeFailedLogs {
		t.Fatalf("failed-up event changed snapshot/log: got %+v want %+v", failedUp, failed)
	}
}

func TestReconciliationCoordinatorInterfaceEventChecksGenerationAfterApplyLock(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), nil,
	)
	firstCandidate := compiledControlSessionCandidate{
		base: []baseSessionCandidate{{config: bfd.SessionConfig{Interface: "first0"}}},
	}
	first := coordinator.applyCandidate(
		context.Background(), firstCandidate,
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "first0"))
			}
			return sourceApplyResult{}
		},
	)

	applyEntered := make(chan struct{})
	releaseApply := make(chan struct{})
	secondResult := make(chan reconciliationSnapshot, 1)
	go func() {
		secondResult <- coordinator.applyCandidate(
			context.Background(),
			compiledControlSessionCandidate{
				base: []baseSessionCandidate{{config: bfd.SessionConfig{Interface: "second0"}}},
			},
			func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
				if source == sourceBase {
					close(applyEntered)
					<-releaseApply
				}
				return sourceApplyResult{}
			},
		)
	}()
	<-applyEntered

	var checkerCalls atomic.Int32
	var applyCalls atomic.Int32
	eventStarted := make(chan struct{})
	eventResult := make(chan reconciliationSnapshot, 1)
	go func() {
		close(eventStarted)
		eventResult <- coordinator.reconcileInterfaceEvent(
			context.Background(), first.DesiredGeneration, "first0", interfaceEventAvailable,
			func([]string) (int, error) { checkerCalls.Add(1); return 0, nil },
			func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
				applyCalls.Add(1)
				return sourceApplyResult{}
			},
		)
	}()
	<-eventStarted
	close(releaseApply)
	second := <-secondResult
	got := <-eventResult
	if checkerCalls.Load() != 0 || applyCalls.Load() != 0 {
		t.Fatalf("stale serialized event checker/apply calls = %d/%d, want 0/0",
			checkerCalls.Load(), applyCalls.Load())
	}
	if got != second || got.DesiredGeneration != first.DesiredGeneration+1 {
		t.Fatalf("serialized stale event snapshot = %+v, want second generation %+v", got, second)
	}
}
