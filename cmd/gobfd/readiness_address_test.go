package main

import (
	"net/netip"
	"testing"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestListenerAddressLossGatesUnboundReadiness(t *testing.T) {
	t.Parallel()
	coordinator, checker := newConvergedReadinessCoordinator(t)
	inventory := &fakeInterfaceInventory{}
	opener := newFakeListenerOpener()
	owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
	available := true
	coordinator.readiness.checkAddresses = func(claims []netio.LocalAddress) (int, error) {
		for _, claim := range claims {
			if claim.Addr == netip.MustParseAddr("192.0.2.1") && !available {
				return 1, bfd.NewResourceUnavailableError(bfd.ResourceRef{Kind: bfd.ResourceKindAddress, ID: "192.0.2.1"})
			}
		}
		return 0, nil
	}
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{{Local: "192.0.2.1", Peer: "192.0.2.2", Type: "multi_hop"}}
	if err := owner.openStartup(cfg); err != nil {
		t.Fatal(err)
	}
	assertReadiness(t, checker, readinessAllServing())
	available = false
	if err := owner.checkBindings(inventory.list); err != nil {
		t.Fatal(err)
	}
	notReady := readinessAllServing()
	notReady.aggregate = grpchealth.StatusNotServing
	notReady.bfd = grpchealth.StatusNotServing
	assertReadiness(t, checker, notReady)
	available = true
	if err := owner.checkBindings(inventory.list); err != nil {
		t.Fatal(err)
	}
	assertReadiness(t, checker, readinessAllServing())
}
