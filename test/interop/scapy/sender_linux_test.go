package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
)

//nolint:paralleltest // The sender owns the fixed BFD source port.
func TestUDPSenderLoopbackTTL(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	loopback := netip.MustParseAddr("127.0.0.1")
	listener, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(loopback, 0)))
	if err != nil {
		t.Fatal(err)
	}
	receiver := ipv4.NewPacketConn(listener)
	t.Cleanup(func() {
		if closeErr := receiver.Close(); closeErr != nil {
			t.Errorf("close receiver: %v", closeErr)
		}
	})
	if controlErr := receiver.SetControlMessage(ipv4.FlagTTL, true); controlErr != nil {
		t.Fatal(controlErr)
	}
	address, ok := listener.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatal("UDP listener address missing")
	}
	sender, err := newUDPSender(loopback, address.AddrPort().Port())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("close sender: %v", closeErr)
		}
		if _, writeErr := sender.conn.WriteToUDP([]byte{0}, address); !errors.Is(writeErr, net.ErrClosed) {
			t.Errorf("write after Close = %v, want net.ErrClosed", writeErr)
		}
	})
	packet, err := validProbePacket()
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len(packet.Payload)+1)
	for _, ttl := range []int{255, 37} {
		packet.TTL = ttl
		if sendErr := sender.Send(ctx, packet); sendErr != nil {
			t.Fatal(sendErr)
		}
		deadline, _ := ctx.Deadline()
		if deadlineErr := receiver.SetReadDeadline(deadline); deadlineErr != nil {
			t.Fatal(deadlineErr)
		}
		n, control, source, err := receiver.ReadFrom(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(buffer[:n], packet.Payload) || control == nil || control.TTL != ttl {
			t.Fatalf("received payload %x control %v, want payload %x TTL %d", buffer[:n], control, packet.Payload, ttl)
		}
		if source.String() != netip.AddrPortFrom(loopback, bfdSourcePort).String() {
			t.Fatalf("source = %v, want loopback BFD source port %d", source, bfdSourcePort)
		}
	}
}
