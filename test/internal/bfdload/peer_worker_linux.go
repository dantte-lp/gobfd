//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

const (
	peerInputCapacity  = 8
	peerRXSeedMask     = uint64(1) << 63
	peerJitterSeedMask = uint64(1) << 62
	peerJitterMinPct   = 75
	peerJitterOnePct   = 90
	percentScale       = 100
)

type peerInput struct {
	packet   bfd.ControlPacket
	meta     netio.PacketMeta
	invalid  bool
	wire     [bfd.MaxPacketSize]byte
	length   int
	sequence uint64
}

type peerSender interface {
	bfd.PacketSender
	Close() error
}

type peerRuntime struct {
	listener         *netio.LinuxPacketConn
	sender           peerSender
	input            chan peerInput
	state            peerState
	tx               *lossSchedule
	rx               *lossSchedule
	jitter           *rand.Rand
	observed         PeerSessionObservation
	started          bool
	readErr          error
	loopErr          error
	up               atomic.Bool
	phase            *atomic.Pointer[phaseStart]
	duration         time.Duration
	echo             bool
	echoReadSequence uint64
	pendingExpiry    int
}

func bindPeer(
	ctx context.Context, cfg PeerConfig, index int, endpoint OwnerEndpoint, logger *slog.Logger,
) (*peerRuntime, error) {
	if index < 0 || index >= sourcePortCapacity {
		return nil, fmt.Errorf("peer index outside source-port capacity: %w", ErrProfile)
	}
	port := int(cfg.SourcePortBase) + index
	if port < 49152 || port > math.MaxUint16 {
		return nil, fmt.Errorf("peer source port outside RFC 5881 range: %w", ErrProfile)
	}
	var state peerState
	if !cfg.Profile.Echo {
		var err error
		state, err = newPeerState(cfg.Profile, uint32(index+1))
		if err != nil {
			return nil, err
		}
	}
	id := uint64(index + 1)
	tx, err := newLossSchedule(cfg.Profile, id)
	if err != nil {
		return nil, err
	}
	receiveProfile := cfg.Profile
	receiveProfile.Seed ^= peerRXSeedMask
	if receiveProfile.TXOnlyBlackhole {
		receiveProfile.BlackholeEvery, receiveProfile.BlackholeFor = 0, 0
	}
	rx, err := newLossSchedule(receiveProfile, id)
	if err != nil {
		return nil, err
	}
	listener, err := netio.NewGenericListener(ctx, endpoint.Peer.Addr(), endpoint.Interface, endpoint.Peer.Port())
	if err != nil {
		return nil, fmt.Errorf("bind peer receive %s: %w", endpoint.Peer, err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, errors.Join(contextErr, listener.Close())
	}
	var sender peerSender
	if cfg.Profile.Echo {
		sender, err = newEchoReturnSender(ctx, endpoint, uint16(port))
	} else {
		//nolint:contextcheck // Sender has no context; the group owns cancellation and rollback.
		sender, err = netio.NewUDPSender(endpoint.Peer.Addr(), uint16(port), false, logger,
			netio.WithDstPort(endpoint.Local.Port()), netio.WithBindDevice(endpoint.Interface))
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("bind peer sender %s:%d: %w", endpoint.Peer.Addr(), port, err), listener.Close())
	}
	return &peerRuntime{
		echo:     cfg.Profile.Echo,
		listener: listener, sender: sender, input: make(chan peerInput, peerInputCapacity), state: state, tx: tx, rx: rx,
		// #nosec G404 -- Reproducible protocol jitter, separate from the loss streams; never security randomness.
		jitter:   rand.New(rand.NewPCG(cfg.Profile.Seed^peerJitterSeedMask, id)),
		observed: PeerSessionObservation{ID: id, Endpoint: endpoint, SourcePort: uint16(port)},
	}, nil
}

func (p *peerRuntime) read(ctx context.Context, cancel context.CancelFunc) {
	defer close(p.input)
	var wire [bfd.MaxPacketSize]byte
	for {
		n, meta, readErr := p.listener.ReadPacket(wire[:])
		if readErr != nil {
			if ctx.Err() != nil {
				return
			}
			if !errors.Is(readErr, netio.ErrPacketTruncated) && !errors.Is(readErr, netio.ErrAncillaryTruncated) {
				p.observed.ReadErrors++
				p.readErr = fmt.Errorf("read peer %s: %w", p.observed.Endpoint.Peer, readErr)
				cancel()
				return
			}
		}
		p.observed.Read++
		var packet bfd.ControlPacket
		parseErr := bfd.UnmarshalControlPacket(wire[:n], &packet)
		// Unsupported authentication must not retain a slice into the reusable wire buffer.
		packet.Auth = bfd.AuthSection{}
		input := peerInput{packet: packet, meta: meta, invalid: readErr != nil || parseErr != nil}
		if p.echo {
			input.length, input.sequence = n, p.observed.Read
			copy(input.wire[:], wire[:n])
		}
		select {
		case p.input <- input:
		case <-ctx.Done():
			p.observed.ReceiveNotProcessed++
			return
		}
	}
}

func (p *peerRuntime) run(ctx context.Context, cancel context.CancelFunc, started time.Time) {
	loopErr := p.loop(ctx, started)
	if loopErr != nil && ctx.Err() == nil {
		p.loopErr = loopErr
		cancel()
	}
}

type peerTimers struct {
	transmit     *time.Timer
	detection    *time.Timer
	lastPeriodic time.Time
}

func (p *peerRuntime) loop(ctx context.Context, started time.Time) error {
	if p.echo {
		return p.echoLoop(ctx, started)
	}
	timers := peerTimers{transmit: time.NewTimer(0), detection: time.NewTimer(time.Hour)}
	defer timers.transmit.Stop()
	timers.detection.Stop()
	defer timers.detection.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case input, open := <-p.input:
			if !open {
				return nil
			}
			if err := p.onInput(ctx, input, started, &timers); err != nil {
				return err
			}
		case <-timers.transmit.C:
			if err := p.onTransmit(ctx, started, &timers); err != nil {
				return err
			}
		case <-timers.detection.C:
			if err := p.onDetection(ctx, started, &timers); err != nil {
				return err
			}
		}
	}
}

func (p *peerRuntime) onInput(ctx context.Context, input peerInput, started time.Time, timers *peerTimers) error {
	p.observeSocketDrops(input.meta)
	now := time.Now()
	decision, err := p.decision(p.rx, now, started)
	if err != nil {
		return err
	}
	switch decision.disposition {
	case packetRandomLoss:
		p.observed.ReceiveRandomOmitted++
		return nil
	case packetBlackhole:
		p.observed.ReceiveBlackholeOmitted++
		segment := omissionPacket(input.packet, true)
		if p.validInput(input) {
			candidate := p.state
			_, err := candidate.receive(input.packet, now)
			segment.ReceiveQualified = err == nil
		}
		return p.recordBlackholeSegment(decision, true, segment)
	case packetTransmit:
	}
	previousState, previousInterval := p.state.state, p.state.periodicInterval()
	accepted, final := p.accept(input, now)
	if !accepted {
		return nil
	}
	if final || previousState != p.state.state {
		if err := p.send(ctx, now, started, final); err != nil {
			return err
		}
		if !final {
			timers.lastPeriodic = time.Now()
		}
	}
	resetPeerDetection(timers.detection, p.state.deadline())
	if previousState != p.state.state || previousInterval != p.state.periodicInterval() {
		p.resetPeriodic(timers.transmit, timers.lastPeriodic)
	}
	return nil
}

func (p *peerRuntime) observeSocketDrops(meta netio.PacketMeta) {
	if meta.SocketDropsInvalid {
		p.observed.KernelDropInvalid++
	} else if meta.SocketDropsKnown {
		p.observed.KernelDropSamples++
		p.observed.KernelDropCounterRaw = meta.SocketDrops
	}
}

func (p *peerRuntime) accept(input peerInput, now time.Time) (bool, bool) {
	if !p.validInput(input) {
		p.observed.Rejected++
		return false, false
	}
	previousState := p.state.state
	final, err := p.state.receive(input.packet, now)
	if err != nil {
		p.observed.Rejected++
		return false, false
	}
	p.observed.Accepted++
	p.pendingExpiry = 0
	p.observeState(previousState)
	return true, final
}

func (p *peerRuntime) validInput(input peerInput) bool {
	return !input.invalid && input.meta.SrcAddr == p.observed.Endpoint.Local.Addr() &&
		input.meta.DstAddr == p.observed.Endpoint.Peer.Addr() && netio.ValidateTTL(input.meta) == nil
}

func (p *peerRuntime) onTransmit(ctx context.Context, started time.Time, timers *peerTimers) error {
	if p.state.periodicInterval() != 0 {
		if err := p.send(ctx, time.Now(), started, false); err != nil {
			return err
		}
	}
	timers.lastPeriodic = time.Now()
	p.resetPeriodic(timers.transmit, timers.lastPeriodic)
	return nil
}

func (p *peerRuntime) onDetection(ctx context.Context, started time.Time, timers *peerTimers) error {
	now, previous := time.Now(), p.state
	if p.state.expire(now) {
		p.observed.DetectionExpirations++
		p.observeState(previous.state)
		if err := p.recordExpiry(previous, now); err != nil {
			return err
		}
		if previous.state != p.state.state {
			if err := p.send(ctx, now, started, false); err != nil {
				return err
			}
			timers.lastPeriodic = time.Now()
			p.resetPeriodic(timers.transmit, timers.lastPeriodic)
		}
	}
	resetPeerDetection(timers.detection, p.state.deadline())
	return nil
}

func (p *peerRuntime) send(ctx context.Context, now, started time.Time, final bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("peer send interrupted: %w", err)
	}
	decision, err := p.decision(p.tx, now, started)
	if err != nil {
		return err
	}
	p.observed.Scheduled++
	switch decision.disposition {
	case packetRandomLoss:
		p.observed.RandomOmitted++
		return nil
	case packetBlackhole:
		p.observed.BlackholeOmitted++
		return p.recordBlackhole(decision, false, p.state.packet(final))
	case packetTransmit:
	}
	packet := p.state.packet(final)
	var wire [bfd.MinPacketSizeNoAuth]byte
	n, err := bfd.MarshalControlPacket(&packet, wire[:])
	if err != nil {
		return fmt.Errorf("encode peer control packet: %w", err)
	}
	p.observed.WriteAttempts++
	var writeStarted time.Time
	if p.pendingExpiry != 0 {
		writeStarted = time.Now()
	}
	if err := p.sender.SendPacket(ctx, wire[:n], p.observed.Endpoint.Local.Addr()); err != nil {
		if ctx.Err() != nil {
			p.observed.WriteCanceled++
		} else {
			p.observed.WriteErrors++
		}
		return fmt.Errorf("transmit peer control packet: %w", err)
	}
	p.observed.Written++
	if p.pendingExpiry != 0 {
		p.recordExpiryWrite(packet, now, writeStarted, time.Now())
	}
	return nil
}

func (p *peerRuntime) observeState(previous bfd.State) {
	p.up.Store(p.state.state == bfd.StateUp)
	if p.state.state == previous {
		return
	}
	switch p.state.state {
	case bfd.StateUp:
		p.observed.UpTransitions++
	case bfd.StateDown:
		p.observed.DownTransitions++
	case bfd.StateAdminDown, bfd.StateInit:
	}
}

func (p *peerRuntime) decision(schedule *lossSchedule, now, started time.Time) (packetDecision, error) {
	if p.phase != nil {
		phase := p.phase.Load()
		if phase == nil || now.Before(phase.goStart) || !now.Before(phase.goStart.Add(p.duration)) {
			return packetDecision{disposition: packetTransmit}, nil
		}
		started = phase.goStart
	}
	return schedule.next(now.Sub(started))
}

func resetPeerDetection(timer *time.Timer, deadline time.Time) {
	if deadline.IsZero() {
		timer.Stop()
		return
	}
	timer.Reset(max(0, time.Until(deadline)))
}

func (p *peerRuntime) resetPeriodic(timer *time.Timer, last time.Time) {
	interval := p.state.periodicInterval()
	if interval == 0 {
		timer.Stop()
		return
	}
	minimum, maximum := interval*peerJitterMinPct/percentScale, interval
	if p.state.multiplier == 1 {
		maximum = interval * peerJitterOnePct / percentScale
	}
	jittered := minimum + time.Duration(p.jitter.Int64N(int64(maximum-minimum)+1))
	timer.Reset(max(0, time.Until(last.Add(jittered))))
}
