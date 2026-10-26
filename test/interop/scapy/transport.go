package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

var (
	errUnknownPeerMode  = errors.New("unknown crafted peer startup mode")
	errPeerPacketTarget = errors.New("control packet targets another crafted session")
)

type peerMode uint8

const (
	peerSingleHop peerMode = iota
	peerMultihopTTL
)

type peerTransport struct {
	lastWire []byte
	port     uint16
	state    *bfd.AuthState
	keys     bfd.AuthKeyStore
	auth     bfd.Authenticator
}

func newPeerTransport(mode peerMode) (*peerTransport, error) {
	transport := &peerTransport{port: bfdDestinationPort}
	switch mode {
	case peerSingleHop:
		return transport, nil
	case peerMultihopTTL:
		transport.port = 4784
	default:
		return nil, fmt.Errorf("startup mode %d: %w", mode, errUnknownPeerMode)
	}
	var err error
	transport.state, err = bfd.NewAuthState(bfd.AuthTypeMeticulousKeyedSHA1)
	if err != nil {
		return nil, fmt.Errorf("peer auth state: %w", err)
	}
	// The public interop key matches the tracked daemon fixture; never use it in production.
	transport.keys, err = bfd.NewStaticAuthKeyStore(bfd.AuthKey{
		ID: 7, Type: bfd.AuthTypeMeticulousKeyedSHA1, Secret: []byte("gobfd-interop"),
	})
	if err != nil {
		return nil, fmt.Errorf("peer auth keys: %w", err)
	}
	transport.auth, err = bfd.NewAuthenticator(bfd.AuthTypeMeticulousKeyedSHA1)
	if err != nil {
		return nil, fmt.Errorf("peer authenticator: %w", err)
	}
	return transport, nil
}

func (transport *peerTransport) marshal(packet bfd.ControlPacket) ([]byte, error) {
	if transport.auth == nil {
		return marshalControl(packet)
	}
	wire := make([]byte, 52)
	if err := transport.auth.Sign(transport.state, transport.keys, &packet, wire, 0); err != nil {
		return nil, fmt.Errorf("sign peer Control: %w", err)
	}
	size, err := bfd.MarshalControlPacket(&packet, wire)
	if err != nil {
		return nil, fmt.Errorf("marshal signed peer Control: %w", err)
	}
	transport.lastWire = slices.Clone(wire[:size])
	return wire[:size], nil
}

func (transport *peerTransport) decode(wire []byte) (bfd.ControlPacket, error) {
	var packet bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(wire, &packet); err != nil {
		return packet, fmt.Errorf("decode peer Control: %w", err)
	}
	if !peerPacketTarget(packet) {
		return packet, fmt.Errorf("peer discriminator %d: %w", packet.YourDiscriminator, errPeerPacketTarget)
	}
	if transport.auth == nil {
		if packet.AuthPresent {
			return packet, fmt.Errorf("unexpected peer authentication: %w", bfd.ErrAuthTypeMismatch)
		}
		return packet, nil
	}
	// Verify overwrites the digest with key material; keep the received datagram intact.
	work := slices.Clone(wire)
	defer clear(work)
	var verified bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(work, &verified); err != nil {
		return packet, fmt.Errorf("decode peer auth work buffer: %w", err)
	}
	if err := transport.auth.Verify(transport.state, transport.keys, verified, work, len(work)); err != nil {
		return packet, fmt.Errorf("verify peer authentication: %w", err)
	}
	return packet, nil
}
