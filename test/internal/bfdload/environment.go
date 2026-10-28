// Package bfdload supplies evidence for the actual UDP-to-owner workload.
package bfdload

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"runtime/metrics"
	"slices"
	"strconv"
	"time"
)

var (
	// ErrEnvironmentLimit means a resource file or descriptor scan exceeded its bound.
	ErrEnvironmentLimit = errors.New("environment observation exceeds its bound")
	errEnvironmentData  = errors.New("invalid or unavailable environment observation")
)

// Environment is a sequential process sample, not an atomic admission proof.
type Environment struct {
	StartedAtUTC time.Time       `json:"started_at_utc"`
	ElapsedNS    int64           `json:"elapsed_ns"`
	Process      ProcessSnapshot `json:"process"`
	Runtime      RuntimeSnapshot `json:"runtime"`
	Cgroup       *CgroupSnapshot `json:"cgroup"`
	CgroupError  string          `json:"cgroup_error,omitempty"`
}

// ProcessSnapshot includes the observer's descriptors and process-wide rusage.
type ProcessSnapshot struct {
	PID                        int    `json:"pid"`
	RSSBytes                   uint64 `json:"rss_bytes"`
	Threads                    uint64 `json:"threads"`
	FDs                        uint64 `json:"fds"`
	ObserverCPUAffinity        int    `json:"observer_cpu_affinity"`
	VoluntaryContextSwitches   int64  `json:"voluntary_context_switches"`
	InvoluntaryContextSwitches int64  `json:"involuntary_context_switches"`
}

// RuntimeSnapshot retains cumulative GC and scheduler observations separately.
type RuntimeSnapshot struct {
	Version            string    `json:"version"`
	GOOS               string    `json:"goos"`
	GOARCH             string    `json:"goarch"`
	GOMAXPROCS         int       `json:"gomaxprocs"`
	Goroutines         int       `json:"goroutines"`
	GCPercent          uint64    `json:"gc_percent"`
	MemoryLimitBytes   uint64    `json:"memory_limit_bytes"`
	GCCycles           uint64    `json:"gc_cycles"`
	HeapAllocatedBytes uint64    `json:"heap_allocated_bytes"`
	HeapFreedBytes     uint64    `json:"heap_freed_bytes"`
	HeapLiveBytes      uint64    `json:"heap_live_bytes"`
	SchedulerLatency   Histogram `json:"scheduler_latency"`
	GCPauses           Histogram `json:"gc_pauses"`
}

// Histogram keeps half-open runtime buckets; strings preserve infinite bounds.
type Histogram struct {
	BoundsSeconds []string `json:"bounds_seconds"`
	Counts        []uint64 `json:"counts"`
}

// Limit distinguishes a read "max" from numeric zero, in the enclosing field's units.
type Limit struct {
	Unlimited bool   `json:"unlimited"`
	Value     uint64 `json:"value"`
}

// CgroupSnapshot covers the visible current cgroup, never effective ancestors.
// Unavailable mandatory data has no snapshot, rather than zero-valued limits.
type CgroupSnapshot struct {
	MemberPath          string            `json:"member_path"`
	MountPoint          string            `json:"mount_point"`
	MountRoot           string            `json:"mount_root"`
	CPUQuotaUS          Limit             `json:"cpu_quota_us"`
	CPUPeriodUS         uint64            `json:"cpu_period_us"`
	CPUStat             map[string]uint64 `json:"cpu_stat"`
	LocalThrottledUS    *uint64           `json:"local_throttled_us"`
	LocalThrottledError string            `json:"local_throttled_error,omitempty"`
	MemoryCurrentBytes  uint64            `json:"memory_current_bytes"`
	MemoryMaxBytes      Limit             `json:"memory_max_bytes"`
	MemoryHighBytes     Limit             `json:"memory_high_bytes"`
	MemoryEvents        map[string]uint64 `json:"memory_events"`
	PidsCurrent         uint64            `json:"pids_current"`
	PidsMax             Limit             `json:"pids_max"`
}

func snapshotRuntime() (RuntimeSnapshot, error) {
	result := RuntimeSnapshot{
		Version: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		GOMAXPROCS: runtime.GOMAXPROCS(0), Goroutines: runtime.NumGoroutine(),
	}
	values := []struct {
		name        string
		destination *uint64
	}{
		{"/gc/gogc:percent", &result.GCPercent},
		{"/gc/gomemlimit:bytes", &result.MemoryLimitBytes},
		{"/gc/cycles/total:gc-cycles", &result.GCCycles},
		{"/gc/heap/allocs:bytes", &result.HeapAllocatedBytes},
		{"/gc/heap/frees:bytes", &result.HeapFreedBytes},
		{"/gc/heap/live:bytes", &result.HeapLiveBytes},
	}
	samples := make([]metrics.Sample, len(values)+2)
	for i, value := range values {
		samples[i].Name = value.name
	}
	samples[len(values)].Name = "/sched/latencies:seconds"
	samples[len(values)+1].Name = "/sched/pauses/total/gc:seconds"
	metrics.Read(samples)
	for i, value := range values {
		if samples[i].Value.Kind() != metrics.KindUint64 {
			return RuntimeSnapshot{}, fmt.Errorf("runtime metric %s: %w", value.name, errEnvironmentData)
		}
		*value.destination = samples[i].Value.Uint64()
	}
	for i, destination := range []*Histogram{&result.SchedulerLatency, &result.GCPauses} {
		sample := samples[len(values)+i]
		if sample.Value.Kind() != metrics.KindFloat64Histogram {
			return RuntimeSnapshot{}, fmt.Errorf("runtime histogram %s: %w", sample.Name, errEnvironmentData)
		}
		var err error
		*destination, err = snapshotHistogram(sample.Value.Float64Histogram())
		if err != nil {
			return RuntimeSnapshot{}, fmt.Errorf("runtime histogram %s: %w", sample.Name, err)
		}
	}
	return result, nil
}

func snapshotHistogram(raw *metrics.Float64Histogram) (Histogram, error) {
	if raw == nil || (len(raw.Buckets) != 0 && len(raw.Counts)+1 != len(raw.Buckets)) ||
		(len(raw.Buckets) == 0 && len(raw.Counts) != 0) || len(raw.Buckets) == 1 {
		return Histogram{}, fmt.Errorf("runtime histogram dimensions: %w", errEnvironmentData)
	}
	result := Histogram{BoundsSeconds: make([]string, len(raw.Buckets)), Counts: slices.Clone(raw.Counts)}
	for i, bound := range raw.Buckets {
		if math.IsNaN(bound) || (i > 0 && bound <= raw.Buckets[i-1]) {
			return Histogram{}, fmt.Errorf("runtime histogram bounds: %w", errEnvironmentData)
		}
		result.BoundsSeconds[i] = strconv.FormatFloat(bound, 'g', -1, 64)
	}
	return result, nil
}
