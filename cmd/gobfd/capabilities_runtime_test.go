package main

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/server"
)

func allCapabilitiesEnabled() server.CapabilitySnapshot {
	state := server.CapabilityRuntime{Known: true, Enabled: true}
	return server.CapabilitySnapshot{Unsolicited: state, Echo: state, MicroBFD: state, VXLAN: state, Geneve: state}
}

func newCapabilityRuntimeFixture(t *testing.T) *reconciliationCoordinator {
	t.Helper()
	c, _ := newConvergedReadinessCoordinator(t)
	c.readiness.beginCapabilities(allCapabilitiesEnabled())
	c.readiness.setSources(allReconciliationSources)
	opener := newFakeListenerOpener()
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth1", 1), upInterface("eth2", 2))
	c.listeners, _ = newTestListenerOwner(t, c.readiness, opener, inventory)
	return c
}

func TestCapabilityStatusRequiresActualProfileReceiver(t *testing.T) {
	t.Parallel()
	c := newCapabilityRuntimeFixture(t)
	initial := c.CapabilitySnapshot()
	if initial.Unsolicited.Ready || initial.Echo.Ready || initial.MicroBFD.Ready ||
		initial.VXLAN.Ready || initial.Geneve.Ready {
		t.Fatal("enabled and reconciled configuration fabricated receive readiness")
	}
	control := singleHopListener("192.0.2.1", "eth1")
	if err := c.listeners.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{control}); err != nil {
		t.Fatal(err)
	}
	if c.CapabilitySnapshot().Unsolicited.Ready {
		t.Fatal("ordinary control listener was treated as unsolicited RX")
	}
	if err := c.listeners.acquire(claimUnsolicited, []netio.ListenerConfig{control}); err != nil {
		t.Fatal(err)
	}
	if !c.CapabilitySnapshot().Unsolicited.Ready {
		t.Fatal("claimed unsolicited listener is not ready")
	}
	echo := echoListener("192.0.2.2", "eth1")
	micro := netio.ListenerConfig{Addr: control.Addr, IfName: "eth2", Port: netio.PortMicroBFD}
	if err := c.listeners.acquire(sourceClaim(sourceEcho), []netio.ListenerConfig{echo}); err != nil {
		t.Fatal(err)
	}
	if err := c.listeners.acquire(sourceClaim(sourceMicro), []netio.ListenerConfig{micro}); err != nil {
		t.Fatal(err)
	}
	got := c.CapabilitySnapshot()
	if !got.Echo.Ready || !got.MicroBFD.Ready || got.VXLAN.Ready || got.Geneve.Ready {
		t.Fatalf("profile readiness = %+v", got)
	}
	c.readiness.setNotReady(componentEchoBindings)
	got = c.CapabilitySnapshot()
	if got.Echo.Ready || !got.MicroBFD.Ready || !got.Unsolicited.Ready {
		t.Fatalf("Echo binding changed unrelated profile readiness: %+v", got)
	}
	c.readiness.setSources(allReconciliationSources &^ sourceMask(sourceBase))
	if c.CapabilitySnapshot().Unsolicited.Ready {
		t.Fatal("failed base source retained unsolicited readiness")
	}
	c.readiness.setNotReady(componentRuntime)
	if c.CapabilitySnapshot().MicroBFD.Ready {
		t.Fatal("runtime loss retained readiness")
	}
}

func TestCapabilityStatusOverlayKindAndClose(t *testing.T) {
	t.Parallel()
	c := newCapabilityRuntimeFixture(t)
	opener := newFakeOverlayOpener()
	c.overlays, _ = newTestOverlayOwner(t, c.readiness, opener)
	vxlan := testVXLANScope("192.0.2.1", 2)
	if _, err := acquireAll(c.overlays, bfd.TransportScopeVXLAN, []bfd.TransportScope{vxlan}); err != nil {
		t.Fatal(err)
	}
	got := c.CapabilitySnapshot()
	if !got.VXLAN.Ready || got.Geneve.Ready {
		t.Fatalf("one overlay kind qualified another: %+v", got)
	}
	geneve := vxlan
	geneve.Kind = bfd.TransportScopeGeneve
	geneve.Owner = bfd.GeneveReconciliationOwner().ID
	if _, err := acquireAll(c.overlays, bfd.TransportScopeGeneve, []bfd.TransportScope{geneve}); err != nil {
		t.Fatal(err)
	}
	if !c.CapabilitySnapshot().Geneve.Ready {
		t.Fatal("owned Geneve backend is not ready")
	}
	c.readiness.setSources(allReconciliationSources &^ sourceMask(sourceGeneve))
	got = c.CapabilitySnapshot()
	if !got.VXLAN.Ready || got.Geneve.Ready {
		t.Fatal("Geneve reconciliation changed another overlay kind")
	}
	c.overlays.close()
	if c.CapabilitySnapshot().VXLAN.Ready {
		t.Fatal("closed overlay retained readiness")
	}
}

func TestCapabilityStatusDefaultsRemainDisabled(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	cfg := config.DefaultConfig()
	c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	if err := c.reconcile(t.Context(), cfg, mgr, newNthFailureDeclarativeSenderFactory(0), nil); err != nil {
		t.Fatal(err)
	}
	snapshot := c.CapabilitySnapshot()
	states := []server.CapabilityRuntime{
		snapshot.Unsolicited, snapshot.Echo, snapshot.MicroBFD, snapshot.VXLAN, snapshot.Geneve,
	}
	for _, state := range states {
		if !state.Known || state.Enabled || state.Ready {
			t.Fatalf("default preview must remain disabled/not_ready: %+v", state)
		}
	}
}

func TestCapabilityStatusDoesNotWaitForReconciliation(t *testing.T) {
	t.Parallel()
	c := newCapabilityRuntimeFixture(t)
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	// The RPC must remain readable while an apply is pending.
	snapshots := make(chan server.CapabilitySnapshot, 1)
	go func() { snapshots <- c.CapabilitySnapshot() }()
	ctx, cancel := context.WithTimeout(t.Context(), readinessTestWait)
	defer cancel()
	select {
	case snapshot := <-snapshots:
		if !snapshot.Echo.Enabled {
			t.Fatal("pending apply hid the last validated enablement")
		}
	case <-ctx.Done():
		t.Fatal("capability status waited for reconciliation")
	}
}

func TestCapabilityStatusConcurrentGenerationSnapshots(t *testing.T) {
	t.Parallel()
	c := newCapabilityRuntimeFixture(t)
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 100 {
			c.readiness.beginCapabilities(server.CapabilitySnapshot{})
			c.readiness.beginCapabilities(allCapabilitiesEnabled())
			c.readiness.setSources(allReconciliationSources)
		}
	})
	for range 100 {
		snapshot := c.CapabilitySnapshot()
		if snapshot.Echo.Ready || snapshot.Unsolicited.Ready || snapshot.VXLAN.Ready {
			t.Error("concurrent generation fabricated a missing receive path")
		}
	}
	workers.Wait()
}

func TestCapabilityStatusFlagsFollowValidatedReload(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	cfg := config.DefaultConfig()
	c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	if c.CapabilitySnapshot().Echo.Known {
		t.Fatal("startup fabricated a validated generation")
	}
	for _, enabled := range []bool{false, true, false} {
		cfg.Echo.Enabled = enabled
		if err := c.reconcile(t.Context(), cfg, mgr, newNthFailureDeclarativeSenderFactory(0), nil); err != nil {
			t.Fatal(err)
		}
		got := c.CapabilitySnapshot()
		if !got.Echo.Known || got.Echo.Enabled != enabled || got.Echo.Ready {
			t.Fatalf("generation flags with missing backend: %+v", got.Echo)
		}
	}
	invalid := cloneStartupContractTestConfig(cfg)
	invalid.Echo.Enabled = true
	invalid.Sessions = []config.SessionConfig{{Peer: "invalid"}}
	if err := c.reconcile(t.Context(), invalid, mgr, newNthFailureDeclarativeSenderFactory(0), nil); err == nil {
		t.Fatal("invalid generation accepted")
	}
	if c.CapabilitySnapshot().Echo.Enabled {
		t.Fatal("invalid generation replaced enablement")
	}
}

func TestCapabilityStatusEmptyEnabledOverlaysStayEnabled(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.VXLAN.Enabled, cfg.Geneve.Enabled = true, true
	logger := slog.New(slog.DiscardHandler)
	c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	if err := c.reconcile(t.Context(), cfg, mgr, newNthFailureDeclarativeSenderFactory(0), nil); err != nil {
		t.Fatal(err)
	}
	snapshot := c.CapabilitySnapshot()
	for _, state := range []server.CapabilityRuntime{snapshot.VXLAN, snapshot.Geneve} {
		if !state.Known || !state.Enabled || state.Ready {
			t.Fatalf("enabled overlay without peers must remain enabled/not_ready: %+v", state)
		}
	}
}
