//go:build linux

package bfdload

import (
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type downWitnessJSON struct {
	LocalDiscriminator uint32    `json:"local_discriminator"`
	ObservationVersion uint64    `json:"observation_version"`
	Cause              bfd.Cause `json:"cause"`
	RemoteKnown        bool      `json:"remote_known"`
	Incarnation        struct {
		Known      bool          `json:"known"`
		Slot       int           `json:"slot"`
		Generation uint64        `json:"generation"`
		Endpoint   OwnerEndpoint `json:"endpoint"`
		Error      string        `json:"error,omitempty"`
	} `json:"incarnation"`
	Timing struct {
		CommitKnown                  bool   `json:"commit_known"`
		CommitStartNS                int64  `json:"commit_start_ns"`
		CommitEndNS                  int64  `json:"commit_end_ns"`
		ValidatedKnown               bool   `json:"validated_known"`
		ValidatedNS                  int64  `json:"validated_ns"`
		DetectionKnown               bool   `json:"detection_known"`
		DetectionDueNS               int64  `json:"detection_due_ns"`
		DetectionNS                  int64  `json:"detection_ns"`
		ValidatedRemoteDiscriminator uint32 `json:"validated_remote_discriminator"`
		Error                        string `json:"error,omitempty"`
	} `json:"timing"`
}

func sourceDownWitness(started time.Time) bfd.StateChange {
	status := bfd.SessionSnapshot{
		LocalDiscr: 42, RemoteDiscr: 7, Type: bfd.SessionTypeSingleHop,
		LocalAddr: netip.MustParseAddr("192.0.2.1"), PeerAddr: netip.MustParseAddr("192.0.2.2"),
		State: bfd.StateDown, LocalDiag: bfd.DiagControlTimeExpired,
		RemoteKnown: true, RemoteState: bfd.StateUp, Cause: bfd.CauseDetectionTimeout, ObservationVersion: 7,
	}
	return bfd.StateChange{
		Status: status, LocalDiscr: 42, OldState: bfd.StateUp, NewState: bfd.StateDown,
		Diag: bfd.DiagControlTimeExpired, Cause: bfd.CauseDetectionTimeout, ObservationVersion: 7,
		Timestamp: started.Add(2 * time.Second),
		TransitionTiming: bfd.TransitionTiming{
			Version: 7, ValidatedAt: started.Add(-100 * time.Millisecond),
			CommitStartedAt: started.Add(201 * time.Millisecond), CommitEndedAt: started.Add(203 * time.Millisecond),
			DetectionDue: started.Add(200 * time.Millisecond), DetectionTime: 300 * time.Millisecond,
			ValidatedRemoteDiscriminator: 7,
		},
	}
}

func projectedDownWitness(
	t *testing.T, started time.Time, change bfd.StateChange,
) (OwnerWorkloadObservation, downWitnessJSON) {
	t.Helper()
	changes := make(chan bfd.StateChange, 1)
	changes <- change
	close(changes)
	report := OwnerWorkloadObservation{Config: PeerConfig{Profile: Profile{Duration: time.Second}}}
	if err := report.joinDownJournal(t.Context(), started, startDownJournal(t.Context(), changes, nil)); err != nil {
		t.Fatal(err)
	}
	//nolint:musttag // Workload JSON intentionally retains core Observation field names.
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Witnesses []downWitnessJSON `json:"down_witnesses"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Witnesses) != 1 {
		t.Fatalf("source witness discarded by notification cutoff: count=%d", len(wire.Witnesses))
	}
	return report, wire.Witnesses[0]
}

func TestDownWitnessProjectionRetainsCommitBeforeDelayedNotification(t *testing.T) {
	t.Parallel()
	started := time.Now()
	report, witness := projectedDownWitness(t, started, sourceDownWitness(started))
	if len(report.DownEvents) != 0 || report.DownJournal.ObservedAfterCutoff != 1 {
		t.Fatal("legacy notification-window accounting changed")
	}
	if witness.LocalDiscriminator != 42 || witness.ObservationVersion != 7 || witness.Cause != bfd.CauseDetectionTimeout ||
		!witness.Timing.CommitKnown || witness.Timing.CommitStartNS != 201000000 || witness.Timing.CommitEndNS != 203000000 ||
		!witness.Timing.ValidatedKnown || witness.Timing.ValidatedNS != -100000000 ||
		!witness.Timing.DetectionKnown || witness.Timing.DetectionDueNS != 200000000 ||
		witness.Timing.DetectionNS != 300000000 ||
		witness.Timing.ValidatedRemoteDiscriminator != 7 ||
		witness.Timing.Error != "" {
		t.Fatalf("frozen source timing was lost or converted through wall time: %+v", witness)
	}
}

func TestEchoDownWitnessProjectionDoesNotInventRemoteControlEvidence(t *testing.T) {
	t.Parallel()
	started := time.Now()
	change := sourceDownWitness(started)
	change.Type, change.Status.Type = bfd.SessionTypeEcho, bfd.SessionTypeEcho
	change.Diag, change.Status.LocalDiag = bfd.DiagEchoFailed, bfd.DiagEchoFailed
	change.RemoteKnown, change.Status.RemoteKnown = false, false
	change.Status.RemoteDiscr, change.TransitionTiming.ValidatedRemoteDiscriminator = 0, 0
	_, witness := projectedDownWitness(t, started, change)
	if !witness.Timing.CommitKnown || !witness.Timing.ValidatedKnown || !witness.Timing.DetectionKnown ||
		witness.Timing.ValidatedRemoteDiscriminator != 0 || witness.RemoteKnown || witness.Timing.Error != "" {
		t.Fatalf("actual Echo witness needs no remote Control discriminator: %+v", witness)
	}
}

func TestDownWitnessProjectionKeepsMissingAndStrippedTimesUnknown(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{
		"version", "phase", "commit", "ordering", "receive", "deadline", "remote_discriminator",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := time.Now()
			change := sourceDownWitness(started)
			wantCommit, wantReceive, wantDetection := false, false, false
			switch mode {
			case "version":
				change.TransitionTiming.Version++
			case "phase":
				started = started.Round(0)
			case "commit":
				change.TransitionTiming.CommitStartedAt = change.TransitionTiming.CommitStartedAt.UTC()
			case "ordering":
				change.TransitionTiming.CommitEndedAt = change.TransitionTiming.CommitStartedAt.Add(-time.Nanosecond)
			case "receive":
				change.TransitionTiming.ValidatedAt = time.Time{}
				wantCommit, wantDetection = true, true
			case "deadline":
				change.TransitionTiming.DetectionDue = time.Time{}
				wantCommit, wantReceive = true, true
			case "remote_discriminator":
				change.TransitionTiming.ValidatedRemoteDiscriminator = 0
				wantCommit, wantReceive, wantDetection = true, true, true
			}
			_, witness := projectedDownWitness(t, started, change)
			if witness.Timing.CommitKnown != wantCommit || witness.Timing.ValidatedKnown != wantReceive ||
				witness.Timing.DetectionKnown != wantDetection || witness.Timing.Error == "" {
				t.Fatalf("unavailable source clock data claimed known: mode=%s witness=%+v", mode, witness)
			}
		})
	}
}
