//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	maxEnvironmentFileSize = 1 << 20
	maxEnvironmentFDs      = 65536
)

// CaptureEnvironment observes this process without changing runtime policy.
func CaptureEnvironment(ctx context.Context) (Environment, error) {
	if err := ctx.Err(); err != nil {
		return Environment{}, fmt.Errorf("capture environment: %w", err)
	}
	started := time.Now()
	proc, err := os.OpenRoot("/proc")
	if err != nil {
		return Environment{}, fmt.Errorf("open process observation root: %w", err)
	}
	result, captureErr := captureEnvironment(ctx, proc, started)
	if joinedErr := errors.Join(captureErr, proc.Close()); joinedErr != nil {
		return Environment{}, fmt.Errorf("capture and close process environment: %w", joinedErr)
	}
	return result, nil
}

func captureEnvironment(ctx context.Context, proc *os.Root, started time.Time) (Environment, error) {
	var result Environment
	var err error
	result.StartedAtUTC = started.UTC()
	result.Process, err = snapshotProcess(ctx, proc)
	if err != nil {
		return Environment{}, err
	}
	result.Runtime, err = snapshotRuntime()
	if err != nil {
		return Environment{}, err
	}
	result.Cgroup, err = snapshotCurrentCgroup(ctx, proc)
	if err != nil {
		result.CgroupError = err.Error()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Environment{}, fmt.Errorf("finish environment capture: %w", contextErr)
	}
	result.ElapsedNS = time.Since(started).Nanoseconds()
	return result, nil
}

func snapshotProcess(ctx context.Context, proc *os.Root) (ProcessSnapshot, error) {
	status, err := readEnvironmentFile(ctx, proc, "self/status")
	if err != nil {
		return ProcessSnapshot{}, err
	}
	threads, err := parseProcField(status, "Threads", false)
	if err != nil || threads == 0 {
		return ProcessSnapshot{}, fmt.Errorf("read kernel thread count: %w", errors.Join(err, errEnvironmentData))
	}
	smaps, err := readEnvironmentFile(ctx, proc, "self/smaps_rollup")
	if err != nil {
		return ProcessSnapshot{}, err
	}
	rss, err := parseProcField(smaps, "Rss", true)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	fds, err := countFDs(ctx, proc, maxEnvironmentFDs)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return ProcessSnapshot{}, fmt.Errorf("read process context switches: %w", err)
	}
	var affinity unix.CPUSet
	if err := unix.SchedGetaffinity(0, &affinity); err != nil {
		return ProcessSnapshot{}, fmt.Errorf("read observer thread CPU affinity: %w", err)
	}
	return ProcessSnapshot{
		PID: os.Getpid(), RSSBytes: rss, Threads: threads, FDs: fds,
		ObserverCPUAffinity: affinity.Count(), VoluntaryContextSwitches: usage.Nvcsw,
		InvoluntaryContextSwitches: usage.Nivcsw,
	}, nil
}

func countFDs(ctx context.Context, proc *os.Root, limit uint64) (uint64, error) {
	directory, err := proc.Open("self/fd")
	if err != nil {
		return 0, fmt.Errorf("open process descriptors: %w", err)
	}
	count, readErr := readFDs(ctx, directory, limit)
	if joinedErr := errors.Join(readErr, directory.Close()); joinedErr != nil {
		return 0, fmt.Errorf("read and close process descriptors: %w", joinedErr)
	}
	return count, nil
}

func readFDs(ctx context.Context, directory *os.File, limit uint64) (uint64, error) {
	var count uint64
	for {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("count process descriptors: %w", err)
		}
		names, readErr := directory.Readdirnames(256)
		count += uint64(len(names))
		if count > limit {
			return 0, fmt.Errorf("count process descriptors: %w", ErrEnvironmentLimit)
		}
		if errors.Is(readErr, io.EOF) {
			return count, nil
		}
		if readErr != nil {
			return 0, fmt.Errorf("read process descriptors: %w", readErr)
		}
	}
}

func readEnvironmentFile(ctx context.Context, root *os.Root, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read resource %s: %w", name, err)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open resource %s: %w", name, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxEnvironmentFileSize+1))
	if joinedErr := errors.Join(readErr, file.Close()); joinedErr != nil {
		return nil, fmt.Errorf("read and close resource %s: %w", name, joinedErr)
	}
	if len(data) > maxEnvironmentFileSize {
		return nil, fmt.Errorf("read resource %s: %w", name, ErrEnvironmentLimit)
	}
	return data, nil
}

func parseProcField(data []byte, name string, kibibytes bool) (uint64, error) {
	var found bool
	var result uint64
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != name {
			continue
		}
		fields := strings.Fields(value)
		if found || len(fields) == 0 || (!kibibytes && len(fields) != 1) ||
			(kibibytes && (len(fields) != 2 || fields[1] != "kB")) {
			return 0, fmt.Errorf("process field %s format: %w", name, errEnvironmentData)
		}
		var err error
		result, err = strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("process field %s value: %w", name, err)
		}
		if kibibytes {
			if result > math.MaxUint64/1024 {
				return 0, fmt.Errorf("process field %s overflow: %w", name, errEnvironmentData)
			}
			result *= 1024
		}
		found = true
	}
	if !found {
		return 0, fmt.Errorf("missing process field %s: %w", name, errEnvironmentData)
	}
	return result, nil
}
