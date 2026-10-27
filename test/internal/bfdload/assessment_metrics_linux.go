//go:build linux

package bfdload

import (
	"fmt"
	"math"
	"slices"

	dto "github.com/prometheus/client_model/go"
)

const (
	assessmentControlTX        = "control_tx"
	assessmentControlDetection = "control_detection"
	assessmentDispatch         = "dispatch"
	assessmentEffect           = "effect"
	assessmentTimingValid      = "valid"
	assessmentP99LimitSeconds  = 0.025
	assessmentP999LimitSeconds = 0.05
)

// AssessedDistribution reports histogram CDF/rank evidence, never interpolated precision.
// Threshold ranks apply only to the two effect distributions, not RX or dispatch.
type AssessedDistribution struct {
	Name     string `json:"name"`
	Timer    string `json:"timer,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Samples  uint64 `json:"samples"`
	At25MS   uint64 `json:"at_25ms"`
	At50MS   uint64 `json:"at_50ms"`
	P99Rank  uint64 `json:"p99_rank,omitempty"`
	P999Rank uint64 `json:"p999_rank,omitempty"`
}

type assessmentMetricSet map[string]*dto.MetricFamily

func newAssessmentMetricSet(snapshot OwnerObservation) (assessmentMetricSet, error) {
	result := make(assessmentMetricSet)
	if len(snapshot.Metrics) == 0 || len(snapshot.Metrics) > 64 {
		return nil, fmt.Errorf("collector metric families are missing or exceed bound: %w", ErrAssessment)
	}
	for _, family := range snapshot.Metrics {
		if family == nil || family.GetName() == "" || result[family.GetName()] != nil {
			return nil, fmt.Errorf("nil or duplicate collector metric family: %w", ErrAssessment)
		}
		if err := assessmentFamilyLabels(family); err != nil {
			return nil, err
		}
		result[family.GetName()] = family
	}
	return result, nil
}

func assessmentFamilyLabels(family *dto.MetricFamily) error {
	var allowed map[string][]string
	kind := dto.MetricType_COUNTER
	switch family.GetName() {
	case "gobfd_bfd_timer_delay_seconds":
		kind = dto.MetricType_HISTOGRAM
		allowed = map[string][]string{
			"timer": {assessmentControlTX, assessmentControlDetection, "echo_tx", "echo_detection"},
			"stage": {assessmentDispatch, assessmentEffect},
		}
	case "gobfd_bfd_timer_events_total":
		allowed = map[string][]string{
			"timer": {assessmentControlTX, assessmentControlDetection, "echo_tx", "echo_detection"},
			"event": {assessmentDispatch, assessmentEffect, "no_effect", "invalid_dispatch", "invalid_effect"},
		}
	case "gobfd_bfd_control_rx_timing_total":
		allowed = map[string][]string{
			"outcome": {"unavailable", assessmentTimingValid, "invalid", "before_calibration", "future", "clock_step"},
		}
	case "gobfd_bfd_control_rx_commit_seconds":
		kind = dto.MetricType_HISTOGRAM
		allowed = map[string][]string{}
	case "gobfd_bfd_socket_drops_total":
		allowed = map[string][]string{"kind": {"control", "echo"}}
	case "gobfd_bfd_socket_drop_samples_total":
		allowed = map[string][]string{
			"kind":    {"control", "echo"},
			"outcome": {"initial", "sample", "unavailable", "invalid", "ambiguous"},
		}
	default:
		return nil // Per-session collector families retain their existing schema.
	}
	if family.Type == nil || family.GetType() != kind {
		return fmt.Errorf("fixed metric family type is invalid: %w", ErrAssessment)
	}
	seen := make(map[string]bool)
	for _, metric := range family.GetMetric() {
		key, err := assessmentLabelKey(metric, allowed)
		if err != nil || seen[key] {
			return fmt.Errorf("invalid/duplicate fixed metric labels in %s: %w", family.GetName(), ErrAssessment)
		}
		seen[key] = true
	}
	return nil
}

func assessmentLabelKey(metric *dto.Metric, allowed map[string][]string) (string, error) {
	if metric == nil || len(metric.GetLabel()) != len(allowed) {
		return "", ErrAssessment
	}
	seen := make(map[string]bool)
	var parts []string
	for _, label := range metric.GetLabel() {
		values, known := allowed[label.GetName()]
		if label == nil || !known || seen[label.GetName()] || !slices.Contains(values, label.GetValue()) {
			return "", ErrAssessment
		}
		seen[label.GetName()] = true
		parts = append(parts, label.GetName()+"="+label.GetValue())
	}
	slices.Sort(parts)
	return fmt.Sprint(parts), nil
}

func (m assessmentMetricSet) metric(name string, labels ...string) (*dto.Metric, bool, error) {
	family := m[name]
	if family == nil {
		return nil, false, nil
	}
	var result *dto.Metric
	for _, metric := range family.GetMetric() {
		if !assessmentLabelsMatch(metric, labels) {
			continue
		}
		if result != nil {
			return nil, false, fmt.Errorf("duplicate metric %s: %w", name, ErrAssessment)
		}
		result = metric
	}
	return result, result != nil, nil
}

func assessmentLabelsMatch(metric *dto.Metric, labels []string) bool {
	if metric == nil || len(metric.GetLabel())*2 != len(labels) {
		return false
	}
	for index := 0; index < len(labels); index += 2 {
		matched := false
		for _, label := range metric.GetLabel() {
			matched = matched || label.GetName() == labels[index] && label.GetValue() == labels[index+1]
		}
		if !matched {
			return false
		}
	}
	return true
}

func assessmentCounter(metric *dto.Metric) (uint64, error) {
	if metric == nil || metric.GetCounter() == nil || metric.GetCounter().Value == nil {
		return 0, fmt.Errorf("counter observation is missing: %w", ErrAssessment)
	}
	value := metric.GetCounter().GetValue()
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1<<53 || value != math.Trunc(value) {
		return 0, fmt.Errorf("counter is not an exact nonnegative integer: %w", ErrAssessment)
	}
	return uint64(value), nil
}

func assessmentCounterDelta(before, after assessmentMetricSet, name string, labels ...string) (uint64, error) {
	right, found, err := after.metric(name, labels...)
	if err != nil || !found || after[name].GetType() != dto.MetricType_COUNTER {
		return 0, fmt.Errorf("final counter %s is unavailable: %w", name, errorsForAssessment(err))
	}
	last, err := assessmentCounter(right)
	if err != nil {
		return 0, err
	}
	left, present, err := before.metric(name, labels...)
	if err != nil {
		return 0, err
	}
	if !present {
		if name == "gobfd_bfd_timer_events_total" && len(labels) == 4 && labels[1] == assessmentControlDetection {
			return last, nil
		}
		return 0, fmt.Errorf("initial counter %s is missing: %w", name, ErrAssessment)
	}
	if before[name].GetType() != dto.MetricType_COUNTER {
		return 0, ErrAssessment
	}
	first, err := assessmentCounter(left)
	if err != nil || last < first {
		return 0, fmt.Errorf("counter %s reset or is invalid: %w", name, errorsForAssessment(err))
	}
	return last - first, nil
}

func errorsForAssessment(err error) error {
	if err != nil {
		return err
	}
	return ErrAssessment
}

func (r *ProfileAssessment) assessMetrics(owner OwnerWorkloadObservation) {
	before, err := newAssessmentMetricSet(owner.Initial)
	if !r.require("initial_metrics", err) {
		return
	}
	after, err := newAssessmentMetricSet(owner.MeasurementFinal)
	if !r.require("measurement_metrics", err) {
		return
	}
	for _, timer := range []string{assessmentControlTX, assessmentControlDetection} {
		r.assessTimer(before, after, timer)
	}
	rx, err := assessmentHistogramDelta(before, after, "gobfd_bfd_control_rx_commit_seconds")
	if r.require("kernel_rx_to_owner_distribution", err) {
		rx.Name = "kernel_rx_to_owner"
		r.Distributions = append(r.Distributions, rx)
	}
	r.require("timing_validity_denominators", r.assessDenominators(before, after))
	final, err := newAssessmentMetricSet(owner.Final)
	if r.require("stopped_metrics", err) {
		r.require("measurement_to_stop_counters", assessmentMetricsContinue(after, final))
		r.require("settled_rx_denominator", assessmentSettledRX(owner.Final, final))
		r.require("settled_timer_denominators", assessmentSettledTimers(final))
	}
}

func (r *ProfileAssessment) assessTimer(before, after assessmentMetricSet, timer string) {
	for _, stage := range []string{assessmentDispatch, assessmentEffect} {
		d, err := assessmentHistogramDelta(before, after, "gobfd_bfd_timer_delay_seconds", "timer", timer, "stage", stage)
		if !r.require(timer+"_"+stage+"_distribution", err) {
			continue
		}
		d.Name, d.Timer, d.Stage = "timer_delay", timer, stage
		if stage == assessmentEffect {
			d.P99Rank, _ = assessmentRank(d.Samples, 99, 100)
			d.P999Rank, _ = assessmentRank(d.Samples, 999, 1000)
			r.limit(timer+"_p99", d.At25MS >= d.P99Rank, "p99 successful effect lateness exceeds25ms")
			r.limit(timer+"_p999", d.At50MS >= d.P999Rank, "p99.9 successful effect lateness exceeds50ms")
		}
		r.Distributions = append(r.Distributions, d)
	}
}

func (r *ProfileAssessment) assessDenominators(before, after assessmentMetricSet) error {
	if err := r.assessTimerDenominators(before, after); err != nil {
		return err
	}
	for _, outcome := range []string{
		"unavailable", assessmentTimingValid, "invalid", "before_calibration", "future", "clock_step",
	} {
		count, err := assessmentCounterDelta(before, after,
			"gobfd_bfd_control_rx_timing_total", "outcome", outcome)
		if err != nil {
			return err
		}
		r.Denominators["kernel_rx_"+outcome] = count
		if outcome != assessmentTimingValid && count != 0 {
			return fmt.Errorf("unqualified kernel RX samples prevent calibrated distribution acceptance: %w", ErrAssessment)
		}
	}
	if r.Denominators["kernel_rx_valid"] == 0 {
		return fmt.Errorf("no valid kernel RX samples: %w", ErrAssessment)
	}
	return nil
}

func (r *ProfileAssessment) assessTimerDenominators(before, after assessmentMetricSet) error {
	for _, timer := range []string{assessmentControlTX, assessmentControlDetection} {
		for _, event := range []string{
			assessmentDispatch, assessmentEffect, "no_effect", "invalid_dispatch", "invalid_effect",
		} {
			count, err := assessmentCounterDelta(before, after,
				"gobfd_bfd_timer_events_total", "timer", timer, "event", event)
			if err != nil {
				return err
			}
			r.Denominators[timer+"_"+event] = count
			if (event == "invalid_dispatch" || event == "invalid_effect") && count != 0 {
				return fmt.Errorf("invalid timer measurements cannot establish the full effect distribution: %w", ErrAssessment)
			}
		}
	}
	return nil
}

func assessmentSettledRX(owner OwnerObservation, metrics assessmentMetricSet) error {
	committed, known := committedMeasurementPackets(owner)
	if !known || committed == 0 {
		return fmt.Errorf("settled commit denominator is missing: %w", ErrAssessment)
	}
	var total, validSamples uint64
	for _, outcome := range []string{
		"unavailable", assessmentTimingValid, "invalid", "before_calibration", "future", "clock_step",
	} {
		metric, _, err := metrics.metric(
			"gobfd_bfd_control_rx_timing_total", "outcome", outcome)
		if err != nil {
			return err
		}
		count, err := assessmentCounter(metric)
		if err != nil {
			return err
		}
		var valid bool
		total, valid = addCount(total, count)
		if !valid {
			return ErrAssessment
		}
		if outcome == assessmentTimingValid {
			validSamples = count
		}
	}
	histogram, _, err := metrics.metric("gobfd_bfd_control_rx_commit_seconds")
	if total != committed || err != nil ||
		!assessmentHistogramMetric(metrics["gobfd_bfd_control_rx_commit_seconds"], histogram) ||
		histogram.GetHistogram().GetSampleCount() != validSamples {
		return fmt.Errorf("stopped RX timing outcomes differ from committed packets: %w", ErrAssessment)
	}
	return nil
}

func assessmentMetricsContinue(before, after assessmentMetricSet) error {
	for _, name := range []string{
		"gobfd_bfd_timer_delay_seconds", "gobfd_bfd_timer_events_total",
		"gobfd_bfd_control_rx_commit_seconds", "gobfd_bfd_control_rx_timing_total",
	} {
		for _, first := range before[name].GetMetric() {
			labels := make([]string, 0, 2*len(first.GetLabel()))
			for _, label := range first.GetLabel() {
				labels = append(labels, label.GetName(), label.GetValue())
			}
			last, found, err := after.metric(name, labels...)
			if err != nil || !found || !assessmentMetricContinues(first, last) {
				return fmt.Errorf("fixed metric reset between measurement freeze and joined stop: %w", ErrAssessment)
			}
		}
	}
	return nil
}

func assessmentMetricContinues(first, last *dto.Metric) bool {
	if first.GetCounter() != nil {
		left, leftErr := assessmentCounter(first)
		right, rightErr := assessmentCounter(last)
		return leftErr == nil && rightErr == nil && right >= left
	}
	if validateAssessmentHistogram(first) != nil || validateAssessmentHistogram(last) != nil {
		return false
	}
	left, right := first.GetHistogram(), last.GetHistogram()
	if right.GetSampleCount() < left.GetSampleCount() || right.GetSampleSum() < left.GetSampleSum() ||
		len(left.GetBucket()) != len(right.GetBucket()) {
		return false
	}
	for index, bucket := range left.GetBucket() {
		if right.GetBucket()[index].GetUpperBound() != bucket.GetUpperBound() ||
			right.GetBucket()[index].GetCumulativeCount() < bucket.GetCumulativeCount() {
			return false
		}
	}
	return true
}

func assessmentSettledTimers(metrics assessmentMetricSet) error {
	for _, timer := range []string{assessmentControlTX, assessmentControlDetection} {
		counts := make(map[string]uint64)
		for _, event := range []string{
			assessmentDispatch, assessmentEffect, "no_effect", "invalid_dispatch", "invalid_effect",
		} {
			metric, _, err := metrics.metric("gobfd_bfd_timer_events_total", "timer", timer, "event", event)
			if err != nil {
				return err
			}
			counts[event], err = assessmentCounter(metric)
			if err != nil {
				return err
			}
		}
		dispatches, valid := addCount(counts[assessmentDispatch], counts["invalid_dispatch"])
		if !valid || !equalPeerCount(dispatches, counts[assessmentEffect], counts["no_effect"], counts["invalid_effect"]) {
			return fmt.Errorf("stopped timer dispatch/effect outcomes do not settle: %w", ErrAssessment)
		}
		for _, stage := range []string{assessmentDispatch, assessmentEffect} {
			metric, _, err := metrics.metric("gobfd_bfd_timer_delay_seconds", "timer", timer, "stage", stage)
			if err != nil || !assessmentHistogramMetric(metrics["gobfd_bfd_timer_delay_seconds"], metric) ||
				metric.GetHistogram().GetSampleCount() != counts[stage] {
				return fmt.Errorf("stopped timer histogram differs from its validity denominator: %w", ErrAssessment)
			}
		}
	}
	return nil
}

// assessmentRank returns ceil(total*numerator/denominator) using checked, bounded remainders.
func assessmentRank(total, numerator, denominator uint64) (uint64, bool) {
	if total == 0 || numerator == 0 || numerator > denominator || denominator == 0 || denominator > 1000 {
		return 0, false
	}
	quotient, remainder := total/denominator, total%denominator
	return quotient*numerator + (remainder*numerator+denominator-1)/denominator, true
}

func assessmentHistogramDelta(
	before, after assessmentMetricSet, name string, labels ...string,
) (AssessedDistribution, error) {
	var result AssessedDistribution
	last, present, err := after.metric(name, labels...)
	if err != nil || !present || !assessmentHistogramMetric(after[name], last) {
		return result, fmt.Errorf("final histogram %s is unavailable/invalid: %w", name, ErrAssessment)
	}
	first, initial, err := before.metric(name, labels...)
	if err != nil || initial && !assessmentHistogramMetric(before[name], first) {
		return result, fmt.Errorf("initial histogram %s is invalid: %w", name, ErrAssessment)
	}
	if !initial && !assessmentLazyDetection(name, labels) {
		return result, fmt.Errorf("initial active histogram %s is missing: %w", name, ErrAssessment)
	}
	right := last.GetHistogram()
	left := &dto.Histogram{}
	if initial {
		left = first.GetHistogram()
		if len(left.GetBucket()) != len(right.GetBucket()) || left.GetSampleSum() > right.GetSampleSum() {
			return result, fmt.Errorf("histogram shape or sum reset: %w", ErrAssessment)
		}
	}
	if right.GetSampleCount() <= left.GetSampleCount() {
		return result, fmt.Errorf("empty or reset measurement histogram: %w", ErrAssessment)
	}
	result.Samples = right.GetSampleCount() - left.GetSampleCount()
	if err := assessmentBucketDelta(&result, left, right, initial); err != nil {
		return AssessedDistribution{}, err
	}
	return result, nil
}

func assessmentHistogramMetric(family *dto.MetricFamily, metric *dto.Metric) bool {
	return family.GetType() == dto.MetricType_HISTOGRAM && validateAssessmentHistogram(metric) == nil
}

func assessmentLazyDetection(name string, labels []string) bool {
	return name == "gobfd_bfd_timer_delay_seconds" && len(labels) == 4 && labels[1] == assessmentControlDetection
}

func validateAssessmentHistogram(metric *dto.Metric) error {
	if metric == nil || metric.GetHistogram() == nil || metric.GetHistogram().SampleCount == nil ||
		metric.GetHistogram().SampleSum == nil ||
		len(metric.GetHistogram().GetBucket()) == 0 || len(metric.GetHistogram().GetBucket()) > 64 {
		return ErrAssessment
	}
	h := metric.GetHistogram()
	if math.IsNaN(h.GetSampleSum()) || math.IsInf(h.GetSampleSum(), 0) || h.GetSampleSum() < 0 {
		return ErrAssessment
	}
	previous, count := math.Inf(-1), uint64(0)
	for _, bucket := range h.GetBucket() {
		if !assessmentHistogramBucket(bucket, previous, count, h.GetSampleCount()) {
			return ErrAssessment
		}
		previous, count = bucket.GetUpperBound(), bucket.GetCumulativeCount()
	}
	return nil
}

func assessmentHistogramBucket(bucket *dto.Bucket, previous float64, count, total uint64) bool {
	return bucket != nil && bucket.UpperBound != nil && bucket.CumulativeCount != nil &&
		!math.IsNaN(bucket.GetUpperBound()) && !math.IsInf(bucket.GetUpperBound(), 0) &&
		bucket.GetUpperBound() > previous && bucket.GetCumulativeCount() >= count && bucket.GetCumulativeCount() <= total
}

func assessmentBucketDelta(
	result *AssessedDistribution, left, right *dto.Histogram, initial bool,
) error {
	var prior uint64
	var found25, found50 bool
	for index, bucket := range right.GetBucket() {
		first := uint64(0)
		if initial {
			if left.GetBucket()[index].GetUpperBound() != bucket.GetUpperBound() {
				return fmt.Errorf("histogram bucket bounds changed: %w", ErrAssessment)
			}
			first = left.GetBucket()[index].GetCumulativeCount()
		}
		last := bucket.GetCumulativeCount()
		if last < first || last-first < prior || last-first > result.Samples {
			return fmt.Errorf("histogram cumulative window reset/inconsistent: %w", ErrAssessment)
		}
		prior = last - first
		if bucket.GetUpperBound() == assessmentP99LimitSeconds {
			result.At25MS, found25 = prior, true
		}
		if bucket.GetUpperBound() == assessmentP999LimitSeconds {
			result.At50MS, found50 = prior, true
		}
	}
	if !found25 || !found50 {
		return fmt.Errorf("histogram cannot resolve25ms/50ms boundaries: %w", ErrAssessment)
	}
	return nil
}
