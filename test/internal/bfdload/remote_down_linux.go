//go:build linux

package bfdload

import (
	"math"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func classifyRemoteDown(
	owner OwnerWorkloadObservation, peers PeerObservation, witness OwnerDownWitness, progress bool,
) (string, string) {
	peer := peers.Peers[witness.Incarnation.Slot]
	if owner.Config.Profile.Echo || !progress || !noObservedPeerLoss(peer) {
		return downUnresolved, "remote Down contains unsupported mode, missing progress or independent loss"
	}
	if !knownRemoteDownReceive(witness, peer.ID) || !completePeerExpiryHistory(peer) {
		return downUnresolved, "remote Down lacks a complete matching peer expiry origin or validated receive"
	}
	first := peer.DetectionExpiryEvents[0]
	receivedUpper, valid := addNS(witness.Timing.ValidatedNS, owner.Phase.BracketNS)
	if !valid || first.PreviousState != bfd.StateUp || first.RemoteDiscriminator != witness.LocalDiscriminator {
		return downUnresolved, "first peer expiry does not match this owner incarnation"
	}
	for _, later := range peer.DetectionExpiryEvents[1:] {
		if later.ExpiredNS <= receivedUpper {
			return downUnresolved, "another peer expiry can originate the indistinguishable received Down"
		}
	}
	if !knownFirstExpiryWrite(first, peer.Scheduled, peers.Profile) {
		return downUnresolved, "first peer expiry has no matching successful Down write"
	}
	writeUpper, valid := addNS(first.FirstDownWrite.StartedNS, peers.Phase.BracketNS)
	if !valid || writeUpper >= witness.Timing.ValidatedNS {
		return downUnresolved, "first Down write and owner validated receive are not strictly ordered"
	}
	return classifyFirstExpiryOmissions(peers, peer, first)
}

func noObservedPeerLoss(peer PeerSessionObservation) bool {
	return peer.ReadErrors == 0 && peer.WriteErrors == 0 && peer.WriteCanceled == 0 &&
		peer.ReceiveNotProcessed == 0 && peer.KernelDropCounterRaw == 0 && peer.KernelDropInvalid == 0
}

func knownRemoteDownReceive(witness OwnerDownWitness, peerID uint64) bool {
	return witness.RemoteKnown && witness.RemoteState == bfd.StateDown &&
		witness.RemoteDiag == bfd.DiagControlTimeExpired && witness.LocalDiag == bfd.DiagNeighborDown &&
		uint64(witness.RemoteDiscriminator) == peerID && witness.Timing.ValidatedKnown &&
		witness.Timing.ValidatedNS <= witness.Timing.CommitStartNS
}

func completePeerExpiryHistory(peer PeerSessionObservation) bool {
	rows := peer.DetectionExpiryEvents
	if len(rows) == 0 || len(rows) > maxPeerExpiryEvents || uint64(len(rows)) != peer.DetectionExpirations ||
		peer.DetectionExpiryEventsOmitted != 0 {
		return false
	}
	var previous int64
	for index, row := range rows {
		if row.ID != uint64(index+1) || !validPeerExpiry(row, peer.ID) || index > 0 && row.LastReceiveNS < previous {
			return false
		}
		previous = row.ExpiredNS
	}
	return true
}

func validPeerExpiry(row PeerDetectionExpiry, peerID uint64) bool {
	return uint64(row.LocalDiscriminator) == peerID && row.RemoteDiscriminator != 0 &&
		row.PhaseKnown && row.TimingKnown && row.LastReceiveNS != math.MinInt64 &&
		row.DetectionDueNS > row.LastReceiveNS && row.ExpiredNS >= row.DetectionDueNS &&
		(row.PreviousState == bfd.StateUp || row.PreviousState == bfd.StateInit || row.PreviousState == bfd.StateDown)
}

func knownFirstExpiryWrite(first PeerDetectionExpiry, scheduled uint64, profile Profile) bool {
	w := first.FirstDownWrite
	return w.Known && w.TimingKnown && w.Scheduled > 0 && w.Scheduled <= scheduled &&
		w.MyDiscriminator == first.LocalDiscriminator && w.State == bfd.StateDown &&
		first.ExpiredNS <= w.DecisionNS && w.DecisionNS <= w.StartedNS && w.StartedNS <= w.EndedNS &&
		knownWithheldControlDown(PeerOmissionSegment{
			YourDiscriminator: w.YourDiscriminator, Diag: w.Diag, DesiredMinTXUS: w.DesiredMinTXUS,
			RequiredMinRXUS: w.RequiredMinRXUS, DetectMult: w.DetectMult, Poll: w.Poll, Final: w.Final,
		}, profile)
}

func classifyFirstExpiryOmissions(
	peers PeerObservation, peer PeerSessionObservation, first PeerDetectionExpiry,
) (string, string) {
	bracket := peers.Phase.BracketNS
	expiredUpper, valid := addNS(first.ExpiredNS, bracket)
	if !valid || first.ExpiredNS < 0 || expiredUpper >= int64(peers.Profile.Duration) {
		return downUnresolved, "first peer expiry is outside measurement or its projection overflows"
	}
	for _, window := range peer.BlackholeWindows {
		startUpper, startOK := addNS(window.StartNS, bracket)
		end, endOK := addNS(window.StartNS, int64(peers.Profile.BlackholeFor))
		if !startOK || !endOK {
			return downUnresolved, "peer blackhole projection overflow"
		}
		if first.DetectionDueNS >= startUpper && expiredUpper < end {
			return firstExpiryRXCoverage(window, first, peers.Profile, bracket)
		}
	}
	return downUnresolved, "first peer old deadline and expiry are not inside one actual RX blackhole"
}

func firstExpiryRXCoverage(
	window PeerBlackholeWindow, first PeerDetectionExpiry, profile Profile, bracket int64,
) (string, string) {
	receiveUpper, receiveOK := addNS(first.LastReceiveNS, bracket)
	dueUpper, dueOK := addNS(first.DetectionDueNS, bracket)
	detection, detectionOK := addNS(first.DetectionDueNS, -first.LastReceiveNS)
	if !receiveOK || !dueOK || !detectionOK || detection <= 0 {
		return downUnresolved, "peer receive or old detection interval projection overflow"
	}
	var last int64
	var count uint64
	for _, segment := range window.Segments {
		if segment.Direction != "rx" {
			continue
		}
		for _, offset := range segment.OffsetsNS {
			upper, valid := addNS(offset, bracket)
			if !valid {
				return downUnresolved, "peer RX omission projection overflow"
			}
			if upper <= first.LastReceiveNS || offset >= dueUpper {
				continue
			}
			if offset <= receiveUpper || upper >= first.DetectionDueNS ||
				!knownQualifiedControlRX(segment, profile, first, detection) {
				return downUnresolved, "peer RX omission acceptance, stable wire identity or old-deadline relation is unproven"
			}
			last, count = offset, count+1
		}
	}
	outcome, reason := classifyDeadlineExtension(last, count, detection, dueUpper)
	if outcome == downIntentional {
		reason = "RemoteDown originates from the first peer old deadline under the stated peer acceptance/reset assumptions"
	}
	return outcome, reason
}

func knownQualifiedControlRX(
	segment PeerOmissionSegment, profile Profile, first PeerDetectionExpiry, detection int64,
) bool {
	return segment.ReceiveQualified && !segment.Valid && segment.State == bfd.StateUp &&
		segment.MyDiscriminator == first.RemoteDiscriminator && knownStableControlUp(segment, profile, OwnerDownWitness{
		LocalDiscriminator: first.LocalDiscriminator, Timing: OwnerDownTiming{DetectionNS: detection},
	})
}
