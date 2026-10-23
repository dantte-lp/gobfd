package main

import (
	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/server"
)

// CapabilitySnapshot observes receive owners without waiting for reconciliation.
// Lock order matches owner publication: listeners, overlays, then readiness.
func (c *reconciliationCoordinator) CapabilitySnapshot() server.CapabilitySnapshot {
	if c.readiness == nil {
		return server.CapabilitySnapshot{}
	}
	if c.listeners != nil {
		c.listeners.mu.Lock()
		defer c.listeners.mu.Unlock()
	}
	if c.overlays != nil {
		c.overlays.mu.Lock()
		defer c.overlays.mu.Unlock()
	}
	c.readiness.mu.Lock()
	defer c.readiness.mu.Unlock()

	unsolicited, echo, micro := c.listeners.capabilityReceiversLocked()
	vxlan, geneve := c.overlays.capabilityBackendsLocked()
	r := c.readiness
	snapshot := r.capabilities
	snapshot.Unsolicited = r.capabilityReadyLocked(
		snapshot.Unsolicited, sourceBase, 1<<componentControlBindings, unsolicited)
	snapshot.Echo = r.capabilityReadyLocked(snapshot.Echo, sourceEcho, 1<<componentEchoBindings, echo)
	snapshot.MicroBFD = r.capabilityReadyLocked(snapshot.MicroBFD, sourceMicro, 1<<componentMicroBindings, micro)
	snapshot.VXLAN = r.capabilityReadyLocked(snapshot.VXLAN, sourceVXLAN, 0, vxlan)
	snapshot.Geneve = r.capabilityReadyLocked(snapshot.Geneve, sourceGeneve, 0, geneve)
	return snapshot
}

func (r *daemonReadiness) capabilityReadyLocked(
	state server.CapabilityRuntime, source reconciliationSource, bindings readinessMask, present bool,
) server.CapabilityRuntime {
	needs := readinessMask(1<<source) | 1<<componentRuntime | bindings
	state.Ready = state.Known && state.Enabled && present && r.ready&needs == needs
	return state
}

func (o *listenerOwner) capabilityReceiversLocked() (bool, bool, bool) {
	if o == nil || o.closed {
		return false, false, false
	}
	var unsolicited, echo, micro bool
	for key, owned := range o.listeners {
		if owned.ln == nil || owned.unclaimed() {
			continue
		}
		switch key.port {
		case netio.PortSingleHop, netio.PortMultiHop:
			unsolicited = unsolicited || owned.claims&claimUnsolicited != 0
		case netio.PortEcho:
			echo = true
		case netio.PortMicroBFD:
			micro = true
		}
	}
	return unsolicited, echo, micro
}

func (o *overlayOwner) capabilityBackendsLocked() (bool, bool) {
	if o == nil || o.closed {
		return false, false
	}
	var vxlan, geneve bool
	for key, backend := range o.backends {
		if backend == nil || backend.conn == nil || len(backend.scopes) == 0 {
			continue
		}
		switch key.kind {
		case bfd.TransportScopeVXLAN:
			vxlan = true
		case bfd.TransportScopeGeneve:
			geneve = true
		default:
			// Other transport kinds cannot establish an overlay receive path.
		}
	}
	return vxlan, geneve
}
