//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

// Synthetic reports test only the numeric conditional predicate, never native qualification.
func TestAssessCommandHonorsConditionalProfileThresholds(t *testing.T) {
	for _, name := range []string{
		"at_limits", "tx_p99_above", "tx_p999_above", "detection_p99_above", "detection_p999_above",
	} {
		t.Run(name, func(t *testing.T) {
			owner, peers := assessmentCommandEvidence(t, name)
			// The existing causal consumer must accept the input independently of the new CLI route.
			if causal := bfdload.ClassifyDowns(owner, peers); !causal.Complete || !causal.Passing {
				t.Fatalf("synthetic causal baseline: complete=%t intentional=%d false=%d unresolved=%d error=%q",
					causal.Complete, causal.Intentional, causal.False, causal.Unresolved, causal.Error)
			}
			directory := t.TempDir()
			ownerPath := filepath.Join(directory, "owner.json")
			peerPath := filepath.Join(directory, "peers.json")
			//nolint:musttag // Preserve the existing core Observation and DeliveryCounter JSON field contract.
			encodedOwner, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			encodedPeers, err := json.Marshal(peers)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(ownerPath, encodedOwner, 0o600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(peerPath, encodedPeers, 0o600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			runErr := run(t.Context(), []string{"assess", ownerPath, peerPath}, &output)
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(output.Bytes(), &fields); err != nil {
				t.Fatalf("assess omitted its structured verdict: command_error=%v decode_error=%v", runErr, err)
			}
			wantStatus := `"fail"`
			if name == "at_limits" {
				wantStatus = `"pass"`
			}
			if (runErr == nil) != (name == "at_limits") || string(fields["status"]) != wantStatus ||
				string(fields["scope"]) != `"conditional-workload-profile-assessment"` ||
				string(fields["conditional"]) != "true" || string(fields["reference_qualified"]) != "false" ||
				string(fields["timing_uncertainty_ns"]) != "null" {
				t.Fatalf("assess changed numeric boundaries/qualification: error=%v status=%s scope=%s conditional=%s",
					runErr, fields["status"], fields["scope"], fields["conditional"])
			}
		})
	}
}

func TestAssessProfileIsIdempotentAndRejectsUnprovenEvidence(t *testing.T) {
	owner, peers := assessmentCommandEvidence(t, "")
	before := assessmentInputDigest(t, owner, peers)
	first, second := bfdload.AssessProfile(owner, peers), bfdload.AssessProfile(owner, peers)
	if first.Status != "pass" || !reflect.DeepEqual(first, second) || before != assessmentInputDigest(t, owner, peers) {
		t.Fatalf("conditional assessment is not pure/idempotent: first=%s second=%s", first.Status, second.Status)
	}
	for _, name := range []string{
		"fixture", "altered_profile", "tx_only", "unjoined", "phase", "ledger", "empty_downs",
		"false_down", "unresolved_down", "unknown_clock", "revoked_clock", "clock_bound", "aborted_guard",
		"stale_reference", "stale_total", "hidden_admission", "tight_ancestor", "task_budget",
		"thread_limit", "fd_limit", "goroutine_limit", "rss_limit", "rss_exact_five_percent", "rss_missing",
		"requested_cpu_only", "missed_cpu", "requested_gc_only", "requested_scheduler_only", "requested_churn_only",
		"final_registration", "missing_metrics", "histogram_reset", "duplicate_metric", "unknown_label", "nonfinite",
	} {
		t.Run(name, func(t *testing.T) {
			o, p := owner, peers
			phase, epoch := *peers.Phase, *owner.Final.ClockEpoch
			p.Phase, o.Final.ClockEpoch = &phase, &epoch
			p.Peers = slices.Clone(peers.Peers)
			o.Final.ClockCalibrations = slices.Clone(owner.Final.ClockCalibrations)
			o.Admission = &bfdload.ResourceAdmission{}
			*o.Admission = *owner.Admission
			o.Admission.Ancestors = slices.Clone(owner.Admission.Ancestors)
			group := *o.Admission.Environment.Cgroup
			o.Admission.Environment.Cgroup = &group
			o.MeasurementFinal.Metrics = assessmentMetrics(1000000000, 47000, 1000000000, "")
			mutateAssessmentEvidence(name, &o, &p)
			report := bfdload.AssessProfile(o, p)
			if report.Status == "pass" || report.ReferenceQualified || report.TimingUncertaintyNS != nil || report.Error == "" {
				t.Fatalf("%s evidence incorrectly passed: status=%s", name, report.Status)
			}
		})
	}
	// PID values are local to their namespaces; cgroup identity establishes separate roles here.
	peers.Environment.Process.PID = owner.Final.Environment.Process.PID
	if report := bfdload.AssessProfile(owner, peers); report.Status != "pass" {
		t.Fatalf("independent cgroups were conflated by equal namespace-local PID: %s", report.Status)
	}
}

func assessmentInputDigest(
	t *testing.T, owner bfdload.OwnerWorkloadObservation, peers bfdload.PeerObservation,
) [32]byte {
	t.Helper()
	digest := sha256.New()
	//nolint:musttag // Preserve the existing core Observation and DeliveryCounter wire field names.
	if err := json.NewEncoder(digest).Encode(owner); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(digest).Encode(peers); err != nil {
		t.Fatal(err)
	}
	return [32]byte(digest.Sum(nil))
}

func mutateAssessmentEvidence(name string, o *bfdload.OwnerWorkloadObservation, p *bfdload.PeerObservation) {
	switch name {
	case "fixture":
		o.Config.Profile.Kind, p.Profile.Kind = "fixture", "fixture"
	case "altered_profile":
		o.Config.Profile.Duration--
		p.Profile = o.Config.Profile
	case "tx_only":
		o.Config.Profile.TXOnlyBlackhole, p.Profile.TXOnlyBlackhole = true, true
	case "unjoined":
		p.Stopped = false
	case "phase":
		p.Phase.NativeStartNS++
	case "ledger":
		p.Peers[0].Scheduled++
	case "empty_downs":
		o.DownWitnesses = nil
	case "false_down", "unresolved_down":
		o.DownWitnesses = []bfdload.OwnerDownWitness{o.DownWitnesses[0]}
		o.DownEvents = o.DownEvents[:1]
		one := uint64(1)
		o.DownJournal.SourceTransitions, o.DownJournal.ObservedBeforeCutoff = 1, 1
		o.DownJournal.FinalSourceTransitions = &one
		if name == "false_down" {
			o.DownWitnesses[0].Cause = bfd.CauseLocalAdmin
		} else {
			o.DownWitnesses[0].Timing.ValidatedKnown = false
		}
	case "unknown_clock":
		o.Final.ConditionalTimingUncertaintyNS = nil
	case "revoked_clock":
		o.Final.ClockCalibrations[0].Calibration.Revoked = true
	case "clock_bound":
		bad := uint64(103)
		o.Final.ConditionalTimingUncertaintyNS = &bad
	case "aborted_guard":
		o.Final.ClockEpoch.NoCancellationObserved = false
	case "stale_reference":
		o.Final.ReferenceQualified = true
	case "stale_total":
		bad := uint64(102)
		o.Final.TimingUncertaintyNS = &bad
	case "hidden_admission":
		o.Admission.Environment.Cgroup.MountRoot = "/hidden"
	case "tight_ancestor":
		o.Admission.Ancestors[1].CPUQuotaUS.Value = 700000
	case "task_budget":
		o.Admission.AdditionalTaskBudget = 0
	case "thread_limit":
		o.Resources.PeakThreads = 49
	case "fd_limit":
		o.Resources.PeakFDs = 2257
	case "goroutine_limit":
		o.Resources.PeakGoroutines = 3257
	case "rss_limit":
		o.Resources.PeakRSSBytes = (512 << 20) + 1
	case "rss_exact_five_percent":
		o.Resources.FirstRSS.MeanBytes, o.Resources.LastRSS.MeanBytes = 100, 105
	case "rss_missing":
		o.Resources.FirstRSS.Samples = 0
	case "requested_cpu_only":
		o.CPUPressure.CPUTimeNS = 0
	case "missed_cpu":
		o.CPUPressure.MissedWindows = 1
	case "requested_gc_only":
		o.Config.Profile.GCPressure, p.Profile.GCPressure = true, true
	case "requested_scheduler_only":
		o.Config.Profile.SchedulerPressure, p.Profile.SchedulerPressure = true, true
	case "requested_churn_only":
		o.Config.Profile.SessionChurn, p.Profile.SessionChurn = true, true
	case "final_registration":
		o.Final.Registrations.ActiveMetricRegistrations = 1
	case "missing_metrics":
		o.MeasurementFinal.Metrics = nil
	case "histogram_reset":
		*o.MeasurementFinal.Metrics[0].Metric[0].Histogram.SampleCount = 0
	case "duplicate_metric":
		o.MeasurementFinal.Metrics = append(o.MeasurementFinal.Metrics, o.MeasurementFinal.Metrics[0])
	case "unknown_label":
		*o.MeasurementFinal.Metrics[0].Metric[0].Label[0].Value = "unrecognized_timer"
	case "nonfinite":
		*o.MeasurementFinal.Metrics[0].Metric[0].Histogram.SampleSum = math.Inf(1)
	}
}

func assessmentCommandEvidence(
	t *testing.T, changed string,
) (bfdload.OwnerWorkloadObservation, bfdload.PeerObservation) {
	t.Helper()
	profile, err := bfdload.DefaultProfile("reference")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := bfdload.LoopbackConfig(profile)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControlDirectory = "/control"
	at := time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)
	phase := &bfdload.Phase{
		NativeStartNS: 1000, TimeNamespaceID: 7, BracketNS: 2, BracketKnown: true,
		Run: bfdload.RunIdentity{BootID: "01234567-89ab-cdef-0123-456789abcdef", Device: 1, Inode: 2},
	}
	initial := assessmentOwnerSnapshot(cfg, at, 1)
	measured := assessmentOwnerSnapshot(cfg, at.Add(profile.Duration), 1000000)
	final := assessmentOwnerSnapshot(cfg, at.Add(profile.Duration+time.Second), 1000000)
	final.Stopped, final.Closing, final.StoppedAtUTC = true, true, at.Add(profile.Duration+time.Second)
	final.Registrations.KnownPeers, final.Registrations.ActiveMetricRegistrations = 0, 0
	for index := range final.Sessions {
		final.Sessions[index].Registered = false
	}
	bound := uint64(102)
	final.ConditionalTimingUncertaintyNS = &bound
	final.ClockEpoch = &bfdload.ClockEpochObservation{
		Mechanism: "linux-timerfd-cancel-on-set", StartedAtUTC: at.Add(-time.Second),
		FinishedAtUTC: at.Add(profile.Duration + 2*time.Second), ElapsedNS: int64(profile.Duration + 3*time.Second),
		NoCancellationObserved: true,
	}
	for _, endpoint := range []bfdload.OwnerEndpoint{cfg.Sessions[0], cfg.Sessions[len(cfg.Sessions)-1]} {
		final.ClockCalibrations = append(final.ClockCalibrations, bfdload.OwnerClockObservation{
			Binding: endpoint.Local,
			Calibration: bfd.ReceiveCalibration{
				AnchorUnixNS: at.Add(-time.Second / 2).UnixNano(), SpanNS: 100,
				RealtimeResolutionNS: 1, MonotonicResolutionNS: 1, ConditionalBoundNS: 102, Known: true,
				Assumption: "native realtime/monotonic clocks; constant offset; " +
					"each clock's errors stay in one fixed interval of width at most its nominal resolution during the run",
			},
		})
	}
	initial.ClockCalibrations, measured.ClockCalibrations = final.ClockCalibrations, final.ClockCalibrations
	owner := bfdload.OwnerWorkloadObservation{
		Scope: "production-core-owner-workload", Config: cfg, Phase: phase, Completed: true,
		StartedAtUTC: at, ElapsedNS: int64(profile.Duration + 2*time.Second), SamplePeriodNS: int64(time.Second),
		Initial: initial, MeasurementFinal: measured, Final: final,
		Admission: assessmentAdmission(initial.Environment, profile.Sessions),
		Resources: bfdload.ResourceWindow{
			Samples: 86400, PeakRSSBytes: 64 << 20, PeakThreads: 16, PeakFDs: 2100, PeakGoroutines: 2100,
			FirstRSS: bfdload.RSSWindow{Samples: 21600, MeanBytes: 64 << 20},
			LastRSS:  bfdload.RSSWindow{Samples: 21600, MeanBytes: 64 << 20},
		},
		CPUPressure: bfdload.CPUPressureObservation{
			RequestedPercent: 80, Workers: 8, PeriodNS: int64(100 * time.Millisecond), ScheduledAtUTC: at,
			DurationNS: int64(profile.Duration), CPUTimeNS: int64(profile.Duration) * 8 / 5 * 4,
			WorkerWallTimeNS: int64(profile.Duration) * 8, Windows: 6912000, WorkChecksum: 1, Completed: true,
		},
		GCPressure: bfdload.GCPressureObservation{ScheduledAtUTC: at, DurationNS: int64(profile.Duration), Completed: true},
		SchedulerPressure: bfdload.SchedulerPressureObservation{
			ScheduledAtUTC: at, DurationNS: int64(profile.Duration), Completed: true,
		},
		SessionChurn:    bfdload.SessionChurnObservation{ScheduledAtUTC: at, Completed: true},
		SchedulerWindow: bfdload.Histogram{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{1000, 0}},
		GCPauseWindow:   bfdload.Histogram{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{100, 0}},
	}
	peers := bfdload.PeerObservation{
		Scope: "udp-peer-generator", Profile: profile, Phase: phase, StartedAtUTC: at,
		ElapsedNS: int64(profile.Duration + time.Second), Completed: true, Stopped: true,
		Environment: assessmentEnvironment(at, "/qualification/peers", 202),
	}
	for slot, endpoint := range cfg.Sessions {
		peers.Peers = append(peers.Peers, assessmentPeerHistory(slot, endpoint))
	}
	for _, window := range peers.Peers[0].BlackholeWindows {
		for slot, session := range initial.Sessions {
			witness := assessmentDownWitness(slot, session, window.StartNS, at)
			owner.DownWitnesses = append(owner.DownWitnesses, witness)
			owner.DownEvents = append(owner.DownEvents, bfdload.OwnerDownEvent{
				AtUTC: witness.AtUTC, ElapsedNS: witness.ElapsedNS,
				LocalDiscriminator: witness.LocalDiscriminator, Diagnostic: "ControlTimeExpired",
			})
		}
	}
	count := uint64(len(owner.DownWitnesses))
	owner.DownJournal = bfdload.DownJournalObservation{
		CutoffAtUTC: at.Add(profile.Duration), SourceSampledAtUTC: at.Add(profile.Duration),
		SourceTransitions: count, FinalSourceTransitions: &count, ObservedBeforeCutoff: count,
		Closed: true, Settled: true, Complete: true, IncarnationsComplete: true,
	}
	owner.Initial.Metrics = assessmentMetrics(1000, 0, 1000, "")
	owner.MeasurementFinal.Metrics = assessmentMetrics(1000000000, count, 1000000000, changed)
	owner.Final.Metrics = assessmentMetrics(1000000000, count, 1000000000, changed)
	return owner, peers
}

func assessmentEnvironment(at time.Time, member string, pid int) bfdload.Environment {
	group := bfdload.CgroupSnapshot{
		MemberPath: member, MountPoint: "/sys/fs/cgroup", MountRoot: "/",
		CPUQuotaUS: bfdload.Limit{Value: 800000}, CPUPeriodUS: 100000,
		MemoryMaxBytes: bfdload.Limit{Value: 8 << 30}, MemoryHighBytes: bfdload.Limit{Unlimited: true},
		PidsCurrent: 32, PidsMax: bfdload.Limit{Value: 8192},
		MemoryCurrentBytes: 128 << 20,
		CPUStat:            map[string]uint64{"usage_usec": 1000, "nr_periods": 1000, "nr_throttled": 0, "throttled_usec": 0},
		MemoryEvents:       map[string]uint64{"low": 0, "high": 0, "max": 0, "oom": 0, "oom_kill": 0},
	}
	return bfdload.Environment{
		StartedAtUTC: at, ElapsedNS: 1, Cgroup: &group,
		Process: bfdload.ProcessSnapshot{PID: pid, RSSBytes: 64 << 20, Threads: 16, FDs: 2100, ObserverCPUAffinity: 8},
		Runtime: bfdload.RuntimeSnapshot{
			Version: "go1.27.3", GOOS: "linux", GOARCH: "amd64", GOMAXPROCS: 8,
			Goroutines: 2100, GCPercent: 100, MemoryLimitBytes: 6 << 30,
			SchedulerLatency: bfdload.Histogram{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{1000, 0}},
			GCPauses:         bfdload.Histogram{BoundsSeconds: []string{"0", "0.001", "+Inf"}, Counts: []uint64{100, 0}},
		},
	}
}

func assessmentAdmission(environment bfdload.Environment, sessions int) *bfdload.ResourceAdmission {
	leaf := *environment.Cgroup
	parent := leaf
	parent.MemberPath = "/qualification"
	parent.CPUQuotaUS.Value, parent.MemoryMaxBytes.Value = 1600000, 24<<30
	return &bfdload.ResourceAdmission{
		Environment: environment, Ancestors: []bfdload.CgroupSnapshot{leaf, parent},
		Admitted: true, AdditionalTaskBudget: uint64(sessions) + 64,
	}
}

func assessmentOwnerSnapshot(cfg bfdload.PeerConfig, at time.Time, packets uint64) bfdload.OwnerObservation {
	count := uint64(len(cfg.Sessions))
	result := bfdload.OwnerObservation{
		Scope: "production-core-owner-component", Environment: assessmentEnvironment(at, "/qualification/owner", 101),
		TimingUncertaintyError: "total clock uncertainty is unknown; conditional model only",
		Delivery:               bfd.DeliveryCounterPairs(), TerminalPackets: packets * count,
		Registrations: bfdload.OwnerRegistrations{
			KnownPeers: len(cfg.Sessions), ActiveMetricRegistrations: count,
			RetainedSessions: count, RetainedPacketRows: 4 * count,
		},
	}
	if packets > 1 {
		result.Environment.Runtime.SchedulerLatency.Counts[0] = 2000
		result.Environment.Runtime.GCPauses.Counts[0] = 200
	}
	for index := range result.Delivery {
		row := &result.Delivery[index]
		if row.Source == bfd.DeliveryPacketOwner && row.Outcome == bfd.DeliveryCommitted {
			row.Count = packets * count
		}
	}
	discriminator, port := uint32(42), uint16(49152)
	for _, endpoint := range cfg.Sessions {
		result.Sessions = append(result.Sessions, bfdload.OwnerSessionObservation{
			Endpoint: endpoint, Generation: 1, Registered: true, LocalDiscriminator: discriminator, SourcePort: port,
			Observation: bfd.Observation{
				State: bfd.StateUp, RemoteKnown: true, RemoteState: bfd.StateUp, ObservationVersion: 1000,
			},
			Sent: packets, Received: packets, NegotiatedTXNS: int64(cfg.Profile.Interval),
			DetectionNS: int64(cfg.Profile.Interval) * int64(cfg.Profile.DetectMultiplier),
		})
		discriminator++
		port++
	}
	return result
}

func assessmentPeerHistory(slot int, endpoint bfdload.OwnerEndpoint) bfdload.PeerSessionObservation {
	if slot < 0 || slot >= 1000 {
		panic("reference evidence slot outside fixture bounds")
	}
	id := uint32(slot) + 1
	peer := bfdload.PeerSessionObservation{
		ID: uint64(id), Endpoint: endpoint, SourcePort: uint16(slot) + 49152, State: "Up",
		RemoteDiscriminator: uint32(slot) + 42, Written: 1000000, WriteAttempts: 1000000,
		RandomOmitted: 1000, Read: 1000000, ReceiveRandomOmitted: 1000, UpTransitions: 48, DownTransitions: 47,
	}
	for period := int64(1); period < 48; period++ {
		start, sequence := period*int64(30*time.Minute), uint64(period-1)*20000+101
		up := bfdload.PeerOmissionSegment{
			Direction: "tx", FirstNS: start + 20000000, LastNS: start + 180000000, Count: 3, Valid: true,
			MyDiscriminator: id, YourDiscriminator: uint32(slot) + 42, State: bfd.StateUp,
			DesiredMinTXUS: 100000, RequiredMinRXUS: 100000, DetectMult: 3,
			OffsetsNS: []int64{start + 20000000, start + 100000000, start + 180000000},
			Sequences: []uint64{sequence, sequence + 1, sequence + 2},
		}
		rx := bfdload.PeerOmissionSegment{
			Direction: "rx", FirstNS: start + 30000000, LastNS: start + 270000000, Count: 4,
			OffsetsNS: []int64{start + 30000000, start + 110000000, start + 190000000, start + 270000000},
			Sequences: []uint64{sequence, sequence + 1, sequence + 2, sequence + 3},
		}
		down := up
		down.FirstNS, down.LastNS, down.Count = start+280000000, start+280000000, 1
		down.OffsetsNS, down.Sequences = []int64{start + 280000000}, []uint64{sequence + 3}
		down.State, down.Diag = bfd.StateDown, bfd.DiagControlTimeExpired
		down.YourDiscriminator, down.DesiredMinTXUS = 0, 1000000
		peer.BlackholeWindows = append(peer.BlackholeWindows, bfdload.PeerBlackholeWindow{
			StartNS: start, FirstTXNS: up.FirstNS, LastTXNS: down.LastNS, TXOmitted: 4,
			FirstRXNS: rx.FirstNS, LastRXNS: rx.LastNS, RXOmitted: 4, Segments: []bfdload.PeerOmissionSegment{up, rx, down},
		})
		peer.BlackholeOmitted += 4
		peer.ReceiveBlackholeOmitted += 4
	}
	peer.Scheduled = peer.Written + peer.RandomOmitted + peer.BlackholeOmitted
	peer.Accepted = peer.Read - peer.ReceiveRandomOmitted - peer.ReceiveBlackholeOmitted
	return peer
}

func assessmentDownWitness(
	slot int, session bfdload.OwnerSessionObservation, start int64, at time.Time,
) bfdload.OwnerDownWitness {
	if slot < 0 || slot >= 1000 {
		panic("reference witness slot outside fixture bounds")
	}
	return bfdload.OwnerDownWitness{
		AtUTC: at.Add(time.Duration(start + 260000000)), ElapsedNS: start + 260000000,
		LocalDiscriminator: session.LocalDiscriminator, LocalAddr: session.Endpoint.Local.Addr(),
		PeerAddr: session.Endpoint.Peer.Addr(), Interface: session.Endpoint.Interface, Type: bfd.SessionTypeSingleHop,
		ObservationVersion: uint64(start/int64(30*time.Minute))*2 + 3,
		Cause:              bfd.CauseDetectionTimeout, LocalDiag: bfd.DiagControlTimeExpired,
		RemoteKnown: true, RemoteState: bfd.StateUp,
		Incarnation: bfdload.OwnerDownIncarnation{Known: true, Slot: slot, Generation: 1, Endpoint: session.Endpoint},
		Timing: bfdload.OwnerDownTiming{
			CommitKnown: true, CommitStartNS: start + 260000000, CommitEndNS: start + 260000001,
			ValidatedKnown: true, ValidatedNS: start - 50000000, DetectionKnown: true,
			DetectionNS: 300000000, DetectionDueNS: start + 250000000, ValidatedRemoteDiscriminator: uint32(slot) + 1,
		},
	}
}

func assessmentMetrics(tx, detection, received uint64, changed string) []*dto.MetricFamily {
	histName, histType := "gobfd_bfd_timer_delay_seconds", dto.MetricType_HISTOGRAM
	histograms := &dto.MetricFamily{Name: &histName, Type: &histType}
	for index, count := range []uint64{tx, detection} {
		if count == 0 {
			continue // Initial detection-effect series may legitimately be lazy/absent.
		}
		timer := "control_tx"
		baseline := uint64(1000)
		if index == 1 {
			timer, baseline = "control_detection", 0
		}
		low, high := assessmentRanks(count - baseline)
		low, high = low+baseline, high+baseline
		if changed == "tx_p99_above" && index == 0 || changed == "detection_p99_above" && index == 1 {
			low--
		}
		if changed == "tx_p999_above" && index == 0 || changed == "detection_p999_above" && index == 1 {
			high--
		}
		metric := assessmentHistogram(count, low, high)
		stageName, stage, timerName := "stage", "effect", "timer"
		metric.Label = []*dto.LabelPair{{Name: &timerName, Value: &timer}, {Name: &stageName, Value: &stage}}
		histograms.Metric = append(histograms.Metric, metric)
		dispatch := assessmentHistogram(count, low, high)
		dispatchStage := "dispatch"
		dispatch.Label = []*dto.LabelPair{{Name: &timerName, Value: &timer}, {Name: &stageName, Value: &dispatchStage}}
		histograms.Metric = append(histograms.Metric, dispatch)
	}
	counterName, counterType := "gobfd_bfd_control_rx_timing_total", dto.MetricType_COUNTER
	counters := &dto.MetricFamily{Name: &counterName, Type: &counterType}
	for _, outcome := range []string{"unavailable", "valid", "invalid", "before_calibration", "future", "clock_step"} {
		count, label := float64(0), "outcome"
		if outcome == "valid" {
			count = float64(received)
		}
		counters.Metric = append(counters.Metric, &dto.Metric{
			Label: []*dto.LabelPair{{Name: &label, Value: &outcome}}, Counter: &dto.Counter{Value: &count},
		})
	}
	rxName := "gobfd_bfd_control_rx_commit_seconds"
	rx := &dto.MetricFamily{
		Name: &rxName, Type: &histType, Metric: []*dto.Metric{assessmentHistogram(received, received, received)},
	}
	return append([]*dto.MetricFamily{histograms, counters, rx, assessmentTimerCounters(tx, detection)},
		assessmentSocketMetrics(received)...)
}

func assessmentSocketMetrics(received uint64) []*dto.MetricFamily {
	name, kind := "gobfd_bfd_socket_drops_total", dto.MetricType_COUNTER
	drops := &dto.MetricFamily{Name: &name, Type: &kind}
	sampleName := "gobfd_bfd_socket_drop_samples_total"
	samples := &dto.MetricFamily{Name: &sampleName, Type: &kind}
	for _, receiver := range []string{"control", "echo"} {
		kindLabel, value := "kind", float64(0)
		drops.Metric = append(drops.Metric, &dto.Metric{
			Label: []*dto.LabelPair{{Name: &kindLabel, Value: &receiver}}, Counter: &dto.Counter{Value: &value},
		})
		for outcome := range bfd.SocketDropOutcomeCount {
			outcomeLabel, outcomeName, count := "outcome", outcome.String(), float64(0)
			if receiver == "control" && outcome == bfd.SocketDropSample {
				count = float64(received)
			}
			samples.Metric = append(samples.Metric, &dto.Metric{
				Label:   []*dto.LabelPair{{Name: &kindLabel, Value: &receiver}, {Name: &outcomeLabel, Value: &outcomeName}},
				Counter: &dto.Counter{Value: &count},
			})
		}
	}
	return []*dto.MetricFamily{drops, samples}
}

func assessmentRanks(count uint64) (uint64, uint64) {
	// Exact ceil(99*N/100), ceil(999*N/1000), without overflowing multiplication.
	return count/100*99 + (count%100*99+99)/100, count/1000*999 + (count%1000*999+999)/1000
}

func assessmentHistogram(count, low, high uint64) *dto.Metric {
	sum := float64(count) / 100
	result := &dto.Metric{Histogram: &dto.Histogram{SampleCount: &count, SampleSum: &sum}}
	for _, bound := range []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 5} {
		cumulative := count
		switch {
		case bound < 0.025:
			cumulative = 0
		case bound < 0.05:
			cumulative = low
		case bound < 0.1:
			cumulative = high
		}
		result.Histogram.Bucket = append(result.Histogram.Bucket, &dto.Bucket{
			UpperBound: &bound, CumulativeCount: &cumulative,
		})
	}
	return result
}

func assessmentTimerCounters(tx, detection uint64) *dto.MetricFamily {
	name, kind := "gobfd_bfd_timer_events_total", dto.MetricType_COUNTER
	result := &dto.MetricFamily{Name: &name, Type: &kind}
	for _, timer := range []string{"control_tx", "control_detection", "echo_tx", "echo_detection"} {
		for _, event := range []string{"dispatch", "effect", "no_effect", "invalid_dispatch", "invalid_effect"} {
			value, timerLabel, eventLabel := float64(0), "timer", "event"
			if event == "dispatch" || event == "effect" {
				switch timer {
				case "control_tx":
					value = float64(tx)
				case "control_detection":
					value = float64(detection)
				}
			}
			result.Metric = append(result.Metric, &dto.Metric{
				Label:   []*dto.LabelPair{{Name: &timerLabel, Value: &timer}, {Name: &eventLabel, Value: &event}},
				Counter: &dto.Counter{Value: &value},
			})
		}
	}
	return result
}
