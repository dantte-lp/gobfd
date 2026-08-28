package bfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUnavailableResourceClassification(t *testing.T) {
	t.Parallel()

	want := ResourceRef{Kind: ResourceKindInterface, ID: "eth-test"}
	err := fmt.Errorf("create transport: %w", NewResourceUnavailableError(want))
	if !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("errors.Is(%v, ErrResourceUnavailable) = false", err)
	}
	var unavailableErr *ResourceUnavailableError
	if !errors.As(err, &unavailableErr) {
		t.Fatalf("errors.As(%v, *ResourceUnavailableError) = false", err)
	}
	if got := unavailableErr.Resource(); got != want {
		t.Fatalf("typed unavailable resource = %+v, want %+v", got, want)
	}
	if got, ok := UnavailableResource(err); !ok || got != want {
		t.Fatalf("UnavailableResource(%v) = (%+v, %t), want (%+v, true)", err, got, ok, want)
	}
}

func TestUnavailableResourceClassifierRejectsPermanentErrors(t *testing.T) {
	t.Parallel()

	permanent := errors.New("permanent failure")
	if got, ok := UnavailableResource(permanent); ok || got != (ResourceRef{}) {
		t.Fatalf("UnavailableResource(permanent) = (%+v, %t), want zero, false", got, ok)
	}
	if got, ok := UnavailableResource(nil); ok || got != (ResourceRef{}) {
		t.Fatalf("UnavailableResource(nil) = (%+v, %t), want zero, false", got, ok)
	}
}

func TestNewResourceUnavailableErrorRejectsMalformedResourceRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resource ResourceRef
	}{
		{name: "unknown kind", resource: ResourceRef{Kind: ResourceKind(255), ID: "eth0"}},
		{name: "empty ID", resource: ResourceRef{Kind: ResourceKindInterface}},
		{name: "NUL in ID", resource: ResourceRef{Kind: ResourceKindInterface, ID: "eth\x000"}},
		{
			name: "ID beyond bound",
			resource: ResourceRef{
				Kind: ResourceKindInterface,
				ID:   strings.Repeat("x", MaxResourceIDLen+1),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := NewResourceUnavailableError(tc.resource)
			if !errors.Is(err, ErrInvalidResourceRef) {
				t.Fatalf("malformed resource error = %v, want ErrInvalidResourceRef", err)
			}
			if errors.Is(err, ErrResourceUnavailable) {
				t.Fatalf("malformed resource exposed ErrResourceUnavailable: %v", err)
			}
			if unavailableErr, ok := errors.AsType[*ResourceUnavailableError](err); ok {
				t.Fatalf("malformed resource exposed ResourceUnavailableError: %+v", unavailableErr)
			}
			if got, ok := UnavailableResource(err); ok || got != (ResourceRef{}) {
				t.Fatalf("UnavailableResource(malformed) = (%+v, %t), want zero, false", got, ok)
			}

			malformedTyped := &ResourceUnavailableError{resource: tc.resource}
			if errors.Is(malformedTyped, ErrResourceUnavailable) {
				t.Fatalf("direct malformed typed error exposed ErrResourceUnavailable: %v", malformedTyped)
			}
			if got, ok := UnavailableResource(malformedTyped); ok || got != (ResourceRef{}) {
				t.Fatalf("UnavailableResource(direct malformed) = (%+v, %t), want zero, false", got, ok)
			}
		})
	}
}

func TestNewResourceUnavailableErrorAcceptsMaximumBoundedID(t *testing.T) {
	t.Parallel()

	want := ResourceRef{
		Kind: ResourceKindInterface,
		ID:   strings.Repeat("x", MaxResourceIDLen),
	}
	err := NewResourceUnavailableError(want)
	if !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("maximum bounded resource error = %v, want ErrResourceUnavailable", err)
	}
	if got, ok := UnavailableResource(err); !ok || got != want {
		t.Fatalf("UnavailableResource(maximum ID) = (%+v, %t), want (%+v, true)", got, ok, want)
	}
}

func TestManagerDetailedReconcileCountsSharedWireOwnerClaims(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	cfg := ownershipTestConfig("192.0.2.210")
	desired := []ReconcileConfig{{
		Key:                "shared",
		SessionConfig:      cfg,
		SenderLeaseFactory: ownershipSenderLeaseFactory(),
	}}

	configResult := mgr.ReconcileSessionsForOwnerDetailed(
		context.Background(), ConfigReconciliationOwner(), desired,
	)
	assertReconcileResult(t, configResult, 1, 0, 0, nil)

	microResult := mgr.ReconcileSessionsForOwnerDetailed(
		context.Background(), MicroBFDReconciliationOwner(), desired,
	)
	assertReconcileResult(t, microResult, 1, 0, 0, nil)
	if got := len(mgr.Sessions()); got != 1 {
		t.Fatalf("wire sessions after shared claim = %d, want 1", got)
	}

	releaseResult := mgr.ReconcileSessionsForOwnerDetailed(
		context.Background(), MicroBFDReconciliationOwner(), nil,
	)
	assertReconcileResult(t, releaseResult, 0, 1, 0, nil)
	if got := len(mgr.Sessions()); got != 1 {
		t.Fatalf("wire sessions after shared claim release = %d, want 1", got)
	}
}

func TestManagerDetailedReconcilePreservesStaleReleaseAndRollsBackCreations(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	if result := mgr.ReconcileSessionsForOwnerDetailed(
		context.Background(),
		ConfigReconciliationOwner(),
		[]ReconcileConfig{{
			Key:                "stale",
			SessionConfig:      ownershipTestConfig("192.0.2.211"),
			SenderLeaseFactory: ownershipSenderLeaseFactory(),
		}},
	); result.Err() != nil {
		t.Fatalf("seed reconciliation: %v", result.Err())
	}

	firstFailure := errors.New("first sender unavailable")
	secondFailure := errors.New("second sender unavailable")
	result := mgr.ReconcileSessionsForOwnerDetailed(
		context.Background(),
		ConfigReconciliationOwner(),
		[]ReconcileConfig{
			{
				Key:                "created-then-rolled-back",
				SessionConfig:      ownershipTestConfig("192.0.2.212"),
				SenderLeaseFactory: ownershipSenderLeaseFactory(),
			},
			{
				Key:           "first-failure",
				SessionConfig: ownershipTestConfig("192.0.2.213"),
				SenderLeaseFactory: func() (*SenderLease, error) {
					return nil, firstFailure
				},
			},
			{
				Key:           "second-failure",
				SessionConfig: ownershipTestConfig("192.0.2.214"),
				SenderLeaseFactory: func() (*SenderLease, error) {
					return nil, secondFailure
				},
			},
		},
	)

	assertReconcileResult(t, result, 0, 1, 2, []ReconcileErrorCode{
		ReconcileErrorCreate,
		ReconcileErrorCreate,
	})
	if !errors.Is(result.Err(), firstFailure) || !errors.Is(result.Err(), secondFailure) {
		t.Fatalf("detailed reconciliation error = %v, want both sender failures", result.Err())
	}
	if got := len(mgr.Sessions()); got != 0 {
		t.Fatalf("sessions after failed creation pass rollback = %d, want 0", got)
	}
}

func TestManagerDetailedReconcileSurfacesCleanupErrorOutsideLocks(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	cleanupErr := errors.New("close sender socket")
	var ownershipUnlocked, lifecycleMutationUnlocked bool
	desired := []ReconcileConfig{{
		Key:           "cleanup",
		SessionConfig: ownershipTestConfig("192.0.2.215"),
		SenderLeaseFactory: func() (*SenderLease, error) {
			return NewSenderLease(ownershipNoopSender{}, func() error {
				ownershipUnlocked = mgr.ownershipMu.TryLock()
				if ownershipUnlocked {
					mgr.ownershipMu.Unlock()
				}
				lifecycleMutationUnlocked = mgr.lifecycleMu.TryLock()
				if lifecycleMutationUnlocked {
					mgr.lifecycleMu.Unlock()
				}
				_ = mgr.Sessions()
				return cleanupErr
			}), nil
		},
	}}
	if result := mgr.ReconcileSessionsForOwnerDetailed(
		context.Background(), ConfigReconciliationOwner(), desired,
	); result.Err() != nil {
		t.Fatalf("seed reconciliation: %v", result.Err())
	}

	result := mgr.ReconcileSessionsForOwnerDetailed(
		context.Background(), ConfigReconciliationOwner(), nil,
	)
	assertReconcileResult(t, result, 0, 1, 1, []ReconcileErrorCode{ReconcileErrorCleanup})
	if !errors.Is(result.Err(), cleanupErr) {
		t.Fatalf("cleanup error = %v, want %v", result.Err(), cleanupErr)
	}
	if !ownershipUnlocked {
		t.Error("sender cleanup callback ran while ownership lock was held")
	}
	if !lifecycleMutationUnlocked {
		t.Error("sender cleanup callback ran while lifecycle mutation lock was held")
	}
}

func TestManagerDetailedEchoReconcileReportsNetRollbackAndLegacyTuple(t *testing.T) {
	mgr := newEchoLeaseManager()
	defer mgr.Close()

	factoryErr := errors.New("second echo sender unavailable")
	first := &echoLeaseCounter{}
	second := &echoLeaseCounter{}
	desired := []EchoReconcileConfig{
		{EchoSessionConfig: echoLeaseConfig("192.0.2.216"), SenderLeaseFactory: first.factory(nil)},
		{EchoSessionConfig: echoLeaseConfig("192.0.2.217"), SenderLeaseFactory: second.factory(factoryErr)},
	}

	result := mgr.ReconcileEchoSessionsDetailed(context.Background(), desired)
	assertReconcileResult(t, result, 0, 0, 1, []ReconcileErrorCode{ReconcileErrorCreate})
	if !errors.Is(result.Err(), factoryErr) {
		t.Fatalf("detailed echo error = %v, want %v", result.Err(), factoryErr)
	}
	if got := len(mgr.EchoSessions()); got != 0 {
		t.Fatalf("echo sessions after rollback = %d, want 0", got)
	}

	created, destroyed, err := mgr.ReconcileEchoSessions(context.Background(), desired)
	if !errors.Is(err, factoryErr) {
		t.Fatalf("legacy echo error = %v, want %v", err, factoryErr)
	}
	if created != 0 || destroyed != 0 {
		t.Fatalf("legacy echo tuple = (%d, %d), want (0, 0)", created, destroyed)
	}
}

func TestManagerDetailedMicroBFDReconcileReportsTypedOutcomesAndLegacyTuple(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	cfg := detailedMicroBFDConfig()
	created := mgr.ReconcileMicroBFDGroupsDetailed([]MicroBFDReconcileConfig{{
		Key: cfg.LAGInterface, Config: cfg,
	}})
	assertReconcileResult(t, created, 1, 0, 0, nil)

	conflicting := cfg
	conflicting.MinActiveLinks++
	conflict := mgr.ReconcileMicroBFDGroupsDetailed([]MicroBFDReconcileConfig{{
		Key: conflicting.LAGInterface, Config: conflicting,
	}})
	assertReconcileResult(t, conflict, 0, 0, 1, []ReconcileErrorCode{ReconcileErrorConflict})
	if !errors.Is(conflict.Err(), ErrMicroBFDConfigConflict) {
		t.Fatalf("detailed Micro-BFD error = %v, want ErrMicroBFDConfigConflict", conflict.Err())
	}

	released := mgr.ReconcileMicroBFDGroupsDetailed(nil)
	assertReconcileResult(t, released, 0, 1, 0, nil)

	legacyCreated, legacyDestroyed, err := mgr.ReconcileMicroBFDGroups([]MicroBFDReconcileConfig{{
		Key: cfg.LAGInterface, Config: cfg,
	}})
	if err != nil {
		t.Fatalf("legacy Micro-BFD reconciliation: %v", err)
	}
	if legacyCreated != 1 || legacyDestroyed != 0 {
		t.Fatalf("legacy Micro-BFD tuple = (%d, %d), want (1, 0)", legacyCreated, legacyDestroyed)
	}
}

func detailedMicroBFDConfig() MicroBFDConfig {
	return MicroBFDConfig{
		LAGInterface:          "bond-detailed",
		MemberLinks:           []string{"eth0", "eth1"},
		PeerAddr:              netip.MustParseAddr("10.0.0.1"),
		LocalAddr:             netip.MustParseAddr("10.0.0.2"),
		DesiredMinTxInterval:  100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond,
		DetectMultiplier:      3,
		MinActiveLinks:        1,
	}
}

func TestManagerLegacySessionTupleKeepsPhysicalWireCounts(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()

	cfg := ownershipTestConfig("192.0.2.218")
	desired := []ReconcileConfig{{
		Key:                "shared",
		SessionConfig:      cfg,
		SenderLeaseFactory: ownershipSenderLeaseFactory(),
	}}
	if created, destroyed, err := mgr.ReconcileSessionsForOwner(
		context.Background(), ConfigReconciliationOwner(), desired,
	); err != nil || created != 1 || destroyed != 0 {
		t.Fatalf("config legacy tuple = (%d, %d, %v), want (1, 0, nil)", created, destroyed, err)
	}
	if created, destroyed, err := mgr.ReconcileSessionsForOwner(
		context.Background(), MicroBFDReconciliationOwner(), desired,
	); err != nil || created != 0 || destroyed != 0 {
		t.Fatalf("shared legacy tuple = (%d, %d, %v), want (0, 0, nil)", created, destroyed, err)
	}
	if created, destroyed, err := mgr.ReconcileSessionsForOwner(
		context.Background(), MicroBFDReconciliationOwner(), nil,
	); err != nil || created != 0 || destroyed != 0 {
		t.Fatalf("shared release legacy tuple = (%d, %d, %v), want (0, 0, nil)", created, destroyed, err)
	}
}

func assertReconcileResult(
	t *testing.T,
	result ReconcileResult,
	wantCreated, wantReleased, wantFailed int,
	wantCodes []ReconcileErrorCode,
) {
	t.Helper()
	if result.Created != wantCreated || result.Released != wantReleased ||
		result.Pending != 0 || result.Failed != wantFailed {
		t.Errorf(
			"reconciliation result counts = (created=%d released=%d pending=%d failed=%d), want (%d, %d, 0, %d)",
			result.Created, result.Released, result.Pending, result.Failed,
			wantCreated, wantReleased, wantFailed,
		)
	}
	codes := make([]ReconcileErrorCode, 0, len(result.Errors))
	for _, reconcileErr := range result.Errors {
		codes = append(codes, reconcileErr.Code)
		if reconcileErr.Err == nil {
			t.Error("reconciliation error has nil cause")
		}
	}
	if !slices.Equal(codes, wantCodes) {
		t.Errorf("reconciliation error codes = %v, want %v", codes, wantCodes)
	}
	if result.Failed != len(result.Errors) {
		t.Errorf("failed count = %d, errors = %d", result.Failed, len(result.Errors))
	}
}
