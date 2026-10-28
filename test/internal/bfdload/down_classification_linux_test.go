//go:build linux

package bfdload

import (
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func classificationFixture(t *testing.T) (OwnerWorkloadObservation, PeerObservation) {
	t.Helper()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 1, time.Second
	profile.BlackholeEvery, profile.BlackholeFor = 400*time.Millisecond, 350*time.Millisecond
	endpoint := OwnerEndpoint{
		Local: netip.MustParseAddrPort("192.0.2.1:3784"), Peer: netip.MustParseAddrPort("192.0.2.2:3784"),
	}
	phase := &Phase{
		NativeStartNS: 1000, TimeNamespaceID: 7, BracketNS: 2, BracketKnown: true,
		Run: RunIdentity{BootID: "01234567-89ab-cdef-0123-456789abcdef", Device: 1, Inode: 2},
	}
	count := uint64(1)
	owner := OwnerWorkloadObservation{
		Completed: true, Phase: phase,
		Config: PeerConfig{Profile: profile, Sessions: []OwnerEndpoint{endpoint}, SourcePortBase: 55100},
		Initial: OwnerObservation{
			Scope:    "production-core-owner-component",
			Sessions: []OwnerSessionObservation{{Endpoint: endpoint, Generation: 1, LocalDiscriminator: 42}},
			Delivery: classificationDelivery(10),
		},
		MeasurementFinal: OwnerObservation{
			Scope:    "production-core-owner-component",
			Delivery: classificationDelivery(20),
		},
		Final: OwnerObservation{Stopped: true},
		DownJournal: DownJournalObservation{
			SourceTransitions: 1, FinalSourceTransitions: &count, ObservedBeforeCutoff: 1,
			Closed: true, Settled: true, Complete: true, IncarnationsComplete: true,
		},
		DownWitnesses: []OwnerDownWitness{{
			LocalDiscriminator: 42, LocalAddr: endpoint.Local.Addr(), PeerAddr: endpoint.Peer.Addr(),
			Type: bfd.SessionTypeSingleHop, ObservationVersion: 7,
			Cause: bfd.CauseDetectionTimeout, LocalDiag: bfd.DiagControlTimeExpired,
			RemoteKnown: true, RemoteState: bfd.StateUp,
			Incarnation: OwnerDownIncarnation{Known: true, Slot: 0, Generation: 1, Endpoint: endpoint},
			Timing: OwnerDownTiming{
				CommitKnown: true, CommitStartNS: 660000000, CommitEndNS: 660000001,
				ValidatedKnown: true, ValidatedNS: 350000000, DetectionKnown: true,
				DetectionDueNS: 650000000, DetectionNS: 300000000,
				ValidatedRemoteDiscriminator: 1,
			},
		}},
	}
	peers := PeerObservation{
		Completed: true, Stopped: true, Profile: profile, Phase: phase,
		Peers: []PeerSessionObservation{{
			ID: 1, Endpoint: endpoint, SourcePort: 55100, BlackholeOmitted: 3,
			Scheduled: 13, WriteAttempts: 10, Written: 10, Read: 10, Accepted: 10,
			BlackholeWindows: []PeerBlackholeWindow{{
				StartNS: 400000000, FirstTXNS: 420000000, LastTXNS: 580000000, TXOmitted: 3,
				Segments: []PeerOmissionSegment{{
					Direction: "tx", FirstNS: 420000000, LastNS: 580000000, Count: 3, Valid: true,
					MyDiscriminator: 1, YourDiscriminator: 42, State: bfd.StateUp,
					DesiredMinTXUS: 100000, RequiredMinRXUS: 100000, DetectMult: 3,
					OffsetsNS: []int64{420000000, 500000000, 580000000}, Sequences: []uint64{11, 12, 13},
				}},
			}},
		}},
	}
	return owner, peers
}

func classificationDelivery(committed uint64) []bfd.DeliveryCounter {
	rows := bfd.DeliveryCounterPairs()
	for index := range rows {
		if rows[index].Source == bfd.DeliveryPacketOwner && rows[index].Outcome == bfd.DeliveryCommitted {
			rows[index].Count = committed
		}
	}
	return rows
}

func TestDownClassificationRejectsImpossiblePeerLedger(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"scheduled_zero", "tx_total_mismatch", "write_total_mismatch", "rx_total_mismatch",
		"tx_sum_overflow", "write_sum_overflow", "rx_sum_overflow",
		"tx_sequence_out_of_range", "rx_sequence_out_of_range",
		"tx_cross_window_reset", "rx_cross_window_reset", "tx_cross_segment_reset",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			owner, peers := classificationFixture(t)
			editPeerLedgerFixture(name, &peers.Peers[0])
			report := ClassifyDowns(owner, peers)
			if report.Complete || report.Passing || report.Intentional != 0 || report.Unresolved != 1 || report.Error == "" {
				t.Fatalf("impossible %s peer ledger accepted: %+v", name, report)
			}
		})
	}
}

func editPeerLedgerFixture(name string, peer *PeerSessionObservation) {
	switch name {
	case "scheduled_zero":
		peer.Scheduled = 0
	case "tx_total_mismatch":
		peer.Scheduled++
	case "write_total_mismatch":
		peer.Written--
	case "rx_total_mismatch":
		peer.Read++
	case "tx_sum_overflow":
		peer.RandomOmitted = math.MaxUint64
	case "write_sum_overflow":
		peer.Written, peer.WriteErrors = math.MaxUint64, 1
	case "rx_sum_overflow":
		peer.Accepted, peer.Rejected = math.MaxUint64, 1
	case "tx_sequence_out_of_range":
		peer.BlackholeWindows[0].Segments[0].Sequences = []uint64{12, 13, 14}
	case "rx_sequence_out_of_range":
		addPeerLedgerRX(peer)
		peer.BlackholeWindows[0].Segments[1].Sequences[0] = 11
	case "tx_cross_window_reset", "rx_cross_window_reset":
		addPeerLedgerWindow(peer, name == "rx_cross_window_reset")
		peer.BlackholeWindows[1].Segments[0].Sequences[0] = 1
	case "tx_cross_segment_reset":
		window := &peer.BlackholeWindows[0]
		segment := window.Segments[0]
		window.Segments[0].OffsetsNS, window.Segments[0].Sequences = []int64{420000000}, []uint64{11}
		window.Segments[0].Count, window.Segments[0].LastNS = 1, 420000000
		segment.OffsetsNS, segment.Sequences = []int64{500000000, 580000000}, []uint64{10, 11}
		segment.Count, segment.FirstNS = 2, 500000000
		window.Segments = append(window.Segments, segment)
	}
}

func addPeerLedgerRX(peer *PeerSessionObservation) {
	peer.Accepted--
	peer.ReceiveBlackholeOmitted++
	window := &peer.BlackholeWindows[0]
	window.RXOmitted, window.FirstRXNS, window.LastRXNS = 1, 450000000, 450000000
	window.Segments = append(window.Segments, PeerOmissionSegment{
		Direction: "rx", FirstNS: 450000000, LastNS: 450000000, Count: 1,
		OffsetsNS: []int64{450000000}, Sequences: []uint64{8},
	})
}

func addPeerLedgerWindow(peer *PeerSessionObservation, receive bool) {
	segment := PeerOmissionSegment{
		Direction: "tx", FirstNS: 850000000, LastNS: 850000000, Count: 1,
		OffsetsNS: []int64{850000000}, Sequences: []uint64{20},
	}
	window := PeerBlackholeWindow{
		StartNS: 800000000, FirstTXNS: 850000000, LastTXNS: 850000000, TXOmitted: 1,
	}
	if receive {
		addPeerLedgerRX(peer)
		segment.Direction, segment.Sequences[0] = "rx", 9
		window.FirstTXNS, window.LastTXNS, window.TXOmitted = 0, 0, 0
		window.FirstRXNS, window.LastRXNS, window.RXOmitted = 850000000, 850000000, 1
		peer.Accepted--
		peer.ReceiveBlackholeOmitted++
	} else {
		peer.Scheduled, peer.BlackholeOmitted = 20, 4
		peer.WriteAttempts, peer.Written = 16, 16
	}
	window.Segments = []PeerOmissionSegment{segment}
	peer.BlackholeWindows = append(peer.BlackholeWindows, window)
}

func TestDownClassificationAcceptsFeasibleLedgerAcrossWindows(t *testing.T) {
	t.Parallel()
	for _, receive := range []bool{false, true} {
		owner, peers := classificationFixture(t)
		addPeerLedgerWindow(&peers.Peers[0], receive)
		// Reads left unprocessed at joined shutdown remain part of the existing ledger.
		peers.Peers[0].Accepted--
		peers.Peers[0].ReceiveNotProcessed++
		if report := ClassifyDowns(owner, peers); !report.Complete || !report.Passing || report.Intentional != 1 {
			t.Fatalf("feasible directional ledger and inter-window gap rejected: receive=%t report=%+v", receive, report)
		}
	}
}

func TestDownClassificationNamesConditionalModelAndUnknownReferenceValidity(t *testing.T) {
	t.Parallel()
	owner, peers := classificationFixture(t)
	report := ClassifyDowns(owner, peers)
	if !report.Complete || report.Eligible != 1 || report.Unresolved != 0 || report.False != 0 ||
		report.Intentional != 1 || !report.Passing || report.ReferenceQualified || len(report.Rows) != 1 ||
		!report.Conditional || len(report.Assumptions) == 0 || report.TimingUncertaintyNS != nil {
		t.Fatalf("conditional classification lost its assumptions or fabricated reference validity: %+v", report)
	}
}

func TestDownClassificationRejectsUnprovenOrFalseDowns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		want string
	}{
		{"early_down", "false"},
		{"no_omissions", "false"},
		{"random_loss_only", "false"},
		{"wrong_tuple", "unresolved"},
		{"wrong_incarnation", "unresolved"},
		{"wrong_packet", "unresolved"},
		{"reused_discriminator", "unresolved"},
		{"peer_down", "unresolved"},
		{"missing_timing", "unresolved"},
		{"cutoff_overlap", "unresolved"},
		{"blackhole_edge", "unresolved"},
		{"unknown_phase", "unresolved"},
		{"unequal_phase", "unresolved"},
		{"unequal_run", "unresolved"},
		{"unequal_boot", "unresolved"},
		{"negative_bracket", "unresolved"},
		{"unknown_identity", "unresolved"},
		{"gap", "unresolved"},
		{"overflow", "unresolved"},
		{"counter_mismatch", "unresolved"},
		{"history_mismatch", "unresolved"},
		{"arithmetic_overflow", "unresolved"},
		{"before_actual_due", "false"},
		{"single_omission", "unresolved"},
		{"sequence_gap", "unresolved"},
		{"attempt_gap", "unresolved"},
		{"fractional_interval_overrun", "unresolved"},
		{"random_loss_lead_in", "unresolved"},
		{"missing_remote_discriminator", "unresolved"},
		{"unknown_bracket", "unresolved"},
		{"unequal_namespace", "unresolved"},
		{"known_other_drop", "unresolved"},
		{"missing_progress", "unresolved"},
		{"reset_ledger", "unresolved"},
		{"offsetting_drop_reset", "unresolved"},
		{"wrong_directional_dt", "unresolved"},
		{"wrong_peer_discriminator", "unresolved"},
		{"peer_expired_before_due", "unresolved"},
		{"blackhole_commit_edge", "unresolved"},
		{"unknown_boot", "unresolved"},
		{"unknown_native_start", "unresolved"},
		{"large_bracket", "unresolved"},
		{"count_overflow", "unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			owner, peers := classificationFixture(t)
			editClassificationFixture(tc.name, &owner, &peers)
			report := ClassifyDowns(owner, peers)
			if report.Passing || report.ReferenceQualified || report.Eligible != 1 || len(report.Rows) != 1 ||
				report.Rows[0].Outcome != tc.want || report.Intentional != 0 || report.False+report.Unresolved != 1 {
				t.Fatalf("%s: causal classification=%+v, want %s", tc.name, report, tc.want)
			}
		})
	}
}

func editClassificationFixture(name string, owner *OwnerWorkloadObservation, peers *PeerObservation) {
	witness := &owner.DownWitnesses[0]
	phase := *peers.Phase
	peers.Phase = &phase
	switch name {
	case "early_down":
		witness.Timing.DetectionDueNS = 390000000
		witness.Timing.CommitStartNS, witness.Timing.CommitEndNS = 395000000, 395000001
	case "no_omissions", "random_loss_only":
		peers.Peers[0].BlackholeWindows, peers.Peers[0].BlackholeOmitted = nil, 0
		peers.Peers[0].WriteAttempts, peers.Peers[0].Written = 13, 13
		if name == "random_loss_only" {
			peers.Peers[0].RandomOmitted = 3
			peers.Peers[0].WriteAttempts, peers.Peers[0].Written = 10, 10
		}
	case "wrong_tuple":
		peers.Peers[0].Endpoint.Peer = netip.MustParseAddrPort("192.0.2.3:3784")
	case "wrong_incarnation":
		witness.Incarnation.Generation = 2
	case "wrong_packet":
		peers.Peers[0].BlackholeWindows[0].Segments[0].YourDiscriminator = 43
	case "reused_discriminator":
		witness.Incarnation.Generation = 2
		owner.SessionChurn.Events = []SessionChurnEvent{{
			Slot: 0, Endpoint: owner.Config.Sessions[0], OldGeneration: 1, NewGeneration: 2,
			OldDiscriminator: 42, NewDiscriminator: 42,
		}}
	case "peer_down":
		witness.Cause, witness.LocalDiag = bfd.CauseRemoteDown, bfd.DiagNeighborDown
		witness.Timing.DetectionKnown = false
	case "missing_timing":
		witness.Timing.ValidatedKnown = false
	case "cutoff_overlap":
		witness.Timing.CommitStartNS, witness.Timing.CommitEndNS = 999999999, 1000000000
	case "blackhole_edge":
		witness.Timing.DetectionDueNS = 420000001
	case "unknown_phase":
		owner.Phase = nil
	case "unequal_phase":
		peers.Phase.NativeStartNS++
	case "unequal_run":
		peers.Phase.Run.Inode++
	case "unequal_boot":
		peers.Phase.Run.BootID = "11111111-89ab-cdef-0123-456789abcdef"
	case "negative_bracket":
		peers.Phase.BracketNS = -1
	case "unknown_identity":
		witness.Incarnation.Known = false
	case "gap":
		owner.NotificationGaps = 1
	case "overflow":
		peers.Peers[0].BlackholeEventsOmitted = 1
	case "counter_mismatch":
		owner.DownJournal.SourceTransitions = 2
	case "history_mismatch":
		peers.Peers[0].BlackholeWindows[0].Segments[0].Count++
	case "arithmetic_overflow":
		witness.Timing.CommitStartNS, witness.Timing.CommitEndNS = math.MaxInt64-1, math.MaxInt64
	case "before_actual_due":
		witness.Timing.DetectionDueNS = 670000000
	case "single_omission":
		window := &peers.Peers[0].BlackholeWindows[0]
		window.TXOmitted, window.LastTXNS = 1, 420000000
		window.Segments[0].Count, window.Segments[0].LastNS = 1, 420000000
		window.Segments[0].OffsetsNS, window.Segments[0].Sequences = []int64{420000000}, []uint64{11}
		peers.Peers[0].BlackholeOmitted = 1
		peers.Peers[0].WriteAttempts, peers.Peers[0].Written = 12, 12
	case "sequence_gap":
		peers.Peers[0].BlackholeWindows[0].Segments[0].Sequences = []uint64{10, 12, 13}
	case "attempt_gap":
		peers.Peers[0].BlackholeWindows[0].Segments[0].OffsetsNS[1] = 450000000
	case "fractional_interval_overrun":
		peers.Peers[0].BlackholeWindows[0].Segments[0].OffsetsNS[1] = 520353374
	case "random_loss_lead_in":
		witness.Timing.ValidatedNS = 250000000
	case "missing_remote_discriminator":
		witness.Timing.ValidatedRemoteDiscriminator = 0
	case "unknown_bracket":
		peers.Phase.BracketKnown = false
	case "unequal_namespace":
		peers.Phase.TimeNamespaceID++
	case "known_other_drop":
		for index := range owner.MeasurementFinal.Delivery {
			row := &owner.MeasurementFinal.Delivery[index]
			if row.Source == bfd.DeliveryPacketAdmission && row.Outcome == bfd.DeliveryQueueFull {
				row.Count = 1
			}
		}
	case "missing_progress":
		owner.MeasurementFinal.Delivery = nil
	case "reset_ledger":
		owner.MeasurementFinal.Delivery = classificationDelivery(1)
	case "offsetting_drop_reset":
		for index := range owner.Initial.Delivery {
			row := &owner.Initial.Delivery[index]
			if row.Source == bfd.DeliveryPacketAdmission && row.Outcome == bfd.DeliveryQueueFull {
				row.Count = 1
			}
		}
		for index := range owner.MeasurementFinal.Delivery {
			row := &owner.MeasurementFinal.Delivery[index]
			if row.Source == bfd.DeliveryPacketOwner && row.Outcome == bfd.DeliveryAuthFailed {
				row.Count = 1
			}
		}
	case "wrong_directional_dt":
		peers.Peers[0].BlackholeWindows[0].Segments[0].DesiredMinTXUS = 200000
	case "wrong_peer_discriminator":
		peers.Peers[0].BlackholeWindows[0].Segments[0].MyDiscriminator = 2
	case "peer_expired_before_due":
		peers.Peers[0].BlackholeWindows[0].Segments[0].State = bfd.StateDown
	case "blackhole_commit_edge":
		witness.Timing.CommitStartNS, witness.Timing.CommitEndNS = 749999999, 750000000
	case "unknown_boot":
		peers.Phase.Run.BootID = ""
	case "unknown_native_start":
		peers.Phase.NativeStartNS = 0
	case "large_bracket":
		peers.Phase.BracketNS = int64(time.Millisecond) + 1
	case "count_overflow":
		owner.DownJournal.ObservedBeforeCutoff, owner.DownJournal.ObservedAfterCutoff = math.MaxUint64, 1
	}
}

func TestDownClassificationDistinguishesMeasuredZeroBracketFromMissing(t *testing.T) {
	t.Parallel()
	owner, peers := classificationFixture(t)
	owner.Phase.BracketNS, peers.Phase.BracketNS = 0, 0
	if report := ClassifyDowns(owner, peers); !report.Passing || report.Intentional != 1 {
		t.Fatalf("known phase-zero conversion bound was treated as unavailable: %+v", report)
	}
	peers.Phase.BracketKnown = false
	if report := ClassifyDowns(owner, peers); report.Passing || report.Unresolved != 1 {
		t.Fatalf("missing conversion bound fabricated zero uncertainty: %+v", report)
	}
}

func TestDownClassificationUsesCommitAndAccountsEveryWitnessOnce(t *testing.T) {
	t.Parallel()
	owner, peers := classificationFixture(t)
	owner.DownWitnesses[0].ElapsedNS = int64(2 * time.Second)
	report := ClassifyDowns(owner, peers)
	if report.Eligible != 1 || report.Outside != 0 || report.Intentional != 1 {
		t.Fatalf("delayed notification changed commit eligibility: %+v", report)
	}
	for _, interval := range [][2]int64{{-10, -5}, {1000000000, 1000000001}} {
		owner.DownWitnesses[0].Timing.CommitStartNS, owner.DownWitnesses[0].Timing.CommitEndNS = interval[0], interval[1]
		report = ClassifyDowns(owner, peers)
		if report.Outside != 1 || report.Eligible != 0 || report.Passing {
			t.Fatalf("wholly outside source witness was classified in measurement: %+v", report)
		}
	}
	owner, peers = classificationFixture(t)
	owner.DownWitnesses = append(owner.DownWitnesses, owner.DownWitnesses[0])
	*owner.DownJournal.FinalSourceTransitions = 2
	owner.DownJournal.SourceTransitions, owner.DownJournal.ObservedBeforeCutoff = 2, 2
	report = ClassifyDowns(owner, peers)
	if report.Complete || report.Passing || report.Eligible != 2 || report.Unresolved != 2 || len(report.Rows) != 2 {
		t.Fatalf("duplicate source identity silently accepted or disappeared: %+v", report)
	}
}

func TestControlledDownClassificationRetainsActualUDPAndUnknownCausality(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 1, 4*time.Second
	profile.BlackholeEvery, profile.BlackholeFor = 2*time.Second, 1200*time.Millisecond
	owner, peers := runControlledWorkload(t, profile, 0)
	report := ClassifyDowns(owner, peers)
	if !report.Complete || report.Eligible == 0 || report.Intentional+report.Unresolved == 0 ||
		report.ReferenceQualified || report.Eligible != report.Intentional+report.False+report.Unresolved ||
		len(peers.Peers) != 1 || peers.Peers[0].BlackholeOmitted == 0 || peers.Peers[0].UpTransitions < 2 {
		t.Fatalf("actual joined UDP did not retain fail-closed causal evidence: %+v", report)
	}
}

func TestControlledTXOnlyBlackholeProducesConditionalIntentionalDown(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 1, 4*time.Second
	profile.BlackholeEvery, profile.BlackholeFor = 2*time.Second, 1200*time.Millisecond
	profile.TXOnlyBlackhole = true
	// RFC 5880 section 6.8.7 gives multiplier1 a 90% TX bound, leaving room for real dispatch lateness.
	profile.DetectMultiplier = 1
	owner, peers := runControlledWorkload(t, profile, 0)
	report := ClassifyDowns(owner, peers)
	if !report.Complete || report.Intentional == 0 || !report.Conditional || report.ReferenceQualified ||
		!peers.Profile.TXOnlyBlackhole || peers.Peers[0].ReceiveBlackholeOmitted != 0 ||
		peers.Peers[0].BlackholeOmitted == 0 || peers.Peers[0].UpTransitions < 2 {
		t.Logf("conditional_fixture_witnesses=%+v", owner.DownWitnesses)
		t.Logf("conditional_fixture_omissions=%+v", peers.Peers[0].BlackholeWindows)
		t.Logf("conditional_fixture_profile=%+v owner_phase=%+v peer_phase=%+v", profile, owner.Phase, peers.Phase)
		t.Fatalf("actual TX-only fault has no conditional intentional Down/recovery evidence: %+v", report)
	}
}

func TestTXOnlyBlackholeCannotChangeNamedQualificationProfiles(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"fixture", "reference", "engineering"} {
		profile, err := DefaultProfile(kind)
		if err != nil {
			t.Fatal(err)
		}
		profile.TXOnlyBlackhole = true
		if valid := profile.Validate() == nil; valid != (kind == "fixture") {
			t.Fatalf("%s changed its qualification loss directions: accepted=%t", kind, valid)
		}
	}
}

func TestControlledRandomLossOnlyDownCannotPass(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration, profile.LossPermille = 1, time.Second, 1000
	owner, peers := runControlledWorkload(t, profile, 0)
	report := ClassifyDowns(owner, peers)
	if !report.Complete || report.False == 0 || report.Passing || report.ReferenceQualified ||
		peers.Peers[0].RandomOmitted == 0 || peers.Peers[0].BlackholeOmitted != 0 {
		t.Fatalf("actual random-loss-only Down was excused as an intentional blackhole: %+v", report)
	}
}
