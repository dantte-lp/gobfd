//go:build linux

package bfdload

import "time"

const cpuFailureSampleLimit = 16

// CPUWindowFailure retains existing clock readings; it does not establish a scheduling cause.
type CPUWindowFailure struct {
	Worker                 int    `json:"worker"`
	Window                 uint64 `json:"window"`
	WindowOffsetNS         int64  `json:"window_offset_ns"`
	WindowNS               int64  `json:"window_ns"`
	BudgetNS               int64  `json:"budget_ns"`
	AcceptedCPUNS          int64  `json:"accepted_cpu_ns"`
	ObservedCPUNS          int64  `json:"observed_cpu_ns"`
	ObservedCPUKnown       bool   `json:"observed_cpu_known"`
	FirstWallCheckOffsetNS int64  `json:"first_wall_check_offset_ns"`
	LastWallCheckOffsetNS  int64  `json:"last_wall_check_offset_ns"`
	WallCheckKnown         bool   `json:"wall_check_known"`
	LastAcceptedOffsetNS   int64  `json:"last_accepted_offset_ns"`
	AcceptedSampleKnown    bool   `json:"accepted_sample_known"`
	Exit                   string `json:"exit"`
}

type cpuWindowResult struct {
	used, observed                    time.Duration
	firstWall, lastWall, acceptedWall time.Time
	observedKnown                     bool
	exit                              string
}

func (r *cpuWorkerResult) recordCPUFailure(
	worker int, at, window, until time.Time, budget time.Duration, w cpuWindowResult,
) {
	r.MissedWindows++
	if len(r.MissedWindowSamples) == cpuFailureSampleLimit {
		r.MissedWindowSamplesOmitted++
		return
	}
	sample := CPUWindowFailure{
		Worker: worker, Window: r.Windows - 1, WindowOffsetNS: int64(window.Sub(at)),
		WindowNS: int64(until.Sub(window)), BudgetNS: int64(budget), AcceptedCPUNS: int64(w.used),
		ObservedCPUNS: int64(w.observed), ObservedCPUKnown: w.observedKnown, Exit: w.exit,
		WallCheckKnown: !w.firstWall.IsZero(), AcceptedSampleKnown: !w.acceptedWall.IsZero(),
	}
	if sample.WallCheckKnown {
		sample.FirstWallCheckOffsetNS = int64(w.firstWall.Sub(window))
		sample.LastWallCheckOffsetNS = int64(w.lastWall.Sub(window))
	}
	if sample.AcceptedSampleKnown {
		sample.LastAcceptedOffsetNS = int64(w.acceptedWall.Sub(window))
	}
	r.MissedWindowSamples = append(r.MissedWindowSamples, sample)
}
