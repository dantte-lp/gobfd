package main

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"sync"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// A source bit is durable until taken; the capacity-one channel only wakes the worker.
type interfaceEventCoalescer struct {
	mu         sync.Mutex
	generation uint64
	selected   reconciliationSourceMask
	wake       chan struct{}
}

func newInterfaceEventCoalescer() *interfaceEventCoalescer {
	return &interfaceEventCoalescer{wake: make(chan struct{}, 1)}
}

func (q *interfaceEventCoalescer) add(generation uint64, selected reconciliationSourceMask) {
	if selected == 0 {
		return
	}
	q.mu.Lock()
	if generation < q.generation {
		q.mu.Unlock()
		return
	}
	if generation > q.generation {
		q.generation, q.selected = generation, 0
	}
	q.selected |= selected
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *interfaceEventCoalescer) take() (uint64, reconciliationSourceMask) {
	q.mu.Lock()
	defer q.mu.Unlock()
	generation, selected := q.generation, q.selected
	q.selected = 0
	return generation, selected
}

func (c *reconciliationCoordinator) interfaceEventSelection(
	ev netio.InterfaceEvent,
) (uint64, reconciliationSourceMask) {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	if ev.Resync || ev.Address {
		// Any source may wait for an address, and a resync may follow a lost
		// address event; retries skip sources that are not pending.
		return c.snapshot.DesiredGeneration, allReconciliationSources
	}
	var selected reconciliationSourceMask
	if ev.IfName != "" {
		selected = c.unboundSources
	}
	for i, dependencies := range c.interfaceDependencies {
		if ev.IfName != "" && slices.Contains(dependencies, ev.IfName) {
			selected |= sourceMask(reconciliationSource(i))
		}
	}
	return c.snapshot.DesiredGeneration, selected
}

func (c *reconciliationCoordinator) runInterfaceReconciliation(
	ctx context.Context, queue *interfaceEventCoalescer,
	checkInterfaces interfaceAvailabilityChecker, applySource sourceApplyFunc,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-queue.wake:
			if ctx.Err() != nil {
				return
			}
			generation, selected := queue.take()
			if selected != 0 {
				c.reconcileInterfaceSources(ctx, generation, selected, checkInterfaces, applySource)
			}
			if !ok {
				return
			}
		}
	}
}

func (c *reconciliationCoordinator) reconcileInterfaceSources(
	ctx context.Context, expectedGeneration uint64, selected reconciliationSourceMask,
	checkInterfaces interfaceAvailabilityChecker, applySource sourceApplyFunc,
) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	snapshot := c.Snapshot()
	if ctx.Err() != nil || !c.hasRetainedCandidate || snapshot.DesiredGeneration != expectedGeneration ||
		snapshot.LastReceipt.Generation != expectedGeneration {
		return
	}
	// Coalesced hints may end with Up after Down; current inventory is authoritative.
	c.reconcileUnavailableInterfaceLocked(snapshot, selected, checkInterfaces)
	c.retryPendingSourcesLocked(ctx, expectedGeneration, selected,
		func(ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate) sourceApplyResult {
			if result := preflightCompiledSourceInterfaces(source, candidate, checkInterfaces); result.Err != nil {
				return result
			}
			return applySource(ctx, source, candidate)
		})
}

func consumeInterfaceEvents(
	ctx context.Context, events <-chan netio.InterfaceEvent, mgr *bfd.Manager,
	coordinator *reconciliationCoordinator, queue *interfaceEventCoalescer, logger *slog.Logger,
) {
	defer close(queue.wake)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			switch {
			case ev.Resync:
				resyncInterfacePaths(ctx, mgr, logger)
			case ev.Address:
				// An address change carries no link state; it only retries
				// sources waiting for an address.
			default:
				mgr.HandleInterfaceEvent(ev.IfName, ev.Up)
			}
			// Bound listener interfaces are rebound and re-checked against the current
			// inventory; a coalesced or lost event cannot leave a false SERVING.
			if err := coordinator.checkListenerBindings(net.Interfaces); err != nil {
				logger.Warn("listener interface check failed; readiness unchanged",
					slog.String("error", err.Error()))
			}
			generation, selected := coordinator.interfaceEventSelection(ev)
			queue.add(generation, selected)
		}
	}
}

func resyncInterfacePaths(ctx context.Context, mgr *bfd.Manager, logger *slog.Logger) {
	interfaces, err := net.Interfaces()
	if err != nil {
		logger.Warn("failed to resynchronize interface paths", slog.String("error", err.Error()))
		return
	}
	available := make(map[string]bool, len(interfaces))
	for _, iface := range interfaces {
		available[iface.Name] = iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagRunning != 0
	}
	unavailable := make(map[string]struct{})
	for _, session := range mgr.Sessions() {
		if session.Interface != "" && !available[session.Interface] {
			unavailable[session.Interface] = struct{}{}
		}
	}
	for ifName := range unavailable {
		if ctx.Err() != nil {
			return
		}
		mgr.HandleInterfaceEvent(ifName, false)
	}
}
