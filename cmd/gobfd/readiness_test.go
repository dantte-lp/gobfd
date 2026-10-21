package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/grpchealth/v2"
	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

const readinessTestWait = 5 * time.Second

var errInventoryUnavailable = errors.New("inventory unavailable")

type readinessWant struct {
	aggregate, bfd, echo, micro grpchealth.Status
}

func readinessAllServing() readinessWant {
	serving := grpchealth.StatusServing
	return readinessWant{serving, serving, serving, serving}
}

func newConvergedReadinessCoordinator(t *testing.T) (*reconciliationCoordinator, *grpchealth.StaticChecker) {
	t.Helper()
	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(config.DefaultConfig(), slog.New(slog.DiscardHandler), checker)
	coordinator.publishReceipt(generationReceipt{Generation: 1}, nil)
	assertReadiness(t, checker, readinessAllServing())
	return coordinator, checker
}

func assertReadiness(t *testing.T, checker *grpchealth.StaticChecker, want readinessWant) {
	t.Helper()
	assertHealthStatus(t, checker, "", want.aggregate)
	assertNamedHealth(t, checker, want.bfd, want.echo, want.micro)
}

func TestDaemonReadinessNamedServicesFollowTheirSources(t *testing.T) {
	t.Parallel()

	notServing, serving := grpchealth.StatusNotServing, grpchealth.StatusServing
	for _, tc := range []struct {
		source reconciliationSource
		want   readinessWant
	}{
		{sourceBase, readinessWant{notServing, notServing, serving, serving}},
		{sourceEcho, readinessWant{notServing, serving, notServing, serving}},
		{sourceMicroGroup, readinessWant{notServing, serving, serving, notServing}},
		{sourceMicroMember, readinessWant{notServing, serving, serving, notServing}},
		{sourceVXLAN, readinessWant{notServing, notServing, serving, serving}},
		{sourceGeneve, readinessWant{notServing, notServing, serving, serving}},
	} {
		t.Run(tc.source.String(), func(t *testing.T) {
			t.Parallel()
			coordinator, checker := newConvergedReadinessCoordinator(t)

			pending := generationReceipt{Generation: 2}
			pending.Sources[tc.source].Pending = 1
			coordinator.publishReceipt(pending, nil)
			assertReadiness(t, checker, tc.want)

			failed := generationReceipt{Generation: 3}
			failed.Sources[tc.source].Failed = 1
			coordinator.publishReceipt(failed, nil)
			assertReadiness(t, checker, tc.want)

			coordinator.publishReceipt(generationReceipt{Generation: 4}, nil)
			assertReadiness(t, checker, readinessAllServing())
		})
	}
}

func TestDaemonReadinessConvergedReconciliationCannotMaskRuntimeLoss(t *testing.T) {
	t.Parallel()

	coordinator, checker := newConvergedReadinessCoordinator(t)
	coordinator.readiness.set(componentRuntime, false)
	coordinator.publishReceipt(generationReceipt{Generation: 2}, nil)

	notServing := grpchealth.StatusNotServing
	assertReadiness(t, checker, readinessWant{notServing, notServing, notServing, notServing})
	assertHealthStatus(t, checker, grpchealth.HealthV1ServiceName, grpchealth.StatusServing)
}

type fakeInterfaceInventory struct {
	mu         sync.Mutex
	interfaces []net.Interface
	err        error
}

func (f *fakeInterfaceInventory) set(err error, interfaces ...net.Interface) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interfaces, f.err = interfaces, err
}

func (f *fakeInterfaceInventory) list() ([]net.Interface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.interfaces), f.err
}

func upInterface(name string, index int) net.Interface {
	return net.Interface{Name: name, Index: index, Flags: net.FlagUp | net.FlagRunning}
}

func TestDaemonReadinessListenerBindingsFollowInterfaceInventory(t *testing.T) {
	t.Parallel()

	coordinator, checker := newConvergedReadinessCoordinator(t)
	inventory := &fakeInterfaceInventory{}
	control, echo, micro := upInterface("eth9", 7), upInterface("eth8", 5), upInterface("eth7", 3)
	inventory.set(nil, control, echo, micro)
	coordinator.readiness.watchBindings([]listenerBinding{
		{ifName: "eth9", ifIndex: 7, component: componentControlBindings},
		{ifName: "eth8", ifIndex: 5, component: componentEchoBindings},
		{ifName: "eth7", ifIndex: 3, component: componentMicroBindings},
	}, inventory.list)
	assertReadiness(t, checker, readinessAllServing())

	notServing, serving := grpchealth.StatusNotServing, grpchealth.StatusServing
	controlDown := control
	controlDown.Flags = net.FlagUp
	for _, step := range []struct {
		name       string
		err        error
		interfaces []net.Interface
		want       readinessWant
	}{
		{
			name: "control link down", interfaces: []net.Interface{controlDown, echo, micro},
			want: readinessWant{notServing, notServing, serving, serving},
		},
		{
			name: "control link up with bind-time index", interfaces: []net.Interface{control, echo, micro},
			want: readinessAllServing(),
		},
		{
			name: "control interface deleted", interfaces: []net.Interface{echo, micro},
			want: readinessWant{notServing, notServing, serving, serving},
		},
		{
			name:       "control interface recreated with another index",
			interfaces: []net.Interface{upInterface("eth9", 11), echo, micro},
			want:       readinessWant{notServing, notServing, serving, serving},
		},
		{
			name:       "echo interface recreated with another index",
			interfaces: []net.Interface{control, upInterface("eth8", 12), micro},
			want:       readinessWant{notServing, serving, notServing, serving},
		},
		{
			name: "micro member link down", interfaces: []net.Interface{control, echo, {Name: "eth7", Index: 3}},
			want: readinessWant{notServing, serving, serving, notServing},
		},
		{
			name: "inventory failure", err: errInventoryUnavailable,
			want: readinessWant{notServing, notServing, notServing, notServing},
		},
		{
			name: "inventory restored", interfaces: []net.Interface{control, echo, micro},
			want: readinessAllServing(),
		},
	} {
		// Steps share one aggregator and run in order.
		inventory.set(step.err, step.interfaces...)
		coordinator.readiness.checkBindings(inventory.list)
		t.Logf("step %q", step.name)
		assertReadiness(t, checker, step.want)
	}
}

func TestResolveListenerBindingsRecordsBindTimeIndexes(t *testing.T) {
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
	cfg.Echo.Peers = []config.EchoPeerConfig{{Peer: "192.0.2.6", Local: "192.0.2.5", Interface: "eth8"}}
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
		LAGInterface: "bond0", MemberLinks: []string{"eth7"}, PeerAddr: "192.0.2.8", LocalAddr: "192.0.2.7",
	}}
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("eth9", 9), upInterface("eth8", 8), upInterface("eth7", 7),
		upInterface("eth6", 6))

	got := resolveListenerBindings(cfg, inventory.list)
	want := []listenerBinding{
		{ifName: "eth6", ifIndex: 6, component: componentControlBindings},
		{ifName: "eth9", ifIndex: 9, component: componentControlBindings},
		{ifName: "eth8", ifIndex: 8, component: componentEchoBindings},
		{ifName: "eth7", ifIndex: 7, component: componentMicroBindings},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("listener bindings = %+v, want %+v", got, want)
	}
}

func TestInterfaceEventsRecheckListenerBindings(t *testing.T) {
	t.Parallel()

	loopback, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skipf("loopback interface required: %v", err)
	}
	coordinator, checker := newConvergedReadinessCoordinator(t)
	staleIndex := loopback.Index + 1000
	inventory := &fakeInterfaceInventory{}
	inventory.set(nil, upInterface("lo", staleIndex))
	coordinator.readiness.watchBindings([]listenerBinding{
		{ifName: "lo", ifIndex: staleIndex, component: componentControlBindings},
	}, inventory.list)
	assertReadiness(t, checker, readinessAllServing())

	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	events := make(chan netio.InterfaceEvent, 1)
	events <- netio.InterfaceEvent{IfName: "lo", IfIndex: loopback.Index, Up: true}
	close(events)
	consumeInterfaceEvents(t.Context(), events, mgr, coordinator, newInterfaceEventCoalescer(),
		slog.New(slog.DiscardHandler))

	notServing, serving := grpchealth.StatusNotServing, grpchealth.StatusServing
	assertReadiness(t, checker, readinessWant{notServing, notServing, serving, serving})
}

func TestRequiredDispatchStopFailsStopAndReportsNotServing(t *testing.T) {
	t.Parallel()

	coordinator, checker := newConvergedReadinessCoordinator(t)
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	mgr.Close()
	g, ctx := errgroup.WithContext(t.Context())
	startManagerDispatch(ctx, g, mgr, coordinator.readiness)

	err := waitGroupBounded(t, g)
	if !errors.Is(err, errRequiredWorkerStopped) {
		t.Fatalf("dispatch stop error = %v, want %v", err, errRequiredWorkerStopped)
	}
	notServing := grpchealth.StatusNotServing
	assertReadiness(t, checker, readinessWant{notServing, notServing, notServing, notServing})
}

// fakeListenerConn blocks reads until Close, as a real socket does.
type fakeListenerConn struct {
	reading   chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeListenerConn() *fakeListenerConn {
	return &fakeListenerConn{reading: make(chan struct{}, 1), closed: make(chan struct{})}
}

func (c *fakeListenerConn) ReadPacket([]byte) (int, netio.PacketMeta, error) {
	select {
	case c.reading <- struct{}{}:
	default:
	}
	<-c.closed
	return 0, netio.PacketMeta{}, net.ErrClosed
}

func (*fakeListenerConn) WritePacket([]byte, netip.Addr) error { return nil }

func (c *fakeListenerConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (*fakeListenerConn) LocalAddr() netip.AddrPort { return netip.AddrPort{} }

func TestReceiverLossFailStopsAndReportsNotServing(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"control", "echo", "micro"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			coordinator, checker := newConvergedReadinessCoordinator(t)
			mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
			t.Cleanup(mgr.Close)
			lost := newFakeListenerConn()
			if err := lost.Close(); err != nil {
				t.Fatal(err)
			}
			listener := netio.NewListenerFromConn(lost, false)
			var listeners listenersResult
			switch kind {
			case "control":
				listeners.control = []*netio.Listener{listener}
			case "echo":
				listeners.echo = []*netio.Listener{listener}
			default:
				listeners.microBFD = []*netio.Listener{listener}
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			g, gCtx := errgroup.WithContext(ctx)
			startReceivers(gCtx, g, coordinator.readiness, listeners, mgr, slog.New(slog.DiscardHandler))

			err := waitGroupBounded(t, g)
			if !errors.Is(err, errRequiredWorkerStopped) || !errors.Is(err, netio.ErrListenerClosed) {
				t.Fatalf("receiver loss error = %v, want %v and %v",
					err, errRequiredWorkerStopped, netio.ErrListenerClosed)
			}
			notServing := grpchealth.StatusNotServing
			assertReadiness(t, checker, readinessWant{notServing, notServing, notServing, notServing})
		})
	}
}

func TestShutdownClosesIdleReceiversAfterDrain(t *testing.T) {
	t.Parallel()

	coordinator, checker := newConvergedReadinessCoordinator(t)
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	idle := newFakeListenerConn()
	t.Cleanup(func() { _ = idle.Close() })
	listeners := listenersResult{control: []*netio.Listener{netio.NewListenerFromConn(idle, false)}}
	closeReceivers := sync.OnceFunc(func() { closeListeners(listeners.control, slog.New(slog.DiscardHandler)) })

	ctx, cancel := context.WithCancel(t.Context())
	g, gCtx := errgroup.WithContext(ctx)
	logger := slog.New(slog.DiscardHandler)
	startReceivers(gCtx, g, coordinator.readiness, listeners, mgr, logger)
	startShutdown(gCtx, g, mgr, logger, nil, coordinator.readiness, closeReceivers)
	select {
	case <-idle.reading:
	case <-time.After(readinessTestWait):
		t.Fatal("receiver did not block in a read")
	}
	cancel()

	if err := waitGroupBounded(t, g); err != nil {
		t.Fatalf("shutdown error = %v, want nil", err)
	}
	notServing := grpchealth.StatusNotServing
	assertReadiness(t, checker, readinessWant{notServing, notServing, notServing, notServing})
}

// waitGroupBounded fails the test instead of hanging when the group does not
// stop; the caller's cleanup releases blocked workers.
func waitGroupBounded(t *testing.T, g *errgroup.Group) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- g.Wait() }()
	select {
	case err := <-result:
		return err
	case <-time.After(readinessTestWait):
		t.Fatalf("errgroup did not stop within %s", readinessTestWait)
		return nil
	}
}
