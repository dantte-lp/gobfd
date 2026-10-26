//go:build linux

package bfdload

import (
	"net/netip"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// OwnerDownTiming projects independently available owner timestamps into the phase.
// Known flags distinguish real phase-zero offsets from unavailable measurements.
type OwnerDownTiming struct {
	CommitKnown    bool   `json:"commit_known"`
	CommitStartNS  int64  `json:"commit_start_ns"`
	CommitEndNS    int64  `json:"commit_end_ns"`
	ValidatedKnown bool   `json:"validated_known"`
	ValidatedNS    int64  `json:"validated_ns"`
	DetectionKnown bool   `json:"detection_known"`
	DetectionDueNS int64  `json:"detection_due_ns"`
	DetectionNS    int64  `json:"detection_ns"`
	Error          string `json:"error,omitempty"`
}

// OwnerDownWitness retains all delivered Up-to-Down source witnesses, including setup/drain.
// Incarnation correlation and causal classification remain separate requirements.
type OwnerDownWitness struct {
	AtUTC               time.Time       `json:"at_utc"`
	ElapsedNS           int64           `json:"elapsed_ns"`
	LocalDiscriminator  uint32          `json:"local_discriminator"`
	RemoteDiscriminator uint32          `json:"remote_discriminator"`
	LocalAddr           netip.Addr      `json:"local_addr"`
	PeerAddr            netip.Addr      `json:"peer_addr"`
	Interface           string          `json:"interface"`
	Type                bfd.SessionType `json:"type"`
	ObservationVersion  uint64          `json:"observation_version"`
	Cause               bfd.Cause       `json:"cause"`
	LocalDiag           bfd.Diag        `json:"local_diag"`
	RemoteKnown         bool            `json:"remote_known"`
	RemoteState         bfd.State       `json:"remote_state"`
	RemoteDiag          bfd.Diag        `json:"remote_diag"`
	Timing              OwnerDownTiming `json:"timing"`
}

func captureDownTransition(change bfd.StateChange) downTransition {
	timing := change.TransitionTiming
	if change.Status.ObservationVersion != change.ObservationVersion {
		timing = bfd.TransitionTiming{}
	}
	return downTransition{
		at: change.Timestamp, discr: change.LocalDiscr, diag: change.Diag, timing: timing,
		witness: OwnerDownWitness{
			AtUTC: change.Timestamp.UTC(), LocalDiscriminator: change.LocalDiscr,
			RemoteDiscriminator: change.Status.RemoteDiscr,
			LocalAddr:           change.Status.LocalAddr, PeerAddr: change.Status.PeerAddr,
			Interface: change.Interface, Type: change.Type, ObservationVersion: change.ObservationVersion,
			Cause: change.Cause, LocalDiag: change.Diag,
			RemoteKnown: change.RemoteKnown, RemoteState: change.RemoteState, RemoteDiag: change.RemoteDiag,
		},
	}
}

func projectDownTiming(timing bfd.TransitionTiming, version uint64, started time.Time) OwnerDownTiming {
	result := OwnerDownTiming{DetectionNS: int64(timing.DetectionTime)}
	if timing.Version == 0 || timing.Version != version {
		result.Error = "missing or mismatched owner commit witness"
		return result
	}
	for _, stamp := range [...]time.Time{started, timing.CommitStartedAt, timing.CommitEndedAt} {
		if stamp != stamp.Round(0) {
			continue
		}
		result.Error = "missing owner-monotonic phase or commit timestamps"
		return result
	}
	if timing.CommitEndedAt.Before(timing.CommitStartedAt) {
		result.Error = "owner commit interval is reversed"
		return result
	}
	result.CommitKnown = true
	result.CommitStartNS = int64(timing.CommitStartedAt.Sub(started))
	result.CommitEndNS = int64(timing.CommitEndedAt.Sub(started))
	if timing.ValidatedAt != timing.ValidatedAt.Round(0) && !timing.ValidatedAt.After(timing.CommitStartedAt) {
		result.ValidatedKnown, result.ValidatedNS = true, int64(timing.ValidatedAt.Sub(started))
	}
	if timing.DetectionDue != timing.DetectionDue.Round(0) && timing.DetectionTime > 0 {
		result.DetectionKnown, result.DetectionDueNS = true, int64(timing.DetectionDue.Sub(started))
	}
	if !result.ValidatedKnown || !result.DetectionKnown {
		result.Error = "last validated receive or actual detection deadline is unavailable"
	}
	return result
}
