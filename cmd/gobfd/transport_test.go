package main

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
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
func TestCompileBaseSessionCandidatesBindsSingleHopInterface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, local, ifName, sessType string
		wantLocal, wantDevice         string
	}{
		{name: "ipv4 interface", local: "192.0.2.1", ifName: "eth0", wantLocal: "192.0.2.1", wantDevice: "eth0"},
		{name: "ipv6 interface", local: "2001:db8::1", ifName: "eth0", wantLocal: "2001:db8::1", wantDevice: "eth0"},
		{name: "ipv6 link-local", local: "fe80::1", ifName: "eth0", wantLocal: "fe80::1%eth0", wantDevice: "eth0"},
		{name: "no interface", local: "192.0.2.1", wantLocal: "192.0.2.1"},
		{name: "multihop", local: "2001:db8::1", sessType: "multi_hop", wantLocal: "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			cfg.Sessions = []config.SessionConfig{{
				Peer: "192.0.2.2", Local: tc.local, Interface: tc.ifName, Type: tc.sessType,
			}}
			if netip.MustParseAddr(tc.local).Is6() {
				cfg.Sessions[0].Peer = "2001:db8::2"
				if tc.name == "ipv6 link-local" {
					cfg.Sessions[0].Peer = "fe80::2"
				}
			}
			candidates, err := compileBaseSessionCandidates(cfg)
			if err != nil {
				t.Fatalf("compileBaseSessionCandidates: %v", err)
			}
			got := candidates[0]
			if got.bindDevice != tc.wantDevice || got.senderLocal != netip.MustParseAddr(tc.wantLocal) {
				t.Fatalf("sender binding = %s on %q, want %s on %q",
					got.senderLocal, got.bindDevice, tc.wantLocal, tc.wantDevice)
			}
			if got.config.LocalAddr != netip.MustParseAddr(tc.local) {
				t.Fatalf("session local = %s, want configured %s", got.config.LocalAddr, tc.local)
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
