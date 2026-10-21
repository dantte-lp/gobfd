package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// fakeListenerOpener stands in for netio.NewListener; each listener blocks in
// a read until it is closed, as a real socket does.
type fakeListenerOpener struct {
	mu     sync.Mutex
	conns  map[listenerKey]*fakeListenerConn
	opened []listenerKey
	fail   map[string]error // by interface name
	onOpen func(netio.ListenerConfig)
}

func newFakeListenerOpener() *fakeListenerOpener {
	return &fakeListenerOpener{conns: make(map[listenerKey]*fakeListenerConn), fail: make(map[string]error)}
}

func (f *fakeListenerOpener) open(cfg netio.ListenerConfig) (*netio.Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onOpen != nil {
		f.onOpen(cfg)
	}
	if err := f.fail[cfg.IfName]; err != nil {
		return nil, err
	}
	conn := newFakeListenerConn()
	key := listenerKeyOf(cfg)
	f.conns[key] = conn
	f.opened = append(f.opened, key)
	return netio.NewListenerFromConn(conn, cfg.MultiHop), nil
}

func (f *fakeListenerOpener) conn(t *testing.T, key listenerKey) *fakeListenerConn {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	conn, ok := f.conns[key]
	if !ok {
		t.Fatalf("listener %+v was never opened", key)
	}
	return conn
}

func (f *fakeListenerOpener) openedKeys() []listenerKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.opened)
}

func fakeConnClosed(conn *fakeListenerConn) bool {
	select {
	case <-conn.closed:
		return true
	default:
		return false
	}
}

func newTestListenerOwner(
	t *testing.T, readiness *daemonReadiness, opener *fakeListenerOpener, inventory *fakeInterfaceInventory,
) (*listenerOwner, *bfd.Manager) {
	t.Helper()
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	owner := newListenerOwner(mgr, readiness, slog.New(slog.DiscardHandler))
	owner.open = opener.open
	owner.interfaces = inventory.list
	t.Cleanup(owner.close)
	return owner, mgr
}

func singleHopListener(addr, ifName string) netio.ListenerConfig {
	return netio.ListenerConfig{Addr: netip.MustParseAddr(addr), IfName: ifName, Port: netio.PortSingleHop}
}

func echoListener(addr, ifName string) netio.ListenerConfig {
	return netio.ListenerConfig{
		Addr: netip.MustParseAddr(addr), IfName: ifName, Port: netio.PortEcho, ExpectedTTL: netio.TTLEchoLoopback,
	}
}

func TestListenerOwnerReleaseClosesOnlyUnclaimedListeners(t *testing.T) {
	t.Parallel()

	coordinator, checker := newConvergedReadinessCoordinator(t)
	opener := newFakeListenerOpener()
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth1", 1), upInterface("eth2", 2), upInterface("eth6", 6))
	owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- owner.run(ctx) }()

	wildcard := singleHopListener("0.0.0.0", "eth6")
	baseA, baseB := singleHopListener("192.0.2.1", "eth1"), singleHopListener("192.0.2.2", "eth2")
	echo := echoListener("192.0.2.1", "eth1")
	for _, step := range []struct {
		claims  listenerClaims
		configs []netio.ListenerConfig
	}{
		{claimUnsolicited, []netio.ListenerConfig{wildcard}},
		{sourceClaim(sourceBase), []netio.ListenerConfig{baseA, baseB, baseA}},
		{sourceClaim(sourceEcho), []netio.ListenerConfig{echo}},
	} {
		if err := owner.acquire(ctx, step.claims, step.configs); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(opener.openedKeys()); got != 4 {
		t.Fatalf("opened listeners = %d, want 4 (duplicate claim reuses the socket)", got)
	}

	owner.release(sourceClaim(sourceBase), []netio.ListenerConfig{baseA})
	for key, wantClosed := range map[listenerKey]bool{
		listenerKeyOf(wildcard): false, listenerKeyOf(baseA): false,
		listenerKeyOf(baseB): true, listenerKeyOf(echo): false,
	} {
		if got := fakeConnClosed(opener.conn(t, key)); got != wantClosed {
			t.Errorf("listener %+v closed = %v, want %v", key, got, wantClosed)
		}
	}

	owner.release(sourceClaim(sourceBase), nil)
	if !fakeConnClosed(opener.conn(t, listenerKeyOf(baseA))) {
		t.Error("base listener survived the empty base desired set")
	}
	if fakeConnClosed(opener.conn(t, listenerKeyOf(wildcard))) {
		t.Error("startup-owned unsolicited listener closed by a base release")
	}
	if got := len(opener.openedKeys()); got != 4 {
		t.Fatalf("opened listeners after release = %d, want 4 (no replacement)", got)
	}

	// Deliberate closes are not receiver loss.
	assertReadiness(t, checker, readinessAllServing())
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("owner run error = %v, want nil after deliberate closes", err)
		}
	case <-time.After(readinessTestWait):
		t.Fatal("owner run did not stop")
	}
}

func TestListenerOwnerReceiverLossFailStops(t *testing.T) {
	t.Parallel()

	for _, cfg := range []netio.ListenerConfig{
		singleHopListener("192.0.2.1", "eth1"),
		echoListener("192.0.2.1", "eth1"),
		{Addr: netip.MustParseAddr("192.0.2.1"), IfName: "eth1", Port: netio.PortMicroBFD},
	} {
		t.Run(strconv.FormatUint(uint64(cfg.Port), 10), func(t *testing.T) {
			t.Parallel()
			coordinator, checker := newConvergedReadinessCoordinator(t)
			opener := newFakeListenerOpener()
			inventory := &fakeInterfaceInventory{}
			inventory.set(nil, upInterface("eth1", 1))
			owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
			if err := owner.acquire(t.Context(), sourceClaim(sourceBase), []netio.ListenerConfig{cfg}); err != nil {
				t.Fatal(err)
			}
			if err := opener.conn(t, listenerKeyOf(cfg)).Close(); err != nil {
				t.Fatal(err)
			}

			result := make(chan error, 1)
			go func() { result <- owner.run(t.Context()) }()
			select {
			case err := <-result:
				if !errors.Is(err, errRequiredWorkerStopped) || !errors.Is(err, netio.ErrListenerClosed) {
					t.Fatalf("receiver loss error = %v, want %v and %v",
						err, errRequiredWorkerStopped, netio.ErrListenerClosed)
				}
			case <-time.After(readinessTestWait):
				t.Fatal("receiver loss did not fail the owner")
			}
			notServing := grpchealth.StatusNotServing
			assertReadiness(t, checker, readinessWant{notServing, notServing, notServing, notServing})
		})
	}
}

func TestListenerOwnerKeepsListenerOfLiveSession(t *testing.T) {
	t.Parallel()

	local := netip.MustParseAddr("192.0.2.1")
	for _, tc := range []struct {
		name   string
		source reconciliationSource
		cfg    netio.ListenerConfig
		create func(*testing.T, *bfd.Manager)
	}{
		{"control", sourceBase, singleHopListener("192.0.2.1", "eth1"), func(t *testing.T, mgr *bfd.Manager) {
			t.Helper()
			if _, err := mgr.CreateSession(t.Context(), bfd.SessionConfig{
				PeerAddr: netip.MustParseAddr("192.0.2.9"), LocalAddr: local, Interface: "eth1",
				Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
				DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
			}, candidateTestSenderLeaseFactory()); err != nil {
				t.Fatal(err)
			}
		}},
		{"echo", sourceEcho, echoListener("192.0.2.1", "eth1"), func(t *testing.T, mgr *bfd.Manager) {
			t.Helper()
			if _, err := mgr.CreateEchoSession(t.Context(), bfd.EchoSessionConfig{
				PeerAddr: netip.MustParseAddr("192.0.2.9"), LocalAddr: local, Interface: "eth1",
				TxInterval: time.Hour, DetectMultiplier: 3,
			}, candidateTestSender{}); err != nil {
				t.Fatal(err)
			}
		}},
		{
			"micro", sourceMicro,
			netio.ListenerConfig{Addr: local, IfName: "eth1", Port: netio.PortMicroBFD},
			func(t *testing.T, mgr *bfd.Manager) {
				t.Helper()
				if _, err := mgr.CreateMicroBFDGroup(bfd.MicroBFDConfig{
					LAGInterface: "bond9", MemberLinks: []string{"eth1"},
					PeerAddr: netip.MustParseAddr("192.0.2.9"), LocalAddr: local,
					DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second,
					DetectMultiplier: 3, MinActiveLinks: 1,
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _ := newConvergedReadinessCoordinator(t)
			opener := newFakeListenerOpener()
			inventory := &fakeInterfaceInventory{}
			inventory.set(nil, upInterface("eth1", 1))
			owner, mgr := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
			if err := owner.acquire(t.Context(), sourceClaim(tc.source), []netio.ListenerConfig{tc.cfg}); err != nil {
				t.Fatal(err)
			}
			// An API-created user still receives on the listener the config opened.
			tc.create(t, mgr)
			owner.release(sourceClaim(tc.source), nil)
			if fakeConnClosed(opener.conn(t, listenerKeyOf(tc.cfg))) {
				t.Fatal("release closed the listener of a live API user")
			}
		})
	}
}

func TestListenerOwnerClaimFailureSkipsApplyAndRollsBack(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                    string
		err                     error
		wantPending, wantFailed int
	}{
		{"absent interface is pending", fmt.Errorf("set SO_BINDTODEVICE(eth3): %w", syscall.ENODEV), 1, 0},
		{"other bind error is failed", fmt.Errorf("listen UDP: %w", syscall.EADDRNOTAVAIL), 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _ := newConvergedReadinessCoordinator(t)
			opener := newFakeListenerOpener()
			opener.fail["eth3"] = tc.err
			inventory := &fakeInterfaceInventory{}
			inventory.set(nil, upInterface("eth1", 1))
			owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
			coordinator.listeners = owner

			first := singleHopListener("192.0.2.1", "eth1")
			var candidate compiledControlSessionCandidate
			candidate.listeners[sourceBase] = []netio.ListenerConfig{first, singleHopListener("192.0.2.3", "eth3")}
			applied := false
			result := coordinator.withListeners(func(
				context.Context, reconciliationSource, compiledControlSessionCandidate,
			) sourceApplyResult {
				applied = true
				return sourceApplyResult{}
			})(t.Context(), sourceBase, candidate)

			if applied {
				t.Fatal("source applied without its receive listeners")
			}
			if result.Pending != tc.wantPending || result.Failed != tc.wantFailed || result.Err == nil {
				t.Fatalf("claim result = %+v, want pending=%d failed=%d with error",
					result, tc.wantPending, tc.wantFailed)
			}
			if tc.wantFailed != 0 && result.Errors.Count(bfd.ReconcileErrorCreate) != 1 {
				t.Fatalf("create errors = %d, want 1", result.Errors.Count(bfd.ReconcileErrorCreate))
			}
			if !fakeConnClosed(opener.conn(t, listenerKeyOf(first))) {
				t.Fatal("listener opened by the failed claim was not rolled back")
			}
		})
	}
}

func TestListenerOwnerStartupFailsOnAbsentInterface(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Unsolicited.Enabled = true
	cfg.Unsolicited.Interfaces = map[string]config.UnsolicitedInterfaceConfig{"eth6": {Enabled: true}}
	cfg.Sessions = []config.SessionConfig{{Peer: "192.0.2.2", Local: "192.0.2.1", Interface: "eth9", Type: "single_hop"}}
	coordinator, _ := newConvergedReadinessCoordinator(t)
	opener := newFakeListenerOpener()
	opener.fail["eth9"] = fmt.Errorf("set SO_BINDTODEVICE(eth9): %w", syscall.ENODEV)
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth6", 6))
	owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)

	if err := owner.openStartup(t.Context(), cfg); !errors.Is(err, bfd.ErrResourceUnavailable) {
		t.Fatalf("startup error = %v, want typed unavailable interface", err)
	}
	opened := opener.openedKeys()
	if len(opened) != 2 {
		t.Fatalf("opened before failure = %v, want both unsolicited wildcards", opened)
	}
	for _, key := range opened {
		if !fakeConnClosed(opener.conn(t, key)) {
			t.Errorf("listener %+v left open after failed startup", key)
		}
	}
}

func readinessBindings(readiness *daemonReadiness) []listenerBinding {
	readiness.mu.Lock()
	defer readiness.mu.Unlock()
	return slices.Clone(readiness.bindings)
}

func TestListenerOwnerStartupRecordsBindTimeIndexes(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{
		{Peer: "192.0.2.2", Local: "192.0.2.1", Interface: "eth9", Type: "single_hop"},
		{Peer: "198.51.100.2", Local: "198.51.100.1", Type: "multi_hop"},
	}
	cfg.Unsolicited.Enabled = true
	cfg.Unsolicited.Interfaces = map[string]config.UnsolicitedInterfaceConfig{
		"eth6": {Enabled: true}, "eth5": {Enabled: false},
	}
	cfg.Echo.Enabled = true
	// Echo binds one listener per local address with the first peer's interface.
	cfg.Echo.Peers = []config.EchoPeerConfig{
		{Peer: "192.0.2.6", Local: "192.0.2.5", Interface: "eth8"},
		{Peer: "192.0.2.9", Local: "192.0.2.5", Interface: "eth4"},
	}
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
		LAGInterface: "bond0", MemberLinks: []string{"eth7"}, PeerAddr: "192.0.2.8", LocalAddr: "192.0.2.7",
	}}
	coordinator, checker := newConvergedReadinessCoordinator(t)
	opener := newFakeListenerOpener()
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth9", 9), upInterface("eth8", 8), upInterface("eth7", 7),
		upInterface("eth6", 6), upInterface("eth4", 4))
	owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)

	if err := owner.openStartup(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	got := readinessBindings(coordinator.readiness)
	want := []listenerBinding{
		{ifName: "eth6", ifIndex: 6, component: componentControlBindings},
		{ifName: "eth9", ifIndex: 9, component: componentControlBindings},
		{ifName: "eth8", ifIndex: 8, component: componentEchoBindings},
		{ifName: "eth7", ifIndex: 7, component: componentMicroBindings},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("listener bindings = %+v, want %+v", got, want)
	}
	assertReadiness(t, checker, readinessAllServing())
}

func TestListenerOwnerStartupInventoryFailure(t *testing.T) {
	t.Parallel()

	coordinator, _ := newConvergedReadinessCoordinator(t)
	inventory := &fakeInterfaceInventory{}
	inventory.set(errInventoryUnavailable)
	bound := config.DefaultConfig()
	bound.Sessions = []config.SessionConfig{{Peer: "192.0.2.2", Local: "192.0.2.1", Interface: "eth9", Type: "single_hop"}}
	opener := newFakeListenerOpener()
	owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
	if err := owner.openStartup(t.Context(), bound); !errors.Is(err, errInventoryUnavailable) {
		t.Fatalf("bound listener inventory error = %v, want %v", err, errInventoryUnavailable)
	}
	if opened := opener.openedKeys(); len(opened) != 0 {
		t.Fatalf("opened %v without a resolved ifindex", opened)
	}

	unbound := config.DefaultConfig()
	unbound.Sessions = []config.SessionConfig{{Peer: "198.51.100.2", Local: "198.51.100.1", Type: "multi_hop"}}
	owner, _ = newTestListenerOwner(t, coordinator.readiness, newFakeListenerOpener(), inventory)
	if err := owner.openStartup(t.Context(), unbound); err != nil {
		t.Fatalf("unbound listener startup = %v, want no inventory read", err)
	}
}

func TestListenerOwnerRebindsRecreatedInterface(t *testing.T) {
	t.Parallel()

	coordinator, checker := newConvergedReadinessCoordinator(t)
	opener := newFakeListenerOpener()
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth9", 7))
	owner, _ := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
	var rebinds int
	var rebindErr error
	owner.rebind = func(*netio.Listener) error {
		rebinds++
		return rebindErr
	}
	cfg := singleHopListener("192.0.2.1", "eth9")
	if err := owner.acquire(t.Context(), sourceClaim(sourceBase), []netio.ListenerConfig{cfg}); err != nil {
		t.Fatal(err)
	}
	assertReadiness(t, checker, readinessAllServing())

	notServing, serving := grpchealth.StatusNotServing, grpchealth.StatusServing
	for _, step := range []struct {
		name        string
		interfaces  []net.Interface
		rebindErr   error
		wantRebinds int
		want        readinessWant
	}{
		{"interface deleted", nil, nil, 0, readinessWant{notServing, notServing, serving, serving}},
		{"recreated with another index", []net.Interface{upInterface("eth9", 11)}, nil, 1, readinessAllServing()},
		{"rebound index is current", []net.Interface{upInterface("eth9", 11)}, nil, 1, readinessAllServing()},
		{
			"failed rebind stays not serving",
			[]net.Interface{upInterface("eth9", 12)},
			syscall.EPERM, 2,
			readinessWant{notServing, notServing, serving, serving},
		},
		{"next check retries the rebind", []net.Interface{upInterface("eth9", 12)}, nil, 3, readinessAllServing()},
	} {
		inventory.set(nil, step.interfaces...)
		rebindErr = step.rebindErr
		if err := owner.checkBindings(inventory.list); err != nil {
			t.Fatalf("step %q: %v", step.name, err)
		}
		t.Logf("step %q", step.name)
		if rebinds != step.wantRebinds {
			t.Errorf("rebinds = %d, want %d", rebinds, step.wantRebinds)
		}
		assertReadiness(t, checker, step.want)
	}
	if got := len(opener.openedKeys()); got != 1 {
		t.Fatalf("opened listeners = %d, want 1 (rebind keeps the socket)", got)
	}

	// Closing the last device-bound listener clears a stale binding state.
	inventory.set(nil)
	if err := owner.checkBindings(inventory.list); err != nil {
		t.Fatal(err)
	}
	assertReadiness(t, checker, readinessWant{notServing, notServing, serving, serving})
	owner.release(sourceClaim(sourceBase), nil)
	assertReadiness(t, checker, readinessAllServing())
}

func TestReloadOpensNewListenerBeforeSessionAndClosesRemovedOne(t *testing.T) {
	t.Parallel()

	if _, err := netio.CheckInterfacesAvailable([]string{"lo"}); err != nil {
		t.Skipf("up loopback interface required: %v", err)
	}
	startup := config.DefaultConfig()
	startup.Sessions = []config.SessionConfig{{
		Peer: "127.0.0.11", Local: "127.0.0.1", Interface: "lo", Type: "single_hop",
	}}
	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(startup, slog.New(slog.DiscardHandler), checker)
	opener := newFakeListenerOpener()
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("lo", 1))
	owner, mgr := newTestListenerOwner(t, coordinator.readiness, opener, inventory)
	coordinator.listeners = owner
	sessionsAtOpen := make(map[listenerKey]int)
	opener.onOpen = func(cfg netio.ListenerConfig) { sessionsAtOpen[listenerKeyOf(cfg)] = len(mgr.Sessions()) }
	sf := newNthFailureDeclarativeSenderFactory(0)
	if err := owner.openStartup(t.Context(), startup); err != nil {
		t.Fatal(err)
	}
	reconcile := func(cfg *config.Config) {
		t.Helper()
		if err := coordinator.reconcile(t.Context(), cfg, mgr, sf, &overlayRuntime{}, nil); err != nil {
			t.Fatal(err)
		}
		if snapshot := coordinator.Snapshot(); snapshot.Stale {
			t.Fatalf("snapshot = %+v, want converged", snapshot)
		}
	}
	reconcile(startup)

	added := cloneStartupContractTestConfig(startup)
	added.Sessions = append(added.Sessions, config.SessionConfig{
		Peer: "127.0.0.12", Local: "127.0.0.2", Interface: "lo", Type: "single_hop",
	})
	reconcile(added)
	newKey := listenerKeyOf(singleHopListener("127.0.0.2", "lo"))
	if got, ok := sessionsAtOpen[newKey]; !ok || got != 1 {
		t.Fatalf("new listener opened=%v with %d sessions, want opened before the second session", ok, got)
	}
	if len(mgr.Sessions()) != 2 {
		t.Fatalf("sessions = %d, want 2", len(mgr.Sessions()))
	}

	removed := cloneStartupContractTestConfig(added)
	removed.Sessions = removed.Sessions[1:]
	reconcile(removed)
	oldKey := listenerKeyOf(singleHopListener("127.0.0.1", "lo"))
	if !fakeConnClosed(opener.conn(t, oldKey)) || fakeConnClosed(opener.conn(t, newKey)) {
		t.Fatal("reload must close exactly the listener of the removed session")
	}
	if got := len(opener.openedKeys()); got != 2 {
		t.Fatalf("opened listeners = %d, want 2", got)
	}
}

// BenchmarkListenerOwnerCheckBindings measures one periodic owner recheck,
// rebind scan included, against the real interface inventory; compare with
// BenchmarkCheckBindingsInventory.
func BenchmarkListenerOwnerCheckBindings(b *testing.B) {
	loopback, err := net.InterfaceByName("lo")
	if err != nil {
		b.Skipf("loopback interface required: %v", err)
	}
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	b.Cleanup(mgr.Close)
	owner := newListenerOwner(mgr, newDaemonReadiness(newDaemonHealthChecker()), slog.New(slog.DiscardHandler))
	owner.open = newFakeListenerOpener().open
	b.Cleanup(owner.close)
	cfg := singleHopListener("127.0.0.1", loopback.Name)
	if err := owner.acquire(b.Context(), sourceClaim(sourceBase), []netio.ListenerConfig{cfg}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := owner.checkBindings(net.Interfaces); err != nil {
			b.Fatal(err)
		}
	}
}
