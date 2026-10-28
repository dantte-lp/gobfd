package main

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestPeriodicOverlayResourceRecheckWithoutMonitor(t *testing.T) {
	for _, tc := range []struct {
		source    reconciliationSource
		permanent bool
	}{
		{sourceVXLAN, false}, {sourceVXLAN, true}, {sourceGeneve, false}, {sourceGeneve, true},
	} {
		name := tc.source.String() + "/unavailable recovers"
		if tc.permanent {
			name = tc.source.String() + "/inventory error needs reload"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				c := newReconciliationCoordinator(config.DefaultConfig(), slog.New(slog.DiscardHandler), newDaemonHealthChecker())
				candidate := compiledControlSessionCandidate{}
				index, kind := 0, bfd.TransportScopeVXLAN
				if tc.source == sourceGeneve {
					index, kind = 1, bfd.TransportScopeGeneve
				}
				candidate.overlays[index].desired = []bfd.ReconcileConfig{{SessionConfig: bfd.SessionConfig{
					TransportScope: bfd.TransportScope{
						Kind: kind, OuterLocalAddr: netip.MustParseAddr("192.0.2.1"),
					},
				}}}
				initial := c.applyCandidate(ctx, candidate,
					func(context.Context, reconciliationSource, compiledControlSessionCandidate) sourceApplyResult {
						return sourceApplyResult{}
					})
				var healthy atomic.Bool
				c.checkAddresses = func(claims []netio.LocalAddress) (int, error) {
					if len(claims) == 0 || healthy.Load() {
						return 0, nil
					}
					if tc.permanent {
						return 0, errors.New("inventory interrupted")
					}
					return 1, bfd.NewResourceUnavailableError(bfd.ResourceRef{Kind: bfd.ResourceKindAddress, ID: "192.0.2.1"})
				}
				applied := 0
				apply := func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
					if source == tc.source {
						applied++
						return sourceApplyResult{Updated: 1}
					}
					return sourceApplyResult{}
				}
				done := make(chan struct{})
				go func() { defer close(done); c.runResourceRechecks(ctx, netio.CheckInterfacesAvailable, apply) }()
				synctest.Sleep(bindingRecheckInterval)
				failed := c.Snapshot()
				wantPending, wantFailed := 1, 0
				if tc.permanent {
					wantPending, wantFailed = 0, 1
				}
				if !failed.Stale || failed.Pending != wantPending || failed.Failed != wantFailed || applied != 0 {
					t.Fatalf("resource loss not reflected without events: %+v, applies=%d", failed, applied)
				}
				notReady := readinessAllServing()
				notReady.aggregate, notReady.bfd = grpchealth.StatusNotServing, grpchealth.StatusNotServing
				assertReadiness(t, c.readiness.checker, notReady)
				if tc.permanent && failed.LastReceipt.Sources[tc.source].Errors[bfd.ReconcileErrorCreate] != 1 {
					t.Fatal("inventory failure lost its create error code")
				}
				healthy.Store(true)
				synctest.Sleep(bindingRecheckInterval)
				after := c.Snapshot()
				if tc.permanent {
					if after != failed || applied != 0 {
						t.Fatalf("failed source retried: %+v", after)
					}
					after = c.applyCandidate(ctx, candidate, apply)
					if after.DesiredGeneration != initial.DesiredGeneration+1 {
						t.Fatal("reload did not advance generation")
					}
				} else if after.DesiredGeneration != initial.DesiredGeneration {
					t.Fatal("automatic recovery changed desired generation")
				}
				if after.Stale || after.Failed != 0 || after.Pending != 0 || applied != 1 {
					t.Fatalf("resource not recovered: %+v", after)
				}
				if after.LastReceipt.Sources[tc.source].Updated != 1 {
					t.Fatal("recovery lost the source update receipt")
				}
				assertReadiness(t, c.readiness.checker, readinessAllServing())
				cancel()
				<-done
			})
		})
	}
}
