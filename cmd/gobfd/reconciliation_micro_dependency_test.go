package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func microTransactionTestCandidate(t *testing.T) compiledControlSessionCandidate {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
		LAGInterface: "bond0", MemberLinks: []string{"eth0", "eth1"},
		PeerAddr: "192.0.2.244", LocalAddr: "127.0.0.1", MinActiveLinks: 1,
	}}
	candidate, err := compileControlSessionCandidate(cfg, &overlayRuntime{})
	if err != nil {
		t.Fatalf("compileControlSessionCandidate: %v", err)
	}
	return candidate
}

// The Micro-BFD source waits for its LAG and member links together and then
// commits each group with its member sessions; a pending interface leaves
// neither a group nor a member session behind.
func TestReconciliationCoordinatorMicroSourceIsOneTransaction(t *testing.T) {
	t.Parallel()

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	coordinator := newReconciliationCoordinator(config.DefaultConfig(), slog.New(slog.DiscardHandler), nil)
	sf := newNthFailureDeclarativeSenderFactory(0)
	var checked []string
	applyWith := func(available bool) sourceApplyFunc {
		return func(ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate) sourceApplyResult {
			if source != sourceMicro {
				return sourceApplyResult{}
			}
			return applyCompiledSourceWithInterfaceChecker(ctx, source, candidate, mgr, sf,
				slog.New(slog.DiscardHandler), func(names []string) (int, error) {
					checked = names
					if available {
						return 0, nil
					}
					return 1, testUnavailableResourceError(t, "eth1")
				})
		}
	}

	pending := coordinator.applyCandidate(context.Background(), microTransactionTestCandidate(t), applyWith(false))
	if got := receiptForSource(t, pending.LastReceipt, sourceMicro); got.Pending != 1 || got.Failed != 0 {
		t.Fatalf("pending Micro-BFD receipt = %+v, want pending=1", got)
	}
	if want := []string{"bond0", "eth0", "eth1"}; len(checked) != len(want) ||
		checked[0] != want[0] || checked[1] != want[1] || checked[2] != want[2] {
		t.Fatalf("Micro-BFD preflight interfaces = %v, want %v", checked, want)
	}
	if got := len(mgr.MicroBFDGroups()) + len(mgr.Sessions()); got != 0 {
		t.Fatalf("groups plus sessions while pending = %d, want 0", got)
	}

	converged := coordinator.retryPendingSources(
		context.Background(), pending.DesiredGeneration, sourceMask(sourceMicro), applyWith(true),
	)
	if converged.Stale || receiptForSource(t, converged.LastReceipt, sourceMicro).Created != 1 {
		t.Fatalf("Micro-BFD retry snapshot = %+v, want one committed group", converged)
	}
	if len(mgr.MicroBFDGroups()) != 1 || len(mgr.Sessions()) != 2 {
		t.Fatalf("groups/sessions = %d/%d, want 1/2", len(mgr.MicroBFDGroups()), len(mgr.Sessions()))
	}
}
