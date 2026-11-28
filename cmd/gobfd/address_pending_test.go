package main

import (
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// A configured sender whose local address is not assigned yet reports a
// typed unavailable address, which reconciliation keeps pending.
func TestDeclarativeSenderUnassignedAddressIsTyped(t *testing.T) {
	t.Parallel()

	local := netip.MustParseAddr("192.0.2.77") // TEST-NET-1, never assigned here.
	sf := newUDPSenderFactory(config.SocketConfig{})
	_, err := declarativeSenderLeaseFactoryFor(sf, local, true, slog.New(slog.DiscardHandler))()
	if !errors.Is(err, syscall.EADDRNOTAVAIL) && !errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Skipf("unassigned-address bind did not fail with EADDRNOTAVAIL here: %v", err)
	}
	resource, ok := bfd.UnavailableResource(err)
	if !ok || resource.Kind != bfd.ResourceKindAddress || resource.ID != local.String() {
		t.Fatalf("sender error = %v (resource %+v), want unavailable address %s", err, resource, local)
	}
}

func TestAddressEventSelectsEverySource(t *testing.T) {
	t.Parallel()

	coordinator, _ := newConvergedReadinessCoordinator(t)
	for _, ev := range []netio.InterfaceEvent{{Address: true, IfIndex: 3}, {Resync: true}} {
		if _, selected := coordinator.interfaceEventSelection(ev); selected != allReconciliationSources {
			t.Errorf("event %+v selected %08b, want every source", ev, selected)
		}
	}
}

// An address event carries no link state: it must not take sessions on the
// named interface down.
func TestAddressEventDoesNotFlapSessions(t *testing.T) {
	t.Parallel()

	logs := &lockedBuffer{}
	mgr := bfd.NewManager(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(mgr.Close)
	if _, err := mgr.CreateSession(t.Context(), bfd.SessionConfig{
		PeerAddr: netip.MustParseAddr("192.0.2.9"), LocalAddr: netip.MustParseAddr("192.0.2.1"),
		Interface: "eth9", Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
	}, candidateTestSenderLeaseFactory()); err != nil {
		t.Fatal(err)
	}
	coordinator, _ := newConvergedReadinessCoordinator(t)
	events := make(chan netio.InterfaceEvent, 1)
	events <- netio.InterfaceEvent{Address: true, IfName: "eth9", IfIndex: 9}
	close(events)
	queue := newInterfaceEventCoalescer()
	consumeInterfaceEvents(t.Context(), events, mgr, coordinator, queue, slog.New(slog.DiscardHandler))
	if strings.Contains(logs.String(), "interface link down") {
		t.Fatal("address event took sessions down")
	}
	if _, selected := queue.take(); selected != allReconciliationSources {
		t.Fatalf("queued sources = %08b, want every source", selected)
	}
}
