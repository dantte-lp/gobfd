//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"go.uber.org/goleak"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestOwnerActualUDPCommitAndCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	guard, guardErr := NewClockEpochGuard(t.Context())
	if guardErr != nil {
		t.Fatal(guardErr)
	}
	t.Cleanup(func() {
		if closeErr := guard.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	endpoints := []OwnerEndpoint{
		{Local: netip.MustParseAddrPort("127.0.0.1:37991"), Peer: netip.MustParseAddrPort("127.0.0.2:37993")},
		{Local: netip.MustParseAddrPort("[::1]:37992"), Peer: netip.MustParseAddrPort("[::1]:37994")},
	}
	peers := make([]*net.UDPConn, len(endpoints))
	for i, endpoint := range endpoints {
		peer, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(endpoint.Peer))
		if err != nil {
			t.Fatal(err)
		}
		peers[i] = peer
		t.Cleanup(func() {
			if err := peer.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	owner, err := StartOwner(ctx, OwnerConfig{Sessions: endpoints, Interval: 100 * time.Millisecond, DetectMultiplier: 3},
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := owner.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	initial, err := owner.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, session := range initial.Sessions {
		sender, sendErr := netio.NewUDPSender(session.Endpoint.Peer.Addr(), 49152, false,
			slog.New(slog.DiscardHandler), netio.WithDstPort(session.Endpoint.Local.Port()))
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		packet := bfd.ControlPacket{
			Version: 1, State: bfd.StateInit, DetectMult: 3,
			MyDiscriminator: uint32(i + 100), YourDiscriminator: session.LocalDiscriminator,
			DesiredMinTxInterval: 100000, RequiredMinRxInterval: 100000,
		}
		var wire [24]byte
		n, marshalErr := bfd.MarshalControlPacket(&packet, wire[:])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		sendErr = sender.SendPacket(t.Context(), wire[:n], session.Endpoint.Local.Addr())
		badErr := sender.SendPacket(t.Context(), []byte{0}, session.Endpoint.Local.Addr())
		if packetErr := errors.Join(sendErr, badErr, sender.Close()); packetErr != nil {
			t.Fatal(packetErr)
		}
	}
	var observed OwnerObservation
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		observed, err = owner.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if deliveryCount(observed, bfd.DeliveryPacketOwner, bfd.DeliveryCommitted) == 2 &&
			deliveryCount(observed, bfd.DeliveryWireParse, bfd.DeliveryParserRejected) == 2 &&
			metricCounter(observed.Metrics, "gobfd_bfd_control_rx_timing_total", nil) == 2 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("actual UDP did not reach owner and parser: %+v", observed.Delivery)
		case <-tick.C:
		}
	}
	for _, session := range observed.Sessions {
		if session.Observation.State != bfd.StateUp || session.Received != 1 {
			t.Fatalf("packet was not processed by the session owner: %+v", session)
		}
	}
	if observed.ReferenceQualified || observed.TimingUncertaintyNS != nil {
		t.Fatal("component fixture incorrectly qualified reference load or clock precision")
	}
	if len(observed.Sessions) != 2 {
		t.Fatalf("owner silently changed session count: %d", len(observed.Sessions))
	}
	//nolint:musttag // Internal snapshot retains core Observation and DeliveryCounter field names.
	if _, err := json.Marshal(observed); err != nil {
		t.Fatalf("actual collector and environment are not JSON-safe: %v", err)
	}
	if got := metricCounter(observed.Metrics, "gobfd_bfd_control_rx_timing_total",
		map[string]string{"outcome": "valid"}); got != 2 {
		t.Fatalf("valid actual kernel receive timing = %g, want 2", got)
	}
	for i, peer := range peers {
		if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var wire [512]byte
		n, source, err := peer.ReadFromUDPAddrPort(wire[:])
		if err != nil {
			t.Fatal(err)
		}
		var packet bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(wire[:n], &packet); err != nil {
			t.Fatal(err)
		}
		if packet.MyDiscriminator != observed.Sessions[i].LocalDiscriminator || source.Port() < 49152 ||
			source.Port() != observed.Sessions[i].SourcePort || source.Addr() != observed.Sessions[i].Endpoint.Local.Addr() {
			t.Fatalf("owner did not return a real BFD packet: %+v from %s", packet, source)
		}
	}
	cancel()
	select {
	case <-owner.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not stop and join on cancellation")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	final, snapshotErr := owner.Snapshot(t.Context())
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if !final.Stopped {
		t.Fatal("final accounting was sampled before worker cleanup")
	}
	witness, witnessErr := guard.Finish(t.Context())
	if witnessErr != nil {
		t.Fatal(witnessErr)
	}
	final = final.WithClockEpoch(witness)
	if final.TimingUncertaintyNS == nil || *final.TimingUncertaintyNS == 0 ||
		final.TimingUncertaintyError != "" || len(final.ClockCalibrations) != 2 || final.ReferenceQualified {
		t.Fatalf("actual guarded clock measurements unavailable: %+v", final)
	}
	for kind, want := range map[string]float64{"control": 4, "echo": 0} {
		if got := metricCounter(final.Metrics, "gobfd_bfd_socket_drop_samples_total",
			map[string]string{"kind": kind}); got != want {
			t.Fatalf("%s socket sample denominator = %g, want %g consumed datagrams", kind, got, want)
		}
	}
	for _, outcome := range []string{"invalid", "ambiguous"} {
		if got := metricCounter(final.Metrics, "gobfd_bfd_socket_drop_samples_total",
			map[string]string{"kind": "control", "outcome": outcome}); got != 0 {
			t.Fatalf("controlled socket samples have %s=%g, want 0", outcome, got)
		}
	}
	for _, row := range final.Delivery {
		got := metricCounter(final.Metrics, "gobfd_bfd_delivery_outcomes_total",
			map[string]string{"source": row.Source.String(), "outcome": row.Outcome.String()})
		if got != float64(row.Count) {
			t.Fatalf("collector/ledger mismatch for %s/%s: %g != %d", row.Source, row.Outcome, got, row.Count)
		}
	}
	for _, session := range observed.Sessions {
		for _, endpoint := range []netip.AddrPort{
			session.Endpoint.Local,
			netip.AddrPortFrom(session.Endpoint.Local.Addr(), session.SourcePort),
		} {
			conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(endpoint))
			if err != nil {
				t.Fatalf("owned socket still bound after Done: %s: %v", endpoint, err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func deliveryCount(observation OwnerObservation, source bfd.DeliverySource, outcome bfd.DeliveryOutcome) uint64 {
	for _, row := range observation.Delivery {
		if row.Source == source && row.Outcome == outcome {
			return row.Count
		}
	}
	return 0
}

func metricCounter(families []*dto.MetricFamily, name string, labels map[string]string) float64 {
	var total float64
	for _, family := range families {
		if family.GetName() == name {
			for _, metric := range family.GetMetric() {
				actual := make(map[string]string)
				for _, label := range metric.GetLabel() {
					actual[label.GetName()] = label.GetValue()
				}
				match := true
				for key, value := range labels {
					match = match && actual[key] == value
				}
				if match {
					total += metric.GetCounter().GetValue()
				}
			}
		}
	}
	return total
}

func TestOwnerConfigRejectsInvalidWireIdentities(t *testing.T) {
	t.Parallel()
	good := OwnerEndpoint{
		Local: netip.MustParseAddrPort("127.0.0.1:37991"),
		Peer:  netip.MustParseAddrPort("127.0.0.2:37993"),
	}
	for _, tc := range []struct {
		name string
		cfg  OwnerConfig
	}{
		{"empty", OwnerConfig{Interval: time.Millisecond, DetectMultiplier: 3}},
		{"zero_interval", OwnerConfig{Sessions: []OwnerEndpoint{good}, DetectMultiplier: 3}},
		{"sub_microsecond", OwnerConfig{Sessions: []OwnerEndpoint{good}, Interval: time.Nanosecond, DetectMultiplier: 3}},
		{"wire_overflow", OwnerConfig{
			Sessions: []OwnerEndpoint{good},
			Interval: 1 << 40 * time.Microsecond, DetectMultiplier: 3,
		}},
		{"zero_multiplier", OwnerConfig{Sessions: []OwnerEndpoint{good}, Interval: time.Millisecond}},
		{"duplicate", OwnerConfig{Sessions: []OwnerEndpoint{good, good}, Interval: time.Millisecond, DetectMultiplier: 3}},
		{"duplicate_other_port", OwnerConfig{
			Sessions: []OwnerEndpoint{
				good,
				{Local: good.Local, Peer: netip.MustParseAddrPort("127.0.0.2:37994")},
			},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"mixed_family", OwnerConfig{
			Sessions: []OwnerEndpoint{
				{Local: good.Local, Peer: netip.MustParseAddrPort("[::1]:37994")},
			},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"unspecified", OwnerConfig{
			Sessions: []OwnerEndpoint{
				{Local: netip.MustParseAddrPort("0.0.0.0:37991"), Peer: good.Peer},
			},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"broadcast", OwnerConfig{Sessions: []OwnerEndpoint{{
			Local: good.Local,
			Peer:  netip.MustParseAddrPort("255.255.255.255:37994"),
		}}, Interval: time.Millisecond, DetectMultiplier: 3}},
		{"zero_port", OwnerConfig{
			Sessions: []OwnerEndpoint{{Local: netip.MustParseAddrPort("127.0.0.1:0"), Peer: good.Peer}},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"echo_receive_port", OwnerConfig{
			Sessions: []OwnerEndpoint{
				{Local: netip.MustParseAddrPort("127.0.0.1:3785"), Peer: good.Peer},
			},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"source_range_first", OwnerConfig{
			Sessions: []OwnerEndpoint{
				{Local: netip.MustParseAddrPort("127.0.0.1:49152"), Peer: good.Peer},
			},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"source_range_last", OwnerConfig{
			Sessions: []OwnerEndpoint{
				{Local: netip.MustParseAddrPort("[::1]:65535"), Peer: netip.MustParseAddrPort("[::1]:37994")},
			},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"mapped", OwnerConfig{
			Sessions: []OwnerEndpoint{{
				Local: netip.MustParseAddrPort("[::ffff:127.0.0.1]:37991"),
				Peer:  netip.MustParseAddrPort("[::ffff:127.0.0.2]:37994"),
			}},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
		{"conflicting_binding", OwnerConfig{
			Sessions: []OwnerEndpoint{
				good,
				{Local: good.Local, Peer: netip.MustParseAddrPort("127.0.0.3:37994"), Interface: "lo"},
			},
			Interval: time.Millisecond, DetectMultiplier: 3,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateOwnerConfig(tc.cfg); !errors.Is(err, errOwnerConfig) {
				t.Fatalf("invalid config accepted or error lost: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if owner, err := StartOwner(ctx, OwnerConfig{
		Sessions: []OwnerEndpoint{good},
		Interval: time.Millisecond, DetectMultiplier: 3,
	},
		slog.New(slog.DiscardHandler)); owner != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup returned owner=%v err=%v", owner, err)
	}
}

func TestOwnerConfigAcceptsThousandMixedPeers(t *testing.T) {
	t.Parallel()
	cfg := OwnerConfig{Interval: 100 * time.Millisecond, DetectMultiplier: 3}
	v6 := netip.MustParseAddr("2001:db8::2")
	for i := range 500 {
		v4 := netip.AddrFrom4([4]byte{198, 18, byte(i >> 8), byte(i)})
		cfg.Sessions = append(cfg.Sessions,
			OwnerEndpoint{Local: netip.MustParseAddrPort("198.19.0.1:3784"), Peer: netip.AddrPortFrom(v4, 3784)},
			OwnerEndpoint{Local: netip.MustParseAddrPort("[2001:db8:1::1]:3784"), Peer: netip.AddrPortFrom(v6, 3784)})
		v6 = v6.Next()
	}
	if err := validateOwnerConfig(cfg); err != nil {
		t.Fatalf("approved1000 mixed peer configuration rejected: %v", err)
	}
}

func TestOwnerPartialStartupRollsBackListeners(t *testing.T) {
	occupied := netip.MustParseAddrPort("127.0.0.1:37996")
	blocker, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(occupied))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := blocker.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	first := netip.MustParseAddrPort("127.0.0.1:37995")
	owner, err := StartOwner(t.Context(), OwnerConfig{
		Interval: time.Millisecond, DetectMultiplier: 3,
		Sessions: []OwnerEndpoint{
			{Local: first, Peer: netip.MustParseAddrPort("127.0.0.2:37994")},
			{Local: occupied, Peer: netip.MustParseAddrPort("127.0.0.3:37994")},
		},
	}, slog.New(slog.DiscardHandler))
	if owner != nil || err == nil {
		if owner != nil {
			if closeErr := owner.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		t.Fatalf("occupied second listener did not fail startup: owner=%v err=%v", owner, err)
	}
	rebound, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(first))
	if err != nil {
		t.Fatalf("first listener leaked after second bind failure: %v", err)
	}
	if err := rebound.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerCanceledBindDoesNotTouchNetwork(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	owner := new(Owner)
	err := owner.start(ctx, OwnerConfig{Sessions: []OwnerEndpoint{{
		Local: netip.MustParseAddrPort("192.0.2.45:37999"), Peer: netip.MustParseAddrPort("192.0.2.46:37999"),
	}}}, slog.New(slog.DiscardHandler))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled binding performed network I/O: %v", err)
	}
}

func TestOwnerReceiveFailureStopsAllWorkers(t *testing.T) {
	owner, err := StartOwner(t.Context(), OwnerConfig{
		Interval: 100 * time.Millisecond, DetectMultiplier: 3,
		Sessions: []OwnerEndpoint{
			{Local: netip.MustParseAddrPort("127.0.0.1:37997"), Peer: netip.MustParseAddrPort("127.0.0.2:37993")},
			{Local: netip.MustParseAddrPort("[::1]:37998"), Peer: netip.MustParseAddrPort("[::1]:37994")},
		},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	if err := owner.listeners[0].Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("failed receiver left other workers running")
	}
	if err := owner.Close(); !errors.Is(err, netio.ErrListenerClosed) {
		t.Fatalf("receiver failure was lost at shutdown: %v", err)
	}
	for _, entry := range owner.sessions {
		conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(entry.endpoint.Local))
		if err != nil {
			t.Fatalf("listener leaked after receive failure: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
