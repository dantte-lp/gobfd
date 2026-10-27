//go:build linux

package bfdload

import (
	"fmt"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

const (
	maxPeerBlackholeWindows = 256
	maxPeerOmissionTimes    = 128
	maxPeerOmissionSegments = 16
)

// PeerOmissionSegment coalesces equal wire identities, retaining every actual omission time and decision sequence.
// Valid means a codec-verified would-be TX packet, not transport, incarnation or delivery proof.
// Control RX remains unqualified; Echo RX records actual codec/tuple/TTL-checked input.
type PeerOmissionSegment struct {
	Direction         string    `json:"direction"`
	FirstNS           int64     `json:"first_ns"`
	LastNS            int64     `json:"last_ns"`
	Count             uint64    `json:"count"`
	Valid             bool      `json:"valid"`
	MyDiscriminator   uint32    `json:"my_discriminator"`
	YourDiscriminator uint32    `json:"your_discriminator"`
	State             bfd.State `json:"state"`
	Diag              bfd.Diag  `json:"diag"`
	DesiredMinTXUS    uint32    `json:"desired_min_tx_us"`
	RequiredMinRXUS   uint32    `json:"required_min_rx_us"`
	DetectMult        uint8     `json:"detect_mult"`
	Poll              bool      `json:"poll"`
	Final             bool      `json:"final"`
	OffsetsNS         []int64   `json:"offsets_ns"`
	Sequences         []uint64  `json:"sequences"`
	ReceivedSequences []uint64  `json:"received_sequences,omitempty"`
}

func omissionPacket(packet bfd.ControlPacket, receive bool) PeerOmissionSegment {
	row := PeerOmissionSegment{
		Direction: "tx", MyDiscriminator: packet.MyDiscriminator, YourDiscriminator: packet.YourDiscriminator,
		State: packet.State, Diag: packet.Diag, DesiredMinTXUS: packet.DesiredMinTxInterval,
		RequiredMinRXUS: packet.RequiredMinRxInterval, DetectMult: packet.DetectMult, Poll: packet.Poll, Final: packet.Final,
	}
	if receive {
		row.Direction = "rx"
		return row
	}
	var wire [bfd.MinPacketSizeNoAuth]byte
	n, err := bfd.MarshalControlPacket(&packet, wire[:])
	if err != nil {
		return row
	}
	var decoded bfd.ControlPacket
	row.Valid = bfd.UnmarshalControlPacket(wire[:n], &decoded) == nil
	return row
}

func (s PeerOmissionSegment) controlPacket() bfd.ControlPacket {
	return bfd.ControlPacket{
		Version: bfd.Version, Length: bfd.MinPacketSizeNoAuth,
		MyDiscriminator: s.MyDiscriminator, YourDiscriminator: s.YourDiscriminator, State: s.State, Diag: s.Diag,
		DesiredMinTxInterval: s.DesiredMinTXUS, RequiredMinRxInterval: s.RequiredMinRXUS,
		DetectMult: s.DetectMult, Poll: s.Poll, Final: s.Final,
	}
}

func sameOmissionIdentity(left, right PeerOmissionSegment) bool {
	return left.Direction == right.Direction && left.Valid == right.Valid &&
		left.MyDiscriminator == right.MyDiscriminator && left.YourDiscriminator == right.YourDiscriminator &&
		left.State == right.State && left.Diag == right.Diag &&
		left.DesiredMinTXUS == right.DesiredMinTXUS && left.RequiredMinRXUS == right.RequiredMinRXUS &&
		left.DetectMult == right.DetectMult && left.Poll == right.Poll && left.Final == right.Final
}

// PeerBlackholeWindow records actual generator omissions, not causal owner Down evidence.
// Times are relative to the adopted phase, or the worker epoch without phase control.
type PeerBlackholeWindow struct {
	StartNS   int64                 `json:"start_ns"`
	FirstTXNS int64                 `json:"first_tx_ns"`
	LastTXNS  int64                 `json:"last_tx_ns"`
	TXOmitted uint64                `json:"tx_omitted"`
	FirstRXNS int64                 `json:"first_rx_ns"`
	LastRXNS  int64                 `json:"last_rx_ns"`
	RXOmitted uint64                `json:"rx_omitted"`
	Segments  []PeerOmissionSegment `json:"segments"`
}

func (p *peerRuntime) recordBlackhole(decision packetDecision, receive bool, packet bfd.ControlPacket) error {
	segment, err := p.omissionSegment(packet, receive)
	if err != nil {
		return err
	}
	schedule := p.tx
	if receive {
		schedule = p.rx
	}
	start := int64(decision.elapsed - decision.elapsed%schedule.blackholeEvery)
	windows := p.observed.BlackholeWindows
	if len(windows) == 0 || windows[len(windows)-1].StartNS != start {
		if len(windows) == maxPeerBlackholeWindows {
			p.observed.BlackholeEventsOmitted++
			return fmt.Errorf("peer blackhole journal exceeds %d windows: %w", maxPeerBlackholeWindows, ErrProfile)
		}
		p.observed.BlackholeWindows = append(p.observed.BlackholeWindows, PeerBlackholeWindow{StartNS: start})
	}
	window := &p.observed.BlackholeWindows[len(p.observed.BlackholeWindows)-1]
	first, last, count := &window.FirstTXNS, &window.LastTXNS, &window.TXOmitted
	if receive {
		first, last, count = &window.FirstRXNS, &window.LastRXNS, &window.RXOmitted
	}
	if *count == 0 {
		*first = int64(decision.elapsed)
	}
	*last = int64(decision.elapsed)
	*count++
	if window.TXOmitted+window.RXOmitted > maxPeerOmissionTimes {
		p.observed.BlackholeEventsOmitted++
		return fmt.Errorf("peer blackhole window exceeds %d omission times: %w", maxPeerOmissionTimes, ErrProfile)
	}
	index := len(window.Segments) - 1
	for index >= 0 && window.Segments[index].Direction != segment.Direction {
		index--
	}
	if index < 0 || !sameOmissionIdentity(window.Segments[index], segment) {
		if len(window.Segments) == maxPeerOmissionSegments {
			p.observed.BlackholeEventsOmitted++
			return fmt.Errorf("peer blackhole window exceeds %d identity segments: %w", maxPeerOmissionSegments, ErrProfile)
		}
		window.Segments = append(window.Segments, segment)
		index = len(window.Segments) - 1
	}
	p.recordOmission(&window.Segments[index], decision)
	return nil
}

func (p *peerRuntime) omissionSegment(packet bfd.ControlPacket, receive bool) (PeerOmissionSegment, error) {
	segment := omissionPacket(packet, receive)
	if p.echo {
		if p.echoReadSequence == 0 {
			return segment, fmt.Errorf("echo omission has no actual received input: %w", ErrProfile)
		}
		segment.Valid = true
	}
	return segment, nil
}

func (p *peerRuntime) recordOmission(row *PeerOmissionSegment, decision packetDecision) {
	if row.Count == 0 {
		row.FirstNS = int64(decision.elapsed)
	}
	row.LastNS, row.Count = int64(decision.elapsed), row.Count+1
	row.OffsetsNS = append(row.OffsetsNS, int64(decision.elapsed))
	row.Sequences = append(row.Sequences, decision.sequence)
	if p.echo {
		row.ReceivedSequences = append(row.ReceivedSequences, p.echoReadSequence)
	}
}
