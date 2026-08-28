package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/grpchealth"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestReconciliationCoordinatorRetryRetainsCandidateAndMergesReceipt(t *testing.T) {
	t.Parallel()

	logs := new(lockedBuffer)
	checker := newDaemonHealthChecker()
	startup := config.DefaultConfig()
	startup.Sessions = []config.SessionConfig{{
		Peer: "192.0.2.240", Local: "127.0.0.1", Interface: "lo",
	}}
	coordinator := newReconciliationCoordinator(startup, slog.New(slog.NewTextHandler(logs, nil)), checker)
	candidate := compiledControlSessionCandidate{
		base: []baseSessionCandidate{{
			peer: "validated-original", senderOpts: []netio.SenderOption{netio.WithDFBit()},
		}},
		echo: []echoSessionCandidate{{key: "echo-original"}},
		microGroups: []bfd.MicroBFDReconcileConfig{{
			Key: "bond-original",
			Config: bfd.MicroBFDConfig{
				LAGInterface: "bond-original", MemberLinks: []string{"eth-original"},
			},
		}},
		microMembers: []microBFDMemberCandidate{{member: "member-original"}},
		overlays: [2]compiledOverlayCandidate{
			{desired: []bfd.ReconcileConfig{{Key: "vxlan-original"}}},
			{desired: []bfd.ReconcileConfig{{Key: "geneve-original"}}},
		},
	}
	initial := coordinator.applyCandidate(
		context.Background(), candidate,
		func(_ context.Context, source reconciliationSource, got compiledControlSessionCandidate) sourceApplyResult {
			switch source {
			case sourceBase:
				got.base[0].peer = "mutated-by-apply"
				got.base[0].senderOpts[0] = nil
				got.echo[0].key = "echo-mutated-by-apply"
				got.microGroups[0].Key = "group-mutated-by-apply"
				got.microGroups[0].Config.MemberLinks[0] = "micro-mutated-by-apply"
				got.microMembers[0].member = "member-mutated-by-apply"
				got.overlays[0].desired[0].Key = "vxlan-mutated-by-apply"
				got.overlays[1].desired[0].Key = "geneve-mutated-by-apply"
				result := resourceErrorSourceResult(2, testUnavailableResourceError(t, "eth-retry"))
				result.Created = 1
				return result
			case sourceEcho:
				return sourceApplyResult{Created: 4}
			default:
				return sourceApplyResult{}
			}
		},
	)
	if initial.DesiredGeneration != 1 || initial.AppliedGeneration != 0 || !initial.Stale {
		t.Fatalf("initial generations = %d/%d stale=%t, want 1/0/true",
			initial.DesiredGeneration, initial.AppliedGeneration, initial.Stale)
	}
	if initial.Pending != 2 || initial.Failed != 0 {
		t.Fatalf("initial pending/failed = %d/%d, want 2/0", initial.Pending, initial.Failed)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusNotServing)

	candidate.base[0].peer = "mutated-after-apply"
	candidate.base[0].senderOpts[0] = nil
	candidate.echo[0].key = "echo-mutated"
	candidate.microGroups[0].Key = "group-mutated"
	candidate.microGroups[0].Config.MemberLinks[0] = "micro-mutated"
	candidate.microMembers[0].member = "member-mutated"
	candidate.overlays[0].desired[0].Key = "vxlan-mutated"
	candidate.overlays[1].desired[0].Key = "geneve-mutated"

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	invalid := cloneStartupContractTestConfig(startup)
	invalid.Sessions[0].Peer = "not-an-address"
	if err := coordinator.reconcile(
		context.Background(), invalid, mgr,
		newNthFailureDeclarativeSenderFactory(0), &overlayRuntime{}, new(slog.LevelVar),
	); err == nil {
		t.Fatal("invalid candidate unexpectedly replaced retained candidate")
	}

	retryCalls := 0
	converged := coordinator.retryPendingSources(
		context.Background(),
		initial.DesiredGeneration,
		sourceMask(sourceBase)|sourceMask(sourceEcho),
		func(_ context.Context, source reconciliationSource, got compiledControlSessionCandidate) sourceApplyResult {
			retryCalls++
			if source != sourceBase {
				t.Fatalf("retried source = %s, want only pending base", source)
			}
			if len(got.base) != 1 || got.base[0].peer != "validated-original" ||
				len(got.base[0].senderOpts) != 1 || got.base[0].senderOpts[0] == nil ||
				len(got.echo) != 1 || got.echo[0].key != "echo-original" ||
				len(got.microGroups) != 1 || got.microGroups[0].Key != "bond-original" ||
				got.microGroups[0].Config.MemberLinks[0] != "eth-original" ||
				len(got.microMembers) != 1 || got.microMembers[0].member != "member-original" ||
				len(got.overlays[0].desired) != 1 || got.overlays[0].desired[0].Key != "vxlan-original" ||
				len(got.overlays[1].desired) != 1 || got.overlays[1].desired[0].Key != "geneve-original" {
				t.Fatalf("retained retry candidate was zero or mutated: %+v", got)
			}
			return sourceApplyResult{Created: 2, Released: 1}
		},
	)
	if retryCalls != 1 {
		t.Fatalf("retry calls = %d, want 1", retryCalls)
	}
	if converged.DesiredGeneration != 1 || converged.AppliedGeneration != 1 || converged.Stale {
		t.Fatalf("retry generations = %d/%d stale=%t, want 1/1/false",
			converged.DesiredGeneration, converged.AppliedGeneration, converged.Stale)
	}
	if converged.Pending != 0 || converged.Failed != 0 {
		t.Fatalf("retry pending/failed = %d/%d, want 0/0", converged.Pending, converged.Failed)
	}
	base := receiptForSource(t, converged.LastReceipt, sourceBase)
	if base.Created != 3 || base.Released != 1 || base.Pending != 0 || base.Failed != 0 {
		t.Fatalf("merged base receipt = %+v, want created=3 released=1 and converged", base)
	}
	if base.Errors != (reconciliationErrorHistogram{}) {
		t.Fatalf("merged base errors = %+v, want replacement empty histogram", base.Errors)
	}
	echo := receiptForSource(t, converged.LastReceipt, sourceEcho)
	if echo.Created != 4 || echo.Released != 0 || echo.Pending != 0 || echo.Failed != 0 {
		t.Fatalf("untouched echo receipt = %+v, want original created=4", echo)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusServing)

	noOp := coordinator.retryPendingSources(
		context.Background(), converged.DesiredGeneration, sourceMask(sourceBase),
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("converged source was retried")
			return sourceApplyResult{}
		},
	)
	if noOp != converged {
		t.Fatalf("repeated retry snapshot = %+v, want unchanged %+v", noOp, converged)
	}
	if got := strings.Count(logs.String(), "configuration reconciliation converged"); got != 1 {
		t.Fatalf("converged log count after no-op retry = %d, want 1; logs=%q", got, logs.String())
	}
}

func TestReconciliationCoordinatorRetrySkipsPermanentFailedSources(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), newDaemonHealthChecker(),
	)
	coordinator.applyCandidate(
		context.Background(), compiledControlSessionCandidate{},
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			switch source {
			case sourceBase:
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "eth-pending"))
			case sourceEcho:
				return resourceErrorSourceResult(1, errors.New("permanent source failure"))
			case sourceMicroMember:
				result := resourceErrorSourceResult(1, testUnavailableResourceError(t, "eth-mixed"))
				result.Failed = 1
				result.Errors[bfd.ReconcileErrorConflict] = 1
				return result
			default:
				return sourceApplyResult{}
			}
		},
	)

	calls := make(map[reconciliationSource]int)
	snapshot := coordinator.retryPendingSources(
		context.Background(), 1,
		sourceMask(sourceBase)|sourceMask(sourceEcho)|sourceMask(sourceMicroMember),
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			calls[source]++
			return resourceErrorSourceResult(1, testUnavailableResourceError(t, "eth-pending"))
		},
	)
	if calls[sourceBase] != 1 || calls[sourceEcho] != 0 || calls[sourceMicroMember] != 0 {
		t.Fatalf("retry calls = %v, want base=1 echo=0 micro_member=0", calls)
	}
	if snapshot.DesiredGeneration != 1 || snapshot.AppliedGeneration != 0 ||
		!snapshot.Stale || snapshot.Pending != 2 || snapshot.Failed != 2 {
		t.Fatalf("partial retry snapshot = %+v, want stable generation with pending=2 failed=2", snapshot)
	}

	unchanged := coordinator.retryPendingSources(
		context.Background(), 1, sourceMask(sourceEcho)|sourceMask(sourceMicroMember),
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("failed-only source was retried")
			return sourceApplyResult{}
		},
	)
	if unchanged != snapshot {
		t.Fatalf("failed-only retry snapshot = %+v, want unchanged %+v", unchanged, snapshot)
	}
}

func TestReconciliationCoordinatorConcurrentRetrySerializesAndConvergesOnce(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), newDaemonHealthChecker(),
	)
	coordinator.applyCandidate(
		context.Background(), compiledControlSessionCandidate{},
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "eth-concurrent"))
			}
			return sourceApplyResult{}
		},
	)

	var calls atomic.Int32
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	applySource := func(
		_ context.Context,
		_ reconciliationSource,
		_ compiledControlSessionCandidate,
	) sourceApplyResult {
		call := calls.Add(1)
		if call == 1 {
			if coordinator.applyMu.TryLock() {
				coordinator.applyMu.Unlock()
				t.Error("retry callback ran without coordinator apply lock")
			}
			close(firstEntered)
			<-releaseFirst
		}
		return sourceApplyResult{Created: 1}
	}

	results := make(chan reconciliationSnapshot, 2)
	go func() {
		results <- coordinator.retryPendingSources(
			context.Background(), 1, sourceMask(sourceBase), applySource,
		)
	}()
	<-firstEntered
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		results <- coordinator.retryPendingSources(
			context.Background(), 1, sourceMask(sourceBase), applySource,
		)
	}()
	<-secondStarted
	close(releaseFirst)

	first := <-results
	second := <-results
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent retry apply calls = %d, want 1", got)
	}
	if first != second || first.DesiredGeneration != 1 || first.AppliedGeneration != 1 || first.Stale {
		t.Fatalf("concurrent retry snapshots = %+v / %+v, want identical converged generation 1", first, second)
	}
}

func TestReconciliationCoordinatorNewGenerationSupersedesStaleRetry(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), newDaemonHealthChecker(),
	)
	pendingApply := func(
		_ context.Context,
		source reconciliationSource,
		_ compiledControlSessionCandidate,
	) sourceApplyResult {
		if source == sourceBase {
			return resourceErrorSourceResult(1, testUnavailableResourceError(t, "eth-generation"))
		}
		return sourceApplyResult{}
	}
	first := coordinator.applyCandidate(
		context.Background(),
		compiledControlSessionCandidate{base: []baseSessionCandidate{{peer: "generation-one"}}},
		pendingApply,
	)
	second := coordinator.applyCandidate(
		context.Background(),
		compiledControlSessionCandidate{base: []baseSessionCandidate{{peer: "generation-two"}}},
		pendingApply,
	)
	if first.DesiredGeneration != 1 || second.DesiredGeneration != 2 {
		t.Fatalf("full apply generations = %d then %d, want 1 then 2",
			first.DesiredGeneration, second.DesiredGeneration)
	}

	staleEvent := coordinator.retryPendingSources(
		context.Background(), first.DesiredGeneration, sourceMask(sourceBase),
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("stale generation event retried current candidate")
			return sourceApplyResult{}
		},
	)
	if staleEvent != second {
		t.Fatalf("stale generation retry snapshot = %+v, want unchanged %+v", staleEvent, second)
	}

	current := coordinator.retryPendingSources(
		context.Background(), second.DesiredGeneration, sourceMask(sourceBase),
		func(_ context.Context, source reconciliationSource, candidate compiledControlSessionCandidate) sourceApplyResult {
			if source != sourceBase || len(candidate.base) != 1 || candidate.base[0].peer != "generation-two" {
				t.Fatalf("current retry source/candidate = %s/%+v, want base/generation-two", source, candidate.base)
			}
			return sourceApplyResult{}
		},
	)
	if current.DesiredGeneration != 2 || current.AppliedGeneration != 2 || current.Stale {
		t.Fatalf("current retry snapshot = %+v, want converged generation 2", current)
	}
}

func TestResourceErrorSourceResultFailsClosed(t *testing.T) {
	t.Parallel()

	typedErr := testUnavailableResourceError(t, "eth-typed")
	typed := resourceErrorSourceResult(2, typedErr)
	if typed.Pending != 2 || typed.Failed != 0 || !errors.Is(typed.Err, typedErr) ||
		typed.Errors != (reconciliationErrorHistogram{}) {
		t.Fatalf("typed resource result = %+v, want pending=2 without failure", typed)
	}
	for _, pendingClaims := range []int{0, -1} {
		result := resourceErrorSourceResult(pendingClaims, typedErr)
		if result.Pending != 0 || result.Failed != 1 || !errors.Is(result.Err, typedErr) {
			t.Errorf("typed resource result for count %d = %+v, want failed=1", pendingClaims, result)
		}
		if got := result.Errors.Count(bfd.ReconcileErrorCreate); got != 1 {
			t.Errorf("typed resource create error count for count %d = %d, want 1", pendingClaims, got)
		}
	}

	for _, permanentErr := range []error{
		errors.New("permanent"),
		bfd.NewResourceUnavailableError(bfd.ResourceRef{Kind: bfd.ResourceKind(255), ID: "eth0"}),
	} {
		result := resourceErrorSourceResult(2, permanentErr)
		if result.Pending != 0 || result.Failed != 1 || !errors.Is(result.Err, permanentErr) {
			t.Errorf("permanent resource result = %+v, want failed=1", result)
		}
		if got := result.Errors.Count(bfd.ReconcileErrorCreate); got != 1 {
			t.Errorf("permanent create error count = %d, want 1", got)
		}
	}

	secondTypedErr := testUnavailableResourceError(t, "eth-second-typed")
	allTyped := fmt.Errorf("wrapped resource errors: %w", errors.Join(typedErr, secondTypedErr))
	allTypedResult := resourceErrorSourceResult(2, allTyped)
	if allTypedResult.Pending != 2 || allTypedResult.Failed != 0 ||
		!errors.Is(allTypedResult.Err, typedErr) || !errors.Is(allTypedResult.Err, secondTypedErr) {
		t.Fatalf("all-typed resource result = %+v, want pending=2", allTypedResult)
	}

	permanentErr := errors.New("mixed permanent")
	mixed := fmt.Errorf("wrapped mixed errors: %w", errors.Join(typedErr, permanentErr))
	mixedResult := resourceErrorSourceResult(2, mixed)
	if mixedResult.Pending != 0 || mixedResult.Failed != 1 ||
		!errors.Is(mixedResult.Err, typedErr) || !errors.Is(mixedResult.Err, permanentErr) {
		t.Fatalf("mixed resource result = %+v, want failed=1", mixedResult)
	}
	if got := mixedResult.Errors.Count(bfd.ReconcileErrorCreate); got != 1 {
		t.Fatalf("mixed resource create error count = %d, want 1", got)
	}
}

func TestUnknownReconciliationSourceMaskIsEmpty(t *testing.T) {
	t.Parallel()

	unknown := reconciliationSource(sourceCount)
	if got := sourceMask(unknown); got != 0 {
		t.Fatalf("sourceMask(%d) = %08b, want empty", unknown, got)
	}

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), newDaemonHealthChecker(),
	)
	want := coordinator.applyCandidate(
		context.Background(), compiledControlSessionCandidate{},
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "eth-unknown-mask"))
			}
			return sourceApplyResult{}
		},
	)
	got := coordinator.retryPendingSources(
		context.Background(), want.DesiredGeneration, sourceMask(unknown),
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			t.Fatal("unknown source mask retried a pending source")
			return sourceApplyResult{}
		},
	)
	if got != want {
		t.Fatalf("unknown-mask retry snapshot = %+v, want unchanged %+v", got, want)
	}
}

func testUnavailableResourceError(t *testing.T, id string) error {
	t.Helper()
	err := bfd.NewResourceUnavailableError(bfd.ResourceRef{Kind: bfd.ResourceKindInterface, ID: id})
	if !errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("test resource error = %v, want ErrResourceUnavailable", err)
	}
	return err
}
