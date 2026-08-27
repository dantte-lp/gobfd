package main

import (
	"bytes"
	"strings"
	"testing"
)

//nolint:dupword // Exact tshark fixtures legitimately contain adjacent False flag fields.
func TestRun(t *testing.T) {
	t.Parallel()
	t.Run("report", func(t *testing.T) {
		t.Parallel()
		input := strings.Join([]string{
			"0.000\t3\tFalse\tFalse", "0.225\t3\tFalse\tFalse", "0.475\t3\tFalse\tFalse",
			"0.750\t3\tFalse\tFalse", "1.050\t3\tFalse\tFalse", "1.275\t3\tFalse\tFalse",
			"1.525\t3\tFalse\tFalse", "1.800\t3\tFalse\tFalse", "2.100\t3\tFalse\tFalse",
			"2.325\t3\tFalse\tFalse",
		}, "\n")
		var output bytes.Buffer
		var errorOutput bytes.Buffer
		if status := run(strings.NewReader(input), &output, &errorOutput); status != 0 {
			t.Fatalf("run() status=%d stderr=%q", status, errorOutput.String())
		}
		if got, want := output.String(), "pass\twithin-bounds\t10\t0.225000\t0.300000\t9\n"; got != want {
			t.Fatalf("run() output=%q, want %q", got, want)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		t.Parallel()
		var output bytes.Buffer
		var errorOutput bytes.Buffer
		if status := run(strings.NewReader("warning"), &output, &errorOutput); status == 0 {
			t.Fatalf("run() accepted malformed TSV: stdout=%q", output.String())
		}
		if !strings.Contains(errorOutput.String(), "parse jitter TSV row 1") {
			t.Fatalf("run() stderr=%q, want parse diagnostic", errorOutput.String())
		}
	})
}
