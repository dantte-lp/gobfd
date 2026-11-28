package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

var errPeerTTLProbe = errors.New("invalid TTL probe progression")

type ttlProbeStage uint8

const (
	ttlProbePaused ttlProbeStage = iota + 1
	ttlProbeReplay
	ttlProbeWaiting
	ttlProbeAwaitFinal
	ttlProbeComplete
)

const peerTTLProbeDelay = 200 * time.Millisecond

type peerTTLProbe struct {
	stage    ttlProbeStage
	floor    int
	wire     []byte
	replay   []byte
	deadline time.Time
}

func peerTTLFloors() []int { return []int{254, 250, 254, 254, 250, 254, 254} }

func (loop *peerLoop) probeSignal(at time.Time) error {
	if loop.mode != peerMultihopTTL || loop.transport == nil || loop.transport.auth == nil {
		return fmt.Errorf("probe requires authenticated multihop startup: %w", errPeerTTLProbe)
	}
	if loop.probe == nil {
		floors := peerTTLFloors()
		if loop.probeIndex >= len(floors) || loop.crafted.state != bfd.StateUp ||
			loop.crafted.remoteState != bfd.StateUp || loop.crafted.poll || len(loop.transport.lastWire) == 0 {
			return fmt.Errorf("probe requires established quiet phase: %w", errPeerTTLProbe)
		}
		loop.probe = &peerTTLProbe{
			floor: floors[loop.probeIndex], stage: ttlProbePaused,
			replay: slices.Clone(loop.transport.lastWire),
		}
		writePeerEvent(loop.output, "ttl-probe-ready", strconv.Itoa(loop.probe.floor))
		return nil
	}
	probe := loop.probe
	switch probe.stage {
	case ttlProbePaused:
		if err := loop.sendRaw(vectorPacket{
			Detail: "previously accepted authenticated replay", Payload: probe.replay, TTL: probe.floor,
		}); err != nil {
			return fmt.Errorf("send prior-sequence replay: %w", err)
		}
		writePeerEvent(loop.output, "ttl-probe-replay", strconv.Itoa(probe.floor))
		packet := loop.crafted.packet(false)
		packet.Poll = true
		wire, err := loop.transport.marshal(packet)
		if err != nil {
			return err
		}
		probe.wire, probe.deadline, probe.stage = wire, at.Add(peerTTLProbeDelay), ttlProbeReplay
		loop.lastSent = at
	case ttlProbeComplete:
		clear(probe.wire)
		clear(probe.replay)
		loop.probe = nil
		loop.probeIndex++
		loop.lastSent = at
		writePeerEvent(loop.output, "ttl-probe-resume", strconv.Itoa(probe.floor))
	default:
		return fmt.Errorf("probe stage %d requires its Final before resume: %w", probe.stage, errPeerTTLProbe)
	}
	return nil
}

func (loop *peerLoop) tick(at time.Time) error {
	probe := loop.probe
	if probe == nil {
		if packet, ok := loop.crafted.periodic(); ok {
			return loop.transmit(packet)
		}
		loop.lastSent = at
		return nil
	}
	if probe.stage == ttlProbeReplay && !at.Before(probe.deadline) {
		if err := loop.sendRaw(vectorPacket{
			Detail: "below-floor authenticated Poll", Payload: probe.wire, TTL: probe.floor - 1,
		}); err != nil {
			return fmt.Errorf("send below-floor probe: %w", err)
		}
		probe.stage, probe.deadline = ttlProbeWaiting, at.Add(peerTTLProbeDelay)
		writePeerEvent(loop.output, "ttl-probe-low", strconv.Itoa(probe.floor-1))
	}
	if probe.stage == ttlProbeWaiting && !at.Before(probe.deadline) {
		if err := loop.sendRaw(vectorPacket{
			Detail: "at-floor identical authenticated Poll", Payload: probe.wire, TTL: probe.floor,
		}); err != nil {
			return fmt.Errorf("send at-floor probe: %w", err)
		}
		probe.stage = ttlProbeAwaitFinal
		writePeerEvent(loop.output, "ttl-probe-good", strconv.Itoa(probe.floor))
	}
	loop.lastSent = at
	return nil
}
