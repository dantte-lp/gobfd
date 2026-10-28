package main

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// RFC 5881 Section 6: control packets MUST be transmitted over the one-hop
// path being protected, so a configured single-hop interface binds TX too.
// An IPv6 link-local sender address is scoped to that interface, as on the
// RFC 9468 unsolicited path; the session identity keeps the configured address.
// Without interface, a link-local zone selects the session interface and the
// listener device.
func TestCompileBaseSessionCandidatesBindsSingleHopInterface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, peer, local, ifName, sessType string
		wantLocal, wantInterface            string
	}{
		{
			name: "ipv4 interface", peer: "192.0.2.2", local: "192.0.2.1", ifName: "eth0",
			wantLocal: "192.0.2.1", wantInterface: "eth0",
		},
		{
			name: "ipv6 interface", peer: "2001:db8::2", local: "2001:db8::1", ifName: "eth0",
			wantLocal: "2001:db8::1", wantInterface: "eth0",
		},
		{
			name: "ipv6 link-local", peer: "fe80::2", local: "fe80::1", ifName: "eth0",
			wantLocal: "fe80::1%eth0", wantInterface: "eth0",
		},
		{
			name: "ipv6 zoned link-local", peer: "fe80::2%eth1", local: "fe80::1%eth1",
			wantLocal: "fe80::1%eth1", wantInterface: "eth1",
		},
		{name: "no interface", peer: "192.0.2.2", local: "192.0.2.1", wantLocal: "192.0.2.1"},
		{name: "multihop", peer: "2001:db8::2", local: "2001:db8::1", sessType: "multi_hop", wantLocal: "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			cfg.Sessions = []config.SessionConfig{{
				Peer: tc.peer, Local: tc.local, Interface: tc.ifName, Type: tc.sessType,
			}}
			candidates, err := compileBaseSessionCandidates(cfg)
			if err != nil {
				t.Fatalf("compileBaseSessionCandidates: %v", err)
			}
			got := candidates[0]
			if got.senderLocal != netip.MustParseAddr(tc.wantLocal) || got.config.Interface != tc.wantInterface {
				t.Fatalf("sender local %s, session interface %q; want %s, %q",
					got.senderLocal, got.config.Interface, tc.wantLocal, tc.wantInterface)
			}
			if got.config.LocalAddr != netip.MustParseAddr(tc.local) {
				t.Fatalf("session local = %s, want configured %s", got.config.LocalAddr, tc.local)
			}
			if listeners := controlListenerConfigs(cfg); len(listeners) != 1 || listeners[0].IfName != tc.wantInterface {
				t.Fatalf("listener configs = %+v, want device %q", listeners, tc.wantInterface)
			}

			sf := &tupleRecordingSenderFactory{nthFailureDeclarativeSenderFactory: newNthFailureDeclarativeSenderFactory(0)}
			mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
			t.Cleanup(mgr.Close)
			result := applyBaseSessionCandidates(context.Background(), candidates, mgr, sf, slog.New(slog.DiscardHandler))
			if err := result.Err(); err != nil {
				t.Fatalf("applyBaseSessionCandidates: %v", err)
			}
			if len(sf.localAddrs) != 1 || sf.localAddrs[0] != netip.MustParseAddr(tc.wantLocal) {
				t.Fatalf("sender local addresses = %v, want [%s]", sf.localAddrs, tc.wantLocal)
			}
		})
	}
}

func TestConfigSessionToBFDMapsMultihopMaxHops(t *testing.T) {
	t.Parallel()
	got, err := configSessionToBFD(config.SessionConfig{
		Peer: "2001:db8::2", Local: "2001:db8::1", Type: "multi_hop",
		MultihopMaxHops: 5, MultihopMaxHopsSet: true,
	}, config.DefaultConfig().BFD)
	if err != nil {
		t.Fatalf("configSessionToBFD: %v", err)
	}
	if got.MultiHopMaxHops != 5 {
		t.Fatalf("MultiHopMaxHops = %d, want 5", got.MultiHopMaxHops)
	}
}

// Option (b): the daemon Manager resolves the interface that owns a single-hop
// local address at session creation and refuses an address no interface owns.
func TestDaemonManagerResolvesSingleHopInterface(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	mgr, err := newManager(cfg, bfdmetrics.NewCollector(prometheus.NewRegistry()), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	t.Cleanup(mgr.Close)
	_, err = mgr.CreateSession(context.Background(), bfd.SessionConfig{
		PeerAddr: netip.MustParseAddr("192.0.2.2"), LocalAddr: netip.MustParseAddr("192.0.2.250"),
		Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
		DesiredMinTxInterval: cfg.BFD.DefaultDesiredMinTx, RequiredMinRxInterval: cfg.BFD.DefaultRequiredMinRx,
		DetectMultiplier: 3,
	}, candidateTestSenderLeaseFactory())
	if !errors.Is(err, netio.ErrLocalAddrNotOwned) {
		t.Fatalf("CreateSession(unowned local) error = %v, want ErrLocalAddrNotOwned", err)
	}
}

// Option (b) on the YAML path: config reconciliation reaches the daemon
// resolver, so an interface-less single-hop session binds to the interface
// owning its local address and an unowned local address fails creation.
func TestDaemonReconcileResolvesSingleHopInterface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, peer, local string
		wantErr           bool
	}{
		{name: "owned ipv4", peer: "127.0.0.2", local: "127.0.0.1"},
		{name: "unowned ipv4", peer: "192.0.2.2", local: "192.0.2.250", wantErr: true},
		{name: "unowned ipv6", peer: "2001:db8::2", local: "2001:db8::250", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			cfg.Sessions = []config.SessionConfig{{Peer: tc.peer, Local: tc.local}}
			candidates, err := compileBaseSessionCandidates(cfg)
			if err != nil {
				t.Fatalf("compileBaseSessionCandidates: %v", err)
			}
			mgr, err := newManager(cfg, bfdmetrics.NewCollector(prometheus.NewRegistry()), nil, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("newManager: %v", err)
			}
			t.Cleanup(mgr.Close)
			sf := &tupleRecordingSenderFactory{nthFailureDeclarativeSenderFactory: newNthFailureDeclarativeSenderFactory(0)}
			err = applyBaseSessionCandidates(context.Background(), candidates, mgr, sf, slog.New(slog.DiscardHandler)).Err()
			if !tc.wantErr {
				if err != nil || len(mgr.Sessions()) != 1 {
					t.Fatalf("reconcile = %v, sessions %d; want one session", err, len(mgr.Sessions()))
				}
				return
			}
			if !errors.Is(err, bfd.ErrInterfaceUnresolved) || !errors.Is(err, netio.ErrLocalAddrNotOwned) {
				t.Fatalf("reconcile error = %v, want ErrInterfaceUnresolved and ErrLocalAddrNotOwned", err)
			}
			if len(sf.localAddrs) != 0 || len(mgr.Sessions()) != 0 {
				t.Fatalf("senders %v, sessions %d; want none", sf.localAddrs, len(mgr.Sessions()))
			}
		})
	}
}

// An unresolvable receive interface is a pending source dependency: the
// neighbouring session is created, the base source stays pending without a
// failure, and an interface event retries and converges it.
func TestReconcileUnresolvedInterfaceIsPendingDependency(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	unresolved := netip.MustParseAddr("192.0.2.250")
	var ready atomic.Bool
	mgr := bfd.NewManager(logger, bfd.WithSessionInterfaceResolver(func(local netip.Addr, _ string) (int, error) {
		if local == unresolved && !ready.Load() {
			return 0, netio.ErrLocalAddrNotOwned
		}
		return 1, nil
	}))
	t.Cleanup(mgr.Close)
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{
		{Peer: "192.0.2.1", Local: "192.0.2.2"},
		{Peer: "192.0.2.3", Local: unresolved.String()},
	}
	base, err := compileBaseSessionCandidates(cfg)
	if err != nil {
		t.Fatalf("compileBaseSessionCandidates: %v", err)
	}
	sf := newNthFailureDeclarativeSenderFactory(0)
	available := func([]string) (int, error) { return 0, nil }
	apply := func(
		ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate,
	) sourceApplyResult {
		if source != sourceBase {
			return sourceApplyResult{} // Only the base source is under test.
		}
		return applyCompiledSourceWithInterfaceChecker(ctx, source, candidate, mgr, sf, logger, nil, available)
	}
	coordinator := newReconciliationCoordinator(config.DefaultConfig(), logger, nil)
	first := coordinator.applyCandidate(context.Background(), compiledControlSessionCandidate{base: base}, apply)
	receipt := first.LastReceipt.Sources[sourceBase]
	if receipt.Created != 1 || receipt.Pending != 1 || receipt.Failed != 0 || !first.Stale || len(mgr.Sessions()) != 1 {
		t.Fatalf("first apply receipt = %+v, stale %t, sessions %d; want 1 created, 1 pending",
			receipt, first.Stale, len(mgr.Sessions()))
	}

	ready.Store(true)
	for _, ev := range []netio.InterfaceEvent{{IfName: "dummy0", Up: true}, {Resync: true}} {
		if _, selected := coordinator.interfaceEventSelection(ev); !selected.contains(sourceBase) {
			t.Fatalf("event %+v did not select the base source", ev)
		}
	}
	generation, selected := coordinator.interfaceEventSelection(netio.InterfaceEvent{IfName: "dummy0", Up: true})
	coordinator.reconcileInterfaceSources(context.Background(), generation, selected, available, apply)
	converged := coordinator.Snapshot()
	if converged.Stale || converged.Pending != 0 || converged.Failed != 0 || len(mgr.Sessions()) != 2 {
		t.Fatalf("after interface event = %+v, sessions %d; want converged with 2 sessions",
			converged, len(mgr.Sessions()))
	}
}
