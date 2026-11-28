//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
)

const (
	schedulerWorkersPerCPU = 4
	schedulerRounds        = 64
)

// SchedulerPressureObservation counts helper activity, not OS context switches.
type SchedulerPressureObservation struct {
	Enabled           bool      `json:"enabled"`
	Workers           int       `json:"workers"`
	JoinedWorkers     int       `json:"joined_workers"`
	RoundsPerWindow   uint64    `json:"rounds_per_window"`
	PeriodNS          int64     `json:"period_ns"`
	ScheduledAtUTC    time.Time `json:"scheduled_at_utc"`
	DurationNS        int64     `json:"duration_ns"`
	Rounds            uint64    `json:"rounds"`
	Yields            uint64    `json:"yields"`
	Windows           uint64    `json:"windows"`
	SkippedWindows    uint64    `json:"skipped_windows"`
	IncompleteWindows uint64    `json:"incomplete_windows"`
	StartLatenessNS   int64     `json:"start_lateness_ns"`
	EndLatenessNS     int64     `json:"end_lateness_ns"`
	WorkChecksum      uint64    `json:"work_checksum"`
	Completed         bool      `json:"completed"`
}

type schedulerWorkerResult struct {
	SchedulerPressureObservation

	err error
}

type schedulerPressure struct {
	observation SchedulerPressureObservation
	results     chan schedulerWorkerResult
}

func startSchedulerPressure(
	ctx context.Context, profile Profile, at time.Time, failed context.CancelFunc,
) (*schedulerPressure, error) {
	pressure := &schedulerPressure{observation: SchedulerPressureObservation{
		Enabled: profile.SchedulerPressure, RoundsPerWindow: schedulerRounds, PeriodNS: int64(pressurePeriod),
		ScheduledAtUTC: at.UTC(), DurationNS: int64(profile.Duration), Completed: true,
	}}
	if !profile.SchedulerPressure {
		return pressure, nil
	}
	processors := runtime.GOMAXPROCS(0)
	if processors > referenceCPUs || profile.Duration <= 0 {
		return nil, fmt.Errorf("scheduler pressure requires a positive duration and at most %d CPUs: %w",
			referenceCPUs, ErrProfile)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start scheduler pressure: %w", err)
	}
	pressure.observation.Workers = schedulerWorkersPerCPU * processors
	pressure.results = make(chan schedulerWorkerResult, pressure.observation.Workers)
	seed := uint64(1)
	for range pressure.observation.Workers {
		workerSeed := seed
		seed++
		go func() {
			result := runSchedulerWorker(ctx, at, profile.Duration, workerSeed)
			if result.err != nil {
				failed()
			}
			pressure.results <- result
		}()
	}
	return pressure, nil
}

func (p *schedulerPressure) join() (SchedulerPressureObservation, error) {
	var joined error
	for range p.observation.Workers {
		worker := <-p.results
		p.observation.JoinedWorkers++
		p.observation.Rounds += worker.Rounds
		p.observation.Yields += worker.Yields
		p.observation.Windows += worker.Windows
		p.observation.SkippedWindows += worker.SkippedWindows
		p.observation.IncompleteWindows += worker.IncompleteWindows
		p.observation.StartLatenessNS = max(p.observation.StartLatenessNS, worker.StartLatenessNS)
		p.observation.EndLatenessNS = max(p.observation.EndLatenessNS, worker.EndLatenessNS)
		p.observation.WorkChecksum ^= worker.WorkChecksum
		p.observation.Completed = p.observation.Completed && worker.Completed
		joined = errors.Join(joined, worker.err)
	}
	return p.observation, joined
}

func runSchedulerWorker(ctx context.Context, at time.Time, duration time.Duration, seed uint64) schedulerWorkerResult {
	var result schedulerWorkerResult
	if result.err = awaitPhaseTime(ctx, at); result.err != nil {
		return result
	}
	result.StartLatenessNS = max(0, int64(time.Since(at)))
	result.WorkChecksum = seed
	end := at.Add(duration)
	count := wholePressureWindows(duration)
	if duration%pressurePeriod != 0 {
		count++
	}
	for window := uint64(0); window < count; window++ {
		current := wholePressureWindows(time.Since(at))
		if current > window {
			next := min(current, count)
			result.SkippedWindows += next - window
			window = next
		}
		if window == count {
			break
		}
		until := at.Add(time.Duration(window) * pressurePeriod).Add(pressurePeriod)
		if end.Before(until) {
			until = end
		}
		before := result.Rounds
		result.err = runSchedulerWindow(ctx, until, &result.SchedulerPressureObservation)
		result.Windows++
		if result.Rounds-before != schedulerRounds {
			result.IncompleteWindows++
		}
		if result.err != nil {
			break
		}
		if result.err = awaitPhaseTime(ctx, until); result.err != nil {
			break
		}
	}
	result.EndLatenessNS = max(0, int64(time.Since(end)))
	result.Completed = result.err == nil && !time.Now().Before(end)
	return result
}

func runSchedulerWindow(ctx context.Context, until time.Time, observed *SchedulerPressureObservation) error {
	for range schedulerRounds {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("scheduler pressure interrupted: %w", err)
		}
		if !time.Now().Before(until) {
			break
		}
		burnPressureChunk(&observed.WorkChecksum)
		observed.Rounds++
		runtime.Gosched()
		observed.Yields++
	}
	return nil
}
