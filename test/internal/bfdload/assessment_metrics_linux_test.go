//go:build linux

package bfdload

import (
	"math"
	"math/big"
	"math/rand/v2"
	"reflect"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestAssessmentRankMatchesIndependentBigIntegerOracle(t *testing.T) {
	t.Parallel()
	const randomCases = 1024
	edges := []uint64{1, 2, 99, 100, 101, 999, 1000, 1001, 1 << 53, math.MaxUint64 - 1, math.MaxUint64}
	values := make([]uint64, 0, len(edges)+randomCases)
	values = append(values, edges...)
	// #nosec G404 -- Reproducible arithmetic corpus, never security randomness.
	random := rand.New(rand.NewPCG(27, 1000))
	for range randomCases {
		values = append(values, random.Uint64()|1)
	}
	for _, ratio := range [][2]uint64{{99, 100}, {999, 1000}, {1, 1}, {1, 1000}, {1000, 1000}} {
		for _, total := range values {
			got, valid := assessmentRank(total, ratio[0], ratio[1])
			product := new(big.Int).Mul(new(big.Int).SetUint64(total), new(big.Int).SetUint64(ratio[0]))
			product.Add(product, new(big.Int).SetUint64(ratio[1]-1))
			want := product.Quo(product, new(big.Int).SetUint64(ratio[1])).Uint64()
			if !valid || got != want || got == 0 || got > total {
				t.Fatalf("rank(%d,%d/%d)=%d valid=%t, oracle=%d", total, ratio[0], ratio[1], got, valid, want)
			}
			if total < math.MaxUint64 {
				next, _ := assessmentRank(total+1, ratio[0], ratio[1])
				if next < got || next-got > 1 {
					t.Fatalf("rank is not monotone one-observation progress: %d -> %d", got, next)
				}
			}
		}
	}
	for _, args := range [][3]uint64{{0, 99, 100}, {1, 0, 100}, {1, 101, 100}, {1, 1, 0}, {1, 1, 1001}} {
		if _, valid := assessmentRank(args[0], args[1], args[2]); valid {
			t.Fatalf("invalid rank accepted: %v", args)
		}
	}
}

func assessmentTestHistogram(count, at25, at50 uint64) assessmentMetricSet {
	name, kind, timerName, stageName := "gobfd_bfd_timer_delay_seconds", dto.MetricType_HISTOGRAM, "timer", "stage"
	timer, stage, sum := "control_detection", "effect", float64(count)/100
	h := &dto.Histogram{SampleCount: &count, SampleSum: &sum}
	for index, bound := range []float64{0.025, 0.05, 0.1} {
		cumulative := []uint64{at25, at50, count}[index]
		h.Bucket = append(h.Bucket, &dto.Bucket{UpperBound: &bound, CumulativeCount: &cumulative})
	}
	return assessmentMetricSet{name: {
		Name: &name, Type: &kind, Metric: []*dto.Metric{{
			Label: []*dto.LabelPair{{Name: &timerName, Value: &timer}, {Name: &stageName, Value: &stage}}, Histogram: h,
		}},
	}}
}

func TestAssessmentHistogramUsesExactCDFAndDoesNotMutateInputs(t *testing.T) {
	t.Parallel()
	before, after := assessmentTestHistogram(10, 10, 10), assessmentTestHistogram(1010, 1000, 1009)
	originalBefore, originalAfter := assessmentTestHistogram(10, 10, 10), assessmentTestHistogram(1010, 1000, 1009)
	labels := []string{"timer", "control_detection", "stage", "effect"}
	first, err := assessmentHistogramDelta(before, after, "gobfd_bfd_timer_delay_seconds", labels...)
	if err != nil || first.Samples != 1000 || first.At25MS != 990 || first.At50MS != 999 {
		t.Fatalf("lost exact CDF boundary: %+v error=%v", first, err)
	}
	second, err := assessmentHistogramDelta(before, after, "gobfd_bfd_timer_delay_seconds", labels...)
	if err != nil || first != second || !reflect.DeepEqual(before, originalBefore) ||
		!reflect.DeepEqual(after, originalAfter) {
		t.Fatal("histogram assessment mutated/reordered source DTOs or changed on repeat")
	}
	for _, threshold := range []struct{ numerator, denominator, observed uint64 }{{99, 100, 990}, {999, 1000, 999}} {
		rank, valid := assessmentRank(first.Samples, threshold.numerator, threshold.denominator)
		if !valid || rank != threshold.observed || threshold.observed-1 >= rank {
			t.Fatalf("boundary must accept equality and reject one-less CDF count: rank=%d", rank)
		}
	}
}

func TestAssessmentHistogramRejectsMissingResetAndMalformedEvidence(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"empty_final", "missing_final", "reset_count", "reset_bucket", "nonmonotonic_bucket", "nan_sum",
		"infinite_bound", "bucket_above_sample_count", "bounds_changed", "missing_threshold", "wrong_type",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			before, after := assessmentTestHistogram(10, 10, 10), assessmentTestHistogram(1010, 1000, 1009)
			metric := after["gobfd_bfd_timer_delay_seconds"].GetMetric()[0]
			switch name {
			case "empty_final":
				after = assessmentTestHistogram(10, 10, 10)
			case "missing_final":
				after = assessmentMetricSet{}
			case "reset_count":
				*metric.Histogram.SampleCount = 9
			case "reset_bucket":
				*metric.Histogram.Bucket[0].CumulativeCount = 9
			case "nonmonotonic_bucket":
				*metric.Histogram.Bucket[1].CumulativeCount = 999
			case "nan_sum":
				*metric.Histogram.SampleSum = math.NaN()
			case "infinite_bound":
				*metric.Histogram.Bucket[2].UpperBound = math.Inf(1)
			case "bucket_above_sample_count":
				*metric.Histogram.Bucket[2].CumulativeCount = 1011
			case "bounds_changed":
				*before["gobfd_bfd_timer_delay_seconds"].Metric[0].Histogram.Bucket[2].UpperBound = 0.2
			case "missing_threshold":
				*metric.Histogram.Bucket[0].UpperBound = 0.02
			case "wrong_type":
				*after["gobfd_bfd_timer_delay_seconds"].Type = dto.MetricType_COUNTER
			}
			if _, err := assessmentHistogramDelta(before, after, "gobfd_bfd_timer_delay_seconds",
				"timer", "control_detection", "stage", "effect"); err == nil {
				t.Fatalf("accepted %s histogram", name)
			}
		})
	}
}

func TestAssessmentAllowsOnlyLazyInitialDetectionSeries(t *testing.T) {
	t.Parallel()
	after := assessmentTestHistogram(1000, 990, 999)
	if _, err := assessmentHistogramDelta(assessmentMetricSet{}, after, "gobfd_bfd_timer_delay_seconds",
		"timer", "control_detection", "stage", "effect"); err != nil {
		t.Fatal(err)
	}
	*after["gobfd_bfd_timer_delay_seconds"].Metric[0].Label[0].Value = "control_tx"
	if _, err := assessmentHistogramDelta(assessmentMetricSet{}, after, "gobfd_bfd_timer_delay_seconds",
		"timer", "control_tx", "stage", "effect"); err == nil {
		t.Fatal("active TX baseline was silently replaced by zero")
	}
}

func TestAssessmentRejectsDuplicateFixedMetricLabels(t *testing.T) {
	t.Parallel()
	metrics := assessmentTestHistogram(1000, 990, 999)
	family := metrics["gobfd_bfd_timer_delay_seconds"]
	family.Metric = append(family.GetMetric(), family.GetMetric()[0])
	if _, err := newAssessmentMetricSet(OwnerObservation{Metrics: []*dto.MetricFamily{family}}); err == nil {
		t.Fatal("duplicate timer distribution was accepted")
	}
	family.Metric = family.GetMetric()[:1]
	*family.Metric[0].Label[0].Value = "unrecognized_timer"
	if _, err := newAssessmentMetricSet(OwnerObservation{Metrics: []*dto.MetricFamily{family}}); err == nil {
		t.Fatal("unknown fixed timer label was accepted")
	}
}
