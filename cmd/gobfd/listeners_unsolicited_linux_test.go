//go:build linux

package main

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

type listenerAdmission struct {
	meta bfd.PacketMeta
	err  error
}

type listenerAdmissionRecorder struct {
	mgr   *bfd.Manager
	calls chan listenerAdmission
}

func (r *listenerAdmissionRecorder) DemuxWithWire(
	pkt bfd.ControlPacket, meta bfd.PacketMeta, wire []byte,
) (bfd.DeliveryOutcome, error) {
	outcome, err := r.mgr.DemuxWithWire(pkt, meta, wire)
	r.calls <- listenerAdmission{meta: meta, err: err}
	return outcome, err
}

func (r *listenerAdmissionRecorder) RecordDelivery(source bfd.DeliverySource, outcome bfd.DeliveryOutcome) {
	r.mgr.RecordDelivery(source, outcome)
}

// Run serially: these tests exercise the real single-hop port in a dedicated network namespace.
func TestUnsolicitedListenersReceiveWithoutConfiguredSession(t *testing.T) {
	for _, tc := range []struct{ name, src, dst string }{
		{"IPv4", "127.0.0.2", "127.0.0.1"},
		{"IPv6", "2001:db8:9468::2", "2001:db8:9468::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Sessions = nil
			cfg.Unsolicited.Enabled = true
			cfg.Unsolicited.MaxSessions = 4
			cfg.Unsolicited.Interfaces = map[string]config.UnsolicitedInterfaceConfig{"lo": {Enabled: true}}
			checkUnsolicitedListenerAdmission(t, cfg, tc.src, tc.dst, false)
		})
	}
}

func TestUnsolicitedListenersCoexistWithConfiguredSession(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{{Peer: "127.0.0.3", Local: "127.0.0.1", Type: "single_hop"}}
	cfg.Unsolicited.Enabled = true
	cfg.Unsolicited.MaxSessions = 4
	cfg.Unsolicited.Interfaces = map[string]config.UnsolicitedInterfaceConfig{"lo": {Enabled: true}}
	checkUnsolicitedListenerAdmission(t, cfg, "127.0.0.2", "127.0.0.1", true)
}

func checkUnsolicitedListenerAdmission(t *testing.T, cfg *config.Config, src, dst string, configured bool) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	// A missing IPv6 namespace fixture is explicit, never a passing IPv6 acceptance.
	probe, err := netio.NewUDPSender(netip.MustParseAddr(src), 49152, false, logger, netio.WithBindDevice("lo"))
	if err != nil {
		t.Skipf("dedicated dual-stack network namespace required: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := probe.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	listeners := openTestListeners(t, controlListenerConfigs(cfg))
	policy, err := buildUnsolicitedPolicy(cfg.Unsolicited)
	if err != nil {
		t.Fatal(err)
	}
	mgr := bfd.NewManager(logger, bfd.WithUnsolicitedPolicy(policy),
		bfd.WithUnsolicitedSessionSender(func(bfd.PacketMeta) (*bfd.SenderLease, error) {
			return bfd.NewSenderLease(candidateTestSender{}, nil), nil
		}))
	t.Cleanup(mgr.Close)
	if configured {
		_, err = mgr.CreateSession(t.Context(), bfd.SessionConfig{
			PeerAddr: netip.MustParseAddr("127.0.0.3"), LocalAddr: netip.MustParseAddr(dst),
			Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
			DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
		}, candidateTestSenderLeaseFactory())
		if err != nil {
			t.Fatal(err)
		}
	}
	recorder := &listenerAdmissionRecorder{mgr: mgr, calls: make(chan listenerAdmission, 10)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- netio.NewReceiver(recorder, logger).Run(ctx, listeners...) }()
	t.Cleanup(func() {
		cancel()
		for _, ln := range listeners {
			_ = ln.Close() // Unblocks the reads; the owning cleanup closes again idempotently.
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("receiver did not stop")
		}
	})
	packet := &bfd.ControlPacket{
		Version: bfd.Version, State: bfd.StateDown, DetectMult: 3,
		MyDiscriminator: 101, DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 1000000,
	}
	wire := make([]byte, bfd.MinPacketSizeNoAuth)
	if _, err := bfd.MarshalControlPacket(packet, wire); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := probe.SendPacket(t.Context(), wire, netip.MustParseAddr(dst)); err != nil {
			t.Fatal(err)
		}
		select {
		case call := <-recorder.calls:
			if call.err != nil {
				t.Fatalf("admission src=%s dst=%s meta=%+v: %v", src, dst, call.meta, call.err)
			}
			if call.meta.DstAddr != netip.MustParseAddr(dst) || call.meta.TTL != 255 {
				t.Fatalf("received metadata=%+v, want actual destination %s and TTL255", call.meta, dst)
			}
		case <-time.After(time.Second):
			t.Fatal("unsolicited packet was not received without a configured session")
		}
	}
	want := 1
	if configured {
		want++
	}
	sessions := mgr.Sessions()
	if len(sessions) != want {
		t.Fatalf("sessions=%+v, want %d (no duplicate admission)", sessions, want)
	}
	for _, session := range sessions {
		if session.PeerAddr == netip.MustParseAddr(src) && session.Interface != "lo" {
			t.Fatalf("unsolicited session interface=%q, want actual ingress lo", session.Interface)
		}
	}
	if configured {
		known, sendErr := netio.NewUDPSender(netip.MustParseAddr("127.0.0.3"), 49153, false, logger)
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		defer func() {
			if closeErr := known.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
		if err := known.SendPacket(t.Context(), wire, netip.MustParseAddr(dst)); err != nil {
			t.Fatal(err)
		}
		select {
		case call := <-recorder.calls:
			if call.err != nil {
				t.Fatalf("configured traffic lost: %v", call.err)
			}
		case <-time.After(time.Second):
			t.Fatal("configured packet was not received")
		}
		if sessions := mgr.Sessions(); len(sessions) != want {
			t.Fatalf("configured packet created a duplicate: %+v", sessions)
		}
	}
	select {
	case call := <-recorder.calls:
		t.Fatalf("duplicate datagram delivery: %+v", call)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestUnsolicitedListenersDisabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config.DefaultConfig()
		cfg.Sessions = nil
		cfg.Unsolicited.Enabled = enabled
		cfg.Unsolicited.Interfaces = map[string]config.UnsolicitedInterfaceConfig{"does-not-exist": {Enabled: !enabled}}
		if listeners := controlListenerConfigs(cfg); len(listeners) != 0 {
			t.Fatalf("global=%v: listeners=%+v, want none", enabled, listeners)
		}
	}
}
