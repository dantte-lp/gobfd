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
	receivedUpper, valid := addNS(witness.Timing.ValidatedNS, owner.Phase.BracketNS)
	if !valid {
		return downUnresolved, "owner validated receive projection overflows"
	}
	var origins int
	for _, expiry := range peer.DetectionExpiryEvents {
		// Without a delivery bound, any earlier expiry may originate a delayed Down.
		if expiry.ExpiredNS > receivedUpper {
			continue
		}
		origins++
		outcome, reason := classifyPeerExpiryOrigin(peers, peer, witness, expiry)
		if outcome != downIntentional {
			return outcome, reason
		}
	}
	if origins == 0 {
		return downUnresolved, "no peer expiry can precede the validated remote Down"
	}
	return downIntentional, "every possible peer old-deadline origin has qualified RX omissions " +
		"under the stated per-origin acceptance/reset assumptions"
}

func classifyPeerExpiryOrigin(
	peers PeerObservation, peer PeerSessionObservation, witness OwnerDownWitness, expiry PeerDetectionExpiry,
) (string, string) {
	if expiry.PreviousState != bfd.StateUp || expiry.RemoteDiscriminator != witness.LocalDiscriminator {
		return downUnresolved, "possible peer expiry origin does not match this Up owner incarnation"
	}
	if !knownFirstExpiryWrite(expiry, peer.Scheduled, peers.Profile) ||
		!validFirstWritePrefix(expiry.FirstDownWrite, peer) {
		return downUnresolved, "possible peer expiry origin has no matching successful Down write"
	}
	writeUpper, valid := addNS(expiry.FirstDownWrite.StartedNS, peers.Phase.BracketNS)
	if !valid || writeUpper >= witness.Timing.ValidatedNS {
		return downUnresolved, "first Down write and owner validated receive are not strictly ordered"
	}
	return classifyPeerExpiryOmissions(peers, peer, expiry)
}

func validFirstWritePrefix(write PeerExpiryWrite, peer PeerSessionObservation) bool {
	for _, window := range peer.BlackholeWindows {
		for _, segment := range window.Segments {
			if segment.Direction != "tx" {
				continue
			}
			for index, offset := range segment.OffsetsNS {
				// Lifetime ordinals include setup; earlier phase ordinals are a strict lower bound.
				if offset < write.DecisionNS && segment.Sequences[index] >= write.Scheduled {
					return false
				}
			}
		}
	}
	return true
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

func knownFirstExpiryWrite(expiry PeerDetectionExpiry, scheduled uint64, profile Profile) bool {
	w := expiry.FirstDownWrite
	return w.Known && w.TimingKnown && w.Scheduled > 0 && w.Scheduled <= scheduled &&
		w.MyDiscriminator == expiry.LocalDiscriminator && w.State == bfd.StateDown &&
		expiry.ExpiredNS <= w.DecisionNS && w.DecisionNS <= w.StartedNS && w.StartedNS <= w.EndedNS &&
		knownWithheldControlDown(PeerOmissionSegment{
			YourDiscriminator: w.YourDiscriminator, Diag: w.Diag, DesiredMinTXUS: w.DesiredMinTXUS,
			RequiredMinRXUS: w.RequiredMinRXUS, DetectMult: w.DetectMult, Poll: w.Poll, Final: w.Final,
		}, profile)
}

func classifyPeerExpiryOmissions(
	peers PeerObservation, peer PeerSessionObservation, expiry PeerDetectionExpiry,
) (string, string) {
	bracket := peers.Phase.BracketNS
	expiredUpper, valid := addNS(expiry.ExpiredNS, bracket)
	if !valid || expiry.ExpiredNS < 0 || expiredUpper >= int64(peers.Profile.Duration) {
		return downUnresolved, "peer expiry origin is outside measurement or its projection overflows"
	}
	for _, window := range peer.BlackholeWindows {
		startUpper, startOK := addNS(window.StartNS, bracket)
		end, endOK := addNS(window.StartNS, int64(peers.Profile.BlackholeFor))
		if !startOK || !endOK {
			return downUnresolved, "peer blackhole projection overflow"
		}
		if expiry.DetectionDueNS >= startUpper && expiredUpper < end {
			return peerExpiryRXCoverage(window, expiry, peers.Profile, bracket)
		}
	}
	return downUnresolved, "peer old deadline and expiry are not inside one actual RX blackhole"
}

func peerExpiryRXCoverage(
	window PeerBlackholeWindow, expiry PeerDetectionExpiry, profile Profile, bracket int64,
) (string, string) {
	receiveUpper, receiveOK := addNS(expiry.LastReceiveNS, bracket)
	dueUpper, dueOK := addNS(expiry.DetectionDueNS, bracket)
	detection, detectionOK := addNS(expiry.DetectionDueNS, -expiry.LastReceiveNS)
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
			if upper <= expiry.LastReceiveNS || offset >= dueUpper {
				continue
			}
			if offset <= receiveUpper || upper >= expiry.DetectionDueNS ||
				!knownQualifiedControlRX(segment, profile, expiry, detection) {
				return downUnresolved, "peer RX omission acceptance, stable wire identity or old-deadline relation is unproven"
			}
			last, count = offset, count+1
		}
	}
	return classifyDeadlineExtension(last, count, detection, dueUpper)
}

func knownQualifiedControlRX(
	segment PeerOmissionSegment, profile Profile, expiry PeerDetectionExpiry, detection int64,
) bool {
	return segment.ReceiveQualified && !segment.Valid && segment.State == bfd.StateUp &&
		segment.MyDiscriminator == expiry.RemoteDiscriminator && knownStableControlUp(segment, profile, OwnerDownWitness{
		LocalDiscriminator: expiry.LocalDiscriminator, Timing: OwnerDownTiming{DetectionNS: detection},
	})
}
