package main

import (
	"io"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

const testRemoteDiscriminator = 0x1111

func remoteControl(state bfd.State) bfd.ControlPacket {
	packet := defaultControlPacket(testRemoteDiscriminator)
	packet.State = state
	packet.YourDiscriminator = peerDiscriminator
	packet.DesiredMinTxInterval = 300_000
	packet.RequiredMinRxInterval = 300_000
	return packet
}

func mustPeriodic(t *testing.T, crafted *peer) bfd.ControlPacket {
	t.Helper()
	packet, ok := crafted.periodic()
	if !ok {
		t.Fatal("periodic transmission suppressed, want a packet")
	}
	return packet
}

func upPeer(t *testing.T) *peer {
	t.Helper()
	crafted := newPeer()
	crafted.receive(remoteControl(bfd.StateInit))
	crafted.receive(finalControl(remoteControl(bfd.StateUp)))
	if crafted.state != bfd.StateUp || crafted.poll {
		t.Fatalf("fixture state=%s poll=%t, want Up without Poll", crafted.state, crafted.poll)
	}
	return crafted
}

func finalControl(packet bfd.ControlPacket) bfd.ControlPacket {
	packet.Final = true
	return packet
}

func pollControl(packet bfd.ControlPacket) bfd.ControlPacket {
	packet.Poll = true
	return packet
}

func TestPeerThreeWayHandshake(t *testing.T) {
	t.Parallel()

	crafted := newPeer()
	down := mustPeriodic(t, crafted)
	// RFC 5880 Section 6.8.3: a session that is not Up advertises at least one second.
	if down.State != bfd.StateDown || down.YourDiscriminator != 0 || down.DesiredMinTxInterval != 1_000_000 ||
		down.RequiredMinRxInterval != 300_000 || down.DetectMult != 3 || down.MyDiscriminator != peerDiscriminator {
		t.Fatalf("initial packet = %+v", down)
	}
	if got := crafted.interval(); got != 900*time.Millisecond {
		t.Fatalf("Down interval = %v, want 900ms", got)
	}
	remoteDown := remoteControl(bfd.StateDown)
	remoteDown.YourDiscriminator = 0
	if _, reply := crafted.receive(remoteDown); reply {
		t.Fatal("non-Poll packet produced a reply")
	}
	if init := mustPeriodic(t, crafted); init.State != bfd.StateInit || init.YourDiscriminator != testRemoteDiscriminator {
		t.Fatalf("Init packet = %+v", init)
	}
	crafted.receive(remoteControl(bfd.StateUp))
	up := mustPeriodic(t, crafted)
	// The Up transition lowers Desired Min TX from the slow rate, so Section 6.8.3 requires a Poll.
	if up.State != bfd.StateUp || !up.Poll || up.DesiredMinTxInterval != 300_000 {
		t.Fatalf("first Up packet = %+v, want Poll with 300ms", up)
	}
	crafted.receive(finalControl(remoteControl(bfd.StateUp)))
	if packet := mustPeriodic(t, crafted); packet.Poll || packet.Final {
		t.Fatalf("Final did not terminate the Poll Sequence: %+v", packet)
	}
	if got := crafted.interval(); got != 270*time.Millisecond {
		t.Fatalf("Up interval = %v, want 270ms", got)
	}
}

func TestPeerAnswersEveryPollWithFinal(t *testing.T) {
	t.Parallel()

	crafted := upPeer(t)
	if err := crafted.apply(phaseRequiredMinRxZero); err != nil {
		t.Fatal(err)
	}
	reply, ok := crafted.receive(pollControl(remoteControl(bfd.StateUp)))
	// RFC 5880 Section 6.5: a Final never carries Poll, even during the peer's own Poll Sequence.
	if !ok || !reply.Final || reply.Poll || reply.RequiredMinRxInterval != 0 || reply.State != bfd.StateUp {
		t.Fatalf("Poll reply = %+v ok=%t", reply, ok)
	}
	if !crafted.poll {
		t.Fatal("answering a Poll terminated the peer's own Poll Sequence")
	}
}

func TestPeerRequiredMinRxZeroUsesPoll(t *testing.T) {
	t.Parallel()

	crafted := upPeer(t)
	if err := crafted.apply(phaseRequiredMinRxZero); err != nil {
		t.Fatal(err)
	}
	packet := mustPeriodic(t, crafted)
	if !packet.Poll || packet.RequiredMinRxInterval != 0 {
		t.Fatalf("RX0 packet = %+v, want Poll with Required Min RX 0", packet)
	}
	crafted.receive(finalControl(remoteControl(bfd.StateUp)))
	if packet = mustPeriodic(t, crafted); packet.Poll || packet.RequiredMinRxInterval != 0 {
		t.Fatalf("RX0 packet after Final = %+v", packet)
	}
	if err := crafted.apply(phasePoll); err != nil {
		t.Fatal(err)
	}
	if packet = mustPeriodic(t, crafted); !packet.Poll || packet.RequiredMinRxInterval != 0 {
		t.Fatalf("explicit Poll packet = %+v", packet)
	}
}

func TestPeerSilenceSendsAndAnswersNothing(t *testing.T) {
	t.Parallel()

	crafted := upPeer(t)
	if err := crafted.apply(phaseSilent); err != nil {
		t.Fatal(err)
	}
	if packet, ok := crafted.periodic(); ok {
		t.Fatalf("silent peer sent %+v", packet)
	}
	if reply, ok := crafted.receive(pollControl(remoteControl(bfd.StateDown))); ok {
		t.Fatalf("silent peer answered %+v", reply)
	}
	if crafted.state != bfd.StateUp {
		t.Fatalf("silent peer processed a packet: state=%s", crafted.state)
	}
	if err := crafted.apply(phaseRecover); err != nil {
		t.Fatal(err)
	}
	packet := mustPeriodic(t, crafted)
	if packet.State != bfd.StateDown || packet.YourDiscriminator != 0 || packet.RequiredMinRxInterval != 300_000 ||
		packet.Poll || packet.Demand {
		t.Fatalf("recovered packet = %+v", packet)
	}
}

func TestPeerDemandUsesPollAndKeepsPeriodicTransmission(t *testing.T) {
	t.Parallel()

	crafted := newPeer()
	// RFC 5880 Section 6.6: Demand requires both systems Up.
	if err := crafted.apply(phaseDemand); err == nil {
		t.Fatal("Demand accepted while Down")
	}
	crafted = upPeer(t)
	if err := crafted.apply(phaseDemand); err != nil {
		t.Fatal(err)
	}
	if packet := mustPeriodic(t, crafted); !packet.Demand || !packet.Poll {
		t.Fatalf("Demand packet = %+v, want D and P", packet)
	}
	crafted.receive(finalControl(remoteControl(bfd.StateUp)))
	// The remote system is not in Demand, so the peer keeps transmitting periodically.
	if packet := mustPeriodic(t, crafted); !packet.Demand || packet.Poll {
		t.Fatalf("Demand packet after Final = %+v", packet)
	}
	if err := crafted.apply(phaseDemandClear); err != nil {
		t.Fatal(err)
	}
	if packet := mustPeriodic(t, crafted); packet.Demand || !packet.Poll {
		t.Fatalf("Demand clear packet = %+v, want D clear with P", packet)
	}
}

func TestPeerHonorsRemoteTimingAndDemand(t *testing.T) {
	t.Parallel()

	crafted := upPeer(t)
	slower := remoteControl(bfd.StateUp)
	slower.RequiredMinRxInterval = 500_000
	crafted.receive(slower)
	if got := crafted.interval(); got != 450*time.Millisecond {
		t.Fatalf("interval = %v, want 450ms from remote Required Min RX", got)
	}
	zero := remoteControl(bfd.StateUp)
	zero.RequiredMinRxInterval = 0
	crafted.receive(zero)
	if packet, ok := crafted.periodic(); ok {
		t.Fatalf("periodic packet %+v sent to remote Required Min RX 0", packet)
	}
	demand := remoteControl(bfd.StateUp)
	demand.Demand = true
	crafted.receive(demand)
	if packet, ok := crafted.periodic(); ok {
		t.Fatalf("periodic packet %+v sent to remote Demand", packet)
	}
	if err := crafted.apply(phasePoll); err != nil {
		t.Fatal(err)
	}
	if packet := mustPeriodic(t, crafted); !packet.Poll {
		t.Fatalf("remote Demand Poll packet = %+v", packet)
	}
}

func TestPeerDropsForeignYourDiscriminator(t *testing.T) {
	t.Parallel()

	crafted := upPeer(t)
	foreign := pollControl(remoteControl(bfd.StateDown))
	foreign.YourDiscriminator = peerDiscriminator + 1
	if reply, ok := crafted.receive(foreign); ok || crafted.state != bfd.StateUp {
		t.Fatalf("foreign packet processed: reply=%+v state=%s", reply, crafted.state)
	}
	crafted.receive(remoteControl(bfd.StateDown))
	if crafted.state != bfd.StateDown {
		t.Fatalf("remote Down left state %s", crafted.state)
	}
	if packet := mustPeriodic(t, crafted); packet.Diag != bfd.DiagNeighborDown {
		t.Fatalf("Down packet diag = %s, want Neighbor Signaled Session Down", packet.Diag)
	}
}

func TestPeerPacketsUseRepositoryCodec(t *testing.T) {
	t.Parallel()

	crafted := upPeer(t)
	if err := crafted.apply(phaseDemand); err != nil {
		t.Fatal(err)
	}
	packet := mustPeriodic(t, crafted)
	payload, err := marshalControl(packet)
	if err != nil {
		t.Fatal(err)
	}
	var decoded bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(payload, &decoded); err != nil {
		t.Fatalf("repository codec rejected peer packet: %v", err)
	}
	decoded.Length = 0
	if decoded != packet {
		t.Fatalf("decoded = %+v, want %+v", decoded, packet)
	}
	if err := crafted.apply("unknown"); err == nil {
		t.Fatal("unknown phase accepted")
	}
}

// The remote Detection Time follows the advertised interval, so an Up
// transition must pull the next send forward from the slow-rate deadline.
func TestPeerLoopDeadlineFollowsUpTransition(t *testing.T) {
	t.Parallel()

	sent := 0
	loop := &peerLoop{crafted: newPeer(), output: io.Discard, send: func(bfd.ControlPacket) error {
		sent++
		return nil
	}}
	last := time.Now().Add(-time.Hour)
	loop.lastSent = last
	if got := loop.deadline(); !got.Equal(last.Add(900 * time.Millisecond)) {
		t.Fatalf("Down deadline = %v, want last send + 900ms", got.Sub(last))
	}
	if err := loop.receive(remoteControl(bfd.StateInit)); err != nil {
		t.Fatal(err)
	}
	if got := loop.deadline(); !got.Equal(last.Add(270 * time.Millisecond)) {
		t.Fatalf("Up deadline = %v, want last send + 270ms", got.Sub(last))
	}
	if err := loop.receive(pollControl(remoteControl(bfd.StateUp))); err != nil {
		t.Fatal(err)
	}
	// A Final counts as a transmission for the remote Detection Time.
	if sent != 1 || !loop.lastSent.After(last) {
		t.Fatalf("Final sends=%d lastSent=%v, want one send after %v", sent, loop.lastSent, last)
	}
}

// Fault injection: lost Final replies keep the remote Poll outstanding while
// the peer still transmits periodically and can cross it with its own Poll.
func TestPeerFinalLossDropsOnlyFinals(t *testing.T) {
	t.Parallel()

	crafted := upPeer(t)
	for _, phase := range []string{phaseFinalLoss, phasePoll} {
		if err := crafted.apply(phase); err != nil {
			t.Fatal(err)
		}
	}
	if reply, ok := crafted.receive(pollControl(remoteControl(bfd.StateUp))); ok {
		t.Fatalf("lost Final transmitted: %+v", reply)
	}
	// RFC 5880 Section 6.5: the crossing Poll never carries Final.
	if packet := mustPeriodic(t, crafted); !packet.Poll || packet.Final {
		t.Fatalf("crossing packet = %+v, want P only", packet)
	}
	crafted.receive(finalControl(remoteControl(bfd.StateUp)))
	if crafted.poll {
		t.Fatal("remote Final did not end the crossing Poll under Final loss")
	}
	if err := crafted.apply(phaseFinalRestore); err != nil {
		t.Fatal(err)
	}
	if reply, ok := crafted.receive(pollControl(remoteControl(bfd.StateUp))); !ok || !reply.Final || reply.Poll {
		t.Fatalf("restored Final = %+v ok=%t", reply, ok)
	}
	phases := peerPhases()
	if got := phases[len(phases)-3:]; got[0] != phaseFinalLoss || got[1] != phasePoll || got[2] != phaseFinalRestore {
		t.Fatalf("scenario tail = %v", got)
	}
}
