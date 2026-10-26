//go:build linux

package bfdload

import "fmt"

const maxPeerBlackholeWindows = 256

// PeerBlackholeWindow records actual generator omissions, not causal owner Down evidence.
// Times are relative to the adopted phase, or the worker epoch without phase control.
type PeerBlackholeWindow struct {
	StartNS   int64  `json:"start_ns"`
	FirstTXNS int64  `json:"first_tx_ns"`
	LastTXNS  int64  `json:"last_tx_ns"`
	TXOmitted uint64 `json:"tx_omitted"`
	FirstRXNS int64  `json:"first_rx_ns"`
	LastRXNS  int64  `json:"last_rx_ns"`
	RXOmitted uint64 `json:"rx_omitted"`
}

func (p *peerRuntime) recordBlackhole(decision packetDecision, receive bool) error {
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
	return nil
}
