package bfdjitter

import (
	"math"
	"strings"
	"testing"
)

//nolint:dupword // Exact tshark fixtures legitimately contain adjacent False flag fields.
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
		"Poll at 50ms is a periodic sample and fails": {
			input: strings.Join([]string{
				"0.000\t3\tFalse\tFalse", "0.050\t3\tTrue\tFalse", "0.100\t3\tFalse\tFalse",
				"0.150\t3\tFalse\tFalse", "0.200\t3\tFalse\tFalse", "0.250\t3\tFalse\tFalse",
				"0.300\t3\tFalse\tFalse", "0.350\t3\tFalse\tFalse", "0.400\t3\tFalse\tFalse",
				"0.450\t3\tFalse\tFalse",
			}, "\n"),
			wantStatus:  StatusFail,
			wantSamples: 9,
			wantMin:     0.050,
			wantMax:     0.050,
		},
		"Poll counts while Final does not advance periodic baseline": {
			input: strings.Join([]string{
				"0.000\t3\tFalse\tFalse", "0.225\t3\tTrue\tFalse", "0.275\t3\tFalse\tTrue",
				"0.475\t3\tFalse\tFalse", "0.750\t3\tFalse\tFalse",
				"1.050\t3\tFalse\tFalse", "1.275\t3\tFalse\tFalse", "1.525\t3\tFalse\tFalse",
				"1.800\t3\tFalse\tFalse", "2.100\t3\tFalse\tFalse", "2.325\t3\tFalse\tFalse",
			}, "\n"),
			wantStatus:  StatusPass,
			wantSamples: 9,
			wantMin:     0.225,
			wantMax:     0.300,
		},
		"Final packet cannot hide a long regular gap": {
			input: strings.Join([]string{
				"0.000\t3\tFalse\tFalse", "0.050\t3\tFalse\tTrue", "0.440\t3\tFalse\tFalse",
				"0.665\t3\tFalse\tFalse", "0.915\t3\tFalse\tFalse", "1.190\t3\tFalse\tFalse",
				"1.490\t3\tFalse\tFalse", "1.715\t3\tFalse\tFalse", "1.965\t3\tFalse\tFalse",
				"2.240\t3\tFalse\tFalse", "2.540\t3\tFalse\tFalse",
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

//nolint:dupword // Exact tshark fixtures legitimately contain adjacent False flag fields.
func TestEvaluateRejectsMalformedTSV(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"warning mixed into stream": "Running as user root\n0.000\t3\tFalse\tFalse",
		"missing state":             "0.000",
		"missing Poll flag":         "0.000\t3",
		"missing Final flag":        "0.000\t3\tFalse",
		"extra field":               "0.000\t3\tFalse\tFalse\textra",
		"invalid epoch":             "not-time\t3\tFalse\tFalse",
		"non-finite epoch":          "NaN\t3\tFalse\tFalse",
		"invalid state":             "0.000\tUp\tFalse\tFalse",
		"out of range state":        "0.000\t4\tFalse\tFalse",
		"invalid Poll flag":         "0.000\t3\t2\tFalse",
		"invalid Final flag":        "0.000\t3\tFalse\t2",
		"lowercase Poll flag":       "0.000\t3\tfalse\tFalse",
		"uppercase Final flag":      "0.000\t3\tFalse\tTRUE",
		"hex numeric Poll flag":     "0.000\t3\t0x0\tFalse",
		"padded numeric Final flag": "0.000\t3\tFalse\t00",
		"empty Poll flag":           "0.000\t3\t\tFalse",
		"non-increasing time":       "1.000\t3\tFalse\tFalse\n0.999\t3\tFalse\tFalse",
		"blank record":              "0.000\t3\tFalse\tFalse\n\n0.225\t3\tFalse\tFalse",
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

//nolint:dupword // Exact tshark fixtures legitimately contain adjacent False or True flag fields.
func TestEvaluateAcceptsExactFlagEncodings(t *testing.T) {
	t.Parallel()
	inputs := map[string]string{
		"canonical unset": "0.000\t3\tFalse\tFalse",
		"canonical Poll":  "0.000\t3\tTrue\tFalse",
		"canonical Final": "0.000\t3\tFalse\tTrue",
		"numeric unset":   "0.000\t3\t0\t0",
		"numeric Poll":    "0.000\t3\t1\t0",
		"numeric Final":   "0.000\t3\t0\t1",
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Evaluate(strings.NewReader(input)); err != nil {
				t.Fatalf("Evaluate() rejected exact flag encoding: %v", err)
			}
		})
	}
}

//nolint:dupword // Exact tshark fixtures legitimately contain adjacent True flag fields.
func TestEvaluateRejectsPollAndFinalTogether(t *testing.T) {
	t.Parallel()
	inputs := map[string]string{
		"canonical":         "0.000\t3\tTrue\tTrue",
		"canonical numeric": "0.000\t3\tTrue\t1",
		"numeric canonical": "0.000\t3\t1\tTrue",
		"numeric":           "0.000\t3\t1\t1",
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if report, err := Evaluate(strings.NewReader(input)); err == nil {
				t.Fatalf("Evaluate() accepted Poll+Final flags: %+v", report)
			}
		})
	}
}

//nolint:dupword // Exact tshark rows require two adjacent canonical False fields.
func noFlags(rows ...string) string {
	for index := range rows {
		rows[index] += "\tFalse\tFalse"
	}
	return strings.Join(rows, "\n")
}
