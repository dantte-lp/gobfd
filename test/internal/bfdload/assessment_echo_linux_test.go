//go:build linux

package bfdload

import (
	"reflect"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestAssessmentBindsEchoModeAcrossCanonicalRoles(t *testing.T) {
	t.Parallel()
	for _, echo := range []bool{false, true} {
		profile, err := DefaultProfile("reference")
		if err != nil {
			t.Fatal(err)
		}
		profile.Echo = echo
		cfg, err := LoopbackConfig(profile)
		if err != nil {
			t.Fatal(err)
		}
		cfg.ControlDirectory = "/fixture"
		baseline, _ := classificationFixture(t)
		phase := baseline.Phase
		owner := OwnerWorkloadObservation{
			Scope: "production-core-owner-workload", Completed: true, Config: cfg, Phase: phase,
			ElapsedNS: int64(profile.Duration), SamplePeriodNS: int64(ownerSamplePeriod),
			Initial: OwnerObservation{
				Scope: "production-core-owner-component", Echo: echo,
				Sessions: make([]OwnerSessionObservation, profile.Sessions),
			},
			MeasurementFinal: OwnerObservation{Scope: "production-core-owner-component", Echo: echo},
			Final:            OwnerObservation{Scope: "production-core-owner-component", Echo: echo, Stopped: true},
		}
		peers := PeerObservation{
			Scope: "udp-peer-generator", Profile: profile, Phase: phase, Completed: true, Stopped: true,
			ElapsedNS: int64(profile.Duration), Peers: make([]PeerSessionObservation, profile.Sessions),
		}
		if err := assessmentInputs(owner, peers); err != nil {
			t.Fatalf("canonical mode echo=%t rejected: %v", echo, err)
		}
		for _, snapshot := range []*OwnerObservation{&owner.Initial, &owner.MeasurementFinal, &owner.Final} {
			snapshot.Echo = !echo
			if err := assessmentInputs(owner, peers); err == nil {
				t.Fatalf("canonical echo=%t accepted a conflicting snapshot mode", echo)
			}
			snapshot.Echo = echo
		}
	}
}

func echoMetricEvidence(t *testing.T, invalid bool) OwnerWorkloadObservation {
	t.Helper()
	registry := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(registry)
	collector.ObserveTimerEvent(bfd.TimerEchoTX, bfd.TimerDispatched, time.Millisecond)
	collector.ObserveTimerEvent(bfd.TimerEchoTX, bfd.TimerEffect, time.Millisecond)
	before, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, timer := range []bfd.TimerKind{bfd.TimerEchoTX, bfd.TimerEchoDetection} {
		for range 100 {
			collector.ObserveTimerEvent(timer, bfd.TimerDispatched, time.Millisecond)
			collector.ObserveTimerEvent(timer, bfd.TimerEffect, time.Millisecond)
		}
	}
	if invalid {
		collector.ObserveTimerEvent(bfd.TimerEchoDetection, bfd.TimerDispatched, -time.Nanosecond)
		collector.ObserveTimerEvent(bfd.TimerEchoDetection, bfd.TimerNoEffect, 0)
	}
	after, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	delivery := bfd.DeliveryCounterPairs()
	for index := range delivery {
		if delivery[index].Source == bfd.DeliveryEchoOwner && delivery[index].Outcome == bfd.DeliveryCommitted {
			delivery[index].Count = 105
		}
	}
	return OwnerWorkloadObservation{
		Config:           PeerConfig{Profile: Profile{Echo: true}},
		Initial:          OwnerObservation{Echo: true, Metrics: before},
		MeasurementFinal: OwnerObservation{Echo: true, Metrics: after},
		Final: OwnerObservation{
			Echo: true, Metrics: after, Stopped: true, Delivery: delivery,
			RetiredReceived: 5, Sessions: []OwnerSessionObservation{{Received: 100}},
		},
	}
}

func TestEchoAssessmentSelectsActualTimerMetrics(t *testing.T) {
	t.Parallel()
	owner := echoMetricEvidence(t, false)
	first := ProfileAssessment{Status: "pass", Profile: owner.Config.Profile, Denominators: make(map[string]uint64)}
	first.assessMetrics(owner)
	second := ProfileAssessment{Status: "pass", Profile: owner.Config.Profile, Denominators: make(map[string]uint64)}
	second.assessMetrics(owner)
	if first.Status != "pass" || !reflect.DeepEqual(first, second) || len(first.Distributions) != 4 ||
		first.Denominators["echo_tx_effect"] != 100 || first.Denominators["echo_detection_effect"] != 100 ||
		first.ReferenceQualified || first.TimingUncertaintyNS != nil {
		t.Fatalf("Echo metrics or idempotence changed: %+v", first)
	}
	for _, distribution := range first.Distributions {
		if distribution.Timer != "echo_tx" && distribution.Timer != "echo_detection" {
			t.Fatalf("Echo assessment invented Control/RX samples: %+v", distribution)
		}
	}
	var rxUnavailable bool
	for _, check := range first.Checks {
		if check.Name == "kernel_rx_to_owner_distribution" {
			rxUnavailable = check.Status == "not_applicable" && check.Reason != ""
		}
	}
	if !rxUnavailable {
		t.Fatal("missing Echo kernel RX instrumentation was not declared explicitly")
	}
}

func TestEchoAssessmentRejectsInvalidEventsAndUnsettledReturns(t *testing.T) {
	t.Parallel()
	for _, invalidTimer := range []bool{false, true} {
		owner := echoMetricEvidence(t, invalidTimer)
		if !invalidTimer {
			owner.Final.RetiredReceived++
		}
		report := ProfileAssessment{Status: "pass", Profile: owner.Config.Profile, Denominators: make(map[string]uint64)}
		report.assessMetrics(owner)
		if report.Status != "incomplete" {
			t.Fatalf("invalid timer or missing settled Echo return accepted: invalidTimer=%t report=%+v", invalidTimer, report)
		}
	}
}

func TestEchoAssessmentRejectsMoreInputsThanActualOwnerTransmits(t *testing.T) {
	t.Parallel()
	owner, peers := echoClassificationFixture(t, []string{"tx", "tx", "tx"})
	entry := owner.Initial.Sessions[0]
	entry.Sent, entry.Received, entry.SourcePort, entry.Registered = 23, 20, 55100, true
	entry.Observation.State = bfd.StateUp
	registrations := OwnerRegistrations{
		KnownPeers: 1, ActiveMetricRegistrations: 1, RetainedSessions: 1, RetainedPacketRows: 4,
	}
	for _, snapshot := range []*OwnerObservation{&owner.Initial, &owner.MeasurementFinal, &owner.Final} {
		snapshot.Sessions, snapshot.Registrations = []OwnerSessionObservation{entry}, registrations
	}
	owner.Final.Sessions[0].Registered = false
	owner.Final.Registrations.KnownPeers, owner.Final.Registrations.ActiveMetricRegistrations = 0, 0
	owner.Final.Delivery = owner.MeasurementFinal.Delivery
	owner.Final.TerminalPackets = 20
	environment := Environment{Runtime: RuntimeSnapshot{
		Version: "go1.27.3", GOOS: "linux", GOARCH: "amd64", GOMAXPROCS: 2,
	}, Process: ProcessSnapshot{PID: 1}, Cgroup: &CgroupSnapshot{MemberPath: "/owner"}}
	owner.Final.Environment = environment
	environment.Process.PID, environment.Cgroup = 2, &CgroupSnapshot{MemberPath: "/peers"}
	peers.Environment = environment
	peer := &peers.Peers[0]
	peer.Read, peer.Accepted, peer.Scheduled, peer.Written, peer.WriteAttempts = 23, 23, 23, 20, 20
	if err := assessmentPackets(owner, peers); err != nil {
		t.Fatalf("consistent Echo input/return ledger rejected: %v", err)
	}
	owner.Final.Sessions[0].Sent--
	if err := assessmentPackets(owner, peers); err == nil {
		t.Fatal("Echo peer inputs exceeded actual current and retired owner transmits")
	}
	owner.Final.RetiredSent = 1
	if err := assessmentPackets(owner, peers); err != nil {
		t.Fatalf("joined retired Echo sender was omitted from lifetime ledger: %v", err)
	}
}

func TestAssessmentPreservesUnknownDownStatusByWorkload(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"control_unknown", "echo_unknown", "echo_false"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			owner, peers := classificationFixture(t)
			want := "fail"
			if name != "control_unknown" {
				owner, peers = echoClassificationFixture(t, []string{"tx", "tx", "tx"})
			}
			if name == "echo_false" {
				owner.DownWitnesses[0].Cause = bfd.CauseLocalAdmin
			} else {
				owner.DownWitnesses[0].Timing.ValidatedKnown = false
				if name == "echo_unknown" {
					want = "incomplete"
				}
			}
			report := ProfileAssessment{Status: "pass", Profile: owner.Config.Profile}
			report.assessDowns(owner, peers)
			if report.Status != want || report.ReferenceQualified || report.TimingUncertaintyNS != nil {
				t.Fatalf("%s changed its evidence/numeric boundary: status=%s want=%s", name, report.Status, want)
			}
		})
	}
}
