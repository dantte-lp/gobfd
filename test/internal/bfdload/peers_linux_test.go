//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func ownedPeerFixture(t *testing.T, profile Profile, port uint16) (*Owner, PeerConfig) {
	t.Helper()

	endpoints := []OwnerEndpoint{
		{
			Local: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port),
			Peer:  netip.AddrPortFrom(netip.MustParseAddr("127.0.0.2"), port+2),
		},
		{
			Local: netip.AddrPortFrom(netip.MustParseAddr("::1"), port+1),
			Peer:  netip.AddrPortFrom(netip.MustParseAddr("::1"), port+3),
		},
	}
	owner, err := StartOwner(t.Context(), OwnerConfig{
		Sessions: endpoints, Interval: profile.Interval, DetectMultiplier: profile.DetectMultiplier,
	}, slog.New(slog.DiscardHandler))
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
	base := uint16(61000)
	used := make(map[uint16]bool)
	for _, session := range initial.Sessions {
		used[session.SourcePort] = true
	}
	for used[base] || used[base+1] {
		base += 2
	}
	return owner, PeerConfig{Profile: profile, Sessions: endpoints, SourcePortBase: base}
}

func TestPeersDriveActualOwnerAndAccount(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	owner, cfg := ownedPeerFixture(t, profile, 38021)
	observed, err := RunPeers(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Stopped || !observed.Completed || observed.ReferenceQualified || len(observed.Peers) != 2 {
		t.Fatalf("peer runtime was not complete or claimed reference qualification: %+v", observed)
	}
	encoded, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope["scope"]) != `"udp-peer-generator"` ||
		string(envelope["reference_qualified"]) != "false" || string(envelope["stopped"]) != "true" {
		t.Fatalf("JSON lost the generator-only scope or joined-worker status: %s", encoded)
	}
	var wirePeers []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["peers"], &wirePeers); err != nil || len(wirePeers) != 2 {
		t.Fatalf("JSON lost actual peer observations: count=%d error=%v", len(wirePeers), err)
	}
	for _, peer := range wirePeers {
		var count uint64
		if err := json.Unmarshal(peer["written"], &count); err != nil || count == 0 ||
			peer["receive_not_processed"] == nil || peer["write_canceled"] == nil {
			t.Fatalf("JSON lost physical write or cancellation accounting: peer=%s error=%v", envelope["peers"], err)
		}
	}
	var written uint64
	for _, peer := range observed.Peers {
		received := peer.Accepted + peer.Rejected + peer.ReceiveRandomOmitted +
			peer.ReceiveBlackholeOmitted + peer.ReceiveNotProcessed
		if peer.State != "Up" || peer.Written == 0 || peer.WriteErrors != 0 || peer.Rejected != 0 ||
			peer.Scheduled != peer.WriteAttempts+peer.RandomOmitted+peer.BlackholeOmitted ||
			peer.WriteAttempts != peer.Written+peer.WriteErrors+peer.WriteCanceled ||
			peer.Read != received {
			t.Fatalf("independent peer did not converge or lost packet accounting: %+v", peer)
		}
		written += peer.Written
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		target, snapshotErr := owner.Snapshot(t.Context())
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		committed := deliveryCount(target, bfd.DeliveryPacketOwner, bfd.DeliveryCommitted)
		if committed == written {
			for _, session := range target.Sessions {
				if session.Received == 0 || metricCounter(target.Metrics, "gobfd_bfd_state_transitions_total",
					map[string]string{"peer_addr": session.Endpoint.Peer.Addr().String(), "to_state": "Up"}) == 0 {
					t.Fatal("actual target never committed a packet and entered Up")
				}
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("successful UDP writes did not reach actual owner commits: written=%d committed=%d", written, committed)
		case <-tick.C:
		}
	}
	for _, endpoint := range cfg.Sessions {
		probe, bindErr := net.ListenUDP("udp", net.UDPAddrFromAddrPort(endpoint.Peer))
		if bindErr != nil {
			t.Fatalf("peer listener remained bound: %v", bindErr)
		}
		if closeErr := probe.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

func TestPeersRejectMalformedActualUDPBeforeUpdatingState(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 1, time.Second
	endpoint := OwnerEndpoint{
		Local: netip.MustParseAddrPort("127.0.0.1:38081"), Peer: netip.MustParseAddrPort("127.0.0.2:38083"),
	}
	target, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(endpoint.Local))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type outcome struct {
		report PeerObservation
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		cfg := PeerConfig{Profile: profile, Sessions: []OwnerEndpoint{endpoint}, SourcePortBase: 61500}
		report, runErr := RunPeers(ctx, cfg, slog.New(slog.DiscardHandler))
		done <- outcome{report: report, err: runErr}
	}()
	if deadlineErr := target.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
		t.Fatal(deadlineErr)
	}
	var wire [64]byte
	n, _, err := target.ReadFromUDPAddrPort(wire[:])
	if err != nil {
		t.Fatal(err)
	}
	var initial bfd.ControlPacket
	if parseErr := bfd.UnmarshalControlPacket(wire[:n], &initial); parseErr != nil {
		t.Fatal(parseErr)
	}
	sender, err := netio.NewUDPSender(endpoint.Local.Addr(), 61400, false, slog.New(slog.DiscardHandler),
		netio.WithDstPort(endpoint.Peer.Port()))
	if err != nil {
		t.Fatal(err)
	}
	badErr := sender.SendPacket(t.Context(), []byte{0}, endpoint.Peer.Addr())
	valid := bfd.ControlPacket{
		Version: 1, State: bfd.StateInit, DetectMult: 3, MyDiscriminator: 777,
		YourDiscriminator: initial.MyDiscriminator, DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 100000,
	}
	n, marshalErr := bfd.MarshalControlPacket(&valid, wire[:])
	if marshalErr != nil {
		t.Fatal(errors.Join(badErr, marshalErr, sender.Close()))
	}
	goodErr := sender.SendPacket(t.Context(), wire[:n], endpoint.Peer.Addr())
	if joinedErr := errors.Join(badErr, goodErr, sender.Close()); joinedErr != nil {
		t.Fatal(joinedErr)
	}
	select {
	case result := <-done:
		if result.err != nil || len(result.report.Peers) != 1 {
			t.Fatalf("peer input run failed: %v", result.err)
		}
		peer := result.report.Peers[0]
		if peer.Read != 2 || peer.Rejected != 1 || peer.Accepted != 1 || peer.State != "Up" {
			t.Fatalf("malformed wire input affected the state or escaped accounting: %+v", peer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer input probe did not join")
	}
}

func TestPeersBlackholeCausesDownAndRecovery(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Duration, profile.BlackholeEvery, profile.BlackholeFor = 4*time.Second, 2*time.Second, 600*time.Millisecond
	owner, cfg := ownedPeerFixture(t, profile, 38061)
	observed, err := RunPeers(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range observed.Peers {
		if peer.UpTransitions < 2 || peer.DownTransitions == 0 ||
			peer.BlackholeOmitted == 0 || peer.ReceiveBlackholeOmitted == 0 || peer.WriteErrors != 0 {
			t.Fatalf("blackhole did not traverse actual UDP Down/recovery: %+v", peer)
		}
	}
	target, err := owner.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range cfg.Sessions {
		labels := map[string]string{"peer_addr": endpoint.Peer.Addr().String(), "to_state": "Up"}
		if metricCounter(target.Metrics, "gobfd_bfd_state_transitions_total", labels) < 2 {
			t.Fatal("actual target did not reenter Up after the blackhole")
		}
	}
}

func TestPeersFullRandomLossIsAnOmissionInBothDirections(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Duration, profile.LossPermille = 2*time.Second, 1000
	owner, cfg := ownedPeerFixture(t, profile, 38071)
	observed, err := RunPeers(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range observed.Peers {
		if peer.Scheduled == 0 || peer.RandomOmitted != peer.Scheduled || peer.Written != 0 ||
			peer.Read == 0 || peer.Accepted != 0 || peer.Rejected != 0 ||
			peer.Read != peer.ReceiveRandomOmitted+peer.ReceiveNotProcessed {
			t.Fatalf("loss was silently counted as a write or accepted receive: %+v", peer)
		}
	}
	target, err := owner.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if deliveryCount(target, bfd.DeliveryPacketOwner, bfd.DeliveryCommitted) != 0 {
		t.Fatal("an intentionally omitted peer packet was recorded as an owner commit")
	}
}

func TestPeerQualificationRejectsUnbalancedOrUnboundInputs(t *testing.T) {
	t.Parallel()

	profile, err := DefaultProfile("reference")
	if err != nil {
		t.Fatal(err)
	}
	cfg := PeerConfig{Profile: profile, SourcePortBase: 49152}
	for i := range 1000 {
		local := netip.MustParseAddr("198.18.0.1")
		peer := netip.AddrFrom4([4]byte{198, 18, byte((i + 2) >> 8), byte(i + 2)})
		if i >= 500 {
			local = netip.MustParseAddr("fd00::1")
			peer = netip.AddrFrom16([16]byte{0xfd, 14: byte((i + 2) >> 8), 15: byte(i + 2)})
		}
		cfg.Sessions = append(cfg.Sessions, OwnerEndpoint{
			Local: netip.AddrPortFrom(local, 3784), Peer: netip.AddrPortFrom(peer, 3784), Interface: "lo",
		})
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*PeerConfig)
	}{
		{"all_ipv4", func(c *PeerConfig) {
			for i := range c.Sessions {
				c.Sessions[i].Local = netip.MustParseAddrPort("198.18.0.1:3784")
				addr := netip.AddrFrom4([4]byte{198, 18, byte((i + 2) >> 8), byte(i + 2)})
				c.Sessions[i].Peer = netip.AddrPortFrom(addr, 3784)
			}
		}},
		{"all_ipv6", func(c *PeerConfig) {
			for i := range c.Sessions {
				c.Sessions[i].Local = netip.MustParseAddrPort("[fd00::1]:3784")
				addr := netip.AddrFrom16([16]byte{0xfd, 14: byte((i + 2) >> 8), 15: byte(i + 2)})
				c.Sessions[i].Peer = netip.AddrPortFrom(addr, 3784)
			}
		}},
		{"missing_interface", func(c *PeerConfig) {
			for i := range c.Sessions {
				c.Sessions[i].Interface = ""
			}
		}},
		{"non_rfc_port", func(c *PeerConfig) {
			c.Sessions[0].Peer = netip.AddrPortFrom(c.Sessions[0].Peer.Addr(), 38000)
		}},
		{"source_port_overflow", func(c *PeerConfig) { c.SourcePortBase = 65000 }},
		{"reduced_sessions", func(c *PeerConfig) { c.Sessions = c.Sessions[:999] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			changed := cfg
			changed.Sessions = slices.Clone(cfg.Sessions)
			tc.edit(&changed)
			if err := changed.Validate(); !errors.Is(err, ErrProfile) {
				t.Fatalf("qualification inputs silently changed: %v", err)
			}
		})
	}
}

func TestPeersLateStartupRollbackPreservesOccupiedSocket(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	endpoints := []OwnerEndpoint{
		{Local: netip.MustParseAddrPort("127.0.0.1:38031"), Peer: netip.MustParseAddrPort("127.0.0.2:38033")},
		{Local: netip.MustParseAddrPort("[::1]:38032"), Peer: netip.MustParseAddrPort("[::1]:38034")},
	}
	occupied, err := net.ListenUDP("udp6", net.UDPAddrFromAddrPort(endpoints[1].Peer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := occupied.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	observed, runErr := RunPeers(t.Context(), PeerConfig{Profile: profile, Sessions: endpoints, SourcePortBase: 61100},
		slog.New(slog.DiscardHandler))
	if !errors.Is(runErr, syscall.EADDRINUSE) || !observed.Stopped || observed.Completed || len(observed.Peers) != 1 {
		t.Fatalf("late bind failure: stopped=%t completed=%t peers=%d error=%v",
			observed.Stopped, observed.Completed, len(observed.Peers), runErr)
	}
	for _, endpoint := range []netip.AddrPort{
		endpoints[0].Peer, netip.AddrPortFrom(endpoints[0].Peer.Addr(), 61100),
	} {
		probe, bindErr := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(endpoint))
		if bindErr != nil {
			t.Fatalf("partially created peer socket leaked: %s: %v", endpoint, bindErr)
		}
		if closeErr := probe.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if deadlineErr := occupied.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
		t.Fatal(deadlineErr)
	}
	sender, err := net.DialUDP("udp6", nil, net.UDPAddrFromAddrPort(endpoints[1].Peer))
	if err != nil {
		t.Fatal(err)
	}
	_, sendErr := sender.Write([]byte{7})
	if err := errors.Join(sendErr, sender.Close()); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if n, readErr := occupied.Read(data[:]); readErr != nil || n != 1 || data[0] != 7 {
		t.Fatalf("rollback damaged a socket it did not own: n=%d error=%v", n, readErr)
	}
}

func TestPeersCancelAfterActualTransmission(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions = 1
	endpoint := OwnerEndpoint{
		Local: netip.MustParseAddrPort("127.0.0.1:38041"), Peer: netip.MustParseAddrPort("127.0.0.2:38043"),
	}
	target, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(endpoint.Local))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type outcome struct {
		report PeerObservation
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		cfg := PeerConfig{Profile: profile, Sessions: []OwnerEndpoint{endpoint}, SourcePortBase: 61200}
		report, runErr := RunPeers(ctx, cfg, slog.New(slog.DiscardHandler))
		done <- outcome{report: report, err: runErr}
	}()
	if deadlineErr := target.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
		t.Fatal(deadlineErr)
	}
	var wire [64]byte
	n, source, err := target.ReadFromUDPAddrPort(wire[:])
	if err != nil {
		t.Fatal(err)
	}
	var packet bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(wire[:n], &packet); err != nil {
		t.Fatal(err)
	}
	if source != netip.AddrPortFrom(endpoint.Peer.Addr(), 61200) || packet.State != bfd.StateDown ||
		packet.MyDiscriminator == 0 || packet.YourDiscriminator != 0 || packet.DesiredMinTxInterval != 1000000 {
		t.Fatalf("initial BFD packet has incorrect state or source identity: packet=%+v source=%s", packet, source)
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || !result.report.Stopped || result.report.Completed ||
			len(result.report.Peers) != 1 || result.report.Peers[0].Written == 0 {
			t.Fatalf("peer cancellation: stopped=%t completed=%t peers=%d error=%v",
				result.report.Stopped, result.report.Completed, len(result.report.Peers), result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer cancellation did not interrupt and join its UDP workers")
	}
}
