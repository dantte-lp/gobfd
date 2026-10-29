//go:build linux

package bfdload

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func validEchoPeerLedger(peer PeerSessionObservation) bool {
	return validPeerLedger(peer) && peer.Accepted == peer.Scheduled && peer.Rejected == 0 &&
		peer.RemoteDiscriminator == 0 && peer.State == "" && peer.UpTransitions == 0 && peer.DownTransitions == 0 &&
		peer.ReadErrors == 0 && peer.WriteErrors == 0 && peer.KernelDropCounterRaw == 0 && peer.KernelDropInvalid == 0
}

func validWorkloadOmission(segment PeerOmissionSegment, echo bool) bool {
	if segment.ReceiveQualified && (echo || segment.Direction != "rx") {
		return false
	}
	if echo {
		return segment.Valid && len(segment.ReceivedSequences) == len(segment.OffsetsNS) &&
			omissionPacket(segment.controlPacket(), false).Valid
	}
	if len(segment.ReceivedSequences) != 0 {
		return false
	}
	if segment.Direction == "rx" {
		return !segment.Valid
	}
	return !segment.Valid || omissionPacket(segment.controlPacket(), false).Valid
}

type echoOmission struct {
	segment  *PeerOmissionSegment
	offset   int64
	received uint64
}

// RX and TX decisions have separate counters; only actual Read sequences join their inputs.
func echoOmissionInputs(window PeerBlackholeWindow, read uint64) ([]echoOmission, error) {
	if !boundedOmissionWindow(window) {
		return nil, historyError()
	}
	var inputs []echoOmission
	for index := range window.Segments {
		segment := &window.Segments[index]
		if !boundedOmissionSegment(*segment) || !validWorkloadOmission(*segment, true) {
			return nil, historyError()
		}
		for position, received := range segment.ReceivedSequences {
			if received == 0 || received > read || len(inputs) == maxPeerOmissionTimes {
				return nil, historyError()
			}
			inputs = append(inputs, echoOmission{segment: segment, offset: segment.OffsetsNS[position], received: received})
		}
	}
	slices.SortFunc(inputs, func(left, right echoOmission) int { return cmp.Compare(left.received, right.received) })
	for index := 1; index < len(inputs); index++ {
		previous, current := inputs[index-1], inputs[index]
		if current.received == previous.received || current.offset < previous.offset {
			return nil, historyError()
		}
	}
	return inputs, nil
}

func matchingEchoDetection(profile Profile, timing OwnerDownTiming) bool {
	interval, multiplier := int64(profile.Interval), int64(profile.DetectMultiplier)
	if interval <= 0 || multiplier == 0 || interval > math.MaxInt64/multiplier ||
		timing.DetectionNS != interval*multiplier {
		return false
	}
	minimumDue, valid := addNS(timing.ValidatedNS, timing.DetectionNS)
	return valid && timing.DetectionDueNS >= minimumDue
}

func knownEchoOmitted(segment PeerOmissionSegment, profile Profile, witness OwnerDownWitness) bool {
	return segment.Valid && segment.MyDiscriminator == witness.LocalDiscriminator &&
		segment.YourDiscriminator == witness.LocalDiscriminator && segment.State == bfd.StateUp &&
		segment.Diag == bfd.DiagNone && !segment.Poll && !segment.Final &&
		time.Duration(segment.DesiredMinTXUS)*time.Microsecond == profile.Interval && segment.RequiredMinRXUS == 0 &&
		segment.DetectMult == profile.DetectMultiplier
}

func conditionalEchoOmissionCoverage(
	owner OwnerWorkloadObservation, peers PeerObservation, window PeerBlackholeWindow, witness OwnerDownWitness,
) (string, string) {
	validatedUpper, valid := addNS(witness.Timing.ValidatedNS, owner.Phase.BracketNS)
	dueUpper, dueValid := addNS(witness.Timing.DetectionDueNS, owner.Phase.BracketNS)
	if !valid || !dueValid {
		return downUnresolved, "Echo receive/deadline projection overflow"
	}
	inputs, err := echoOmissionInputs(window, peers.Peers[witness.Incarnation.Slot].Read)
	if err != nil {
		return downUnresolved, "actual Echo input history is unavailable"
	}
	var count, previous uint64
	var last int64
	for _, input := range inputs {
		upper, bounded := addNS(input.offset, peers.Phase.BracketNS)
		if !bounded {
			return downUnresolved, "Echo omission projection overflow"
		}
		if upper <= witness.Timing.ValidatedNS || input.offset >= dueUpper {
			continue
		}
		if input.offset <= validatedUpper || upper >= witness.Timing.DetectionDueNS ||
			!knownEchoOmitted(*input.segment, owner.Config.Profile, witness) {
			return downUnresolved, "Echo input identity or relation to the old receive/deadline is ambiguous"
		}
		if count > 0 && (previous == math.MaxUint64 || input.received != previous+1) {
			return downUnresolved, "actual Echo omission inputs are not contiguous"
		}
		previous, count = input.received, count+1
		last = input.offset
	}
	return classifyDeadlineExtension(last, count, witness.Timing.DetectionNS, dueUpper)
}

func classifyDeadlineExtension(last int64, count uint64, detection, dueUpper int64) (string, string) {
	if count == 0 {
		return downUnresolved, "actual omission inputs before the old deadline are unavailable"
	}
	replacementDue, valid := addNS(last, detection)
	if !valid {
		return downUnresolved, "replacement deadline projection overflow"
	}
	if replacementDue <= dueUpper {
		return downUnresolved, "actual omission inputs do not extend the old deadline"
	}
	return downIntentional, "old deadline is intentional only under the stated delivery and reset assumptions"
}
