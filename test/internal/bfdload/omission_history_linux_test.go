//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

type omissionHistoryJSON struct {
	Direction         string    `json:"direction"`
	FirstNS           int64     `json:"first_ns"`
	LastNS            int64     `json:"last_ns"`
	Count             uint64    `json:"count"`
	Valid             bool      `json:"valid"`
	MyDiscriminator   uint32    `json:"my_discriminator"`
	YourDiscriminator uint32    `json:"your_discriminator"`
	State             bfd.State `json:"state"`
	DesiredMinTXUS    uint32    `json:"desired_min_tx_us"`
}

func TestPeerBlackholeHistoryOverflowCannotComplete(t *testing.T) {
	t.Parallel()
	t.Logf("PeerOmissionSegmentBytes=%d PeerBlackholeWindowBytes=%d",
		reflect.TypeFor[PeerOmissionSegment]().Size(), reflect.TypeFor[PeerBlackholeWindow]().Size())
	for _, mode := range []string{"times", "identities"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			peer := blackholePeer(t)
			peer.state = fixturePeerState(t)
			started := time.Now()
			limit := 128
			if mode == "identities" {
				limit = 16
			}
			for index := range limit + 1 {
				if mode == "identities" {
					packet := fixturePeerPacket(bfd.StateDown)
					packet.MyDiscriminator = uint32(index + 1)
					if _, err := peer.state.receive(packet, started); err != nil {
						t.Fatal(err)
					}
				}
				err := peer.send(t.Context(), started.Add(10*time.Millisecond), started, false)
				if (index < limit && err != nil) || (index == limit && !errors.Is(err, ErrProfile)) {
					t.Fatalf("omission %d did not enforce its bounded history: %v", index, err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			group := peerGroup{peers: []*peerRuntime{peer}, started: started}
			report, err := group.finish(ctx, PeerObservation{}, true, nil)
			if !errors.Is(err, ErrProfile) || report.Completed || peer.observed.BlackholeEventsOmitted != 1 {
				t.Fatalf("bounded history overflow passed canceled finish: report=%+v error=%v", report, err)
			}
			window := peer.observed.BlackholeWindows[0]
			var times int
			for _, segment := range window.Segments {
				times += len(segment.OffsetsNS)
			}
			if times != limit || len(window.Segments) > 16 {
				t.Fatalf("overflow retained unbounded or missing prior history: times=%d segments=%d", times, len(window.Segments))
			}
		})
	}
}

func decodeOmissionHistory(t *testing.T, observed PeerSessionObservation) []omissionHistoryJSON {
	t.Helper()
	encoded, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Windows []struct {
			Segments []omissionHistoryJSON `json:"segments"`
		} `json:"blackhole_windows"`
	}
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Windows) != 1 {
		t.Fatalf("expected one actual blackhole window, got %d", len(report.Windows))
	}
	return report.Windows[0].Segments
}

func TestPeerBlackholeHistoryRetainsFrozenPacketIdentity(t *testing.T) {
	t.Parallel()
	peer := blackholePeer(t)
	peer.state = fixturePeerState(t)
	started := time.Now()
	for index, state := range []bfd.State{bfd.StateDown, bfd.StateInit} {
		at := started.Add(time.Duration(index) * time.Millisecond)
		if _, err := peer.state.receive(fixturePeerPacket(state), at); err != nil {
			t.Fatal(err)
		}
	}
	if peer.state.state != bfd.StateUp {
		t.Fatal("reference peer did not reach Up before the first omission")
	}
	assertOmittedPacketValid(t, peer.state.packet(false))
	if err := peer.send(t.Context(), started.Add(10*time.Millisecond), started, false); err != nil {
		t.Fatal(err)
	}
	replacement := fixturePeerPacket(bfd.StateDown)
	replacement.MyDiscriminator = 88
	if _, err := peer.state.receive(replacement, started.Add(11*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	assertOmittedPacketValid(t, peer.state.packet(false))
	if err := peer.send(t.Context(), started.Add(12*time.Millisecond), started, false); err != nil {
		t.Fatal(err)
	}
	if peer.observed.Scheduled != 2 || peer.observed.BlackholeOmitted != 2 || peer.observed.WriteAttempts != 0 ||
		len(peer.observed.BlackholeWindows) != 1 || peer.observed.BlackholeWindows[0].TXOmitted != 2 {
		t.Fatalf("fixture did not execute two actual omissions in one window: %+v", peer.observed)
	}
	history := decodeOmissionHistory(t, peer.observed)
	want := []omissionHistoryJSON{
		{
			Direction: "tx", FirstNS: 10000000, LastNS: 10000000, Count: 1, Valid: true,
			MyDiscriminator: 11, YourDiscriminator: 77, State: bfd.StateUp, DesiredMinTXUS: 100000,
		},
		{
			Direction: "tx", FirstNS: 12000000, LastNS: 12000000, Count: 1, Valid: true,
			MyDiscriminator: 11, YourDiscriminator: 88, State: bfd.StateDown, DesiredMinTXUS: 1000000,
		},
	}
	if len(history) != len(want) {
		t.Fatalf("actual omissions lost historical packet identity: history=%+v want=%+v", history, want)
	}
	for index, expected := range want {
		if history[index] != expected {
			t.Fatalf("omission %d borrowed another packet identity: got=%+v want=%+v", index, history[index], expected)
		}
	}
}

func assertOmittedPacketValid(t *testing.T, packet bfd.ControlPacket) {
	t.Helper()
	var wire [bfd.MinPacketSizeNoAuth]byte
	n, err := bfd.MarshalControlPacket(&packet, wire[:])
	if err != nil {
		t.Fatal(err)
	}
	var decoded bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(wire[:n], &decoded); err != nil {
		t.Fatalf("would-be omitted packet is not a valid Control packet: %v", err)
	}
}

func TestPeerBlackholeHistoryDoesNotQualifyUnvalidatedInput(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"invalid_packet", "wrong_source", "wrong_destination", "invalid_ttl"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				peer := blackholePeer(t)
				peer.state = fixturePeerState(t)
				peer.observed.Endpoint = OwnerEndpoint{
					Local: netip.MustParseAddrPort("192.0.2.1:3784"), Peer: netip.MustParseAddrPort("192.0.2.2:3784"),
				}
				input := peerInput{
					packet: fixturePeerPacket(bfd.StateDown),
					meta: netio.PacketMeta{
						SrcAddr: peer.observed.Endpoint.Local.Addr(), DstAddr: peer.observed.Endpoint.Peer.Addr(), TTL: 255,
					},
				}
				switch mode {
				case "invalid_packet":
					input.invalid = true
				case "wrong_source":
					input.meta.SrcAddr = netip.MustParseAddr("192.0.2.3")
				case "wrong_destination":
					input.meta.DstAddr = netip.MustParseAddr("192.0.2.3")
				case "invalid_ttl":
					input.meta.TTL = 254
				}
				before := peer.state
				if err := peer.onInput(t.Context(), input, time.Now().Add(-10*time.Millisecond), nil); err != nil {
					t.Fatal(err)
				}
				if peer.state != before || peer.observed.ReceiveBlackholeOmitted != 1 || peer.observed.Accepted != 0 ||
					peer.observed.Rejected != 0 || len(peer.observed.BlackholeWindows) != 1 ||
					peer.observed.BlackholeWindows[0].RXOmitted != 1 {
					t.Fatalf("blackhole discard unexpectedly entered packet acceptance: %+v", peer.observed)
				}
				history := decodeOmissionHistory(t, peer.observed)
				if len(history) != 1 || history[0].Direction != "rx" || history[0].Valid || history[0].Count != 1 {
					t.Fatalf("unvalidated actual RX discard lost its unknown validity: %+v", history)
				}
			})
		})
	}
}
