//go:build linux

package bfdload

import (
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func echoClassificationFixture(t *testing.T, directions []string) (OwnerWorkloadObservation, PeerObservation) {
	t.Helper()
	owner, peers := classificationFixture(t)
	owner.Config.Profile.Echo = true
	peers.Profile = owner.Config.Profile
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
	peer.Endpoint, peer.Read = endpoint, 13
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
	for _, directions := range [][]string{{"rx", "rx", "rx"}, {"tx", "tx", "tx"}, {"rx", "tx", "rx"}} {
		owner, peers := echoClassificationFixture(t, directions)
		first, second := ClassifyDowns(owner, peers), ClassifyDowns(owner, peers)
		if !first.Complete || !first.Passing || first.Intentional != 1 || first.False != 0 || first.Unresolved != 0 ||
			first.ReferenceQualified || first.TimingUncertaintyNS != nil || !second.Passing {
			t.Fatalf("Echo conditional old-deadline model failed for %v: %+v", directions, first)
		}
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
		"missing_input", "duplicate_input", "input_gap", "input_reversal", "wrong_packet", "invalid_packet",
		"fake_remote", "wrong_type", "wrong_detection", "missing_receive", "missing_version",
		"deadline_boundary", "owner_drop", "io_failure", "insufficient_inputs",
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
			case "insufficient_inputs":
				window.Segments, window.LastTXNS, window.TXOmitted = window.Segments[:2], 500000000, 2
				peer.Read, peer.Accepted, peer.Scheduled, peer.BlackholeOmitted = 12, 12, 12, 2
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
