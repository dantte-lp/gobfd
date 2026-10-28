//go:build linux

package bfdload

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

var errScheduleInput = errors.New("invalid loss schedule progress")

type packetDisposition uint8

const (
	packetTransmit packetDisposition = iota
	packetRandomLoss
	packetBlackhole
)

type packetDecision struct {
	sequence    uint64
	disposition packetDisposition
	elapsed     time.Duration
}

// Each peer worker owns its stream; traversal order cannot couple different peers.
type lossSchedule struct {
	random         *rand.Rand
	lossPermille   uint64
	blackholeEvery time.Duration
	blackholeFor   time.Duration
	lastElapsed    time.Duration
	sequence       uint64
}

func newLossSchedule(profile Profile, peerID uint64) (*lossSchedule, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if peerID > sourcePortCapacity {
		return nil, fmt.Errorf("loss schedule peer capacity %d: %w", peerID, ErrProfile)
	}
	if peerID == 0 || int(peerID) > profile.Sessions {
		return nil, fmt.Errorf("loss schedule peer identity %d: %w", peerID, ErrProfile)
	}
	return &lossSchedule{
		// #nosec G404 -- Reproducible packet-loss simulation; never security randomness.
		random: rand.New(rand.NewPCG(profile.Seed, peerID)), lossPermille: profile.LossPermille,
		blackholeEvery: profile.BlackholeEvery, blackholeFor: profile.BlackholeFor,
	}, nil
}

func (s *lossSchedule) next(elapsed time.Duration) (packetDecision, error) {
	if elapsed < 0 || elapsed < s.lastElapsed || s.sequence == math.MaxUint64 {
		return packetDecision{}, fmt.Errorf("loss schedule time or sequence exhausted: %w", errScheduleInput)
	}
	s.lastElapsed = elapsed
	s.sequence++
	// Blackholes consume a draw too, so they cannot shift subsequent random omissions.
	draw := s.random.Uint64N(1000)
	disposition := packetTransmit
	switch {
	case s.blackholeEvery > 0 && elapsed >= s.blackholeEvery && elapsed%s.blackholeEvery < s.blackholeFor:
		disposition = packetBlackhole
	case draw < s.lossPermille:
		disposition = packetRandomLoss
	}
	return packetDecision{sequence: s.sequence, disposition: disposition, elapsed: elapsed}, nil
}
