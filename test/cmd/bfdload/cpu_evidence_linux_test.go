//go:build linux

package main

import (
	"bytes"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func TestAssessmentCPUFailureEvidenceSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data string
		fail bool
	}{
		{name: "historical", data: `{"cpu_pressure":{"missed_windows":1}}`},
		{name: "diagnostic", data: `{"cpu_pressure":{"missed_windows":1,"missed_window_samples_omitted":0,
			"missed_window_samples":[{"worker":0,"window":1,"window_offset_ns":100000000,"window_ns":100000000,
			"budget_ns":80000000,"accepted_cpu_ns":70000000,"observed_cpu_ns":81000000,"observed_cpu_known":true,
			"first_wall_check_offset_ns":1,"last_wall_check_offset_ns":100000001,"wall_check_known":true,
			"last_accepted_offset_ns":90000000,"accepted_sample_known":true,"exit":"late_sample"}]}}`},
		{name: "unknown", data: `{"cpu_pressure":{"missed_window_samples":[{"invented_cpu_credit":1}]}}`, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var owner bfdload.OwnerWorkloadObservation
			err := decodeAssessmentReport(t.Context(), bytes.NewBufferString(tc.data), &owner, 4096)
			if (err != nil) != tc.fail {
				t.Fatalf("strict CPU evidence decoder: unexpected error=%v", err)
			}
			if !tc.fail && owner.CPUPressure.MissedWindows != 1 {
				t.Fatal("diagnostic schema erased the missed-window counter")
			}
		})
	}
}
