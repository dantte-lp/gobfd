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
			input: noFlags(
				"0.000\t3", "0.225\t3", "0.475\t3", "0.750\t3", "1.050\t3",
				"1.275\t3", "1.525\t3", "1.800\t3", "2.100\t3", "2.325\t3",
			),
			wantStatus:  StatusPass,
			wantSamples: 9,
			wantMin:     0.225,
			wantMax:     0.300,
		},
		"ordinary 50ms Up packets fail": {
			input: noFlags(
				"0.000\t3", "0.050\t3", "0.100\t3", "0.150\t3", "0.200\t3",
				"0.250\t3", "0.300\t3", "0.350\t3", "0.400\t3", "0.450\t3",
			),
			wantStatus:  StatusFail,
			wantSamples: 9,
			wantMin:     0.050,
			wantMax:     0.050,
		},
		"Poll and Final packets do not advance periodic baseline": {
			input: strings.Join([]string{
				"0.000\t3\t0\t0", "0.050\t3\t1\t0", "0.225\t3\t0\t0",
				"0.275\t3\t0\t1", "0.475\t3\t0\t0", "0.750\t3\t0\t0",
				"1.050\t3\t0\t0", "1.275\t3\t0\t0", "1.525\t3\t0\t0",
				"1.800\t3\t0\t0", "2.100\t3\t0\t0", "2.325\t3\t0\t0",
			}, "\n"),
			wantStatus:  StatusPass,
			wantSamples: 9,
			wantMin:     0.225,
			wantMax:     0.300,
		},
		"Final packet cannot hide a long regular gap": {
			input: strings.Join([]string{
				"0.000\t3\t0\t0", "0.050\t3\t0\t1", "0.440\t3\t0\t0",
				"0.665\t3\t0\t0", "0.915\t3\t0\t0", "1.190\t3\t0\t0",
				"1.490\t3\t0\t0", "1.715\t3\t0\t0", "1.965\t3\t0\t0",
				"2.240\t3\t0\t0", "2.540\t3\t0\t0",
			}, "\n"),
			wantStatus:  StatusFail,
			wantSamples: 9,
			wantMin:     0.225,
			wantMax:     0.440,
		},
		"Down boundary excludes managed downtime": {
			input: noFlags(
				"0.000\t0x03", "0.225\t0x03", "0.475\t0x03", "0.750\t0x03", "1.050\t0x03",
				"1.200\t0x01", "5.488\t0x02", "5.713\t0x03", "5.963\t0x03", "6.238\t0x03",
				"6.538\t0x03", "6.763\t0x03",
			),
			wantStatus:  StatusPass,
			wantSamples: 8,
			wantMin:     0.225,
			wantMax:     0.300,
		},
		"long gap inside continuous Up fails": {
			input: noFlags(
				"0.000\t3", "0.225\t3", "0.475\t3", "0.750\t3", "1.050\t3",
				"1.275\t3", "1.525\t3", "1.800\t3", "2.238\t3", "2.463\t3",
			),
			wantStatus:  StatusFail,
			wantSamples: 9,
			wantMin:     0.225,
			wantMax:     0.438,
		},
		"no continuous Up samples explicitly skips": {
			input: noFlags(
				"0.000\t3", "0.100\t1", "0.200\t3", "0.300\t2", "0.400\t3",
				"0.500\t1", "0.600\t3", "0.700\t2", "0.800\t3", "0.900\t1",
			),
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
		"warning mixed into stream": "Running as user root\n0.000\t3\t0\t0",
		"missing state":             "0.000",
		"missing Poll flag":         "0.000\t3",
		"missing Final flag":        "0.000\t3\t0",
		"extra field":               "0.000\t3\t0\t0\textra",
		"invalid epoch":             "not-time\t3\t0\t0",
		"non-finite epoch":          "NaN\t3\t0\t0",
		"invalid state":             "0.000\tUp\t0\t0",
		"out of range state":        "0.000\t4\t0\t0",
		"invalid Poll flag":         "0.000\t3\t2\t0",
		"invalid Final flag":        "0.000\t3\t0\t2",
		"empty Poll flag":           "0.000\t3\t\t0",
		"non-increasing time":       "1.000\t3\t0\t0\n0.999\t3\t0\t0",
		"blank record":              "0.000\t3\t0\t0\n\n0.225\t3\t0\t0",
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

func noFlags(rows ...string) string {
	for index := range rows {
		rows[index] += "\t0\t0"
	}
	return strings.Join(rows, "\n")
}
