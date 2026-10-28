//go:build linux

package bfdload

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"
)

const (
	gcChunkBytes      = 64 * 1024
	gcObjectsPerBatch = 64
	gcRetainedBatches = 4
	gcRetainedObjects = gcObjectsPerBatch * gcRetainedBatches
)

// GCPressureObservation bounds retained references, not total heap or RSS.
type GCPressureObservation struct {
	Enabled             bool      `json:"enabled"`
	Workers             int       `json:"workers"`
	PeriodNS            int64     `json:"period_ns"`
	BytesPerBatch       uint64    `json:"bytes_per_batch"`
	RetainedLimitBytes  uint64    `json:"retained_limit_bytes"`
	ScheduledAtUTC      time.Time `json:"scheduled_at_utc"`
	DurationNS          int64     `json:"duration_ns"`
	AllocatedBytes      uint64    `json:"allocated_bytes"`
	Objects             uint64    `json:"objects"`
	Batches             uint64    `json:"batches"`
	SkippedBatches      uint64    `json:"skipped_batches"`
	TruncatedBatches    uint64    `json:"truncated_batches"`
	PeakRetainedObjects uint64    `json:"peak_retained_objects"`
	ReleasedObjects     uint64    `json:"released_objects"`
	StartLatenessNS     int64     `json:"start_lateness_ns"`
	EndLatenessNS       int64     `json:"end_lateness_ns"`
	Completed           bool      `json:"completed"`
}

type gcWorkerResult struct {
	GCPressureObservation

	err error
}

type gcPressure struct {
	observation GCPressureObservation
	result      chan gcWorkerResult
}

func startGCPressure(
	ctx context.Context, profile Profile, at time.Time, failed context.CancelFunc,
) (*gcPressure, error) {
	pressure := &gcPressure{observation: GCPressureObservation{
		Enabled: profile.GCPressure, PeriodNS: int64(pressurePeriod),
		BytesPerBatch: gcChunkBytes * gcObjectsPerBatch, RetainedLimitBytes: gcChunkBytes * gcRetainedObjects,
		ScheduledAtUTC: at.UTC(), DurationNS: int64(profile.Duration), Completed: true,
	}}
	if !profile.GCPressure {
		return pressure, nil
	}
	if profile.Duration <= 0 {
		return nil, fmt.Errorf("GC pressure requires a positive duration: %w", ErrProfile)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start GC pressure: %w", err)
	}
	pressure.observation.Workers, pressure.observation.Completed = 1, false
	pressure.result = make(chan gcWorkerResult, 1)
	go func() {
		result := runGCWorker(ctx, at, pressure.observation)
		if result.err != nil {
			failed()
		}
		pressure.result <- result
	}()
	return pressure, nil
}

func (p *gcPressure) join() (GCPressureObservation, error) {
	if p.result == nil {
		return p.observation, nil
	}
	result := <-p.result
	return result.GCPressureObservation, result.err
}

func runGCWorker(ctx context.Context, at time.Time, observation GCPressureObservation) gcWorkerResult {
	result := gcWorkerResult{GCPressureObservation: observation}
	if result.err = awaitPhaseTime(ctx, at); result.err != nil {
		return result
	}
	result.StartLatenessNS = max(0, int64(time.Since(at)))
	duration := time.Duration(observation.DurationNS)
	end := at.Add(duration)
	count := wholePressureWindows(duration)
	if duration%pressurePeriod != 0 {
		count++
	}
	var retained [gcRetainedObjects][]byte
	for batch := uint64(0); batch < count; batch++ {
		// Skip elapsed windows instead of allocating accumulated debt.
		current := wholePressureWindows(time.Since(at))
		if current > batch {
			next := min(current, count)
			result.SkippedBatches += next - batch
			batch = next
		}
		if batch == count {
			break
		}
		until := at.Add(time.Duration(batch) * pressurePeriod).Add(pressurePeriod)
		if end.Before(until) {
			until = end
		}
		result.err = allocateGCBatch(ctx, until, &retained, &result.GCPressureObservation)
		if result.err != nil {
			break
		}
		if result.err = awaitPhaseTime(ctx, until); result.err != nil {
			break
		}
	}
	runtime.KeepAlive(retained)
	for _, object := range retained {
		if object != nil {
			result.ReleasedObjects++
		}
	}
	clear(retained[:])
	result.EndLatenessNS = max(0, int64(time.Since(end)))
	result.Completed = result.err == nil && !time.Now().Before(end)
	return result
}

func allocateGCBatch(
	ctx context.Context, until time.Time, retained *[gcRetainedObjects][]byte, observed *GCPressureObservation,
) error {
	var objects uint64
	var interrupted error
	pageSize := os.Getpagesize()
	for range gcObjectsPerBatch {
		if interrupted = ctx.Err(); interrupted != nil || !time.Now().Before(until) {
			break
		}
		object := make([]byte, gcChunkBytes)
		for offset := 0; offset < len(object); offset += pageSize {
			object[offset] = 1
		}
		retained[observed.Objects%gcRetainedObjects] = object
		objects++
		observed.Objects++
		observed.AllocatedBytes += uint64(len(object))
		observed.PeakRetainedObjects = min(observed.Objects, gcRetainedObjects)
	}
	if objects != 0 {
		observed.Batches++
	}
	if objects != gcObjectsPerBatch {
		observed.TruncatedBatches++
	}
	if interrupted != nil {
		return fmt.Errorf("GC pressure interrupted: %w", interrupted)
	}
	return nil
}
