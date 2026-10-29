//go:build linux

package bfdload

import (
	"fmt"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

const maxPeerExpiryEvents = 256

// PeerDetectionExpiry retains an actual reference-peer timer expiry, not its cause.
// Without PhaseKnown, offsets use the generator group epoch and cannot join owner evidence.
type PeerDetectionExpiry struct {
	ID                        uint64          `json:"id"`
	PreviousState             bfd.State       `json:"previous_state"`
	LocalDiscriminator        uint32          `json:"local_discriminator"`
	RemoteDiscriminator       uint32          `json:"remote_discriminator"`
	TimingKnown               bool            `json:"timing_known"`
	PhaseKnown                bool            `json:"phase_known"`
	LastReceiveNS             int64           `json:"last_receive_ns"`
	DetectionDueNS            int64           `json:"detection_due_ns"`
	ExpiredNS                 int64           `json:"expired_ns"`
	FirstDownWrite            PeerExpiryWrite `json:"first_down_write"`
	lastReceive, due, expired time.Time
}

// PeerExpiryWrite records the first successful socket write before another accepted input.
// Known proves SendPacket returned nil; it proves neither delivery nor owner acceptance.
type PeerExpiryWrite struct {
	Known                    bool      `json:"known"`
	TimingKnown              bool      `json:"timing_known"`
	Scheduled                uint64    `json:"scheduled"`
	DecisionNS               int64     `json:"decision_ns"`
	StartedNS                int64     `json:"started_ns"`
	EndedNS                  int64     `json:"ended_ns"`
	MyDiscriminator          uint32    `json:"my_discriminator"`
	YourDiscriminator        uint32    `json:"your_discriminator"`
	State                    bfd.State `json:"state"`
	Diag                     bfd.Diag  `json:"diag"`
	DesiredMinTXUS           uint32    `json:"desired_min_tx_us"`
	RequiredMinRXUS          uint32    `json:"required_min_rx_us"`
	DetectMult               uint8     `json:"detect_mult"`
	Poll                     bool      `json:"poll"`
	Final                    bool      `json:"final"`
	decision, started, ended time.Time
}

func (p *peerRuntime) recordExpiry(previous peerState, now time.Time) error {
	p.pendingExpiry = 0
	if len(p.observed.DetectionExpiryEvents) == maxPeerExpiryEvents {
		p.observed.DetectionExpiryEventsOmitted++
		return fmt.Errorf("peer expiry journal exceeds %d events: %w", maxPeerExpiryEvents, ErrProfile)
	}
	p.observed.DetectionExpiryEvents = append(p.observed.DetectionExpiryEvents, PeerDetectionExpiry{
		ID: p.observed.DetectionExpirations, PreviousState: previous.state,
		LocalDiscriminator: previous.localDiscriminator, RemoteDiscriminator: previous.remoteDiscriminator,
		lastReceive: previous.lastReceive, due: previous.deadline(), expired: now,
	})
	if previous.state == bfd.StateInit || previous.state == bfd.StateUp {
		p.pendingExpiry = len(p.observed.DetectionExpiryEvents)
	}
	return nil
}

func (p *peerRuntime) recordExpiryWrite(packet bfd.ControlPacket, decision, started, ended time.Time) {
	row := &p.observed.DetectionExpiryEvents[p.pendingExpiry-1]
	row.FirstDownWrite = PeerExpiryWrite{
		Known: true, Scheduled: p.observed.Scheduled, decision: decision, started: started, ended: ended,
		MyDiscriminator: packet.MyDiscriminator, YourDiscriminator: packet.YourDiscriminator,
		State: packet.State, Diag: packet.Diag, DesiredMinTXUS: packet.DesiredMinTxInterval,
		RequiredMinRXUS: packet.RequiredMinRxInterval, DetectMult: packet.DetectMult, Poll: packet.Poll, Final: packet.Final,
	}
	p.pendingExpiry = 0
}

func (p *PeerDetectionExpiry) project(started time.Time, phaseKnown bool) {
	p.PhaseKnown = phaseKnown
	p.LastReceiveNS, p.DetectionDueNS, p.ExpiredNS = 0, 0, 0
	p.TimingKnown = monotonicExpiryOffsets(started, p.lastReceive, p.due, p.expired) &&
		p.due.After(p.lastReceive) && !p.expired.Before(p.due)
	if p.TimingKnown {
		p.LastReceiveNS, p.DetectionDueNS, p.ExpiredNS =
			int64(p.lastReceive.Sub(started)), int64(p.due.Sub(started)), int64(p.expired.Sub(started))
	}
	w := &p.FirstDownWrite
	w.DecisionNS, w.StartedNS, w.EndedNS = 0, 0, 0
	w.TimingKnown = w.Known && monotonicExpiryOffsets(started, w.decision, w.started, w.ended) &&
		!w.started.Before(w.decision) && !w.ended.Before(w.started)
	if w.TimingKnown {
		w.DecisionNS, w.StartedNS, w.EndedNS =
			int64(w.decision.Sub(started)), int64(w.started.Sub(started)), int64(w.ended.Sub(started))
	}
}

func monotonicExpiryOffsets(started time.Time, stamps ...time.Time) bool {
	//nolint:staticcheck // Structural equality detects the monotonic component; Equal ignores it.
	if started == started.Round(0) {
		return false
	}
	for _, stamp := range stamps {
		//nolint:staticcheck // Structural equality detects the monotonic component; Equal ignores it.
		if stamp == stamp.Round(0) || !started.Add(stamp.Sub(started)).Equal(stamp) {
			return false
		}
	}
	return true
}
