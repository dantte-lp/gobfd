//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

// Synthetic canonical reports exercise the consumer, never native qualification.
func TestAssessCommandEchoUsesConditionalBoundaries(t *testing.T) {
	for _, name := range []string{"at_limits", "detection_p999_above", "unresolved_down"} {
		t.Run(name, func(t *testing.T) {
			owner, peers := echoAssessmentCommandEvidence(t, name)
			before := assessmentInputDigest(t, owner, peers)
			want := "pass"
			wantIntentional, wantUnresolved := uint64(len(owner.DownWitnesses)), uint64(0)
			if name != "at_limits" {
				want = "fail"
			}
			if name == "unresolved_down" {
				owner.DownWitnesses[0].Timing.ValidatedKnown = false
				before = assessmentInputDigest(t, owner, peers)
				want, wantIntentional, wantUnresolved = "incomplete", wantIntentional-1, 1
			}
			direct := bfdload.AssessProfile(owner, peers)
			if direct.Status != want || before != assessmentInputDigest(t, owner, peers) {
				t.Fatalf("Echo consumer changed its conditional predicate or inputs: %+v", direct)
			}
			directory := t.TempDir()
			paths := []string{filepath.Join(directory, "owner.json"), filepath.Join(directory, "peers.json")}
			for index, value := range []any{owner, peers} {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths[index], encoded, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			runErr := run(t.Context(), []string{"assess", paths[0], paths[1]}, &output)
			var report bfdload.ProfileAssessment
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatalf("Echo verdict missing: command=%v decode=%v", runErr, err)
			}
			if report.Status != want || (runErr == nil) != (want == "pass") || !report.Profile.Echo ||
				!report.Conditional || report.ReferenceQualified || report.TimingUncertaintyNS != nil ||
				report.Downs.Intentional != wantIntentional || report.Downs.Unresolved != wantUnresolved {
				t.Fatalf("Echo CLI boundaries or old-deadline classification changed: %+v command=%v", report, runErr)
			}
		})
	}
}

func echoAssessmentCommandEvidence(
	t *testing.T, changed string,
) (bfdload.OwnerWorkloadObservation, bfdload.PeerObservation) {
	t.Helper()
	owner, peers := assessmentCommandEvidence(t, changed)
	owner.Config.Profile.Echo = true
	peers.Profile = owner.Config.Profile
	for index, endpoint := range owner.Config.Sessions {
		endpoint.Local, endpoint.Peer = netip.AddrPortFrom(endpoint.Local.Addr(), 3785),
			netip.AddrPortFrom(endpoint.Peer.Addr(), 3785)
		owner.Config.Sessions[index] = endpoint
		peer := &peers.Peers[index]
		peer.Endpoint, peer.State, peer.RemoteDiscriminator = endpoint, "", 0
		peer.UpTransitions, peer.DownTransitions, peer.BlackholeOmitted, peer.ReceiveBlackholeOmitted = 0, 0, 0, 0
		for position := range peer.BlackholeWindows {
			window := &peer.BlackholeWindows[position]
			row := window.Segments[0]
			row.Direction, row.RequiredMinRXUS = "rx", 0
			row.MyDiscriminator = owner.Initial.Sessions[index].LocalDiscriminator
			row.YourDiscriminator = row.MyDiscriminator
			row.ReceivedSequences = slices.Clone(row.Sequences)
			window.FirstTXNS, window.LastTXNS, window.TXOmitted = 0, 0, 0
			window.FirstRXNS, window.LastRXNS, window.RXOmitted = row.FirstNS, row.LastNS, row.Count
			window.Segments = []bfdload.PeerOmissionSegment{row}
			peer.ReceiveBlackholeOmitted += row.Count
		}
		peer.Scheduled = peer.Written + peer.RandomOmitted
		peer.Accepted = peer.Scheduled
		peer.Read = peer.Accepted + peer.ReceiveRandomOmitted + peer.ReceiveBlackholeOmitted
		for position, snapshot := range []*bfdload.OwnerObservation{&owner.Initial, &owner.MeasurementFinal, &owner.Final} {
			snapshot.Echo = true
			session := &snapshot.Sessions[index]
			session.Endpoint, session.Observation.RemoteKnown = endpoint, false
			session.Observation.RemoteState, session.Observation.RemoteDiag = 0, 0
			if position > 0 {
				session.Sent = peer.Read + 2
			}
		}
	}
	for index := range owner.DownWitnesses {
		witness := &owner.DownWitnesses[index]
		witness.Type, witness.LocalDiag, witness.RemoteKnown = bfd.SessionTypeEcho, bfd.DiagEchoFailed, false
		witness.RemoteDiscriminator, witness.Timing.ValidatedRemoteDiscriminator = 0, 0
		witness.RemoteState, witness.RemoteDiag = 0, 0
		witness.Incarnation.Endpoint = owner.Config.Sessions[witness.Incarnation.Slot]
		owner.DownEvents[index].Diagnostic = "EchoFailed"
	}
	for position, snapshot := range []*bfdload.OwnerObservation{&owner.Initial, &owner.MeasurementFinal, &owner.Final} {
		for index := range snapshot.ClockCalibrations {
			binding := snapshot.ClockCalibrations[index].Binding
			snapshot.ClockCalibrations[index].Binding = netip.AddrPortFrom(binding.Addr(), 3785)
		}
		var received uint64
		for _, session := range snapshot.Sessions {
			received += session.Received
		}
		snapshot.Delivery = bfd.DeliveryCounterPairs()
		for index := range snapshot.Delivery {
			row := &snapshot.Delivery[index]
			if row.Source == bfd.DeliveryEchoOwner && row.Outcome == bfd.DeliveryCommitted {
				row.Count = received
			}
		}
		downs := uint64(len(owner.DownWitnesses))
		if position == 0 {
			downs = 0
		}
		snapshot.Metrics = echoAssessmentCommandMetrics(received, downs, changed)
	}
	return owner, peers
}

func echoAssessmentCommandMetrics(received, detection uint64, changed string) []*dto.MetricFamily {
	families := assessmentMetrics(received, detection, received, changed)
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "timer" {
					timer := label.GetValue()
					switch timer {
					case "control_tx":
						timer = "echo_tx"
					case "control_detection":
						timer = "echo_detection"
					case "echo_tx":
						timer = "control_tx"
					case "echo_detection":
						timer = "control_detection"
					}
					label.Value = &timer
				}
			}
			switch family.GetName() {
			case "gobfd_bfd_control_rx_timing_total":
				*metric.Counter.Value = 0
			case "gobfd_bfd_control_rx_commit_seconds":
				*metric.Histogram.SampleCount, *metric.Histogram.SampleSum = 0, 0
				for _, bucket := range metric.GetHistogram().GetBucket() {
					*bucket.CumulativeCount = 0
				}
			case "gobfd_bfd_socket_drop_samples_total":
				*metric.Counter.Value = 0
				if metric.GetLabel()[0].GetValue() == "echo" && metric.GetLabel()[1].GetValue() == "sample" {
					*metric.Counter.Value = float64(received)
				}
			}
		}
	}
	return families
}
