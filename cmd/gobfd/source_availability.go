package main

import (
	"context"
	"fmt"
	"net"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func (c *reconciliationCoordinator) preflightSourceResources(
	source reconciliationSource, candidate compiledControlSessionCandidate, checkInterfaces interfaceAvailabilityChecker,
) sourceApplyResult {
	if int(source) >= sourceCount {
		return failedSourceResult(bfd.ReconcileErrorInvalid, errUnknownReconciliationSource)
	}
	if result := preflightCompiledSourceInterfaces(source, candidate, checkInterfaces); result.Err != nil {
		return result
	}
	if c.checkAddresses == nil {
		return sourceApplyResult{}
	}
	var addresses []netio.LocalAddress
	if source == sourceVXLAN || source == sourceGeneve {
		overlay := candidate.overlays[0]
		if source == sourceGeneve {
			overlay = candidate.overlays[1]
		}
		for _, desired := range overlay.desired {
			addresses = append(addresses, netio.LocalAddress{Addr: desired.SessionConfig.TransportScope.OuterLocalAddr})
		}
	} else {
		for _, listener := range candidate.listeners[source] {
			addresses = append(addresses, netio.LocalAddress{Addr: listener.Addr, Interface: listener.IfName})
		}
	}
	pending, err := c.checkAddresses(addresses)
	if pending < 0 || pending > len(addresses) || (err == nil && pending != 0) {
		return failedSourceResult(bfd.ReconcileErrorCreate,
			fmt.Errorf("preflight %s local address count %d: %w", source, pending, errInvalidInterfacePreflightResult))
	}
	if err != nil {
		return resourceErrorSourceResult(pending, fmt.Errorf("preflight %s local addresses: %w", source, err))
	}
	return sourceApplyResult{}
}

func (c *reconciliationCoordinator) runResourceRechecks(
	ctx context.Context, checkInterfaces interfaceAvailabilityChecker, applySource sourceApplyFunc,
) {
	recheckBindings(ctx, bindingRecheckInterval, func() error {
		bindingErr := c.checkListenerBindings(net.Interfaces)
		generation := c.Snapshot().DesiredGeneration
		c.reconcileInterfaceSources(ctx, generation, allReconciliationSources, checkInterfaces, applySource)
		return bindingErr
	}, c.logger)
}
