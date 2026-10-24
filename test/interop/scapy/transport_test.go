package main

import (
	"context"
	"io"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestPeerTransportRejectBeforeFSM(t *testing.T) {
	t.Parallel()
	sender, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	loop := &peerLoop{crafted: newPeer(), transport: receiver, output: io.Discard}
	loop.crafted.state = bfd.StateInit
	wire, err := sender.marshal(remoteControl(bfd.StateUp))
	if err != nil {
		t.Fatal(err)
	}
	before, authBefore := *loop.crafted, *receiver.state
	invalid := slices.Clone(wire)
	invalid[len(invalid)-1] ^= 1
	if err := loop.receiveWire(invalid); err != nil {
		t.Fatal(err)
	}
	if *loop.crafted != before || *receiver.state != authBefore {
		t.Fatal("invalid authentication mutated peer FSM/auth state")
	}
	if err := loop.receiveWire(wire); err != nil {
		t.Fatal(err)
	}
	if loop.crafted.state != bfd.StateUp || !receiver.state.AuthSeqKnown {
		t.Fatal("valid authenticated receive not committed")
	}
}

func TestPeerTransportSessionAdmissionBeforeAuth(t *testing.T) {
	t.Parallel()
	sender, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	loop := &peerLoop{crafted: newPeer(), transport: receiver, output: io.Discard}
	loop.crafted.state = bfd.StateInit
	for _, silent := range []bool{false, true} {
		loop.crafted.silent = silent
		packet := remoteControl(bfd.StateUp)
		if !silent {
			packet.YourDiscriminator = peerDiscriminator + 1
		}
		wire, marshalErr := sender.marshal(packet)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		before, authBefore := *loop.crafted, *receiver.state
		if receiveErr := loop.receiveWire(wire); receiveErr != nil {
			t.Fatal(receiveErr)
		}
		if *loop.crafted != before || *receiver.state != authBefore {
			t.Fatalf("inadmissible packet mutated state: silent=%t", silent)
		}
	}
}

func TestPeerTransportReceiveBufferOwnership(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	packets := make(chan []byte, 2)
	done := make(chan struct{})
	go func() { defer close(done); readPeerPackets(ctx, listener, netip.MustParseAddr("127.0.0.1"), packets) }()
	t.Cleanup(func() {
		cancel()
		if closeErr := listener.Close(); closeErr != nil {
			t.Errorf("close reader: %v", closeErr)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("UDP reader leaked")
		}
	})
	address, ok := listener.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatal("UDP listener address missing")
	}
	client, err := net.DialUDP("udp4", nil, address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("close sender: %v", err)
		}
	}()
	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var first []byte
	for index, wire := range [][]byte{[]byte("first-wire"), []byte("second-wire")} {
		if _, err := client.Write(wire); err != nil {
			t.Fatal(err)
		}
		select {
		case received := <-packets:
			if index == 0 {
				first = received
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if string(first) != "first-wire" {
		t.Fatalf("queued datagram changed after next read: %q", first)
	}
}

func TestPeerTransportStartup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		mode          peerMode
		port          uint16
		authenticated bool
	}{
		{peerSingleHop, 3784, false}, {peerMultihopTTL, 4784, true},
	} {
		transport, err := newPeerTransport(test.mode)
		if err != nil {
			t.Fatal(err)
		}
		if transport.port != test.port || (transport.auth != nil) != test.authenticated {
			t.Fatalf("startup mode %d: port=%d authenticated=%t", test.mode, transport.port, transport.auth != nil)
		}
	}
	if _, err := newPeerTransport(9); err == nil {
		t.Fatal("unknown startup mode accepted")
	}
}

func TestPeerTransportSignedSequence(t *testing.T) {
	t.Parallel()
	sender, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	var previous uint32
	for index := range 3 {
		wire, marshalErr := sender.marshal(remoteControl(bfd.StateUp))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		original := slices.Clone(wire)
		packet, decodeErr := receiver.decode(wire)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if len(wire) != 52 || !packet.AuthPresent || packet.Auth == nil ||
			packet.Auth.Type != bfd.AuthTypeMeticulousKeyedSHA1 || packet.Auth.KeyID != 7 {
			t.Fatal("startup profile did not produce meticulous SHA1 PDU")
		}
		if index > 0 && packet.Auth.SequenceNumber-previous != 1 {
			t.Fatal("sequence reset or skipped")
		}
		previous = packet.Auth.SequenceNumber
		if !slices.Equal(wire, original) {
			t.Fatal("verification overwrote captured wire with key material")
		}
		if _, replayErr := receiver.decode(wire); replayErr == nil {
			t.Fatal("meticulous replay accepted")
		}
	}
}

func TestPeerTransportRejectBeforeState(t *testing.T) {
	t.Parallel()
	sender, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := newPeerTransport(peerMultihopTTL)
	if err != nil {
		t.Fatal(err)
	}
	if receiver.state == nil {
		t.Fatal("missing authentication state")
	}
	wire, err := sender.marshal(remoteControl(bfd.StateUp))
	if err != nil {
		t.Fatal(err)
	}
	before := *receiver.state
	invalid := slices.Clone(wire)
	invalid[len(invalid)-1] ^= 1
	if _, decodeErr := receiver.decode(invalid); decodeErr == nil {
		t.Fatal("invalid MAC accepted")
	}
	if *receiver.state != before {
		t.Fatal("rejected MAC advanced receive state")
	}
	if _, decodeErr := receiver.decode(wire); decodeErr != nil {
		t.Fatalf("valid packet after rejection: %v", decodeErr)
	}
	plain, err := newPeerTransport(peerSingleHop)
	if err != nil {
		t.Fatal(err)
	}
	if _, decodeErr := plain.decode(wire); decodeErr == nil {
		t.Fatal("unauthenticated startup accepted auth packet")
	}
	noAuth, err := plain.marshal(remoteControl(bfd.StateUp))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.decode(noAuth); err == nil {
		t.Fatal("authenticated startup accepted missing auth")
	}
}
