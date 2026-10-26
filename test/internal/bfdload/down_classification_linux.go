//go:build linux

package bfdload

import (
	"fmt"
	"math"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// DownVerdict identifies one source witness; notification time never selects its phase.
type DownVerdict struct {
	Witness            int    `json:"witness"`
	Slot               int    `json:"slot"`
	Generation         uint64 `json:"generation"`
	ObservationVersion uint64 `json:"observation_version"`
	Outcome            string `json:"outcome"`
	Reason             string `json:"reason"`
}

// DownClassification is a conditional model verdict, never reference qualification.
// Passing requires complete evidence, nonempty eligible inputs and no false/unresolved Down.
type DownClassification struct {
	OwnerPhase             *Phase        `json:"owner_phase"`
	PeerPhase              *Phase        `json:"peer_phase"`
	Complete               bool          `json:"complete"`
	Conditional            bool          `json:"conditional"`
	Assumptions            []string      `json:"assumptions"`
	TimingUncertaintyNS    *uint64       `json:"timing_uncertainty_ns"`
	TimingUncertaintyError string        `json:"timing_uncertainty_error"`
	ReferenceQualified     bool          `json:"reference_qualified"`
	Passing                bool          `json:"passing"`
	Eligible               uint64        `json:"eligible"`
	Outside                uint64        `json:"outside"`
	Intentional            uint64        `json:"intentional"`
	False                  uint64        `json:"false"`
	Unresolved             uint64        `json:"unresolved"`
	Rows                   []DownVerdict `json:"rows"`
	Error                  string        `json:"error,omitempty"`
}

const (
	downIntentional = "intentional"
	downFalse       = "false"
	downUnresolved  = "unresolved"
)

// ClassifyDowns joins independently serialized owner/peer reports after both roles stop.
// It does not establish total RX uncertainty, a transport delay bound or supported scale.
func ClassifyDowns(owner OwnerWorkloadObservation, peers PeerObservation) DownClassification {
	result := DownClassification{
		Conditional:            true,
		TimingUncertaintyError: "total clock/transport uncertainty is not established by the conditional omission model",
		Assumptions: []string{
			"the recorded boot, time namespace and native epoch denote one unchanged monotonic clock domain",
			"omitted valid expected-source Up packets would traverse the unchanged transport and reach the owner before due",
			"no independent hidden transport or owner fault coincides with the observed omission sequence",
		},
	}
	if owner.Phase != nil {
		phase := *owner.Phase
		result.OwnerPhase = &phase
	}
	if peers.Phase != nil {
		phase := *peers.Phase
		result.PeerPhase = &phase
	}
	if len(owner.DownWitnesses) > maxDownEvents {
		result.Eligible, result.Unresolved = uint64(len(owner.DownWitnesses)), uint64(len(owner.DownWitnesses))
		result.Error = "source witness count exceeds bounded journal"
		return result
	}
	result.Error = classificationInputs(owner, peers)
	result.Complete = result.Error == ""
	progress := uncontestedMeasurementProgress(owner)
	for index, witness := range owner.DownWitnesses {
		result.observe(owner, peers, index, witness, progress)
	}
	result.Passing = result.Complete && result.Eligible > 0 && result.False == 0 && result.Unresolved == 0
	return result
}

func (r *DownClassification) observe(
	owner OwnerWorkloadObservation, peers PeerObservation, index int, witness OwnerDownWitness, progress bool,
) {
	row := DownVerdict{
		Witness: index, Slot: witness.Incarnation.Slot, Generation: witness.Incarnation.Generation,
		ObservationVersion: witness.ObservationVersion, Outcome: downUnresolved, Reason: r.Error,
	}
	if r.Complete {
		outside, phaseKnown := downMeasurementPhase(witness.Timing, owner.Phase, owner.Config.Profile.Duration)
		if outside {
			r.Outside++
			return
		}
		if !phaseKnown {
			row.Reason = "owner commit bracket is unknown or intersects a measurement boundary"
		} else {
			row.Outcome, row.Reason = classifyDownWitness(owner, peers, witness, progress)
		}
	}
	r.Eligible++
	r.Rows = append(r.Rows, row)
	switch row.Outcome {
	case downIntentional:
		r.Intentional++
	case downFalse:
		r.False++
	case downUnresolved:
		r.Unresolved++
	}
}

func classificationInputs(owner OwnerWorkloadObservation, peers PeerObservation) string {
	if !joinedClassificationRoles(owner, peers) {
		return "owner or peer execution is incomplete or unjoined"
	}
	if !settledClassificationSource(owner) {
		return "source/subscriber count or incarnation history is incomplete"
	}
	if !matchingClassificationPhases(owner.Phase, peers.Phase) {
		return "run, boot, clock domain or phase conversion bounds are missing or unequal"
	}
	if !matchingClassificationManifest(owner, peers) {
		return "owner/peer workload manifest, lifecycle or profile differs"
	}
	seen := make(map[downWitnessKey]struct{}, len(owner.DownWitnesses))
	for _, witness := range owner.DownWitnesses {
		key := downWitnessKey{witness.Incarnation.Slot, witness.Incarnation.Generation, witness.ObservationVersion}
		if _, duplicate := seen[key]; duplicate {
			return "duplicate source witness identity"
		}
		seen[key] = struct{}{}
	}
	for slot, peer := range peers.Peers {
		if peer.ID != uint64(slot+1) || peer.Endpoint != owner.Config.Sessions[slot] ||
			int(peer.SourcePort) != int(owner.Config.SourcePortBase)+slot || validatePeerHistory(peer, peers.Profile) != nil {
			return "peer transport identity or actual omission history is incomplete"
		}
	}
	return ""
}

func matchingClassificationManifest(owner OwnerWorkloadObservation, peers PeerObservation) bool {
	return owner.Config.Profile == peers.Profile && owner.Config.Validate() == nil &&
		len(owner.Config.Sessions) == owner.Config.Profile.Sessions && len(peers.Peers) == len(owner.Config.Sessions) &&
		len(owner.Initial.Sessions) == len(owner.Config.Sessions) &&
		owner.SessionChurn.Enabled == owner.Config.Profile.SessionChurn && owner.SessionChurn.EventsOmitted == 0 &&
		(owner.SessionChurn.Enabled || len(owner.SessionChurn.Events) == 0)
}

func joinedClassificationRoles(owner OwnerWorkloadObservation, peers PeerObservation) bool {
	return owner.Completed && owner.Final.Stopped && owner.Error == "" &&
		peers.Completed && peers.Stopped && peers.Error == ""
}

func settledClassificationSource(owner OwnerWorkloadObservation) bool {
	j := owner.DownJournal
	if !j.Complete || !j.Settled || !j.Closed || !j.IncarnationsComplete || j.Error != "" ||
		j.IncarnationError != "" || owner.NotificationGaps != 0 || owner.DownEventsOmitted != 0 ||
		j.FinalSourceTransitions == nil {
		return false
	}
	count, valid := addCount(j.ObservedBeforeCutoff, j.ObservedAfterCutoff)
	return valid && count == *j.FinalSourceTransitions && uint64(len(owner.DownWitnesses)) == count &&
		j.ObservedBeforeCutoff <= j.SourceTransitions && j.SourceTransitions <= count
}

type downWitnessKey struct {
	slot                int
	generation, version uint64
}

func matchingClassificationPhases(owner, peer *Phase) bool {
	return owner != nil && peer != nil && owner.BracketKnown && peer.BracketKnown &&
		owner.NativeStartNS > 0 && owner.NativeStartNS == peer.NativeStartNS &&
		owner.TimeNamespaceID != 0 && owner.TimeNamespaceID == peer.TimeNamespaceID &&
		owner.Run.valid() && owner.Run == peer.Run &&
		owner.BracketNS >= 0 && owner.BracketNS <= int64(maxClockBracket) &&
		peer.BracketNS >= 0 && peer.BracketNS <= int64(maxClockBracket)
}

func downMeasurementPhase(timing OwnerDownTiming, phase *Phase, duration time.Duration) (bool, bool) {
	if !timing.CommitKnown || timing.CommitEndNS < timing.CommitStartNS {
		return false, false
	}
	upper, valid := addNS(timing.CommitEndNS, phase.BracketNS)
	if !valid {
		return false, false
	}
	if upper < 0 || timing.CommitStartNS >= int64(duration) {
		return true, true
	}
	return false, timing.CommitStartNS >= 0 && upper < int64(duration)
}

func classifyDownWitness(
	owner OwnerWorkloadObservation, peers PeerObservation, witness OwnerDownWitness, progress bool,
) (string, string) {
	if !knownDownIdentity(owner, witness) {
		return downUnresolved, "frozen source identity is missing, inconsistent or ambiguous across incarnations"
	}
	if witness.Cause == bfd.CauseRemoteDown || witness.Cause == bfd.CauseRemoteAdminDown {
		return downUnresolved, "remote Down has no independently joined peer expiry/cause witness"
	}
	if witness.Cause != bfd.CauseDetectionTimeout {
		if witness.Cause == bfd.CauseUnknown {
			return downUnresolved, "owner cause is unknown"
		}
		return downFalse, "owner committed an unrelated non-detection cause"
	}
	timing := witness.Timing
	if !knownDetectionWitness(witness) {
		return downUnresolved, "old receive/deadline/diagnostic evidence is unavailable or inconsistent"
	}
	if timing.CommitEndNS < timing.DetectionDueNS {
		return downFalse, "owner Down precedes the actual detection deadline"
	}
	if timing.CommitStartNS < timing.DetectionDueNS {
		return downUnresolved, "owner commit bracket intersects its actual detection deadline"
	}
	peer := peers.Peers[witness.Incarnation.Slot]
	return classifyOmissions(owner, peers, peer, witness, progress)
}

func knownDetectionWitness(witness OwnerDownWitness) bool {
	timing := witness.Timing
	return timing.Error == "" && timing.ValidatedKnown && timing.DetectionKnown && timing.DetectionNS > 0 &&
		timing.ValidatedRemoteDiscriminator != 0 &&
		timing.ValidatedNS <= timing.CommitStartNS && timing.DetectionDueNS >= timing.ValidatedNS &&
		witness.LocalDiag == bfd.DiagControlTimeExpired && witness.RemoteKnown && witness.RemoteState == bfd.StateUp
}

func knownDownIdentity(owner OwnerWorkloadObservation, witness OwnerDownWitness) bool {
	identity := witness.Incarnation
	if identity.Slot < 0 || identity.Slot >= len(owner.Config.Sessions) {
		return false
	}
	endpoint := owner.Config.Sessions[identity.Slot]
	if !coherentDownManifest(witness, endpoint) {
		return false
	}
	initial := owner.Initial.Sessions[identity.Slot]
	if initial.Endpoint != endpoint || initial.Generation != 1 || initial.LocalDiscriminator == 0 {
		return false
	}
	return historicalDownGeneration(initial, owner.SessionChurn, witness)
}

func coherentDownManifest(witness OwnerDownWitness, endpoint OwnerEndpoint) bool {
	return witness.Incarnation.Known && witness.Incarnation.Error == "" && witness.Incarnation.Generation != 0 &&
		witness.ObservationVersion != 0 && witness.LocalDiscriminator != 0 && witness.Type == bfd.SessionTypeSingleHop &&
		witness.Incarnation.Endpoint == endpoint && witness.LocalAddr == endpoint.Local.Addr() &&
		witness.PeerAddr == endpoint.Peer.Addr() && witness.Interface == endpoint.Interface
}

func historicalDownGeneration(
	initial OwnerSessionObservation, churn SessionChurnObservation, witness OwnerDownWitness,
) bool {
	identity := witness.Incarnation
	found := identity.Generation == 1 && initial.LocalDiscriminator == witness.LocalDiscriminator
	previousGeneration, previousDiscriminator := uint64(1), initial.LocalDiscriminator
	seen := map[uint32]struct{}{initial.LocalDiscriminator: {}}
	for _, event := range churn.Events {
		if event.Slot != identity.Slot || event.SkippedNotUp {
			continue
		}
		if !coherentChurnGeneration(event, initial.Endpoint, previousGeneration, previousDiscriminator) {
			return false
		}
		if _, reused := seen[event.NewDiscriminator]; reused {
			return false
		}
		seen[event.NewDiscriminator] = struct{}{}
		previousGeneration, previousDiscriminator = event.NewGeneration, event.NewDiscriminator
		if event.NewGeneration == identity.Generation && event.NewDiscriminator == witness.LocalDiscriminator {
			found = true
		}
	}
	return found && churn.EventsOmitted == 0 && (!churn.Enabled || (churn.Joined && churn.Completed))
}

func coherentChurnGeneration(
	event SessionChurnEvent, endpoint OwnerEndpoint, generation uint64, discriminator uint32,
) bool {
	return event.Endpoint == endpoint && event.Error == "" && event.OldGeneration == generation &&
		event.OldDiscriminator == discriminator && event.OldGeneration != math.MaxUint64 &&
		event.NewGeneration == event.OldGeneration+1 && event.NewDiscriminator != 0
}

func addNS(left, right int64) (int64, bool) {
	if (right > 0 && left > math.MaxInt64-right) || (right < 0 && left < math.MinInt64-right) {
		return 0, false
	}
	return left + right, true
}

func addCount(left, right uint64) (uint64, bool) {
	if right > math.MaxUint64-left {
		return 0, false
	}
	return left + right, true
}

func historyError() error { return fmt.Errorf("invalid independent omission history: %w", ErrProfile) }
