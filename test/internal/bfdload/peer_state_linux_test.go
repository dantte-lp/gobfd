//go:build linux

package bfdload

import (
	"errors"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func fixturePeerState(t *testing.T) peerState {
	t.Helper()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	state, err := newPeerState(profile, 11)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func fixturePeerPacket(state bfd.State) bfd.ControlPacket {
	return bfd.ControlPacket{
		Version: 1, Length: 24, State: state, DetectMult: 3,
		MyDiscriminator: 77, YourDiscriminator: 11,
		DesiredMinTxInterval: 100000, RequiredMinRxInterval: 100000,
	}
}

func TestPeerReferenceStateTransitions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		local bfd.State
		want  [4]bfd.State
	}{
		{bfd.StateDown, [4]bfd.State{bfd.StateDown, bfd.StateInit, bfd.StateUp, bfd.StateDown}},
		{bfd.StateInit, [4]bfd.State{bfd.StateDown, bfd.StateInit, bfd.StateUp, bfd.StateUp}},
		{bfd.StateUp, [4]bfd.State{bfd.StateDown, bfd.StateDown, bfd.StateUp, bfd.StateUp}},
	} {
		for remote, want := range tc.want {
			t.Run(tc.local.String()+"/"+bfd.State(remote).String(), func(t *testing.T) {
				t.Parallel()

				state := fixturePeerState(t)
				state.state = tc.local
				if _, err := state.receive(fixturePeerPacket(bfd.State(remote)), time.Now()); err != nil {
					t.Fatal(err)
				}
				if state.state != want {
					t.Fatalf("local=%s remote=%s got=%s want=%s", tc.local, bfd.State(remote), state.state, want)
				}
			})
		}
	}
}

func TestPeerRejectedPacketCannotRefreshDetection(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		edit func(*bfd.ControlPacket)
	}{
		{"wrong_discriminator", func(p *bfd.ControlPacket) { p.YourDiscriminator = 12 }},
		{"zero_discriminator_up", func(p *bfd.ControlPacket) { p.YourDiscriminator = 0; p.State = bfd.StateUp }},
		{"authentication", func(p *bfd.ControlPacket) { p.AuthPresent = true }},
		{"demand", func(p *bfd.ControlPacket) { p.Demand = true }},
		{"zero_remote_interval", func(p *bfd.ControlPacket) { p.DesiredMinTxInterval = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			state := fixturePeerState(t)
			now := time.Now()
			if _, err := state.receive(fixturePeerPacket(bfd.StateDown), now); err != nil {
				t.Fatal(err)
			}
			before := state
			packet := fixturePeerPacket(bfd.StateInit)
			tc.edit(&packet)
			if _, err := state.receive(packet, now.Add(time.Second)); !errors.Is(err, errPeerPacket) {
				t.Fatalf("unsupported or misdirected packet accepted: %v", err)
			}
			if state != before {
				t.Fatal("rejected packet mutated session state or refreshed detection")
			}
			if state.expire(now.Add(300*time.Millisecond - time.Nanosecond)) {
				t.Fatal("detection expired before its negotiated deadline")
			}
			if !state.expire(now.Add(300*time.Millisecond)) || state.state != bfd.StateDown ||
				state.diag != bfd.DiagControlTimeExpired || state.remoteDiscriminator != 0 {
				t.Fatal("detection did not expire at its original deadline and clear remote identity")
			}
			if state.expire(now.Add(time.Second)) {
				t.Fatal("one expired receive window was counted repeatedly")
			}
		})
	}
}

func TestPeerPollFinalNegotiationAndRestart(t *testing.T) {
	t.Parallel()

	state := fixturePeerState(t)
	if packet := state.packet(false); packet.State != bfd.StateDown || packet.YourDiscriminator != 0 ||
		packet.DesiredMinTxInterval != 1000000 {
		t.Fatalf("initial peer violated the non-Up one-second minimum: %+v", packet)
	}
	now := time.Now()
	packet := fixturePeerPacket(bfd.StateInit)
	packet.DesiredMinTxInterval, packet.RequiredMinRxInterval, packet.DetectMult, packet.Poll = 250000, 400000, 5, true
	final, err := state.receive(packet, now)
	if err != nil || !final || state.state != bfd.StateUp || !state.poll ||
		state.periodicInterval() != 400*time.Millisecond || !state.deadline().Equal(now.Add(1250*time.Millisecond)) {
		t.Fatalf("asymmetric negotiation or Poll response failed: final=%v state=%+v error=%v", final, state, err)
	}
	response := state.packet(true)
	if !response.Final || response.Poll || response.DesiredMinTxInterval != 100000 || response.YourDiscriminator != 77 {
		t.Fatalf("Final response did not preserve local Poll or advertise the new interval: %+v", response)
	}
	packet.Poll, packet.Final = false, true
	if _, err = state.receive(packet, now); err != nil || state.packet(false).Poll {
		t.Fatalf("Final did not complete the outstanding Poll: %v", err)
	}
	packet.RequiredMinRxInterval, packet.Final, packet.Poll = 0, false, true
	if final, err = state.receive(packet, now); err != nil || !final || state.periodicInterval() != 0 {
		t.Fatalf("zero remote RX interval suppressed Final or allowed periodic TX: final=%v error=%v", final, err)
	}
	packet = fixturePeerPacket(bfd.StateDown)
	packet.MyDiscriminator, packet.YourDiscriminator = 88, 0
	if _, err = state.receive(packet, now); err != nil || state.state != bfd.StateDown ||
		state.diag != bfd.DiagNeighborDown || state.packet(false).YourDiscriminator != 88 {
		t.Fatalf("remote restart did not replace its discriminator and signal Down: %v", err)
	}
	packet.State, packet.YourDiscriminator = bfd.StateInit, 11
	if _, err = state.receive(packet, now); err != nil || state.state != bfd.StateUp {
		t.Fatalf("remote restart could not recover the session: %v", err)
	}
}
