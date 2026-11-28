package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestReconciliationPendingResourceClassesPreserveFailurePolicy(t *testing.T) {
	t.Parallel()
	iface := bfd.NewResourceUnavailableError(bfd.ResourceRef{Kind: bfd.ResourceKindInterface, ID: "pending0"})
	address := bfd.NewResourceUnavailableError(bfd.ResourceRef{Kind: bfd.ResourceKindAddress, ID: "192.0.2.77"})
	for _, tc := range []struct {
		name                   string
		err                    error
		iface, address, failed bool
	}{
		{"wrapped address", fmt.Errorf("bind: %w", address), false, true, false},
		{"wrapped joined classes", fmt.Errorf("prepare: %w", errors.Join(iface, address)), true, true, false},
		{"mixed permanent", errors.Join(iface, errors.New("permanent")), false, false, true},
		{"invalid reference", bfd.NewResourceUnavailableError(bfd.ResourceRef{}), false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logs := new(lockedBuffer)
			coordinator := newReconciliationCoordinator(config.DefaultConfig(),
				slog.New(slog.NewJSONHandler(logs, nil)), newDaemonHealthChecker())
			got := coordinator.applyCandidate(t.Context(), compiledControlSessionCandidate{},
				func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
					if source == sourceBase {
						return resourceErrorSourceResult(2, tc.err)
					}
					return sourceApplyResult{}
				})
			if tc.failed && (got.Pending != 0 || got.Failed != 1) ||
				!tc.failed && (got.Pending != 2 || got.Failed != 0) {
				t.Fatalf("typed classification changed retry policy: %+v", got)
			}
			assertResourceReceipt(t, logs.String(), 1, tc.iface, tc.address)
		})
	}
}

// Equal pending counts must not hide a change in the unavailable resource class.
func TestReconciliationPendingResourceClassTransition(t *testing.T) {
	t.Parallel()
	logs := new(lockedBuffer)
	coordinator := newReconciliationCoordinator(config.DefaultConfig(),
		slog.New(slog.NewJSONHandler(logs, nil)), newDaemonHealthChecker())
	apply := func(resource bfd.ResourceRef) sourceApplyFunc {
		return func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return resourceErrorSourceResult(1, bfd.NewResourceUnavailableError(resource))
			}
			return sourceApplyResult{}
		}
	}
	before := coordinator.applyCandidate(t.Context(), compiledControlSessionCandidate{},
		apply(bfd.ResourceRef{Kind: bfd.ResourceKindInterface, ID: "pending0"}))
	assertResourceReceipt(t, logs.String(), 1, true, false)

	address := apply(bfd.ResourceRef{Kind: bfd.ResourceKindAddress, ID: "192.0.2.77"})
	after := coordinator.retryPendingSources(t.Context(), before.DesiredGeneration, sourceMask(sourceBase), address)
	if after.DesiredGeneration != before.DesiredGeneration || after.AppliedGeneration != before.AppliedGeneration ||
		after.Pending != 1 || after.Failed != 0 || !after.Stale {
		t.Fatalf("resource transition changed generation or policy: before=%+v after=%+v", before, after)
	}
	assertResourceReceipt(t, logs.String(), 2, false, true)
	unchanged := logs.String()
	coordinator.retryPendingSources(t.Context(), after.DesiredGeneration, sourceMask(sourceBase), address)
	if logs.String() != unchanged {
		t.Fatal("identical unavailable resource class emitted a duplicate receipt")
	}

	converged := coordinator.retryPendingSources(t.Context(), after.DesiredGeneration, sourceMask(sourceBase),
		func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
			return sourceApplyResult{}
		})
	if converged.AppliedGeneration != before.DesiredGeneration || converged.Stale {
		t.Fatalf("resource recovery did not converge: %+v", converged)
	}
	assertResourceReceipt(t, logs.String(), 3, false, false)
}

func assertResourceReceipt(t *testing.T, logs string, count int, iface, address bool) {
	t.Helper()
	rows := strings.Split(strings.TrimSpace(logs), "\n")
	if len(rows) != count {
		t.Errorf("receipt count = %d, want %d; logs=%s", len(rows), count, logs)
	}
	var latest struct {
		Base struct {
			InterfacePending bool `json:"interface_pending"`
			AddressPending   bool `json:"address_pending"`
		} `json:"base"`
	}
	if err := json.Unmarshal([]byte(rows[len(rows)-1]), &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Base.InterfacePending != iface || latest.Base.AddressPending != address {
		t.Errorf("resource classes = interface:%t address:%t, want %t/%t; logs=%s",
			latest.Base.InterfacePending, latest.Base.AddressPending, iface, address, logs)
	}
}
