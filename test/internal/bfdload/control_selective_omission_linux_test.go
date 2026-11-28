//go:build linux

package bfdload

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type selectiveControlInput struct {
	at    int64
	state bfd.State
}

// Keep actual decisions and all withheld packets in the report; no peer replay is implied.
func selectiveControlFixture(t *testing.T, inputs []selectiveControlInput) (OwnerWorkloadObservation, PeerObservation) {
	t.Helper()
	owner, peers := classificationFixture(t)
	phase := *peers.Phase
	peers.Phase = &phase
	peer := &peers.Peers[0]
	window := &peer.BlackholeWindows[0]
	window.Segments = nil
	window.TXOmitted = uint64(len(inputs))
	peer.BlackholeOmitted, peer.Scheduled = window.TXOmitted, 10+window.TXOmitted
	peer.WriteAttempts, peer.Written = 10, 10
	for index, input := range inputs {
		segment := PeerOmissionSegment{
			Direction: "tx", FirstNS: input.at, LastNS: input.at, Count: 1, Valid: true,
			MyDiscriminator: 1, YourDiscriminator: 42, State: input.state,
			DesiredMinTXUS: 100000, RequiredMinRXUS: 100000, DetectMult: 3,
			OffsetsNS: []int64{input.at}, Sequences: []uint64{uint64(11 + index)},
		}
		if input.state == bfd.StateDown {
			segment.YourDiscriminator, segment.Diag = 0, bfd.DiagControlTimeExpired
			segment.DesiredMinTXUS, segment.Poll = 1000000, true
			peer.DetectionExpirations, peer.DownTransitions, peer.UpTransitions = 1, 1, 2
		}
		window.Segments = append(window.Segments, segment)
	}
	if len(inputs) != 0 {
		window.FirstTXNS, window.LastTXNS = inputs[0].at, inputs[len(inputs)-1].at
	}
	return owner, peers
}

func requireSelectiveControlOutcome(t *testing.T, owner OwnerWorkloadObservation, peers PeerObservation, want string) {
	t.Helper()
	report := ClassifyDowns(owner, peers)
	if !report.Complete || len(report.Rows) != 1 || report.Rows[0].Outcome != want ||
		report.Eligible != 1 || report.ReferenceQualified || report.TimingUncertaintyNS != nil {
		t.Fatalf("selective old-deadline outcome = %+v, want %s", report, want)
	}
	if report.Passing != (want == downIntentional) {
		t.Fatalf("selective passing=%t, want outcome %s", report.Passing, want)
	}
}

func TestControlSelectiveRestorationUsesCapturedUpInputs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		inputs []selectiveControlInput
	}{
		{"one", []selectiveControlInput{{420000000, bfd.StateUp}}},
		{"two_with_large_actual_gap", []selectiveControlInput{{420000000, bfd.StateUp}, {590000000, bfd.StateUp}}},
		{"three", []selectiveControlInput{{420000000, bfd.StateUp}, {500000000, bfd.StateUp}, {580000000, bfd.StateUp}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			owner, peers := selectiveControlFixture(t, test.inputs)
			requireSelectiveControlOutcome(t, owner, peers, downIntentional)
		})
	}
}

func TestControlSelectiveRestorationKeepsExpiredDownWithheld(t *testing.T) {
	t.Parallel()
	for _, at := range []int64{620000000, 650000000, 650000001} {
		owner, peers := selectiveControlFixture(t, []selectiveControlInput{
			{420000000, bfd.StateUp}, {500000000, bfd.StateUp}, {580000000, bfd.StateUp}, {at, bfd.StateDown},
		})
		requireSelectiveControlOutcome(t, owner, peers, downIntentional)
	}
}

func TestControlSelectiveRestorationRecognizesProducerExpiry(t *testing.T) {
	t.Parallel()
	intervals := []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, time.Second, 2 * time.Second}
	for _, interval := range intervals {
		t.Run(interval.String(), func(t *testing.T) {
			t.Parallel()
			profile, err := DefaultProfile(fixtureProfile)
			if err != nil {
				t.Fatal(err)
			}
			profile.Interval = interval
			peer, err := newPeerState(profile, 1)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Unix(1, 0)
			packet := bfd.ControlPacket{
				State: bfd.StateInit, MyDiscriminator: 42, YourDiscriminator: 1,
				DesiredMinTxInterval:  uint32(interval / time.Microsecond),
				RequiredMinRxInterval: uint32(interval / time.Microsecond), DetectMult: profile.DetectMultiplier,
			}
			if _, err = peer.receive(packet, now); err != nil {
				t.Fatal(err)
			}
			packet.State, packet.Final = bfd.StateUp, true
			if _, err = peer.receive(packet, now); err != nil {
				t.Fatal(err)
			}
			if !peer.expire(peer.deadline()) {
				t.Fatal("live peer did not expire at its actual deadline")
			}
			segment := omissionPacket(peer.packet(false), false)
			if !segment.Valid || !knownWithheldControlDown(segment, profile) {
				t.Fatalf("actual producer expiry was not recognized: %+v", segment)
			}
			segment.Poll = !segment.Poll
			if knownWithheldControlDown(segment, profile) {
				t.Fatal("changed producer Poll flag was accepted")
			}
			segment = omissionPacket(peer.packet(true), false)
			if knownWithheldControlDown(segment, profile) {
				t.Fatal("Final response was accepted as periodic expiry")
			}
		})
	}
}

func TestControlSelectiveRestorationReplacesNominalCoveragePolicy(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"single_omission", "attempt_gap", "fractional_interval_overrun", "random_loss_lead_in"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			owner, peers := classificationFixture(t)
			editClassificationFixture(name, &owner, &peers)
			requireSelectiveControlOutcome(t, owner, peers, downIntentional)
		})
	}
}

func TestControlSelectiveRestorationRequiresStrictActualDueExtension(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		due  int64
		want string
	}{
		{"one_nanosecond", 519999997, downIntentional},
		{"equal", 519999998, downUnresolved},
		{"insufficient", 519999999, downUnresolved},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			owner, peers := selectiveControlFixture(t, []selectiveControlInput{{420000000, bfd.StateUp}})
			owner.Config.Profile.DetectMultiplier = 1
			peers.Profile = owner.Config.Profile
			peers.Peers[0].BlackholeWindows[0].Segments[0].DetectMult = 1
			timing := &owner.DownWitnesses[0].Timing
			timing.DetectionNS, timing.DetectionDueNS = 100000000, test.due
			timing.CommitStartNS, timing.CommitEndNS = 530000000, 530000001
			// Last receive is 350ms; the old actual Reset can be later.
			// Equality at serialized precision is not strict proof.
			requireSelectiveControlOutcome(t, owner, peers, test.want)
		})
	}
}

func TestControlSelectiveRestorationValidatesEveryUp(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*PeerOmissionSegment)
	}{
		{"poll", func(s *PeerOmissionSegment) { s.Poll = true }},
		{"final", func(s *PeerOmissionSegment) { s.Final = true }},
		{"zero_required_rx", func(s *PeerOmissionSegment) { s.RequiredMinRXUS = 0 }},
		{"changed_required_rx", func(s *PeerOmissionSegment) { s.RequiredMinRXUS = 200000 }},
		{"changed_desired_tx", func(s *PeerOmissionSegment) { s.DesiredMinTXUS = 50000 }},
		{"changed_diagnostic", func(s *PeerOmissionSegment) { s.Diag = bfd.DiagNeighborDown }},
		{"wrong_owner", func(s *PeerOmissionSegment) { s.YourDiscriminator = 43 }},
		{"wrong_peer", func(s *PeerOmissionSegment) { s.MyDiscriminator = 2 }},
		{"changed_detection", func(s *PeerOmissionSegment) { s.DetectMult = 4 }},
		{"invalid", func(s *PeerOmissionSegment) { s.Valid = false }},
		{"init", func(s *PeerOmissionSegment) { s.State = bfd.StateInit }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			owner, peers := selectiveControlFixture(t, []selectiveControlInput{
				{420000000, bfd.StateUp}, {500000000, bfd.StateUp}, {580000000, bfd.StateUp},
			})
			test.edit(&peers.Peers[0].BlackholeWindows[0].Segments[2])
			requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
		})
	}
}

func TestControlSelectiveRestorationValidatesWithheldDown(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*PeerOmissionSegment)
	}{
		{"wrong_peer", func(s *PeerOmissionSegment) { s.MyDiscriminator = 2 }},
		{"wrong_owner", func(s *PeerOmissionSegment) { s.YourDiscriminator = 42 }},
		{"wrong_diag", func(s *PeerOmissionSegment) { s.Diag = bfd.DiagNeighborDown }},
		{"wrong_tx", func(s *PeerOmissionSegment) { s.DesiredMinTXUS = 100000 }},
		{"wrong_rx", func(s *PeerOmissionSegment) { s.RequiredMinRXUS = 200000 }},
		{"wrong_mult", func(s *PeerOmissionSegment) { s.DetectMult = 2 }},
		{"wrong_poll", func(s *PeerOmissionSegment) { s.Poll = false }},
		{"wrong_final", func(s *PeerOmissionSegment) { s.Poll, s.Final = false, true }},
		{"invalid", func(s *PeerOmissionSegment) { s.Valid = false }},
		{"admin_down", func(s *PeerOmissionSegment) { s.State = bfd.StateAdminDown }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			owner, peers := selectiveControlFixture(t, []selectiveControlInput{
				{420000000, bfd.StateUp}, {500000000, bfd.StateUp}, {580000000, bfd.StateUp}, {620000000, bfd.StateDown},
			})
			test.edit(&peers.Peers[0].BlackholeWindows[0].Segments[3])
			requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
		})
	}
}

func TestControlSelectiveRestorationRejectsUnsupportedCausality(t *testing.T) {
	t.Parallel()
	t.Run("no_up", func(t *testing.T) {
		t.Parallel()
		owner, peers := selectiveControlFixture(t, []selectiveControlInput{{620000000, bfd.StateDown}})
		requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
	})
	t.Run("up_after_down", func(t *testing.T) {
		t.Parallel()
		owner, peers := selectiveControlFixture(t, []selectiveControlInput{
			{420000000, bfd.StateUp}, {500000000, bfd.StateDown}, {580000000, bfd.StateUp},
		})
		requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
	})
	t.Run("up_due_boundary", func(t *testing.T) {
		t.Parallel()
		owner, peers := selectiveControlFixture(t, []selectiveControlInput{
			{420000000, bfd.StateUp}, {650000000, bfd.StateUp},
		})
		requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
	})
	t.Run("source_remote_down", func(t *testing.T) {
		t.Parallel()
		owner, peers := selectiveControlFixture(t, []selectiveControlInput{{420000000, bfd.StateUp}})
		editClassificationFixture("peer_down", &owner, &peers)
		requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
	})
	t.Run("unknown_down_on_due_boundary", func(t *testing.T) {
		t.Parallel()
		owner, peers := selectiveControlFixture(t, []selectiveControlInput{
			{420000000, bfd.StateUp}, {650000001, bfd.StateDown},
		})
		peers.Peers[0].BlackholeWindows[0].Segments[1].MyDiscriminator = 2
		requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
	})
	t.Run("malformed_down_on_due_boundary", func(t *testing.T) {
		t.Parallel()
		owner, peers := selectiveControlFixture(t, []selectiveControlInput{
			{420000000, bfd.StateUp}, {650000001, bfd.StateDown},
		})
		peers.Peers[0].BlackholeWindows[0].Segments[1].Valid = false
		requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
	})
}

func TestControlSelectiveRestorationRejectsExtensionOverflow(t *testing.T) {
	t.Parallel()
	owner, peers := selectiveControlFixture(t, []selectiveControlInput{
		{math.MaxInt64 - 250000000, bfd.StateUp},
		{math.MaxInt64 - 225000000, bfd.StateUp},
		{math.MaxInt64 - 200000000, bfd.StateUp},
	})
	owner.Config.Profile.Duration = math.MaxInt64
	owner.Config.Profile.BlackholeEvery = math.MaxInt64 - time.Second
	owner.Config.Profile.BlackholeFor = time.Second
	peers.Profile = owner.Config.Profile
	peers.Peers[0].BlackholeWindows[0].StartNS = math.MaxInt64 - int64(time.Second)
	timing := &owner.DownWitnesses[0].Timing
	timing.ValidatedNS, timing.DetectionDueNS = math.MaxInt64-400000000, math.MaxInt64-100000000
	timing.CommitStartNS, timing.CommitEndNS = math.MaxInt64-50000000, math.MaxInt64-49999999
	// No capture lies close enough to prove the new deadline without checked overflowing arithmetic.
	requireSelectiveControlOutcome(t, owner, peers, downUnresolved)
	if report := ClassifyDowns(owner, peers); !strings.Contains(report.Rows[0].Reason, "overflow") {
		t.Fatalf("overflow was not distinguished from insufficient extension: %+v", report)
	}
}
