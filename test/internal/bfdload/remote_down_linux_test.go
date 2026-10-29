//go:build linux

package bfdload

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func remoteDownFixture(t *testing.T) (OwnerWorkloadObservation, PeerObservation) {
	t.Helper()
	owner, peers := classificationFixture(t)
	w := &owner.DownWitnesses[0]
	w.Cause, w.LocalDiag = bfd.CauseRemoteDown, bfd.DiagNeighborDown
	w.RemoteDiscriminator, w.RemoteState, w.RemoteDiag = 1, bfd.StateDown, bfd.DiagControlTimeExpired
	w.Timing = OwnerDownTiming{
		CommitKnown: true, CommitStartNS: 770000000, CommitEndNS: 770000001,
		ValidatedKnown: true, ValidatedNS: 760000000,
		Error: "last validated receive, remote discriminator or actual detection deadline is unavailable",
	}
	p := &peers.Peers[0]
	p.Scheduled, p.WriteAttempts, p.Written = 14, 11, 11
	p.Read, p.Accepted, p.ReceiveBlackholeOmitted = 13, 10, 3
	p.DetectionExpirations, p.DownTransitions = 1, 1
	p.DetectionExpiryEvents = []PeerDetectionExpiry{{
		ID: 1, PreviousState: bfd.StateUp, LocalDiscriminator: 1, RemoteDiscriminator: 42,
		TimingKnown: true, PhaseKnown: true, LastReceiveNS: 350000000,
		DetectionDueNS: 650000000, ExpiredNS: 650000010,
		FirstDownWrite: PeerExpiryWrite{
			Known: true, TimingKnown: true, Scheduled: 14, DecisionNS: 750000010,
			StartedNS: 750000020, EndedNS: 750000030, MyDiscriminator: 1,
			State: bfd.StateDown, Diag: bfd.DiagControlTimeExpired,
			DesiredMinTXUS: 1000000, RequiredMinRXUS: 100000, DetectMult: 3, Poll: true,
		},
	}}
	window := &p.BlackholeWindows[0]
	window.RXOmitted, window.FirstRXNS, window.LastRXNS = 3, 420000000, 580000000
	window.Segments = append(window.Segments, PeerOmissionSegment{
		Direction: "rx", ReceiveQualified: true, FirstNS: 420000000, LastNS: 580000000, Count: 3,
		MyDiscriminator: 42, YourDiscriminator: 1, State: bfd.StateUp,
		DesiredMinTXUS: 100000, RequiredMinRXUS: 100000, DetectMult: 3,
		OffsetsNS: []int64{420000000, 500000000, 580000000}, Sequences: []uint64{11, 12, 13},
	})
	return owner, peers
}

func TestRemoteDownFirstExpiryExcludesOnlyStrictlyLaterEpochs(t *testing.T) {
	t.Parallel()
	owner, peers := remoteDownFixture(t)
	p := &peers.Peers[0]
	later := p.DetectionExpiryEvents[0]
	later.ID, later.LastReceiveNS = 2, 800000000
	later.DetectionDueNS, later.ExpiredNS = 1100000000, 1100000000
	p.DetectionExpirations++
	p.DetectionExpiryEvents = append(p.DetectionExpiryEvents, later)
	if got := ClassifyDowns(owner, peers); !got.Passing || got.Intentional != 1 {
		t.Fatalf("a strictly later expiry invalidated the unique earlier origin: %+v", got)
	}
	p.DetectionExpiryEvents[1].LastReceiveNS = 650000020
	p.DetectionExpiryEvents[1].DetectionDueNS = 760000002
	p.DetectionExpiryEvents[1].ExpiredNS = 760000002
	if got := ClassifyDowns(owner, peers); got.Passing || got.Unresolved != 1 {
		t.Fatal("intersecting expiry and validated-receive brackets were treated as ordered")
	}
}

func TestRemoteDownSelectiveRXExtendsRealPeerOldDeadline(t *testing.T) {
	t.Parallel()
	for _, count := range []int{1, 2, 3} {
		peer := fixturePeerState(t)
		started := time.Now()
		if _, err := peer.receive(fixturePeerPacket(bfd.StateInit), started); err != nil {
			t.Fatal(err)
		}
		oldDue := started.Add(300 * time.Millisecond)
		unchanged := peer
		if !unchanged.expire(oldDue) {
			t.Fatal("actual old peer deadline did not expire")
		}
		for index := range count {
			received := started.Add(time.Duration(70+index*80) * time.Millisecond)
			if _, err := peer.receive(fixturePeerPacket(bfd.StateUp), received); err != nil {
				t.Fatal(err)
			}
		}
		if peer.expire(oldDue) || peer.state != bfd.StateUp {
			t.Fatal("selected actual Up acceptance did not extend the old peer deadline")
		}
	}
}

func TestRemoteDownFirstExpiryUsesQualifiedPeerDeadlineAncestry(t *testing.T) {
	t.Parallel()
	owner, peers := remoteDownFixture(t)
	data, err := json.Marshal(peers)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PeerObservation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	got := ClassifyDowns(owner, decoded)
	if !got.Complete || !got.Passing || got.Intentional != 1 || got.Unresolved != 0 ||
		got.False != 0 || got.ReferenceQualified || !got.Conditional {
		t.Fatalf("proven first peer old-deadline ancestry was not retained: %+v", got)
	}
	if !reflect.DeepEqual(got, ClassifyDowns(owner, decoded)) {
		t.Fatal("classification mutated source evidence or changed on repeated assessment")
	}
}

func TestRemoteDownRejectsAmbiguousOrUnsupportedPeerProof(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"missing_journal", "omitted", "count", "later_id", "init", "old_identity", "peer_identity",
		"phase", "timing", "write_missing", "write_timing", "write_body", "write_poll", "write_final",
		"write_sequence", "write_order", "write_receive_boundary", "write_overflow", "owner_receive",
		"owner_diag", "remote_diag", "unqualified_rx", "rx_identity", "rx_poll", "rx_boundary",
		"early_expiry", "expiry_window_boundary", "second_before_receive", "second_unknown", "reused_id",
		"known_loss", "missing_progress", "admin_down", "unsupported_tx_flag", "tx_only",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			owner, peers := remoteDownFixture(t)
			p, w := &peers.Peers[0], &owner.DownWitnesses[0]
			e := &p.DetectionExpiryEvents[0]
			rx := &p.BlackholeWindows[0].Segments[1]
			switch name {
			case "missing_journal":
				p.DetectionExpiryEvents = nil
			case "omitted":
				p.DetectionExpiryEventsOmitted = 1
			case "count":
				p.DetectionExpirations++
			case "later_id":
				e.ID = 2
			case "init":
				e.PreviousState = bfd.StateInit
			case "old_identity":
				e.RemoteDiscriminator++
			case "peer_identity":
				w.RemoteDiscriminator++
			case "phase":
				e.PhaseKnown = false
			case "timing":
				e.TimingKnown = false
			case "write_missing":
				e.FirstDownWrite.Known = false
			case "write_timing":
				e.FirstDownWrite.TimingKnown = false
			case "write_body":
				e.FirstDownWrite.YourDiscriminator = 42
			case "write_poll":
				e.FirstDownWrite.Poll = false
			case "write_final":
				e.FirstDownWrite.Final = true
			case "write_sequence":
				e.FirstDownWrite.Scheduled = 0
			case "write_order":
				e.FirstDownWrite.DecisionNS = e.ExpiredNS - 1
			case "write_receive_boundary":
				e.FirstDownWrite.StartedNS = w.Timing.ValidatedNS - 2
				e.FirstDownWrite.EndedNS = w.Timing.ValidatedNS
			case "write_overflow":
				e.FirstDownWrite.StartedNS = math.MaxInt64
				e.FirstDownWrite.EndedNS = math.MaxInt64
			case "owner_receive":
				w.Timing.ValidatedKnown = false
			case "owner_diag":
				w.LocalDiag = bfd.DiagPathDown
			case "remote_diag":
				w.RemoteDiag = bfd.DiagNeighborDown
			case "unqualified_rx":
				rx.ReceiveQualified = false
			case "rx_identity":
				rx.MyDiscriminator++
			case "rx_poll":
				rx.Poll = true
			case "rx_boundary":
				e.LastReceiveNS, e.DetectionDueNS, e.ExpiredNS = 420000000, 720000000, 720000010
				rx.OffsetsNS[0], rx.FirstNS = 420000002, 420000002
				p.BlackholeWindows[0].FirstRXNS = 420000002
			case "early_expiry":
				e.ExpiredNS = e.DetectionDueNS - 1
			case "expiry_window_boundary":
				e.ExpiredNS = 749999999
			case "second_before_receive", "second_unknown", "reused_id":
				second := *e
				second.ID, second.LastReceiveNS = 2, 650000020
				second.DetectionDueNS, second.ExpiredNS = 750000000, 750000000
				if name == "second_unknown" {
					second.TimingKnown = false
				}
				if name == "reused_id" {
					second.ID = 1
				}
				p.DetectionExpiryEvents = append(p.DetectionExpiryEvents, second)
				p.DetectionExpirations++
			case "known_loss":
				p.KernelDropCounterRaw = 1
			case "missing_progress":
				owner.MeasurementFinal.Delivery = classificationDelivery(10)
			case "admin_down":
				w.Cause = bfd.CauseRemoteAdminDown
			case "unsupported_tx_flag":
				p.BlackholeWindows[0].Segments[0].ReceiveQualified = true
			case "tx_only":
				owner.Config.Profile.TXOnlyBlackhole, peers.Profile.TXOnlyBlackhole = true, true
			}
			got := ClassifyDowns(owner, peers)
			if got.Passing || got.Intentional != 0 || got.Unresolved != 1 {
				t.Fatalf("unsupported first-expiry proof passed: %+v", got)
			}
		})
	}
}

func TestControlTXOnlyProfileRejectsContradictoryRXHistory(t *testing.T) {
	t.Parallel()
	owner, _ := classificationFixture(t)
	_, peers := remoteDownFixture(t)
	owner.Config.Profile.TXOnlyBlackhole, peers.Profile.TXOnlyBlackhole = true, true
	if got := ClassifyDowns(owner, peers); got.Complete || got.Passing || got.Unresolved != 1 {
		t.Fatalf("local timeout accepted RX evidence disabled by its profile: %+v", got)
	}
}

func TestRemoteDownRejectsWriteOrdinalBeforeEarlierTXDecisions(t *testing.T) {
	t.Parallel()
	for _, scheduled := range []uint64{11, 12, 13} {
		owner, peers := remoteDownFixture(t)
		peers.Peers[0].DetectionExpiryEvents[0].FirstDownWrite.Scheduled = scheduled
		if got := ClassifyDowns(owner, peers); got.Passing || got.Intentional != 0 || got.Unresolved != 1 {
			t.Fatalf("write ordinal %d contradicts earlier TX omission prefix: %+v", scheduled, got)
		}
	}
}

func TestRemoteDownWriteOrdinalCanEqualLaterPhaseOmission(t *testing.T) {
	t.Parallel()
	owner, peers := remoteDownFixture(t)
	p := &peers.Peers[0]
	// A phase decision and a lifetime write use different origins when setup sent packets.
	p.BlackholeWindows[0].Segments[0].Sequences = []uint64{8, 9, 10}
	p.Scheduled, p.WriteAttempts, p.Written, p.BlackholeOmitted = 18, 12, 12, 6
	p.BlackholeWindows = append(p.BlackholeWindows, PeerBlackholeWindow{
		StartNS: 800000000, FirstTXNS: 820000000, LastTXNS: 980000000, TXOmitted: 3,
		Segments: []PeerOmissionSegment{{
			Direction: "tx", FirstNS: 820000000, LastNS: 980000000, Count: 3, Valid: true,
			MyDiscriminator: 1, YourDiscriminator: 42, State: bfd.StateUp,
			DesiredMinTXUS: 100000, RequiredMinRXUS: 100000, DetectMult: 3,
			OffsetsNS: []int64{820000000, 900000000, 980000000}, Sequences: []uint64{14, 15, 16},
		}},
	})
	if got := ClassifyDowns(owner, peers); !got.Passing || got.Intentional != 1 {
		t.Fatalf("a later phase ordinal was equated to an earlier lifetime write: %+v", got)
	}
}
