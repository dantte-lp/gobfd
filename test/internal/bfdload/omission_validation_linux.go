//go:build linux

package bfdload

import (
	"math"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func validatePeerHistory(peer PeerSessionObservation, profile Profile) error {
	validLedger := validPeerLedger(peer)
	if profile.Echo {
		validLedger = validEchoPeerLedger(peer)
	}
	if !validLedger || peer.BlackholeEventsOmitted != 0 || len(peer.BlackholeWindows) > maxPeerBlackholeWindows {
		return historyError()
	}
	var tx, rx uint64
	txDirection := omissionDirection{total: peer.Scheduled}
	rxDirection := omissionDirection{total: peer.Read}
	var previous int64
	var previousInput uint64
	for _, window := range peer.BlackholeWindows {
		if !validOmissionWindowStart(window, profile, previous) {
			return historyError()
		}
		if err := validateOmissionWindow(window, profile, &txDirection, &rxDirection); err != nil {
			return err
		}
		if profile.Echo {
			inputs, err := echoOmissionInputs(window, peer.Read)
			if err != nil || inputs[0].received <= previousInput {
				return historyError()
			}
			previousInput = inputs[len(inputs)-1].received
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
		(!profile.TXOnlyBlackhole || window.RXOmitted == 0) &&
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
		case "rx":
			direction = rx
		default:
			return historyError()
		}
		if !validWorkloadOmission(segment, profile.Echo) {
			return historyError()
		}
		if err := direction.observe(segment, window.StartNS, end, int64(profile.Duration), !profile.Echo); err != nil {
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

func (d *omissionDirection) observe(segment PeerOmissionSegment, start, end, duration int64, contiguous bool) error {
	if !boundedOmissionSegment(segment) || d.count > maxPeerOmissionTimes-segment.Count {
		return historyError()
	}
	for index, offset := range segment.OffsetsNS {
		sequence := segment.Sequences[index]
		if offset < start || offset >= end || offset >= duration || offset < d.last ||
			sequence <= d.sequence || sequence > d.total || (contiguous && d.count > 0 && sequence-d.sequence != 1) {
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
			if owner.Config.Profile.Echo {
				return conditionalEchoOmissionCoverage(owner, peers, window, witness)
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
	last, count, reason := controlOmissionInputs(owner, peers, window, witness, validatedUpper, dueUpper)
	if reason != "" {
		return downUnresolved, reason
	}
	return classifyDeadlineExtension(last, count, witness.Timing.DetectionNS, dueUpper)
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
		if !workloadTerminalDelivery(owner.Echo, row) || row.Outcome == bfd.DeliveryCommitted {
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
	if initial.Echo != final.Echo {
		return false
	}
	before, beforeKnown := terminalLossCounters(initial)
	after, afterKnown := terminalLossCounters(final)
	if !beforeKnown || !afterKnown {
		return false
	}
	for _, pair := range bfd.DeliveryCounterPairs() {
		if !workloadTerminalDelivery(initial.Echo, pair) || pair.Outcome == bfd.DeliveryCommitted {
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
	source := bfd.DeliveryPacketOwner
	if owner.Echo {
		source = bfd.DeliveryEchoOwner
	}
	for _, row := range owner.Delivery {
		if row.Source == source && row.Outcome == bfd.DeliveryCommitted {
			if found {
				return 0, false
			}
			count, found = row.Count, true
		}
	}
	return count, found
}

func controlOmissionInputs(
	owner OwnerWorkloadObservation, peers PeerObservation, window PeerBlackholeWindow,
	witness OwnerDownWitness, validatedUpper, dueUpper int64,
) (int64, uint64, string) {
	var selection controlOmissionSelection
	for _, segment := range window.Segments {
		if segment.Direction != "tx" {
			continue
		}
		if reason := selection.observe(segment, owner.Config.Profile, witness,
			validatedUpper, dueUpper, peers.Phase.BracketNS); reason != "" {
			return 0, 0, reason
		}
	}
	return selection.last, selection.count, ""
}

type controlOmissionSelection struct {
	last    int64
	count   uint64
	expired bool
}

func (s *controlOmissionSelection) observe(
	segment PeerOmissionSegment, profile Profile, witness OwnerDownWitness, validatedUpper, dueUpper, peerBracket int64,
) string {
	for _, offset := range segment.OffsetsNS {
		wasExpired := s.expired
		s.expired = s.expired || segment.State == bfd.StateDown
		upper, valid := addNS(offset, peerBracket)
		if !valid {
			return "omission projection overflow"
		}
		if upper <= witness.Timing.ValidatedNS || offset >= dueUpper {
			continue
		}
		up, reason := controlOmissionKind(segment, profile, witness)
		if reason != "" {
			return reason
		}
		if !up {
			continue
		}
		if wasExpired {
			return "actual Control Up follows expired Down inside the retained blackhole"
		}
		if offset <= validatedUpper || upper >= witness.Timing.DetectionDueNS {
			return "restored Up interval intersects validated receive or actual due"
		}
		s.last, s.count = offset, s.count+1
	}
	return ""
}

func controlOmissionKind(segment PeerOmissionSegment, profile Profile, witness OwnerDownWitness) (bool, string) {
	if !knownControlOmissionIdentity(segment, witness) {
		return false, "actual Control omission codec or peer identity is unproven"
	}
	switch segment.State {
	case bfd.StateUp:
		if !knownStableControlUp(segment, profile, witness) {
			return false, "actual Control Up does not preserve the owner and stable timer tuple"
		}
		return true, ""
	case bfd.StateDown:
		if !knownWithheldControlDown(segment, profile) {
			return false, "withheld Control Down is not the supported peer expiry packet"
		}
		return false, ""
	case bfd.StateAdminDown, bfd.StateInit:
		return false, "actual Control omission state is unsupported"
	}
	return false, "actual Control omission state is unknown"
}

func knownControlOmissionIdentity(segment PeerOmissionSegment, witness OwnerDownWitness) bool {
	slot := witness.Incarnation.Slot
	if slot < 0 || slot >= sourcePortCapacity {
		return false
	}
	return segment.Valid &&
		segment.MyDiscriminator == uint32(slot)+1 &&
		segment.MyDiscriminator == witness.Timing.ValidatedRemoteDiscriminator
}

func knownStableControlUp(segment PeerOmissionSegment, profile Profile, witness OwnerDownWitness) bool {
	interval, multiplier := int64(profile.Interval), int64(segment.DetectMult)
	return interval > 0 && multiplier > 0 && interval <= math.MaxInt64/multiplier &&
		segment.YourDiscriminator == witness.LocalDiscriminator && segment.Diag == bfd.DiagNone &&
		!segment.Poll && !segment.Final && segment.DetectMult == profile.DetectMultiplier &&
		time.Duration(segment.DesiredMinTXUS)*time.Microsecond == profile.Interval &&
		time.Duration(segment.RequiredMinRXUS)*time.Microsecond == profile.Interval &&
		interval*multiplier == witness.Timing.DetectionNS
}

func knownWithheldControlDown(segment PeerOmissionSegment, profile Profile) bool {
	slow := peerInitialTXUS * time.Microsecond
	return segment.YourDiscriminator == 0 && segment.Diag == bfd.DiagControlTimeExpired && !segment.Final &&
		time.Duration(segment.RequiredMinRXUS)*time.Microsecond == profile.Interval &&
		time.Duration(segment.DesiredMinTXUS)*time.Microsecond == max(profile.Interval, slow) &&
		segment.DetectMult == profile.DetectMultiplier && segment.Poll == (profile.Interval < slow)
}
