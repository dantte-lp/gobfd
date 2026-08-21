package bfdjitter

import (
	"math"
	"strings"
	"testing"
)

func TestEvaluateSegments(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input       string
		wantStatus  Status
		wantSamples int
		wantMin     float64
		wantMax     float64
	}{
		"continuous Up segment passes": {
			input: strings.Join([]string{
				"0.000\t3", "0.225\t3", "0.475\t3", "0.750\t3", "1.050\t3",
				"1.275\t3", "1.525\t3", "1.800\t3", "2.100\t3", "2.325\t3",
			}, "\n"),
			wantStatus:  StatusPass,
			wantSamples: 9,
			wantMin:     0.225,
			wantMax:     0.300,
		},
		"sub-100ms Up burst is not a periodic sample": {
			input: strings.Join([]string{
				"0.000\t3", "0.225\t3", "0.275\t3", "0.500\t3", "0.750\t3",
				"1.025\t3", "1.325\t3", "1.550\t3", "1.800\t3", "2.100\t3",
			}, "\n"),
			wantStatus:  StatusPass,
			wantSamples: 8,
			wantMin:     0.225,
			wantMax:     0.300,
		},
		"Down boundary excludes managed downtime": {
			input: strings.Join([]string{
				"0.000\t0x03", "0.225\t0x03", "0.475\t0x03", "0.750\t0x03", "1.050\t0x03",
				"1.200\t0x01", "5.488\t0x02", "5.713\t0x03", "5.963\t0x03", "6.238\t0x03",
				"6.538\t0x03", "6.763\t0x03",
			}, "\n"),
			wantStatus:  StatusPass,
			wantSamples: 8,
			wantMin:     0.225,
			wantMax:     0.300,
		},
		"long gap inside continuous Up fails": {
			input: strings.Join([]string{
				"0.000\t3", "0.225\t3", "0.475\t3", "0.750\t3", "1.050\t3",
				"1.275\t3", "1.525\t3", "1.800\t3", "2.238\t3", "2.463\t3",
			}, "\n"),
			wantStatus:  StatusFail,
			wantSamples: 9,
			wantMin:     0.225,
			wantMax:     0.438,
		},
		"no continuous Up samples explicitly skips": {
			input: strings.Join([]string{
				"0.000\t3", "0.100\t1", "0.200\t3", "0.300\t2", "0.400\t3",
				"0.500\t1", "0.600\t3", "0.700\t2", "0.800\t3", "0.900\t1",
			}, "\n"),
			wantStatus: StatusSkip,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			report, err := Evaluate(strings.NewReader(test.input))
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if report.Status != test.wantStatus || report.Samples != test.wantSamples {
				t.Fatalf("Evaluate() = %+v, want status=%s samples=%d", report, test.wantStatus, test.wantSamples)
			}
			if math.Abs(report.MinDelta-test.wantMin) > 1e-9 ||
				math.Abs(report.MaxDelta-test.wantMax) > 1e-9 {
				t.Fatalf("Evaluate() bounds = %.3f..%.3f, want %.3f..%.3f",
					report.MinDelta, report.MaxDelta, test.wantMin, test.wantMax)
			}
		})
	}
}

func TestEvaluateRejectsMalformedTSV(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"warning mixed into stream": "Running as user root\n0.000\t3",
		"missing state":             "0.000",
		"extra field":               "0.000\t3\textra",
		"invalid epoch":             "not-time\t3",
		"non-finite epoch":          "NaN\t3",
		"invalid state":             "0.000\tUp",
		"out of range state":        "0.000\t4",
		"non-increasing time":       "1.000\t3\n0.999\t3",
		"blank record":              "0.000\t3\n\n0.225\t3",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if report, err := Evaluate(strings.NewReader(input)); err == nil {
				t.Fatalf("Evaluate() accepted malformed TSV: %+v", report)
			}
		})
	}
}
