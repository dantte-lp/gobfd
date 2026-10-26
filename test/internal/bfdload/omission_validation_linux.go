//go:build linux

package bfdload

import (
	"math"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func validatePeerHistory(peer PeerSessionObservation, profile Profile) error {
	if !validPeerLedger(peer) || peer.BlackholeEventsOmitted != 0 ||
		len(peer.BlackholeWindows) > maxPeerBlackholeWindows {
		return historyError()
	}
	var tx, rx uint64
	txDirection := omissionDirection{total: peer.Scheduled}
	rxDirection := omissionDirection{total: peer.Read}
	var previous int64
	for _, window := range peer.BlackholeWindows {
		if !validOmissionWindowStart(window, profile, previous) {
			return historyError()
		}
		if err := validateOmissionWindow(window, profile, &txDirection, &rxDirection); err != nil {
			return err
		}
		var txOK, rxOK bool
		tx, txOK = addCount(tx, window.TXOmitted)
		rx, rxOK = addCount(rx, window.RXOmitted)
		if !txOK || !rxOK {
			return historyError()
		}
		previous = window.StartNS
	}
	if tx != peer.BlackholeOmitted || rx != peer.ReceiveBlackholeOmitted {
		return historyError()
	}
	return nil
}

func validOmissionWindowStart(window PeerBlackholeWindow, profile Profile, previous int64) bool {
	return profile.BlackholeEvery > 0 && profile.BlackholeFor > 0 &&
		window.StartNS >= int64(profile.BlackholeEvery) && window.StartNS%int64(profile.BlackholeEvery) == 0 &&
		window.StartNS > previous
}

func validPeerLedger(peer PeerSessionObservation) bool {
	return equalPeerCount(peer.Scheduled, peer.RandomOmitted, peer.BlackholeOmitted, peer.WriteAttempts) &&
		equalPeerCount(peer.WriteAttempts, peer.Written, peer.WriteErrors, peer.WriteCanceled) &&
		equalPeerCount(peer.Read, peer.Accepted, peer.Rejected, peer.ReceiveRandomOmitted,
			peer.ReceiveBlackholeOmitted, peer.ReceiveNotProcessed)
}

func equalPeerCount(total uint64, parts ...uint64) bool {
	var sum uint64
	for _, part := range parts {
		var valid bool
		sum, valid = addCount(sum, part)
		if !valid {
			return false
		}
	}
	return sum == total
}

type omissionDirection struct {
	count       uint64
	first, last int64
	sequence    uint64
	total       uint64
}

func validateOmissionWindow(window PeerBlackholeWindow, profile Profile, tx, rx *omissionDirection) error {
	end, valid := addNS(window.StartNS, int64(profile.BlackholeFor))
	if !valid || !boundedOmissionWindow(window) {
		return historyError()
	}
	tx.count, tx.first, tx.last = 0, 0, 0
	rx.count, rx.first, rx.last = 0, 0, 0
	for _, segment := range window.Segments {
		direction := tx
		switch segment.Direction {
		case "tx":
			if segment.Valid && !omissionPacket(segment.controlPacket(), false).Valid {
				return historyError()
			}
		case "rx":
			if segment.Valid {
				return historyError()
			}
			direction = rx
		default:
			return historyError()
		}
		if err := direction.observe(segment, window.StartNS, end, int64(profile.Duration)); err != nil {
			return err
		}
	}
	if !tx.matches(window.TXOmitted, window.FirstTXNS, window.LastTXNS) ||
		!rx.matches(window.RXOmitted, window.FirstRXNS, window.LastRXNS) {
		return historyError()
	}
	return nil
}

func boundedOmissionWindow(window PeerBlackholeWindow) bool {
	return len(window.Segments) > 0 && len(window.Segments) <= maxPeerOmissionSegments &&
		window.TXOmitted <= maxPeerOmissionTimes && window.RXOmitted <= maxPeerOmissionTimes &&
		window.TXOmitted+window.RXOmitted <= maxPeerOmissionTimes
}

func (d *omissionDirection) matches(count uint64, first, last int64) bool {
	return d.count == count && d.first == first && d.last == last
}

func boundedOmissionSegment(segment PeerOmissionSegment) bool {
	return segment.Count > 0 && segment.Count <= maxPeerOmissionTimes &&
		uint64(len(segment.OffsetsNS)) == segment.Count && len(segment.Sequences) == len(segment.OffsetsNS) &&
		segment.FirstNS == segment.OffsetsNS[0] && segment.LastNS == segment.OffsetsNS[len(segment.OffsetsNS)-1]
}

func (d *omissionDirection) observe(segment PeerOmissionSegment, start, end, duration int64) error {
	if !boundedOmissionSegment(segment) || d.count > maxPeerOmissionTimes-segment.Count {
		return historyError()
	}
	for index, offset := range segment.OffsetsNS {
		sequence := segment.Sequences[index]
		if offset < start || offset >= end || offset >= duration || offset < d.last ||
			sequence <= d.sequence || sequence > d.total || (d.count > 0 && sequence-d.sequence != 1) {
			return historyError()
		}
		if d.count == 0 {
			d.first = offset
		}
		d.last, d.sequence = offset, sequence
		d.count++
	}
	return nil
}

type omissionCoverage struct {
	count                 uint64
	first, last, interval int64
	sequence              uint64
}

func classifyOmissions(
	owner OwnerWorkloadObservation, peers PeerObservation, peer PeerSessionObservation,
	witness OwnerDownWitness, progress bool,
) (string, string) {
	commitUpper, valid := addNS(witness.Timing.CommitEndNS, owner.Phase.BracketNS)
	if !valid {
		return downUnresolved, "owner commit projection overflow"
	}
	prior := false
	for _, window := range peer.BlackholeWindows {
		for _, segment := range window.Segments {
			if segment.FirstNS < commitUpper {
				prior = true
			}
		}
		startUpper, startOK := addNS(window.StartNS, peers.Phase.BracketNS)
		end, endOK := addNS(window.StartNS, int64(peers.Profile.BlackholeFor))
		if !startOK || !endOK {
			return downUnresolved, "blackhole projection overflow"
		}
		if witness.Timing.CommitStartNS >= startUpper && commitUpper < end {
			if !progress {
				return downUnresolved, "measurement contains missing progress, counter reset or an uncorrelated independent drop"
			}
			return conditionalOmissionCoverage(owner, peers, window, witness)
		}
	}
	if !prior {
		return downFalse, "no actual blackhole omission precedes this owner Down"
	}
	return downUnresolved, "actual omissions do not place the full owner commit inside one blackhole window"
}

func conditionalOmissionCoverage(
	owner OwnerWorkloadObservation, peers PeerObservation, window PeerBlackholeWindow, witness OwnerDownWitness,
) (string, string) {
	validatedUpper, receiveOK := addNS(witness.Timing.ValidatedNS, owner.Phase.BracketNS)
	dueUpper, dueOK := addNS(witness.Timing.DetectionDueNS, owner.Phase.BracketNS)
	if !receiveOK || !dueOK {
		return downUnresolved, "receive/deadline projection overflow"
	}
	var coverage omissionCoverage
	if reason := coverage.collect(owner, peers, window, witness, validatedUpper, dueUpper); reason != "" {
		return downUnresolved, reason
	}
	firstLimit, firstOK := addNS(witness.Timing.ValidatedNS, coverage.interval)
	lastLimit, lastOK := addNS(coverage.last, coverage.interval)
	firstUpper, upperOK := addNS(coverage.first, peers.Phase.BracketNS)
	if !firstOK || !lastOK || !upperOK || !coverage.coversDetection(witness.Timing.DetectionNS) ||
		firstUpper > firstLimit || lastLimit < dueUpper {
		return downUnresolved, "actual omission sequence does not cover the old receive-to-deadline model"
	}
	return downIntentional, "intentional only under the stated unchanged-transport and no-independent-fault assumptions"
}

func (c *omissionCoverage) coversDetection(detection int64) bool {
	if c.interval <= 0 || detection <= 0 {
		return false
	}
	required := detection / c.interval
	if required < 1 || required > maxPeerOmissionTimes {
		return false
	}
	return c.count >= uint64(required)
}

func uncontestedMeasurementProgress(owner OwnerWorkloadObservation) bool {
	initial, final := owner.Initial, owner.MeasurementFinal
	if initial.Scope == "" || final.Scope != initial.Scope || initial.Stopped || final.Stopped {
		return false
	}
	before, beforeErr := terminalPacketCount(initial)
	after, afterErr := terminalPacketCount(final)
	if beforeErr != nil || afterErr != nil || after < before {
		return false
	}
	beforeCommitted, beforeKnown := committedMeasurementPackets(initial)
	afterCommitted, afterKnown := committedMeasurementPackets(final)
	return beforeKnown && afterKnown && afterCommitted > beforeCommitted &&
		after-before == afterCommitted-beforeCommitted && unchangedTerminalLoss(initial, final)
}

type measurementDeliveryKey struct {
	source  bfd.DeliverySource
	outcome bfd.DeliveryOutcome
}

func terminalLossCounters(owner OwnerObservation) (map[measurementDeliveryKey]uint64, bool) {
	rows := make(map[measurementDeliveryKey]uint64)
	for _, row := range owner.Delivery {
		if !terminalDelivery(row) || row.Outcome == bfd.DeliveryCommitted {
			continue
		}
		key := measurementDeliveryKey{row.Source, row.Outcome}
		if _, duplicate := rows[key]; duplicate {
			return nil, false
		}
		rows[key] = row.Count
	}
	return rows, true
}

func unchangedTerminalLoss(initial, final OwnerObservation) bool {
	before, beforeKnown := terminalLossCounters(initial)
	after, afterKnown := terminalLossCounters(final)
	if !beforeKnown || !afterKnown {
		return false
	}
	for _, pair := range bfd.DeliveryCounterPairs() {
		if !terminalDelivery(pair) || pair.Outcome == bfd.DeliveryCommitted {
			continue
		}
		key := measurementDeliveryKey{pair.Source, pair.Outcome}
		left, leftKnown := before[key]
		right, rightKnown := after[key]
		if !leftKnown || !rightKnown || left != right {
			return false
		}
	}
	return true
}

func committedMeasurementPackets(owner OwnerObservation) (uint64, bool) {
	var count uint64
	found := false
	for _, row := range owner.Delivery {
		if row.Source == bfd.DeliveryPacketOwner && row.Outcome == bfd.DeliveryCommitted {
			if found {
				return 0, false
			}
			count, found = row.Count, true
		}
	}
	return count, found
}

func (c *omissionCoverage) collect(
	owner OwnerWorkloadObservation, peers PeerObservation, window PeerBlackholeWindow,
	witness OwnerDownWitness, validatedUpper, dueUpper int64,
) string {
	for _, segment := range window.Segments {
		if segment.Direction != "tx" {
			continue
		}
		for index, offset := range segment.OffsetsNS {
			upper, valid := addNS(offset, peers.Phase.BracketNS)
			if !valid {
				return "omission projection overflow"
			}
			if upper <= witness.Timing.ValidatedNS || offset >= dueUpper {
				continue
			}
			if offset <= validatedUpper || upper >= witness.Timing.DetectionDueNS {
				return "omission interval intersects validated receive or actual due"
			}
			if !c.add(segment, index, owner, witness) {
				return "omitted wire identity, negotiated interval or actual progress is unproven"
			}
		}
	}
	return ""
}

func (c *omissionCoverage) add(
	segment PeerOmissionSegment, index int, owner OwnerWorkloadObservation, witness OwnerDownWitness,
) bool {
	interval := max(int64(owner.Config.Profile.Interval), int64(segment.DesiredMinTXUS)*int64(time.Microsecond))
	if !knownOmittedTX(segment, witness, interval) {
		return false
	}
	offset, sequence := segment.OffsetsNS[index], segment.Sequences[index]
	if c.count > 0 && (interval != c.interval || offset-c.last > interval || c.sequence == math.MaxUint64 ||
		sequence != c.sequence+1) {
		return false
	}
	if c.count == 0 {
		c.first, c.interval = offset, interval
	}
	c.last, c.sequence = offset, sequence
	c.count++
	return true
}

func knownOmittedTX(segment PeerOmissionSegment, witness OwnerDownWitness, interval int64) bool {
	slot := witness.Incarnation.Slot
	if slot < 0 || slot >= sourcePortCapacity {
		return false
	}
	return segment.Valid && segment.State == bfd.StateUp &&
		segment.MyDiscriminator == uint32(slot)+1 &&
		segment.MyDiscriminator == witness.Timing.ValidatedRemoteDiscriminator &&
		segment.YourDiscriminator == witness.LocalDiscriminator &&
		segment.DetectMult != 0 && interval > 0 && interval <= math.MaxInt64/int64(segment.DetectMult) &&
		interval*int64(segment.DetectMult) == witness.Timing.DetectionNS
}
