//go:build linux

package bfdload

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// ErrAssessment means conditional numeric acceptance failed or evidence is incomplete.
var ErrAssessment = errors.New("conditional workload profile assessment rejected")

const assessmentStoppedSnapshotIndex = 2

// AssessmentCheck is one bounded, named predicate, not an operator waiver.
type AssessmentCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// AssessedDowns retains the causal denominator without duplicating its bounded journal.
type AssessedDowns struct {
	Complete    bool   `json:"complete"`
	Eligible    uint64 `json:"eligible"`
	Outside     uint64 `json:"outside"`
	Intentional uint64 `json:"intentional"`
	False       uint64 `json:"false"`
	Unresolved  uint64 `json:"unresolved"`
}

// ProfileAssessment evaluates measured inputs under the stated software-clock model.
// A numeric pass never qualifies the native runner, required variant matrix or release.
type ProfileAssessment struct {
	Scope                          string                 `json:"scope"`
	Status                         string                 `json:"status"`
	Profile                        Profile                `json:"profile"`
	Conditional                    bool                   `json:"conditional"`
	Assumptions                    []string               `json:"assumptions"`
	ReferenceQualified             bool                   `json:"reference_qualified"`
	TimingUncertaintyNS            *uint64                `json:"timing_uncertainty_ns"`
	TimingUncertaintyError         string                 `json:"timing_uncertainty_error"`
	ConditionalTimingUncertaintyNS *uint64                `json:"conditional_timing_uncertainty_ns"`
	Checks                         []AssessmentCheck      `json:"checks"`
	Distributions                  []AssessedDistribution `json:"distributions"`
	Denominators                   map[string]uint64      `json:"denominators"`
	Downs                          AssessedDowns          `json:"downs"`
	Error                          string                 `json:"error,omitempty"`
}

// AssessProfile consumes producer-recorded observations; it never re-creates monotonic provenance from JSON.
func AssessProfile(owner OwnerWorkloadObservation, peers PeerObservation) ProfileAssessment {
	r := ProfileAssessment{
		Scope: "conditional-workload-profile-assessment", Status: "pass", Profile: owner.Config.Profile,
		Conditional: true, Denominators: make(map[string]uint64),
		TimingUncertaintyError: "total physical/transport uncertainty is unknown; numeric acceptance is conditional",
		Assumptions: []string{
			"native realtime/monotonic clocks with constant offset; " +
				"fixed per-clock error intervals of width at most nominal resolution",
			"producer-recorded process-local calibration coverage; serialized metadata does not recreate that provenance",
			"native guest, clock discipline, source provenance, required variants and release acceptance are separate gates",
		},
	}
	if !r.require("profile_and_roles", assessmentInputs(owner, peers)) {
		return r
	}
	r.require("resource_admission", assessmentAdmissionEvidence(owner))
	bound, clockErr := assessmentClock(owner, peers)
	if r.require("conditional_clock", clockErr) {
		r.ConditionalTimingUncertaintyNS = &bound
	}
	r.require("settled_packet_ledger", assessmentPackets(owner, peers))
	r.assessDowns(owner, peers)
	r.assessResources(owner)
	r.require("measured_cpu_pressure", assessmentCPU(owner))
	r.require("requested_stress_variants", assessmentVariants(owner))
	r.assessMetrics(owner)
	return r
}

func (r *ProfileAssessment) require(name string, err error) bool {
	row := AssessmentCheck{Name: name, Status: "pass"}
	if err != nil {
		row.Status, row.Reason = "incomplete", err.Error()
		r.Status, r.Error = "incomplete", "one or more required observations are unavailable or inconsistent"
	}
	r.Checks = append(r.Checks, row)
	return err == nil
}

func (r *ProfileAssessment) limit(name string, passed bool, reason string) {
	row := AssessmentCheck{Name: name, Status: "pass"}
	if !passed {
		row.Status, row.Reason = "fail", reason
		if r.Status == "pass" {
			r.Status, r.Error = "fail", "one or more measured profile limits were exceeded"
		}
	}
	r.Checks = append(r.Checks, row)
}

func assessmentInputs(owner OwnerWorkloadObservation, peers PeerObservation) error {
	p := owner.Config.Profile
	if p.Kind != "reference" && p.Kind != "engineering" {
		return fmt.Errorf("only canonical reference/engineering inputs are assessable: %w", ErrAssessment)
	}
	if err := owner.Config.Validate(); err != nil {
		return fmt.Errorf("canonical manifest: %w", err)
	}
	if !assessmentRoleSnapshots(owner, peers) {
		return fmt.Errorf("joined roles, measurement snapshots or shared manifest/phase are missing: %w", ErrAssessment)
	}
	if !assessmentWindowEvidence(owner, peers) {
		return fmt.Errorf("observation duration, epoch or resource sample period differs: %w", ErrAssessment)
	}
	var ipv4, ipv6 bool
	port := uint16(3784)
	if p.Echo {
		port = 3785
	}
	for _, endpoint := range owner.Config.Sessions {
		if endpoint.Local.Port() != port || endpoint.Peer.Port() != port {
			return fmt.Errorf("qualification uses workload port%d: %w", port, ErrAssessment)
		}
		ipv4, ipv6 = ipv4 || endpoint.Local.Addr().Is4(), ipv6 || endpoint.Local.Addr().Is6()
	}
	if !ipv4 || !ipv6 {
		return fmt.Errorf("mixed IPv4/IPv6 manifest is required: %w", ErrAssessment)
	}
	return nil
}

func assessmentWindowEvidence(owner OwnerWorkloadObservation, peers PeerObservation) bool {
	return owner.ElapsedNS >= int64(owner.Config.Profile.Duration) &&
		peers.ElapsedNS >= int64(owner.Config.Profile.Duration) && owner.StartedAtUTC.Equal(peers.StartedAtUTC) &&
		owner.SamplePeriodNS == int64(ownerSamplePeriod)
}

func assessmentRoleSnapshots(owner OwnerWorkloadObservation, peers PeerObservation) bool {
	return owner.Scope == "production-core-owner-workload" && peers.Scope == "udp-peer-generator" &&
		joinedClassificationRoles(owner, peers) && matchingClassificationManifest(owner, peers) &&
		matchingClassificationPhases(owner.Phase, peers.Phase) && owner.MeasurementFinal.Scope == owner.Initial.Scope &&
		owner.Initial.Scope == "production-core-owner-component" && !owner.MeasurementFinal.Stopped &&
		!owner.Initial.Stopped && owner.Final.Scope == owner.Initial.Scope && owner.Config.ControlDirectory != ""
}

func assessmentAdmissionEvidence(owner OwnerWorkloadObservation) error {
	a := owner.Admission
	if a == nil || !a.Admitted || a.Environment.Cgroup == nil || a.Environment.Cgroup.MountRoot != "/" ||
		a.Environment.Cgroup.MemberPath == "/" ||
		a.AdditionalTaskBudget != uint64(len(owner.Config.Sessions))+baselineTaskMargin {
		return fmt.Errorf("admission or exposed ancestor budget is missing: %w", ErrAssessment)
	}
	if err := validateReferenceResources(a.Environment, a.Ancestors, a.AdditionalTaskBudget); err != nil {
		return err
	}
	for _, snapshot := range []OwnerObservation{owner.Initial, owner.MeasurementFinal, owner.Final} {
		e := snapshot.Environment
		if err := validateReferenceResources(e, a.Ancestors, a.AdditionalTaskBudget); err != nil {
			return err
		}
		if !assessmentAdmittedIdentity(e, *a.Environment.Cgroup, a.Ancestors[0]) {
			return fmt.Errorf("observed owner resource identity differs from admission: %w", ErrAssessment)
		}
	}
	return nil
}

func assessmentAdmittedIdentity(e Environment, observed, admitted CgroupSnapshot) bool {
	return e.Cgroup != nil && e.Cgroup.MemberPath == observed.MemberPath &&
		e.Cgroup.CPUQuotaUS == admitted.CPUQuotaUS && e.Cgroup.CPUPeriodUS == admitted.CPUPeriodUS &&
		e.Cgroup.MemoryMaxBytes == admitted.MemoryMaxBytes && referenceAncestorFits(*e.Cgroup) &&
		referenceTasksFit(*e.Cgroup, 0)
}

func assessmentClock(owner OwnerWorkloadObservation, peers PeerObservation) (uint64, error) {
	f := owner.Final
	if !assessmentRawClaims(owner, peers) || !assessmentRecordedClock(f) {
		return 0, fmt.Errorf("missing conditional clock receipt or stale total/reference claim: %w", ErrAssessment)
	}
	bindings := make(map[string]bool)
	for _, endpoint := range owner.Config.Sessions {
		bindings[endpoint.Local.String()] = false
	}
	var maximum uint64
	for _, clock := range f.ClockCalibrations {
		key := clock.Binding.String()
		seen, expected := bindings[key]
		if !expected || seen {
			return 0, fmt.Errorf("unknown, duplicate, revoked or inconsistent listener calibration: %w", ErrAssessment)
		}
		bound, err := assessmentCalibration(clock.Calibration, f.ClockEpoch.StartedAtUTC, f.StoppedAtUTC)
		if err != nil {
			return 0, err
		}
		bindings[key], maximum = true, max(maximum, bound)
	}
	for _, seen := range bindings {
		if !seen {
			return 0, fmt.Errorf("listener calibration is missing: %w", ErrAssessment)
		}
	}
	if maximum == 0 || maximum != *f.ConditionalTimingUncertaintyNS ||
		f.StoppedAtUTC.Before(owner.StartedAtUTC) || f.StoppedAtUTC.After(f.ClockEpoch.FinishedAtUTC) {
		return 0, fmt.Errorf("producer-recorded bound or clock window is inconsistent: %w", ErrAssessment)
	}
	return maximum, nil
}

func assessmentRawClaims(owner OwnerWorkloadObservation, peers PeerObservation) bool {
	return !owner.ReferenceQualified && !peers.ReferenceQualified && !owner.Initial.ReferenceQualified &&
		!owner.MeasurementFinal.ReferenceQualified && !owner.Final.ReferenceQualified &&
		owner.Initial.TimingUncertaintyNS == nil && owner.MeasurementFinal.TimingUncertaintyNS == nil &&
		owner.Final.TimingUncertaintyNS == nil
}

func assessmentRecordedClock(f OwnerObservation) bool {
	return f.ConditionalTimingUncertaintyNS != nil && f.TimingUncertaintyError != "" && f.ClockEpoch != nil &&
		f.ClockEpoch.supportsConditionalBound()
}

func assessmentCalibration(c bfd.ReceiveCalibration, from, to time.Time) (uint64, error) {
	_, bound, valid := c.ConditionalInterval(0)
	if !valid || bound < 0 || c.AnchorUnixNS < from.UnixNano() || c.AnchorUnixNS > to.UnixNano() {
		return 0, fmt.Errorf("listener calibration is unknown, revoked or inconsistent: %w", ErrAssessment)
	}
	return uint64(bound), nil
}

func assessmentPackets(owner OwnerWorkloadObservation, peers PeerObservation) error {
	if !assessmentIndependentPeers(owner, peers) {
		return fmt.Errorf("independent joined roles or final sessions are missing: %w", ErrAssessment)
	}
	if owner.Config.Profile.Echo {
		if err := assessmentEchoTransmits(owner, peers); err != nil {
			return err
		}
	}
	var written uint64
	for index, peer := range peers.Peers {
		var valid bool
		written, valid = addCount(written, peer.Written)
		if !valid || !assessmentPeerProgress(peer, peers.Profile) {
			return fmt.Errorf("peer writes, recovery or ledger is invalid: %w", ErrAssessment)
		}
		for position, snapshot := range []OwnerObservation{owner.Initial, owner.MeasurementFinal, owner.Final} {
			session := snapshot.Sessions[index]
			if !assessmentSessionProgress(session, owner.Config.Sessions[index], position < 2, owner.Config.Profile.Echo) {
				return fmt.Errorf("owner session has no settled bilateral Up/progress: %w", ErrAssessment)
			}
		}
	}
	if err := assessmentRegistrations(owner); err != nil {
		return err
	}
	terminal, err := terminalPacketCount(owner.Final)
	if err != nil || written == 0 || terminal != written || owner.Final.TerminalPackets != terminal {
		return fmt.Errorf("final terminal ledger differs from successful peer writes: %w", errors.Join(err, ErrAssessment))
	}
	return nil
}

func assessmentEchoTransmits(owner OwnerWorkloadObservation, peers PeerObservation) error {
	sent, read := owner.Final.RetiredSent, uint64(0)
	for index, peer := range peers.Peers {
		var sentKnown, readKnown bool
		sent, sentKnown = addCount(sent, owner.Final.Sessions[index].Sent)
		read, readKnown = addCount(read, peer.Read)
		if !sentKnown || !readKnown {
			return fmt.Errorf("echo input/transmit counter overflow: %w", ErrAssessment)
		}
	}
	if read == 0 || sent < read {
		return fmt.Errorf("echo peer inputs exceed actual owner transmits: %w", ErrAssessment)
	}
	return nil
}

func assessmentIndependentPeers(owner OwnerWorkloadObservation, peers PeerObservation) bool {
	o, p := owner.Final.Environment, peers.Environment
	return len(owner.Final.Sessions) == len(owner.Config.Sessions) &&
		len(owner.MeasurementFinal.Sessions) == len(owner.Config.Sessions) && p.Process.PID > 0 && o.Process.PID > 0 &&
		p.Runtime.Version == o.Runtime.Version && p.Runtime.GOOS == "linux" && p.Runtime.GOARCH == o.Runtime.GOARCH &&
		p.Runtime.GOMAXPROCS > 0 && p.Cgroup != nil && o.Cgroup != nil && p.Cgroup.MemberPath != o.Cgroup.MemberPath
}

func assessmentPeerProgress(peer PeerSessionObservation, profile Profile) bool {
	if profile.Echo {
		return peer.Written > 0 && peer.Read > 0 && validEchoPeerLedger(peer) && validatePeerHistory(peer, profile) == nil
	}
	return peer.Written > 0 && peer.Read > 0 && peer.WriteErrors == 0 && peer.ReadErrors == 0 &&
		peer.UpTransitions >= 2 && peer.State == "Up" && validatePeerHistory(peer, profile) == nil
}

func assessmentSessionProgress(s OwnerSessionObservation, endpoint OwnerEndpoint, registered, echo bool) bool {
	remote := s.Observation.RemoteKnown && s.Observation.RemoteState == bfd.StateUp
	if echo {
		remote = !s.Observation.RemoteKnown
	}
	return s.Endpoint == endpoint && s.Observation.State == bfd.StateUp && remote &&
		s.Sent > 0 && s.Received > 0 && s.SourcePort >= 49152 &&
		s.Registered == registered && s.Generation > 0 && s.LocalDiscriminator != 0
}

func assessmentRegistrations(owner OwnerWorkloadObservation) error {
	n := uint64(len(owner.Config.Sessions))
	for index, snapshot := range []OwnerObservation{owner.Initial, owner.MeasurementFinal, owner.Final} {
		known := n
		if index == assessmentStoppedSnapshotIndex {
			known = 0
		}
		r := snapshot.Registrations
		if r.KnownPeers < 0 || uint64(r.KnownPeers) != known || r.ActiveMetricRegistrations != known ||
			r.RetainedSessions != n || r.RetainedPacketRows != 4*n {
			return fmt.Errorf("active registrations and fixed-manifest retained series disagree: %w", ErrAssessment)
		}
	}
	return nil
}

func (r *ProfileAssessment) assessDowns(owner OwnerWorkloadObservation, peers PeerObservation) {
	c := ClassifyDowns(owner, peers)
	r.Assumptions = append(r.Assumptions, c.Assumptions...)
	r.Downs = AssessedDowns{c.Complete, c.Eligible, c.Outside, c.Intentional, c.False, c.Unresolved}
	if !r.require("complete_down_evidence", assessmentDownCompleteness(c)) {
		return
	}
	if owner.Config.Profile.Echo && c.Unresolved > 0 {
		r.require("resolved_echo_down_evidence", fmt.Errorf("unresolved Echo old-deadline evidence: %w", ErrAssessment))
		return
	}
	r.limit("intentional_downs_only", c.Passing, "false or unresolved Down prevents numeric acceptance")
}

func assessmentDownCompleteness(c DownClassification) error {
	if !c.Complete || c.Eligible == 0 || c.Error != "" {
		return fmt.Errorf("empty or incomplete source-settled Down evidence: %w", ErrAssessment)
	}
	return nil
}

func (r *ProfileAssessment) assessResources(owner OwnerWorkloadObservation) {
	w, n := owner.Resources, uint64(len(owner.Config.Sessions))
	if !r.require("sampled_resources", assessmentResourceSamples(owner)) {
		return
	}
	r.limit("threads", w.PeakThreads <= 2*referenceCPUs+32, "settled sampled thread peak exceeds48")
	r.limit("file_descriptors", w.PeakFDs <= 2*n+256, "settled sampled FD peak exceeds2N+256")
	r.limit("goroutines", w.PeakGoroutines <= 3*len(owner.Config.Sessions)+256,
		"settled sampled goroutine peak exceeds3N+256")
	r.limit("rss", w.PeakRSSBytes <= 512<<20, "settled sampled RSS peak exceeds512MiB")
	if owner.Config.Profile.Kind == "engineering" {
		r.Checks = append(r.Checks, AssessmentCheck{
			Name: "six_hour_rss_growth", Status: "not_applicable",
			Reason: "30-minute engineering run has no disjoint six-hour windows",
		})
		return
	}
	r.limit("six_hour_rss_growth", assessmentRSSGrowth(w.FirstRSS.MeanBytes, w.LastRSS.MeanBytes),
		"reference first/final six-hour mean RSS growth is not strictly below5percent")
}

func assessmentResourceSamples(owner OwnerWorkloadObservation) error {
	w := owner.Resources
	if w.Samples == 0 || w.PeakThreads == 0 || w.PeakFDs == 0 || w.PeakGoroutines <= 0 || w.PeakRSSBytes == 0 {
		return fmt.Errorf("resource peak samples are empty: %w", ErrAssessment)
	}
	if owner.Config.Profile.Kind == "reference" && (!validRSSWindow(w.FirstRSS) || !validRSSWindow(w.LastRSS)) {
		return fmt.Errorf("reference six-hour RSS windows are unavailable: %w", ErrAssessment)
	}
	return nil
}

func validRSSWindow(w RSSWindow) bool {
	return w.Samples > 0 && w.MeanBytes > 0 && !math.IsNaN(w.MeanBytes) && !math.IsInf(w.MeanBytes, 0)
}

func assessmentRSSGrowth(first, last float64) bool {
	if !validRSSWindow(RSSWindow{Samples: 1, MeanBytes: first}) ||
		!validRSSWindow(RSSWindow{Samples: 1, MeanBytes: last}) {
		return false
	}
	// Compare the exact represented means; the underlying RSS samples remain estimates.
	left := new(big.Rat).Mul(new(big.Rat).SetFloat64(last), big.NewRat(100, 1))
	right := new(big.Rat).Mul(new(big.Rat).SetFloat64(first), big.NewRat(105, 1))
	return left.Cmp(right) < 0
}

func assessmentCPU(owner OwnerWorkloadObservation) error {
	p, c := owner.Config.Profile, owner.CPUPressure
	duration := int64(p.Duration)
	if !assessmentCPUIdentity(owner) {
		return fmt.Errorf("CPU duty lacks completed, correctly scoped worker measurements: %w", ErrAssessment)
	}
	if c.StartLatenessNS < 0 || c.StartLatenessNS >= duration || c.EndLatenessNS < 0 {
		return fmt.Errorf("CPU worker lateness is outside the measured window: %w", ErrAssessment)
	}
	wall := duration * referenceCPUs
	// Individual starts are no later than the recorded maximum; do not treat it as their sum.
	minimumWall := (duration - c.StartLatenessNS) * referenceCPUs
	if c.Windows != wholePressureWindows(p.Duration)*referenceCPUs || c.CPUTimeNS < wall/5*4 ||
		c.WorkerWallTimeNS < minimumWall || c.CPUTimeNS > c.WorkerWallTimeNS {
		return fmt.Errorf("measured CPU time/windows do not establish generated80percent duty: %w", ErrAssessment)
	}
	return nil
}

func assessmentCPUIdentity(owner OwnerWorkloadObservation) bool {
	p, c := owner.Config.Profile, owner.CPUPressure
	return c.Completed && c.RequestedPercent == p.CPULoadPercent && c.RequestedPercent == 80 &&
		c.Workers == referenceCPUs && c.PeriodNS == int64(pressurePeriod) && c.DurationNS == int64(p.Duration) &&
		c.ScheduledAtUTC.Equal(owner.StartedAtUTC) && c.MissedWindows == 0 &&
		p.Duration > 0 && p.Duration <= time.Duration(math.MaxInt64/referenceCPUs)
}

func assessmentVariants(owner OwnerWorkloadObservation) error {
	p := owner.Config.Profile
	if owner.GCPressure.Enabled != p.GCPressure || owner.SchedulerPressure.Enabled != p.SchedulerPressure ||
		owner.SessionChurn.Enabled != p.SessionChurn {
		return fmt.Errorf("requested stress variant differs from observations: %w", ErrAssessment)
	}
	if p.GCPressure {
		if err := assessmentGC(owner); err != nil {
			return err
		}
	}
	if p.SchedulerPressure && !assessmentScheduler(owner) {
		return fmt.Errorf("scheduler variant has no joined actual contention: %w", ErrAssessment)
	}
	if p.SessionChurn {
		return assessmentChurn(owner)
	}
	return nil
}

func assessmentScheduler(owner OwnerWorkloadObservation) bool {
	s := owner.SchedulerPressure
	return s.Completed && s.Workers == referenceCPUs*schedulerWorkersPerCPU &&
		s.JoinedWorkers == s.Workers && s.Rounds > 0 && s.Yields == s.Rounds && s.Windows > 0 &&
		s.DurationNS == int64(owner.Config.Profile.Duration) && s.PeriodNS == int64(pressurePeriod) &&
		s.ScheduledAtUTC.Equal(owner.StartedAtUTC)
}

func assessmentGC(owner OwnerWorkloadObservation) error {
	if !assessmentGCIdentity(owner) || !assessmentGCActivity(owner.GCPressure) ||
		owner.MeasurementFinal.Environment.Runtime.GCCycles <= owner.Initial.Environment.Runtime.GCCycles {
		return fmt.Errorf("GC variant has no consistent completed allocation/cycle/release evidence: %w", ErrAssessment)
	}
	return nil
}

func assessmentGCIdentity(owner OwnerWorkloadObservation) bool {
	g := owner.GCPressure
	return g.Completed && g.Workers == 1 && g.DurationNS == int64(owner.Config.Profile.Duration) &&
		g.PeriodNS == int64(pressurePeriod) && g.ScheduledAtUTC.Equal(owner.StartedAtUTC)
}

func assessmentGCActivity(g GCPressureObservation) bool {
	return g.AllocatedBytes > 0 && g.Batches > 0 && g.Objects > 0 && g.Objects <= math.MaxUint64/gcChunkBytes &&
		g.AllocatedBytes == g.Objects*gcChunkBytes && g.PeakRetainedObjects > 0 &&
		g.PeakRetainedObjects <= gcRetainedObjects &&
		g.ReleasedObjects == g.PeakRetainedObjects && g.RetainedLimitBytes == gcChunkBytes*gcRetainedObjects &&
		g.BytesPerBatch == gcChunkBytes*gcObjectsPerBatch
}

func assessmentChurn(owner OwnerWorkloadObservation) error {
	c := owner.SessionChurn
	if !assessmentChurnHeader(owner) {
		return fmt.Errorf("churn variant lacks joined release/recovery/cardinality evidence: %w", ErrAssessment)
	}
	var recovered, skipped uint64
	for _, event := range c.Events {
		if !assessmentChurnIdentity(event, owner.Config) {
			return fmt.Errorf("churn event identity/phase is invalid: %w", ErrAssessment)
		}
		if event.SkippedNotUp {
			skipped++
			continue
		}
		if !assessmentChurnEvent(event, owner.Config.Profile.Sessions) {
			return fmt.Errorf("churn event lacks scalar lifecycle/cardinality evidence: %w", ErrAssessment)
		}
		recovered++
	}
	if recovered != c.Recovered || skipped != c.SkippedNotUp {
		return fmt.Errorf("churn counters differ from bounded events: %w", ErrAssessment)
	}
	return nil
}

func assessmentChurnHeader(owner OwnerWorkloadObservation) bool {
	c := owner.SessionChurn
	return c.Completed && c.Joined && c.Created > 0 && c.Released == c.Created && c.Recovered == c.Created &&
		c.EventsOmitted == 0 && len(c.Events) > 0 && len(c.Events) <= maxSessionChurnEvents &&
		c.ScheduledAtUTC.Equal(owner.StartedAtUTC) && c.PeriodNS == int64(sessionChurnPeriod) &&
		c.GapNS == int64(sessionChurnGap) && c.RecoveryTimeoutNS == int64(sessionChurnBudget)
}

func assessmentChurnIdentity(e SessionChurnEvent, cfg PeerConfig) bool {
	return e.Error == "" && e.Slot >= 0 && e.Slot < cfg.Profile.Sessions &&
		e.Endpoint == cfg.Sessions[e.Slot] && e.ElapsedNS >= 0 && e.ElapsedNS < int64(cfg.Profile.Duration)
}

func assessmentChurnEvent(e SessionChurnEvent, sessions int) bool {
	if e.Before == nil || e.Absent == nil || e.Restored == nil || e.OldGeneration == 0 ||
		e.OldGeneration == math.MaxUint64 || e.NewGeneration != e.OldGeneration+1 ||
		e.OldDiscriminator == 0 || e.NewDiscriminator == 0 || e.OldSourcePort < 49152 || e.NewSourcePort < 49152 ||
		!assessmentChurnChronology(e) {
		return false
	}
	return assessmentChurnCardinality(e, sessions)
}

func assessmentChurnChronology(e SessionChurnEvent) bool {
	return !e.ReleaseStartedAtUTC.IsZero() && !e.ReleasedAtUTC.Before(e.ReleaseStartedAtUTC) &&
		!e.CreatedAtUTC.Before(e.ReleasedAtUTC) && !e.RecoveredAtUTC.Before(e.CreatedAtUTC)
}

func assessmentChurnCardinality(e SessionChurnEvent, sessions int) bool {
	if sessions < 1 || sessions > sourcePortCapacity {
		return false
	}
	n := uint64(sessions)
	return e.Before.KnownPeers == sessions && e.Before.ActiveMetricRegistrations == n &&
		e.Absent.KnownPeers == sessions-1 && e.Absent.ActiveMetricRegistrations == n-1 &&
		e.Restored.KnownPeers == sessions && e.Restored.ActiveMetricRegistrations == n &&
		e.Before.RetainedSessions == n && e.Absent.RetainedSessions == n && e.Restored.RetainedSessions == n &&
		e.Before.RetainedPacketRows == 4*n && e.Absent.RetainedPacketRows == 4*n && e.Restored.RetainedPacketRows == 4*n
}
