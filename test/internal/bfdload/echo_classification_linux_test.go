//go:build linux

package bfdload

import (
	"math"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func echoClassificationFixture(t *testing.T, directions []string) (OwnerWorkloadObservation, PeerObservation) {
	t.Helper()
	owner, peers := classificationFixture(t)
	owner.Config.Profile.Echo = true
	peers.Profile = owner.Config.Profile
	phase := *peers.Phase
	peers.Phase = &phase
	owner.Initial.Echo, owner.MeasurementFinal.Echo, owner.Final.Echo = true, true, true
	endpoint := owner.Config.Sessions[0]
	endpoint.Local = netip.AddrPortFrom(endpoint.Local.Addr(), 3785)
	endpoint.Peer = netip.AddrPortFrom(endpoint.Peer.Addr(), 3785)
	owner.Config.Sessions[0], owner.Initial.Sessions[0].Endpoint = endpoint, endpoint
	for _, snapshot := range []*OwnerObservation{&owner.Initial, &owner.MeasurementFinal} {
		for index := range snapshot.Delivery {
			row := &snapshot.Delivery[index]
			if row.Source == bfd.DeliveryPacketOwner && row.Outcome == bfd.DeliveryCommitted {
				count := row.Count
				row.Count = 0
				for target := range snapshot.Delivery {
					if snapshot.Delivery[target].Source == bfd.DeliveryEchoOwner &&
						snapshot.Delivery[target].Outcome == bfd.DeliveryCommitted {
						snapshot.Delivery[target].Count = count
					}
				}
			}
		}
	}
	witness := &owner.DownWitnesses[0]
	witness.Type, witness.LocalDiag, witness.RemoteKnown = bfd.SessionTypeEcho, bfd.DiagEchoFailed, false
	witness.RemoteDiscriminator, witness.Timing.ValidatedRemoteDiscriminator = 0, 0
	witness.Incarnation.Endpoint = endpoint
	peer := &peers.Peers[0]
	peer.Endpoint, peer.Read = endpoint, 10+uint64(len(directions))
	peer.BlackholeOmitted, peer.ReceiveBlackholeOmitted = 0, 0
	window := &peer.BlackholeWindows[0]
	window.Segments = nil
	window.FirstTXNS, window.LastTXNS, window.TXOmitted = 0, 0, 0
	window.FirstRXNS, window.LastRXNS, window.RXOmitted = 0, 0, 0
	for index, direction := range directions {
		offset := []int64{420000000, 500000000, 580000000}[index]
		row := PeerOmissionSegment{
			Direction: direction, FirstNS: offset, LastNS: offset, Count: 1, Valid: true,
			MyDiscriminator: 42, YourDiscriminator: 42, State: bfd.StateUp,
			DesiredMinTXUS: 100000, RequiredMinRXUS: 0, DetectMult: 3,
			OffsetsNS: []int64{offset}, ReceivedSequences: []uint64{uint64(11 + index)},
		}
		if direction == "rx" {
			row.Sequences = []uint64{uint64(11 + index)}
			peer.ReceiveBlackholeOmitted++
			window.RXOmitted++
			if window.FirstRXNS == 0 {
				window.FirstRXNS = offset
			}
			window.LastRXNS = offset
		} else {
			row.Sequences = []uint64{10 + window.TXOmitted + 1}
			peer.BlackholeOmitted++
			window.TXOmitted++
			if window.FirstTXNS == 0 {
				window.FirstTXNS = offset
			}
			window.LastTXNS = offset
		}
		window.Segments = append(window.Segments, row)
	}
	peer.Accepted = peer.Read - peer.ReceiveBlackholeOmitted
	peer.Scheduled = peer.Accepted
	return owner, peers
}

func TestEchoDownClassificationUsesActualReceivedInputs(t *testing.T) {
	t.Parallel()
	for _, directions := range [][]string{
		{"rx"},
		{"tx"},
		{"rx", "tx"},
		{"tx", "tx"},
		{"rx", "rx", "rx"},
		{"tx", "tx", "tx"},
		{"rx", "tx", "rx"},
	} {
		t.Run(strings.Join(directions, "_"), func(t *testing.T) {
			t.Parallel()
			owner, peers := echoClassificationFixture(t, directions)
			first, second := ClassifyDowns(owner, peers), ClassifyDowns(owner, peers)
			if !first.Complete || !first.Passing || first.Intentional != 1 || first.False != 0 || first.Unresolved != 0 ||
				first.ReferenceQualified || first.TimingUncertaintyNS != nil || !second.Passing {
				t.Fatalf("Echo conditional old-deadline model failed for %v: %+v", directions, first)
			}
		})
	}
}

func TestEchoDownClassificationRequiresStrictDeadlineExtension(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		due  int64
		want string
	}{
		{"one_nanosecond_extension", 739999997, downIntentional},
		{"equal_deadline", 739999998, downUnresolved},
		{"insufficient_extension", 739999999, downUnresolved},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			owner, peers := echoClassificationFixture(t, []string{"tx", "tx", "tx"})
			window := &peers.Peers[0].BlackholeWindows[0]
			for index := range window.Segments {
				offset := int64(420000000 + index*10000000)
				row := &window.Segments[index]
				row.FirstNS, row.LastNS, row.OffsetsNS[0] = offset, offset, offset
			}
			window.LastTXNS = 440000000
			timing := &owner.DownWitnesses[0].Timing
			timing.DetectionDueNS = test.due
			timing.CommitStartNS, timing.CommitEndNS = 740000010, 740000011
			// Abstract serialized clock readings may coincide; no processing-time lower bound is assumed.
			report := ClassifyDowns(owner, peers)
			if !report.Complete || len(report.Rows) != 1 || report.Rows[0].Outcome != test.want ||
				report.ReferenceQualified || report.TimingUncertaintyNS != nil {
				t.Fatalf("strict old-deadline extension %s: %+v", test.name, report)
			}
		})
	}
}

func TestEchoDownClassificationRejectsDeadlineExtensionOverflow(t *testing.T) {
	t.Parallel()
	owner, peers := echoClassificationFixture(t, []string{"tx", "tx", "tx"})
	owner.Config.Profile.Duration = math.MaxInt64
	owner.Config.Profile.BlackholeEvery = math.MaxInt64 - time.Second
	owner.Config.Profile.BlackholeFor = time.Second
	peers.Profile = owner.Config.Profile
	window := &peers.Peers[0].BlackholeWindows[0]
	window.StartNS = math.MaxInt64 - int64(time.Second)
	for index := range window.Segments {
		offset := int64(math.MaxInt64 - 250000000 + index*25000000)
		row := &window.Segments[index]
		row.FirstNS, row.LastNS, row.OffsetsNS[0] = offset, offset, offset
	}
	window.FirstTXNS, window.LastTXNS = math.MaxInt64-250000000, math.MaxInt64-200000000
	timing := &owner.DownWitnesses[0].Timing
	timing.ValidatedNS, timing.DetectionDueNS = math.MaxInt64-400000000, math.MaxInt64-100000000
	timing.CommitStartNS, timing.CommitEndNS = math.MaxInt64-50000000, math.MaxInt64-49999999
	if report := ClassifyDowns(owner, peers); !report.Complete || report.Passing || report.Intentional != 0 ||
		report.Unresolved != 1 || report.ReferenceQualified || report.TimingUncertaintyNS != nil {
		t.Fatalf("overflowing Echo replacement deadline qualified: %+v", report)
	}
}

func TestEchoDownClassificationDoesNotReplaceActualInputsWithIntervalGuesses(t *testing.T) {
	t.Parallel()
	owner, peers := echoClassificationFixture(t, []string{"tx", "tx", "tx"})
	row := &peers.Peers[0].BlackholeWindows[0].Segments[1]
	row.FirstNS, row.LastNS, row.OffsetsNS[0] = 520353374, 520353374, 520353374
	if report := ClassifyDowns(owner, peers); !report.Passing || report.Intentional != 1 {
		t.Fatalf("actual contiguous Echo inputs were replaced by an assumed TX schedule: %+v", report)
	}
}

func TestEchoDownClassificationRejectsUnprovenReturns(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"missing_input", "duplicate_input", "input_gap", "input_reversal", "wrong_packet", "wrong_later_packet",
		"invalid_packet",
		"fake_remote", "wrong_type", "wrong_detection", "missing_receive", "missing_version",
		"deadline_boundary", "owner_drop", "io_failure", "no_predeadline_inputs",
		"unequal_run", "unequal_namespace", "unknown_bracket", "truncated_history",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			owner, peers := echoClassificationFixture(t, []string{"tx", "tx", "tx"})
			witness, peer := &owner.DownWitnesses[0], &peers.Peers[0]
			window := &peer.BlackholeWindows[0]
			switch name {
			case "missing_input":
				window.Segments[0].ReceivedSequences = nil
			case "duplicate_input":
				window.Segments[1].ReceivedSequences[0] = 11
			case "input_gap":
				window.Segments[0].ReceivedSequences[0] = 10
			case "input_reversal":
				window.Segments[1].ReceivedSequences[0], window.Segments[2].ReceivedSequences[0] = 13, 12
			case "wrong_packet":
				window.Segments[0].MyDiscriminator = 1
			case "wrong_later_packet":
				window.Segments[2].MyDiscriminator = 1
			case "invalid_packet":
				window.Segments[0].Valid = false
			case "fake_remote":
				witness.RemoteKnown = true
			case "wrong_type":
				witness.Type = bfd.SessionTypeSingleHop
			case "wrong_detection":
				witness.Timing.DetectionNS++
			case "missing_receive":
				witness.Timing.ValidatedKnown = false
			case "missing_version":
				witness.ObservationVersion = 0
			case "deadline_boundary":
				row := &window.Segments[2]
				row.FirstNS, row.LastNS, row.OffsetsNS[0], window.LastTXNS = 650000000, 650000000, 650000000, 650000000
			case "owner_drop":
				for index := range owner.MeasurementFinal.Delivery {
					row := &owner.MeasurementFinal.Delivery[index]
					if row.Source == bfd.DeliveryEchoOwner && row.Outcome == bfd.DeliveryAbandoned {
						row.Count = 1
					}
				}
			case "io_failure":
				peer.WriteErrors, peer.Written = 1, 9
			case "no_predeadline_inputs":
				for index := range window.Segments {
					offset := int64(655000000 + index*1000000)
					row := &window.Segments[index]
					row.FirstNS, row.LastNS, row.OffsetsNS[0] = offset, offset, offset
				}
				window.FirstTXNS, window.LastTXNS = 655000000, 657000000
			case "unequal_run":
				peers.Phase.Run.Inode++
			case "unequal_namespace":
				peers.Phase.TimeNamespaceID++
			case "unknown_bracket":
				peers.Phase.BracketKnown = false
			case "truncated_history":
				peer.BlackholeEventsOmitted = 1
			}
			if report := ClassifyDowns(owner, peers); report.Passing || report.Intentional != 0 ||
				report.Unresolved != 1 || report.ReferenceQualified || report.TimingUncertaintyNS != nil {
				t.Fatalf("unproven Echo return %s qualified: %+v", name, report)
			}
		})
	}
}

func TestEchoDownClassificationRejectsPeerKernelLoss(t *testing.T) {
	t.Parallel()
	for _, invalid := range []bool{false, true} {
		owner, peers := echoClassificationFixture(t, []string{"tx", "tx", "tx"})
		if invalid {
			peers.Peers[0].KernelDropInvalid = 1
		} else {
			peers.Peers[0].KernelDropCounterRaw = 1
		}
		if report := ClassifyDowns(owner, peers); report.Passing || report.Intentional != 0 || report.Unresolved != 1 {
			t.Fatalf("independent Echo kernel loss qualified: invalid=%t report=%+v", invalid, report)
		}
	}
}
