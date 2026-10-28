package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// Crafted peer constants: microsecond timers follow the RFC 5880 wire format.
const (
	peerDiscriminator   = 0x0C0FFEE1
	peerDesiredMinTx    = 300_000
	peerRequiredMinRx   = 300_000
	peerSlowMinTx       = 1_000_000
	peerDetectMult      = 3
	peerInitialRemoteRx = 1
	peerTimeout         = 5 * time.Minute
	peerReadBuffer      = 512
	peerPacketQueue     = 16
	peerJitterPercent   = 90
)

// Scenario phases, applied in order on each SIGUSR1 from the test harness.
const (
	phaseRequiredMinRxZero = "rx0"
	phasePoll              = "poll"
	phaseSilent            = "silent"
	phaseRecover           = "recover"
	phaseDemand            = "demand"
	phaseDemandClear       = "demand-clear"
	phaseFinalLoss         = "final-loss"
	phaseFinalRestore      = "final-restore"
)

// peerPhases is the fixed scenario order.
func peerPhases() []string {
	return []string{
		phaseRequiredMinRxZero, phasePoll, phaseSilent, phaseRecover, phaseDemand, phaseDemandClear,
		phaseFinalLoss, phasePoll, phaseFinalRestore,
	}
}

var (
	errUnknownPhase    = errors.New("unknown crafted peer phase")
	errDemandNotUp     = errors.New("demand requires both systems Up")
	errPhasesExhausted = errors.New("crafted peer phases exhausted")
)

// peer is the RFC 5880 asynchronous state model; peerTransport validates authentication.
// It exists to drive GoBFD into remote Required Min RX
// Interval zero, remote silence and remote Demand, which pinned FRR and BIRD
// cannot advertise.
type peer struct {
	state, remoteState bfd.State
	diag               bfd.Diag
	remoteDiscr        uint32
	remoteMinRx        uint32
	requiredMinRx      uint32
	remoteDemand       bool
	demand             bool
	poll               bool
	silent             bool
	// dropFinals simulates loss of every Final reply (fault injection).
	dropFinals bool
}

func newPeer() *peer {
	return &peer{
		state: bfd.StateDown, remoteState: bfd.StateDown,
		remoteMinRx: peerInitialRemoteRx, requiredMinRx: peerRequiredMinRx,
	}
}

// desiredMinTx applies the RFC 5880 Section 6.8.3 slow rate outside Up.
func (crafted *peer) desiredMinTx() uint32 {
	if crafted.state != bfd.StateUp {
		return max(peerDesiredMinTx, peerSlowMinTx)
	}
	return peerDesiredMinTx
}

// packet builds the next Control packet (RFC 5880 Section 6.8.7). A Final
// never carries Poll (Section 6.5); Demand needs both systems Up.
func (crafted *peer) packet(final bool) bfd.ControlPacket {
	return bfd.ControlPacket{
		Version:               bfd.Version,
		Diag:                  crafted.diag,
		State:                 crafted.state,
		Poll:                  crafted.poll && !final,
		Final:                 final,
		Demand:                crafted.demand && crafted.state == bfd.StateUp && crafted.remoteState == bfd.StateUp,
		DetectMult:            peerDetectMult,
		MyDiscriminator:       peerDiscriminator,
		YourDiscriminator:     crafted.remoteDiscr,
		DesiredMinTxInterval:  crafted.desiredMinTx(),
		RequiredMinRxInterval: crafted.requiredMinRx,
	}
}

// interval is the negotiated transmit interval (RFC 5880 Section 6.8.7)
// reduced by a fixed 10 percent, inside the required 0-25 percent jitter.
func (crafted *peer) interval() time.Duration {
	interval := time.Duration(max(crafted.desiredMinTx(), crafted.remoteMinRx)) * time.Microsecond
	return interval * peerJitterPercent / 100
}

// periodic returns the scheduled packet unless RFC 5880 Section 6.8.7
// forbids periodic transmission.
func (crafted *peer) periodic() (bfd.ControlPacket, bool) {
	if crafted.silent || crafted.remoteMinRx == 0 {
		return bfd.ControlPacket{}, false
	}
	if crafted.remoteDemand && crafted.state == bfd.StateUp && crafted.remoteState == bfd.StateUp && !crafted.poll {
		return bfd.ControlPacket{}, false
	}
	return crafted.packet(false), true
}

// nextState applies the RFC 5880 Section 6.8.6 state table for a received
// remote state; the crafted peer itself is never AdminDown.
func nextState(local, remote bfd.State) bfd.State {
	switch {
	case remote == bfd.StateAdminDown || (local == bfd.StateUp && remote == bfd.StateDown):
		return bfd.StateDown
	case local == bfd.StateDown && remote == bfd.StateDown:
		return bfd.StateInit
	case (local == bfd.StateDown && remote == bfd.StateInit) || (local == bfd.StateInit && remote != bfd.StateDown):
		return bfd.StateUp
	default:
		return local
	}
}

// receive applies the RFC 5880 Section 6.8.6 reception rules to a codec-valid
// packet and returns the Final reply that a Poll requires (Section 6.8.7).
func (crafted *peer) receive(packet bfd.ControlPacket) (bfd.ControlPacket, bool) {
	if crafted.silent || !peerPacketTarget(packet) {
		return bfd.ControlPacket{}, false
	}
	if packet.Final {
		crafted.poll = false
	}
	crafted.remoteDiscr, crafted.remoteState = packet.MyDiscriminator, packet.State
	crafted.remoteDemand, crafted.remoteMinRx = packet.Demand, packet.RequiredMinRxInterval
	previous := crafted.state
	crafted.state = nextState(previous, packet.State)
	switch {
	case previous != bfd.StateDown && crafted.state == bfd.StateDown:
		crafted.poll, crafted.diag = false, bfd.DiagNeighborDown
	case previous != bfd.StateUp && crafted.state == bfd.StateUp:
		// Leaving the slow rate changes Desired Min TX (RFC 5880 Section 6.8.3).
		crafted.poll, crafted.diag = true, bfd.DiagNone
	}
	if packet.Poll && !crafted.dropFinals {
		return crafted.packet(true), true
	}
	return bfd.ControlPacket{}, false
}

// apply advances the crafted scenario. Timer and Demand changes start a Poll
// Sequence (RFC 5880 Sections 6.6 and 6.8.3).
func (crafted *peer) apply(phase string) error {
	switch phase {
	case phaseRequiredMinRxZero:
		crafted.requiredMinRx, crafted.poll = 0, true
	case phasePoll:
		crafted.poll = true
	case phaseSilent:
		crafted.silent = true
	case phaseFinalLoss, phaseFinalRestore:
		crafted.dropFinals = phase == phaseFinalLoss
	case phaseRecover:
		*crafted = *newPeer()
	case phaseDemand, phaseDemandClear:
		if crafted.state != bfd.StateUp || crafted.remoteState != bfd.StateUp {
			return fmt.Errorf("apply %s in %s/%s: %w", phase, crafted.state, crafted.remoteState, errDemandNotUp)
		}
		crafted.demand, crafted.poll = phase == phaseDemand, true
	default:
		return fmt.Errorf("apply %q: %w", phase, errUnknownPhase)
	}
	return nil
}

type peerEvent struct {
	Time  time.Time `json:"time"`
	Event string    `json:"event"`
	Value string    `json:"value"`
}

func writePeerEvent(output io.Writer, event, value string) {
	line, err := json.Marshal(peerEvent{Time: time.Now().UTC(), Event: event, Value: value})
	if err == nil {
		fmt.Fprintf(output, "%s\n", line)
	}
}

// runPeerMain runs the crafted peer until SIGTERM or SIGINT.
func runPeerMain(mode peerMode) int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, peerTimeout)
	defer timeout()
	advance := make(chan os.Signal, 1)
	signal.Notify(advance, syscall.SIGUSR1, syscall.SIGUSR2)
	defer signal.Stop(advance)
	if err := runPeer(ctx, os.Getenv, os.Stdout, advance, mode); err != nil {
		fmt.Fprintf(os.Stderr, "BFD crafted peer: %v\n", err)
		return 2
	}
	return 0
}

func runPeer(
	ctx context.Context, getenv func(string) string, output io.Writer, advance <-chan os.Signal, mode peerMode,
) (runErr error) {
	transport, err := newPeerTransport(mode)
	if err != nil {
		return err
	}
	target, err := parseTarget(getenv)
	if err != nil {
		return err
	}
	sender, err := newUDPSender(target, transport.port)
	if err != nil {
		return err
	}
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: int(transport.port)})
	if err != nil {
		return errors.Join(fmt.Errorf("listen on BFD port %d: %w", transport.port, err), sender.Close())
	}
	defer func() {
		runErr = errors.Join(runErr, receiver.Close(), sender.Close())
	}()
	packets := make(chan []byte, peerPacketQueue)
	go readPeerPackets(ctx, receiver, target, packets)
	loop := &peerLoop{
		crafted: newPeer(), transport: transport, mode: mode, output: output,
		sendRaw: func(packet vectorPacket) error { return sender.Send(ctx, packet) },
		send: func(packet bfd.ControlPacket) error {
			payload, marshalErr := transport.marshal(packet)
			if marshalErr != nil {
				return marshalErr
			}
			return sender.Send(ctx, vectorPacket{Detail: "crafted peer control", Payload: payload, TTL: defaultTTL})
		},
	}
	writePeerEvent(output, "start", target.String())
	return loop.run(ctx, packets, advance)
}

// peerLoop owns the crafted peer state on one goroutine.
type peerLoop struct {
	crafted    *peer
	transport  *peerTransport
	mode       peerMode
	probe      *peerTTLProbe
	probeIndex int
	sendRaw    func(vectorPacket) error
	output     io.Writer
	send       func(bfd.ControlPacket) error
	next       int
	// lastSent is the last transmission or skipped periodic slot.
	lastSent time.Time
}

// deadline is the next periodic slot. Re-arming after every event applies a
// shorter advertised interval at once, so the remote Detection Time derived
// from it never expires between packets (RFC 5880 Sections 6.8.3 and 6.8.4).
func (loop *peerLoop) deadline() time.Time {
	deadline := loop.lastSent.Add(loop.crafted.interval())
	if probe := loop.probe; probe != nil &&
		(probe.stage == ttlProbeReplay || probe.stage == ttlProbeWaiting) && probe.deadline.Before(deadline) {
		return probe.deadline
	}
	return deadline
}

func (loop *peerLoop) transmit(packet bfd.ControlPacket) error {
	loop.lastSent = time.Now()
	return loop.send(packet)
}

func (loop *peerLoop) run(ctx context.Context, packets <-chan []byte, advance <-chan os.Signal) error {
	loop.lastSent = time.Now()
	timer := time.NewTimer(loop.crafted.interval())
	defer timer.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			writePeerEvent(loop.output, "stop", context.Cause(ctx).Error())
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("crafted peer scenario: %w", ctx.Err())
			}
			return nil
		case wire := <-packets:
			err = loop.receiveWire(wire)
		case <-timer.C:
			err = loop.tick(time.Now())
		case event := <-advance:
			err = loop.signal(event)
		}
		if err != nil {
			return err
		}
		timer.Reset(max(0, time.Until(loop.deadline())))
	}
}

func (loop *peerLoop) receiveWire(wire []byte) error {
	if loop.crafted.silent {
		return nil
	}
	packet, err := loop.transport.decode(wire)
	if err != nil {
		writePeerEvent(loop.output, "discard", err.Error())
		return nil
	}
	return loop.receive(packet)
}

func peerPacketTarget(packet bfd.ControlPacket) bool {
	return packet.YourDiscriminator == 0 || packet.YourDiscriminator == peerDiscriminator
}

func (loop *peerLoop) receive(packet bfd.ControlPacket) error {
	if packet.Final && loop.probe != nil {
		if loop.probe.stage != ttlProbeAwaitFinal {
			return fmt.Errorf("unexpected Final before admissible probe: %w", errPeerTTLProbe)
		}
		loop.probe.stage = ttlProbeComplete
		writePeerEvent(loop.output, "ttl-probe-final", strconv.Itoa(loop.probe.floor))
	}
	previous := loop.crafted.state
	reply, ok := loop.crafted.receive(packet)
	if loop.crafted.state != previous {
		writePeerEvent(loop.output, "state", loop.crafted.state.String())
	}
	if !ok {
		return nil
	}
	return loop.transmit(reply)
}

func (loop *peerLoop) signal(event os.Signal) error {
	if event == syscall.SIGUSR2 {
		if loop.mode == peerMultihopTTL {
			return loop.probeSignal(time.Now())
		}
		return loop.crafted.apply(phasePoll)
	}
	return loop.advance()
}

func (loop *peerLoop) advance() error {
	if loop.probe != nil {
		return fmt.Errorf("advance while probe paused: %w", errPeerTTLProbe)
	}
	phases := peerPhases()
	if loop.mode == peerMultihopTTL {
		phases = []string{phaseDemand, phaseDemandClear}
	}
	if loop.next == len(phases) {
		return errPhasesExhausted
	}
	if err := loop.crafted.apply(phases[loop.next]); err != nil {
		return err
	}
	writePeerEvent(loop.output, "phase", phases[loop.next])
	loop.next++
	return nil
}

// readPeerPackets transfers owned datagrams; the owner validates codec and authentication before FSM mutation.
func readPeerPackets(ctx context.Context, conn *net.UDPConn, target netip.Addr, packets chan<- []byte) {
	buffer := make([]byte, peerReadBuffer)
	for {
		size, source, err := conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			return
		}
		if source.Addr().Unmap() != target {
			continue
		}
		select {
		case packets <- slices.Clone(buffer[:size]):
		case <-ctx.Done():
			return
		}
	}
}
