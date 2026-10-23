//go:build linux

package bfdload

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

var errPeerPacket = errors.New("packet cannot update the asynchronous reference peer")

const peerInitialTXUS = 1000000

// peerState is an independent RFC 5880 reference, not a target Session or FSM.
// The generator supports unauthenticated asynchronous Control profiles only.
type peerState struct {
	state               bfd.State
	diag                bfd.Diag
	localDiscriminator  uint32
	remoteDiscriminator uint32
	intervalUS          uint32
	multiplier          uint8
	remoteMinRXUS       uint32
	remoteDesiredTXUS   uint32
	remoteMultiplier    uint8
	poll                bool
	lastReceive         time.Time
}

func newPeerState(profile Profile, discriminator uint32) (peerState, error) {
	if err := profile.Validate(); err != nil {
		return peerState{}, err
	}
	interval := profile.Interval / time.Microsecond
	if discriminator == 0 || interval < 1 || interval > math.MaxUint32 {
		return peerState{}, fmt.Errorf("reference peer discriminator or interval: %w", ErrProfile)
	}
	return peerState{
		state: bfd.StateDown, localDiscriminator: discriminator,
		intervalUS: uint32(interval), multiplier: profile.DetectMultiplier, remoteMinRXUS: 1,
	}, nil
}

// receive consumes a decoded packet; session selection precedes any state mutation.
func (s *peerState) receive(packet bfd.ControlPacket, now time.Time) (bool, error) {
	if packet.AuthPresent || packet.Demand || packet.DesiredMinTxInterval == 0 {
		return false, fmt.Errorf("unsupported peer mode or remote interval: %w", errPeerPacket)
	}
	if packet.YourDiscriminator != 0 && packet.YourDiscriminator != s.localDiscriminator {
		return false, fmt.Errorf("reference peer discriminator mismatch: %w", errPeerPacket)
	}
	if packet.YourDiscriminator == 0 && packet.State != bfd.StateDown && packet.State != bfd.StateAdminDown {
		return false, fmt.Errorf("zero reference peer discriminator with live state: %w", errPeerPacket)
	}
	s.remoteDiscriminator = packet.MyDiscriminator
	s.remoteMinRXUS, s.remoteDesiredTXUS = packet.RequiredMinRxInterval, packet.DesiredMinTxInterval
	s.remoteMultiplier = packet.DetectMult
	if packet.Final {
		s.poll = false
	}
	if s.state == bfd.StateAdminDown {
		return false, fmt.Errorf("administratively stopped reference peer: %w", errPeerPacket)
	}
	s.transition(packet.State)
	s.lastReceive = now
	return packet.Poll, nil
}

// transition follows RFC 5880 section 6.8.6 without reusing the target's FSM.
func (s *peerState) transition(remote bfd.State) {
	switch {
	case remote == bfd.StateAdminDown:
		if s.state != bfd.StateDown {
			s.changeState(bfd.StateDown, bfd.DiagNeighborDown)
		}
	case s.state == bfd.StateDown:
		switch remote {
		case bfd.StateDown:
			s.changeState(bfd.StateInit, s.diag)
		case bfd.StateInit:
			s.changeState(bfd.StateUp, bfd.DiagNone)
		case bfd.StateAdminDown, bfd.StateUp:
		}
	case s.state == bfd.StateInit:
		if remote == bfd.StateInit || remote == bfd.StateUp {
			s.changeState(bfd.StateUp, bfd.DiagNone)
		}
	case s.state == bfd.StateUp && remote == bfd.StateDown:
		s.changeState(bfd.StateDown, bfd.DiagNeighborDown)
	}
}

func (s *peerState) changeState(state bfd.State, diag bfd.Diag) {
	previousInterval := s.desiredTXUS()
	s.state, s.diag = state, diag
	if s.desiredTXUS() != previousInterval {
		s.poll = true
	}
}

func (s *peerState) desiredTXUS() uint32 {
	if s.state == bfd.StateUp {
		return s.intervalUS
	}
	return max(s.intervalUS, peerInitialTXUS)
}

func (s *peerState) packet(final bool) bfd.ControlPacket {
	return bfd.ControlPacket{
		Version: bfd.Version, Length: bfd.MinPacketSizeNoAuth, State: s.state, Diag: s.diag,
		MyDiscriminator: s.localDiscriminator, YourDiscriminator: s.remoteDiscriminator,
		DesiredMinTxInterval: s.desiredTXUS(), RequiredMinRxInterval: s.intervalUS,
		DetectMult: s.multiplier, Poll: s.poll && !final, Final: final,
	}
}

func (s *peerState) periodicInterval() time.Duration {
	if s.remoteMinRXUS == 0 {
		return 0
	}
	return time.Duration(max(s.desiredTXUS(), s.remoteMinRXUS)) * time.Microsecond
}

func (s *peerState) deadline() time.Time {
	if s.lastReceive.IsZero() {
		return time.Time{}
	}
	interval := time.Duration(max(s.intervalUS, s.remoteDesiredTXUS)) * time.Microsecond
	detection := time.Duration(s.remoteMultiplier) * interval
	return s.lastReceive.Add(detection)
}

func (s *peerState) expire(now time.Time) bool {
	deadline := s.deadline()
	if deadline.IsZero() || now.Before(deadline) {
		return false
	}
	s.remoteDiscriminator, s.lastReceive = 0, time.Time{}
	if s.state == bfd.StateInit || s.state == bfd.StateUp {
		s.changeState(bfd.StateDown, bfd.DiagControlTimeExpired)
	}
	return true
}
