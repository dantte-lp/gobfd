package main

import (
	"io"
	"slices"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestPeerTTLProbeTransaction(t *testing.T) {
	t.Parallel()
	transport, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	var frames []vectorPacket
	periodic := 0
	loop := &peerLoop{
		crafted: newPeer(), transport: transport, mode: peerMultihopTTL, output: io.Discard,
		send: func(bfd.ControlPacket) error { periodic++; return nil },
		sendRaw: func(frame vectorPacket) error {
			frame.Payload = slices.Clone(frame.Payload)
			frames = append(frames, frame)
			return nil
		},
	}
	loop.crafted.state, loop.crafted.remoteState = bfd.StateUp, bfd.StateUp
	loop.crafted.remoteDiscr = testRemoteDiscriminator
	if _, err := transport.marshal(loop.crafted.packet(false)); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(100, 0)
	loop.lastSent = at
	sequence := transport.state.XmitAuthSeq
	if err := loop.probeSignal(at); err != nil {
		t.Fatal(err)
	}
	if loop.probe == nil || loop.probe.stage != ttlProbePaused || len(frames) != 0 {
		t.Fatal("probe did not pause before snapshot")
	}
	if err := loop.tick(at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if periodic != 0 {
		t.Fatal("periodic transmission escaped pause")
	}
	if err := loop.probeSignal(at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0].TTL != 254 || transport.state.XmitAuthSeq != sequence+1 {
		t.Fatal("missing prior-sequence replay")
	}
	if !loop.deadline().Equal(at.Add(time.Second + 200*time.Millisecond)) {
		t.Fatal("good probe not scheduled at bounded deadline")
	}
	if err := loop.tick(at.Add(time.Second + 199*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatal("good probe sent early")
	}
	if err := loop.tick(at.Add(time.Second + 200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[1].TTL != 253 {
		t.Fatal("missing signed below-floor probe")
	}
	if err := loop.tick(at.Add(time.Second + 400*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || frames[2].TTL != 254 || !slices.Equal(frames[1].Payload, frames[2].Payload) ||
		transport.state.XmitAuthSeq != sequence+1 {
		t.Fatal("good probe changed signed payload/sequence")
	}
	var packet bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(frames[2].Payload, &packet); err != nil {
		t.Fatal(err)
	}
	if !packet.Poll || packet.Final || packet.Auth == nil || packet.Auth.Type != bfd.AuthTypeMeticulousKeyedSHA1 {
		t.Fatal("probe is not an authenticated Poll")
	}
	final := remoteControl(bfd.StateUp)
	final.Final = true
	if err := loop.receive(final); err != nil {
		t.Fatal(err)
	}
	if loop.probe.stage != ttlProbeComplete {
		t.Fatal("authenticated Final not recorded")
	}
	if err := loop.tick(at.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if periodic != 0 {
		t.Fatal("periodic transmission resumed before counter snapshot")
	}
	if err := loop.probeSignal(at.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if loop.probe != nil || loop.probeIndex != 1 {
		t.Fatal("probe did not resume explicitly")
	}
}

func TestPeerTTLReplayBeforeFreshBoundary(t *testing.T) {
	t.Parallel()
	transport, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	crafted := newPeer()
	crafted.state, crafted.remoteState, crafted.remoteDiscr = bfd.StateUp, bfd.StateUp, testRemoteDiscriminator
	accepted, err := transport.marshal(crafted.packet(false))
	if err != nil {
		t.Fatal(err)
	}
	var sent []vectorPacket
	loop := &peerLoop{
		crafted: crafted, transport: transport, mode: peerMultihopTTL, output: io.Discard,
		sendRaw: func(frame vectorPacket) error { sent = append(sent, frame); return nil },
	}
	at := time.Unix(100, 0)
	if err := loop.probeSignal(at); err != nil {
		t.Fatal(err)
	}
	if err := loop.probeSignal(at); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].TTL != 254 || !slices.Equal(sent[0].Payload, accepted) {
		t.Fatal("fresh probe cannot expose reload reset of receive replay knowledge")
	}
}
