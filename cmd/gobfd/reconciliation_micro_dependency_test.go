package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestReconciliationCoordinatorGatesMicroMemberOnGroupReceipt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		groupResult       sourceApplyResult
		wantMemberPending int
		wantMemberFailed  int
	}{
		{
			name: "pending group",
			groupResult: resourceErrorSourceResult(
				1, testUnavailableResourceError(t, "bond-pending"),
			),
			wantMemberPending: 1,
		},
		{
			name: "failed group",
			groupResult: failedSourceResult(
				bfd.ReconcileErrorCreate, errors.New("group apply failed"),
			),
			wantMemberFailed: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			logs := new(lockedBuffer)
			coordinator := newReconciliationCoordinator(
				config.DefaultConfig(), slog.New(slog.NewTextHandler(logs, nil)), nil,
			)
			candidate := compiledControlSessionCandidate{
				microGroups:  []bfd.MicroBFDReconcileConfig{{Key: "bond0"}},
				microMembers: []microBFDMemberCandidate{{member: "eth0"}},
			}
			memberCalls := 0
			snapshot := coordinator.applyCandidate(
				context.Background(), candidate,
				func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
					switch source {
					case sourceMicroGroup:
						return test.groupResult
					case sourceMicroMember:
						memberCalls++
						return sourceApplyResult{Created: 1}
					default:
						return sourceApplyResult{}
					}
				},
			)
			if memberCalls != 0 {
				t.Fatalf("member apply calls = %d, want 0 for incomplete group", memberCalls)
			}
			member := receiptForSource(t, snapshot.LastReceipt, sourceMicroMember)
			if member.Pending != test.wantMemberPending || member.Failed != test.wantMemberFailed ||
				member.Created != 0 || member.Released != 0 {
				t.Fatalf("member dependency receipt = %+v, want pending=%d failed=%d",
					member, test.wantMemberPending, test.wantMemberFailed)
			}
			if test.wantMemberFailed != 0 {
				if got := member.Errors.Count(bfd.ReconcileErrorLifecycle); got != 1 {
					t.Errorf("member dependency lifecycle errors = %d, want 1", got)
				}
				if !strings.Contains(logs.String(), "micro-BFD member source depends on incomplete group source") {
					t.Errorf("incomplete log lacks bounded dependency cause: %q", logs.String())
				}
			}
		})
	}
}

func TestReconciliationCoordinatorMicroMemberRetryWaitsForGroupConvergence(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), nil,
	)
	candidate := compiledControlSessionCandidate{
		microGroups:  []bfd.MicroBFDReconcileConfig{{Key: "bond0"}},
		microMembers: []microBFDMemberCandidate{{member: "eth0"}},
	}
	initialMemberCalls := 0
	initial := coordinator.applyCandidate(
		context.Background(), candidate,
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			switch source {
			case sourceMicroGroup:
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "bond0"))
			case sourceMicroMember:
				initialMemberCalls++
				return sourceApplyResult{Created: 99}
			default:
				return sourceApplyResult{}
			}
		},
	)
	if initialMemberCalls != 0 {
		t.Fatalf("initial member calls = %d, want 0 while group pending", initialMemberCalls)
	}

	memberOnlyCalls := 0
	memberOnly := coordinator.retryPendingSources(
		context.Background(), initial.DesiredGeneration, sourceMask(sourceMicroMember),
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			memberOnlyCalls++
			return sourceApplyResult{Created: 99}
		},
	)
	if memberOnlyCalls != 0 {
		t.Fatalf("member-only retry calls = %d, want 0 while group pending", memberOnlyCalls)
	}
	if memberOnly != initial {
		t.Fatalf("member-only retry snapshot changed: got %+v want %+v", memberOnly, initial)
	}

	stillPendingOrder := make([]reconciliationSource, 0, 2)
	stillPending := coordinator.retryPendingSources(
		context.Background(), initial.DesiredGeneration,
		sourceMask(sourceMicroGroup),
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			stillPendingOrder = append(stillPendingOrder, source)
			if source == sourceMicroGroup {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "bond0"))
			}
			return sourceApplyResult{Created: 99}
		},
	)
	if !slices.Equal(stillPendingOrder, []reconciliationSource{sourceMicroGroup}) {
		t.Fatalf("still-pending retry order = %v, want only micro_group", stillPendingOrder)
	}
	wantPendingMember := receiptForSource(t, initial.LastReceipt, sourceMicroMember)
	if got := receiptForSource(t, stillPending.LastReceipt, sourceMicroMember); got != wantPendingMember {
		t.Fatalf("member receipt changed while group remained pending: got %+v", got)
	}

	convergedOrder := make([]reconciliationSource, 0, 2)
	converged := coordinator.retryPendingSources(
		context.Background(), initial.DesiredGeneration,
		sourceMask(sourceMicroGroup),
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			convergedOrder = append(convergedOrder, source)
			if source == sourceMicroMember {
				return sourceApplyResult{Created: 1}
			}
			return sourceApplyResult{}
		},
	)
	if !slices.Equal(convergedOrder, []reconciliationSource{sourceMicroGroup, sourceMicroMember}) {
		t.Fatalf("converged retry order = %v, want group then member", convergedOrder)
	}
	if converged.DesiredGeneration != initial.DesiredGeneration ||
		converged.AppliedGeneration != initial.DesiredGeneration || converged.Stale {
		t.Fatalf("converged dependency retry snapshot = %+v", converged)
	}
	member := receiptForSource(t, converged.LastReceipt, sourceMicroMember)
	if member.Created != 1 || member.Pending != 0 || member.Failed != 0 {
		t.Fatalf("converged member receipt = %+v, want created=1", member)
	}
}

func TestReconciliationCoordinatorGroupOnlyRetryLeavesIndependentMemberPending(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), nil,
	)
	initial := coordinator.applyCandidate(
		context.Background(),
		compiledControlSessionCandidate{
			microGroups:  []bfd.MicroBFDReconcileConfig{{Key: "bond0"}},
			microMembers: []microBFDMemberCandidate{{member: "eth0"}},
		},
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceMicroMember {
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "eth0"))
			}
			return sourceApplyResult{}
		},
	)
	group := receiptForSource(t, initial.LastReceipt, sourceMicroGroup)
	member := receiptForSource(t, initial.LastReceipt, sourceMicroMember)
	if group.Pending != 0 || group.Failed != 0 || member.Pending != 1 || member.Failed != 0 {
		t.Fatalf("initial dependency receipts: group=%+v member=%+v", group, member)
	}

	calls := 0
	got := coordinator.retryPendingSources(
		context.Background(), initial.DesiredGeneration, sourceMask(sourceMicroGroup),
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			calls++
			return sourceApplyResult{}
		},
	)
	if calls != 0 {
		t.Fatalf("group-only retry calls = %d, want 0 when only member is independently pending", calls)
	}
	if got != initial {
		t.Fatalf("group-only retry changed independent member snapshot: got %+v want %+v", got, initial)
	}
}

func TestReconciliationCoordinatorIncompleteGroupPreservesMembersForEmptyDesired(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	wantSessions, wantGroups := seedDuplicateMicroBFDTestState(t, mgr)
	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), nil,
	)
	memberCalls := 0
	snapshot := coordinator.applyCandidate(
		context.Background(),
		compiledControlSessionCandidate{
			microGroups: []bfd.MicroBFDReconcileConfig{{Key: "bond-new"}},
		},
		func(ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate) sourceApplyResult {
			switch source {
			case sourceMicroGroup:
				return resourceErrorSourceResult(1, testUnavailableResourceError(t, "bond-new"))
			case sourceMicroMember:
				memberCalls++
				return sourceResultFromBFD(applyMicroBFDMemberCandidates(
					ctx, candidate.microMembers, mgr,
					newNthFailureDeclarativeSenderFactory(0), slog.New(slog.DiscardHandler),
				))
			default:
				return sourceApplyResult{}
			}
		},
	)
	if memberCalls != 0 {
		t.Fatalf("empty desired member apply calls = %d, want 0 while group pending", memberCalls)
	}
	member := receiptForSource(t, snapshot.LastReceipt, sourceMicroMember)
	if member.Pending != 1 || member.Failed != 0 || member.Released != 0 {
		t.Fatalf("empty desired blocked member receipt = %+v, want pending=1", member)
	}
	assertDuplicateMicroBFDTestStateUnchanged(t, mgr, wantSessions, wantGroups)

	retryOrder := make([]reconciliationSource, 0, 2)
	converged := coordinator.retryPendingSources(
		context.Background(), snapshot.DesiredGeneration, sourceMask(sourceMicroGroup),
		func(ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate) sourceApplyResult {
			retryOrder = append(retryOrder, source)
			if source == sourceMicroMember {
				return sourceResultFromBFD(applyMicroBFDMemberCandidates(
					ctx, candidate.microMembers, mgr,
					newNthFailureDeclarativeSenderFactory(0), slog.New(slog.DiscardHandler),
				))
			}
			return sourceApplyResult{}
		},
	)
	if !slices.Equal(retryOrder, []reconciliationSource{sourceMicroGroup, sourceMicroMember}) {
		t.Fatalf("empty desired retry order = %v, want group then member", retryOrder)
	}
	if converged.Stale || converged.AppliedGeneration != snapshot.DesiredGeneration {
		t.Fatalf("empty desired dependency retry snapshot = %+v, want converged", converged)
	}
	member = receiptForSource(t, converged.LastReceipt, sourceMicroMember)
	if member.Pending != 0 || member.Failed != 0 || member.Released != 1 {
		t.Fatalf("empty desired released member receipt = %+v, want released=1", member)
	}
	remaining := mgr.Sessions()
	if len(remaining) != 1 || remaining[0].Type == bfd.SessionTypeMicroBFD {
		t.Fatalf("sessions after deferred member release = %+v, want only seeded base session", remaining)
	}
}

func TestReconciliationCoordinatorGroupOnlyRetryFailureFailsDependentMember(t *testing.T) {
	t.Parallel()

	coordinator := newReconciliationCoordinator(
		config.DefaultConfig(), slog.New(slog.DiscardHandler), nil,
	)
	candidate := compiledControlSessionCandidate{
		microGroups:  []bfd.MicroBFDReconcileConfig{{Key: "bond0"}},
		microMembers: []microBFDMemberCandidate{{member: "eth0"}},
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

	retryOrder := make([]reconciliationSource, 0, 2)
	failed := coordinator.retryPendingSources(
		context.Background(), initial.DesiredGeneration, sourceMask(sourceMicroGroup),
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			retryOrder = append(retryOrder, source)
			if source == sourceMicroMember {
				return sourceApplyResult{Created: 99}
			}
			return failedSourceResult(bfd.ReconcileErrorCreate, errors.New("group retry failed"))
		},
	)
	if !slices.Equal(retryOrder, []reconciliationSource{sourceMicroGroup}) {
		t.Fatalf("failed group-only retry order = %v, want only micro_group", retryOrder)
	}
	member := receiptForSource(t, failed.LastReceipt, sourceMicroMember)
	if member.Pending != 0 || member.Failed != 1 || member.Created != 0 || member.Released != 0 {
		t.Fatalf("failed dependent member receipt = %+v, want failed=1 without mutation", member)
	}
	if got := member.Errors.Count(bfd.ReconcileErrorLifecycle); got != 1 {
		t.Fatalf("failed dependent member lifecycle errors = %d, want 1", got)
	}
}
