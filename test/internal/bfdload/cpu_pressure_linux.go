//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

const (
	cpuPressurePeriod = 100 * time.Millisecond
	cpuPercentScale   = 100
)

// CPUPressureObservation reports generated thread CPU time, not a held host-load claim.
type CPUPressureObservation struct {
	RequestedPercent uint64    `json:"requested_percent"`
	Workers          int       `json:"workers"`
	PeriodNS         int64     `json:"period_ns"`
	ScheduledAtUTC   time.Time `json:"scheduled_at_utc"`
	DurationNS       int64     `json:"duration_ns"`
	CPUTimeNS        int64     `json:"cpu_time_ns"`
	WorkerWallTimeNS int64     `json:"worker_wall_time_ns"`
	Windows          uint64    `json:"windows"`
	MissedWindows    uint64    `json:"missed_windows"`
	StartLatenessNS  int64     `json:"start_lateness_ns"`
	EndLatenessNS    int64     `json:"end_lateness_ns"`
	WorkChecksum     uint64    `json:"work_checksum"`
	Completed        bool      `json:"completed"`
}

type cpuWorkerResult struct {
	CPUPressureObservation

	err error
}

type cpuPressure struct {
	observation CPUPressureObservation
	results     chan cpuWorkerResult
}

func startCPUPressure(
	ctx context.Context, profile Profile, at time.Time, failed context.CancelFunc,
) (*cpuPressure, error) {
	result := &cpuPressure{observation: CPUPressureObservation{
		RequestedPercent: profile.CPULoadPercent, PeriodNS: int64(cpuPressurePeriod),
		ScheduledAtUTC: at.UTC(), DurationNS: int64(profile.Duration), Completed: true,
	}}
	if profile.CPULoadPercent == 0 {
		return result, nil
	}
	workers := runtime.GOMAXPROCS(0)
	if profile.CPULoadPercent > cpuPercentScale || profile.Duration <= 0 || workers > referenceCPUs {
		return nil, fmt.Errorf("CPU pressure requires a valid duty and at most %d workers: %w", referenceCPUs, ErrProfile)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start CPU pressure: %w", err)
	}
	result.observation.Workers = workers
	result.results = make(chan cpuWorkerResult, workers)
	duty := int64(profile.CPULoadPercent)
	for range workers {
		go func() {
			observed := runCPUWorker(ctx, at, profile.Duration, duty)
			if observed.err != nil {
				failed()
			}
			result.results <- observed
		}()
	}
	return result, nil
}

func (p *cpuPressure) join() (CPUPressureObservation, error) {
	var joined error
	for range p.observation.Workers {
		worker := <-p.results
		p.observation.CPUTimeNS += worker.CPUTimeNS
		p.observation.WorkerWallTimeNS += worker.WorkerWallTimeNS
		p.observation.Windows += worker.Windows
		p.observation.MissedWindows += worker.MissedWindows
		p.observation.StartLatenessNS = max(p.observation.StartLatenessNS, worker.StartLatenessNS)
		p.observation.EndLatenessNS = max(p.observation.EndLatenessNS, worker.EndLatenessNS)
		p.observation.WorkChecksum ^= worker.WorkChecksum
		p.observation.Completed = p.observation.Completed && worker.Completed
		joined = errors.Join(joined, worker.err)
	}
	return p.observation, joined
}

func runCPUWorker(ctx context.Context, at time.Time, duration time.Duration, duty int64) cpuWorkerResult {
	var result cpuWorkerResult
	// A bounded helper thread keeps its CPU clock attributable to this worker.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if result.err = awaitPhaseTime(ctx, at); result.err != nil {
		return result
	}
	actualStart := time.Now()
	result.StartLatenessNS = max(0, int64(actualStart.Sub(at)))
	initial, err := threadCPUTime()
	if err != nil {
		result.err = err
		return result
	}
	end := at.Add(duration)
	result.WorkChecksum = 1
	for window := at; window.Before(end); window = window.Add(cpuPressurePeriod) {
		until := window.Add(cpuPressurePeriod)
		if end.Before(until) {
			until = end
		}
		budget := time.Duration(int64(until.Sub(window)) * duty / cpuPercentScale)
		var used time.Duration
		used, result.err = burnCPUWindow(ctx, until, budget, &result.WorkChecksum)
		result.Windows++
		if used < budget {
			result.MissedWindows++
		}
		if result.err != nil {
			break
		}
		if result.err = awaitPhaseTime(ctx, until); result.err != nil {
			break
		}
	}
	final, finalErr := threadCPUTime()
	result.WorkerWallTimeNS = int64(time.Since(actualStart))
	if finalErr == nil && final >= initial {
		result.CPUTimeNS = int64(final - initial)
	}
	result.EndLatenessNS = max(0, int64(time.Since(end)))
	result.err = errors.Join(result.err, finalErr)
	if result.err == nil && final < initial {
		result.err = fmt.Errorf("CPU worker clock decreased: %w", errEnvironmentData)
	}
	result.Completed = result.err == nil && !time.Now().Before(end)
	return result
}

func burnCPUWindow(
	ctx context.Context, until time.Time, budget time.Duration, checksum *uint64,
) (time.Duration, error) {
	initial, err := threadCPUTime()
	if err != nil {
		return 0, err
	}
	var used time.Duration
	for used < budget && time.Now().Before(until) {
		if err := ctx.Err(); err != nil {
			return used, fmt.Errorf("CPU pressure interrupted: %w", err)
		}
		for range 1024 {
			*checksum = bits.RotateLeft64(*checksum+0x9e3779b97f4a7c15, 13)
		}
		current, clockErr := threadCPUTime()
		if clockErr != nil {
			return used, clockErr
		}
		if current < initial {
			return used, fmt.Errorf("CPU duty clock decreased: %w", errEnvironmentData)
		}
		used = current - initial
	}
	return used, nil
}

func threadCPUTime() (time.Duration, error) {
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &now); err != nil {
		return 0, fmt.Errorf("read CPU worker thread clock: %w", err)
	}
	return time.Duration(now.Nano()), nil
}
