//go:build linux

package bfdload

import (
	"encoding/json/v2"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

// Run serially: worker attribution requires exactly two helpers.
func TestCPUFailureEvidenceBoundsAndIdentifiesRealWindows(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for _, tc := range []struct {
		name     string
		duration time.Duration
		windows  uint64
		stored   int
		omitted  uint64
	}{
		{name: "partial", duration: 150 * time.Millisecond, windows: 4, stored: 4},
		{name: "bounded", duration: 1650 * time.Millisecond, windows: 34, stored: 32, omitted: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				at := time.Now().Add(-2 * time.Second)
				pressure, err := startCPUPressure(t.Context(), Profile{
					Duration: tc.duration, CPULoadPercent: 80,
				}, at, func() {})
				if err != nil {
					t.Fatal(err)
				}
				observed, err := pressure.join()
				if err != nil || !observed.Completed || observed.Windows != tc.windows ||
					observed.MissedWindows != tc.windows || observed.WorkChecksum != 0 {
					t.Fatalf("expired real workers changed CPU accounting: %+v: %v", observed, err)
				}
				data, err := json.Marshal(observed)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Omitted uint64 `json:"missed_window_samples_omitted"`
					Samples []struct {
						Worker                 int    `json:"worker"`
						Window                 uint64 `json:"window"`
						WindowOffsetNS         int64  `json:"window_offset_ns"`
						WindowNS               int64  `json:"window_ns"`
						BudgetNS               int64  `json:"budget_ns"`
						AcceptedCPUNS          int64  `json:"accepted_cpu_ns"`
						ObservedCPUKnown       bool   `json:"observed_cpu_known"`
						FirstWallCheckOffsetNS int64  `json:"first_wall_check_offset_ns"`
						LastWallCheckOffsetNS  int64  `json:"last_wall_check_offset_ns"`
						WallCheckKnown         bool   `json:"wall_check_known"`
						AcceptedSampleKnown    bool   `json:"accepted_sample_known"`
						Exit                   string `json:"exit"`
					} `json:"missed_window_samples"`
				}
				if err := json.Unmarshal(data, &wire); err != nil {
					t.Fatal(err)
				}
				if len(wire.Samples) != tc.stored || wire.Omitted != tc.omitted ||
					uint64(len(wire.Samples))+wire.Omitted != observed.MissedWindows {
					t.Fatalf("missed CPU windows have no bounded evidence: stored=%d omitted=%d missed=%d",
						len(wire.Samples), wire.Omitted, observed.MissedWindows)
				}
				seen := make(map[[2]uint64]bool)
				for _, sample := range wire.Samples {
					if sample.Worker < 0 || sample.Worker > 1 {
						t.Fatalf("unknown worker identity: %+v", sample)
					}
					key := [2]uint64{uint64(sample.Worker), sample.Window}
					if seen[key] {
						t.Fatalf("duplicate physical CPU window: %+v", sample)
					}
					seen[key] = true
					windowNS, budgetNS := int64(100*time.Millisecond), int64(80*time.Millisecond)
					if tc.name == "partial" && sample.Window == 1 {
						windowNS, budgetNS = int64(50*time.Millisecond), int64(40*time.Millisecond)
					}
					if sample.Window >= uint64(tc.stored/2) ||
						sample.WindowOffsetNS != int64(sample.Window)*int64(100*time.Millisecond) ||
						sample.WindowNS != windowNS || sample.BudgetNS != budgetNS || sample.AcceptedCPUNS != 0 ||
						!sample.WallCheckKnown || sample.AcceptedSampleKnown || sample.ObservedCPUKnown ||
						sample.Exit != "deadline" ||
						sample.FirstWallCheckOffsetNS != int64(2*time.Second)-sample.WindowOffsetNS ||
						sample.LastWallCheckOffsetNS != sample.FirstWallCheckOffsetNS {
						t.Fatalf("CPU failure evidence invents work or changes its window: %+v", sample)
					}
				}
			})
		})
	}
}

func TestCPUWindowDiagnosticsDoNotAllocate(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	at := time.Now()
	until, checksum := at.Add(time.Hour), uint64(1)
	var result cpuWorkerResult
	result.Windows = 1
	result.MissedWindowSamples = make([]CPUWindowFailure, 0, cpuFailureSampleLimit)
	allocations := testing.AllocsPerRun(10, func() {
		window, err := burnCPUWindow(t.Context(), until, time.Nanosecond, &checksum)
		if err != nil || window.used < time.Nanosecond {
			t.Fatalf("real CPU budget failed: %+v: %v", window, err)
		}
		result.MissedWindowSamples = result.MissedWindowSamples[:0]
		result.recordCPUFailure(0, at, at, until, 2*time.Hour, window)
	})
	if allocations != 0 {
		t.Fatalf("CPU diagnostic bookkeeping allocated inside a window: %g", allocations)
	}
}
