//go:build linux

package bfdload

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestAssessmentReviewCurrentLeaf(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory_high_tightened", "pids_limit_below_current", "running_tasks_not_recharged"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			owner := assessmentReviewAdmission()
			current := *owner.MeasurementFinal.Environment.Cgroup
			owner.MeasurementFinal.Environment.Cgroup = &current
			switch name {
			case "memory_high_tightened":
				current.MemoryHighBytes = Limit{Value: 4 << 30}
			case "pids_limit_below_current":
				current.PidsMax = Limit{Value: current.PidsCurrent - 1}
			case "running_tasks_not_recharged":
				current.PidsCurrent = current.PidsMax.Value - 1
			}
			err := assessmentAdmissionEvidence(owner)
			if (err == nil) != (name == "running_tasks_not_recharged") {
				t.Fatalf("current recorded leaf %s: error=%v", name, err)
			}
		})
	}
}

func assessmentReviewAdmission() OwnerWorkloadObservation {
	leaf := CgroupSnapshot{
		MemberPath: "/qualification/owner", MountPoint: "/sys/fs/cgroup", MountRoot: "/",
		CPUQuotaUS: Limit{Value: 800000}, CPUPeriodUS: 100000,
		MemoryMaxBytes: Limit{Value: 8 << 30}, MemoryHighBytes: Limit{Unlimited: true},
		PidsCurrent: 32, PidsMax: Limit{Value: 2000},
	}
	parent := leaf
	parent.MemberPath = "/qualification"
	environment := Environment{
		Cgroup: &leaf, Process: ProcessSnapshot{ObserverCPUAffinity: 8},
		Runtime: RuntimeSnapshot{Version: "go1.27.3", GOOS: "linux", GOMAXPROCS: 8, GCPercent: 100},
	}
	return OwnerWorkloadObservation{
		Config: PeerConfig{Sessions: make([]OwnerEndpoint, 1)},
		Admission: &ResourceAdmission{
			Environment: environment, Ancestors: []CgroupSnapshot{leaf, parent},
			Admitted: true, AdditionalTaskBudget: 65,
		},
		Initial: OwnerObservation{Environment: environment}, MeasurementFinal: OwnerObservation{Environment: environment},
		Final: OwnerObservation{Environment: environment},
	}
}

func TestAssessmentReviewWorkerStartLateness(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("engineering")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)
	owner := OwnerWorkloadObservation{
		StartedAtUTC: at, Config: PeerConfig{Profile: profile},
		CPUPressure: CPUPressureObservation{
			RequestedPercent: 80, Workers: 8, PeriodNS: int64(pressurePeriod), ScheduledAtUTC: at,
			DurationNS: int64(profile.Duration), CPUTimeNS: int64(profile.Duration) * 8 / 5 * 4,
			WorkerWallTimeNS: int64(profile.Duration-time.Millisecond) * 8,
			Windows:          wholePressureWindows(profile.Duration) * 8, StartLatenessNS: int64(time.Millisecond),
			Completed: true,
		},
	}
	if err := assessmentCPU(owner); err != nil {
		t.Fatalf("valid measured duty with late actualStart was rejected: %v", err)
	}
	owner.CPUPressure.CPUTimeNS--
	if err := assessmentCPU(owner); err == nil {
		t.Fatal("late start excused insufficient measured CPU time")
	}
}

func TestAssessmentReviewCausalAssumptions(t *testing.T) {
	t.Parallel()
	owner, peers := classificationFixture(t)
	causal := ClassifyDowns(owner, peers)
	if !causal.Passing || len(causal.Assumptions) == 0 {
		t.Fatal("existing conditional causal baseline is unavailable")
	}
	report := ProfileAssessment{Status: "pass"}
	report.assessDowns(owner, peers)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Assumptions []string `json:"assumptions"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, assumption := range causal.Assumptions {
		if !slices.Contains(decoded.Assumptions, assumption) {
			t.Fatalf("assessment JSON lost causal condition %q", assumption)
		}
	}
}

func TestAssessmentRSSGrowthUsesStrictReportedMeanBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		first, last float64
		pass        bool
	}{
		{100, 100, true},
		{100, 104, true},
		{100, math.Nextafter(105, 0), true},
		{100, 105, false},
		{100, math.Nextafter(105, math.Inf(1)), false},
		{0, 1, false},
		{100, math.NaN(), false},
		{math.Inf(1), 100, false},
	} {
		if got := assessmentRSSGrowth(tc.first, tc.last); got != tc.pass {
			t.Fatalf("RSS growth(%v,%v)=%t, want %t", tc.first, tc.last, got, tc.pass)
		}
	}
}

func TestAssessmentEngineeringDoesNotInventSixHourRSSWindows(t *testing.T) {
	t.Parallel()
	owner := OwnerWorkloadObservation{
		Config:    PeerConfig{Profile: Profile{Kind: "engineering"}},
		Resources: ResourceWindow{Samples: 2, PeakRSSBytes: 1, PeakThreads: 1, PeakFDs: 1, PeakGoroutines: 1},
	}
	r := ProfileAssessment{Status: "pass"}
	r.assessResources(owner)
	if r.Status != "pass" || r.Checks[len(r.Checks)-1].Name != "six_hour_rss_growth" ||
		r.Checks[len(r.Checks)-1].Status != "not_applicable" {
		t.Fatalf("30-minute engineering windows were treated as six-hour proof: %+v", r)
	}
	owner.Config.Profile.Kind = "reference"
	r = ProfileAssessment{Status: "pass"}
	r.assessResources(owner)
	if r.Status != "incomplete" {
		t.Fatal("reference accepted missing six-hour windows")
	}
}

func TestAssessmentCPURequiresMeasuredDutyAndAllowsXORCancellation(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("engineering")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)
	owner := OwnerWorkloadObservation{
		StartedAtUTC: at, Config: PeerConfig{Profile: profile},
		CPUPressure: CPUPressureObservation{
			RequestedPercent: 80, Workers: 8, PeriodNS: int64(pressurePeriod), ScheduledAtUTC: at,
			DurationNS: int64(profile.Duration), CPUTimeNS: int64(profile.Duration) * 8 / 5 * 4,
			WorkerWallTimeNS: int64(profile.Duration) * 8, Windows: wholePressureWindows(profile.Duration) * 8,
			Completed: true, WorkChecksum: 0, // Joined XOR can be zero even when all eight workers ran.
		},
	}
	if err := assessmentCPU(owner); err != nil {
		t.Fatal(err)
	}
	owner.CPUPressure.CPUTimeNS--
	if err := assessmentCPU(owner); err == nil {
		t.Fatal("requested80percent substituted for measured generated duty")
	}
	owner.CPUPressure.CPUTimeNS++
	owner.CPUPressure.MissedWindows = 1
	if err := assessmentCPU(owner); err == nil {
		t.Fatal("missed CPU budget was silently hidden")
	}
}

func TestAssessmentCalibrationChecksRecordedModelWithoutRebuildingMonotonicTime(t *testing.T) {
	t.Parallel()
	// The public scalar model is independently checked. This does not call CoveredBy on replayed JSON.
	clock := bfd.NewReceiveClock().Calibration()
	if !clock.Known {
		t.Fatal(clock.UnknownReason)
	}
	from, to := time.Unix(0, clock.AnchorUnixNS).Add(-time.Second), time.Unix(0, clock.AnchorUnixNS).Add(time.Second)
	if bound, err := assessmentCalibration(clock, from, to); err != nil || bound == 0 {
		t.Fatalf("measured scalar model unavailable: bound=%d error=%v", bound, err)
	}
	for _, name := range []string{"unknown", "revoked", "assumption", "overflow", "bound_mismatch", "uncovered"} {
		copyClock := clock
		switch name {
		case "unknown":
			copyClock.Known = false
		case "revoked":
			copyClock.Revoked = true
		case "assumption":
			copyClock.Assumption = "nominal resolution alone proves accuracy"
		case "overflow":
			copyClock.SpanNS = math.MaxInt64
		case "bound_mismatch":
			copyClock.ConditionalBoundNS++
		case "uncovered":
			copyClock.AnchorUnixNS = from.Add(-time.Second).UnixNano()
		}
		if _, err := assessmentCalibration(copyClock, from, to); err == nil {
			t.Fatalf("accepted %s clock metadata", name)
		}
	}
}

func TestAssessmentEmptyEvidenceIsIncompleteAndNeverQualified(t *testing.T) {
	t.Parallel()
	report := AssessProfile(OwnerWorkloadObservation{}, PeerObservation{})
	if report.Status != "incomplete" || report.ReferenceQualified || report.TimingUncertaintyNS != nil ||
		!report.Conditional || report.Error == "" {
		t.Fatalf("empty input produced qualification: %+v", report)
	}
}
