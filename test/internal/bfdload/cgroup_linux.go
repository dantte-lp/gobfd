//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type cgroupLocation struct {
	memberPath, mountRoot, mountPoint, relative string
}

const (
	mountInfoFieldCount = 6
	kernelPairFields    = 2
)

func unifiedMembership(membership []byte) (string, error) {
	var member string
	for line := range strings.SplitSeq(string(membership), "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			if member != "" || !cleanAbsolute(path) {
				return "", fmt.Errorf("unified cgroup membership: %w", errEnvironmentData)
			}
			member = path
		}
	}
	if member == "" {
		return "", fmt.Errorf("unified cgroup unavailable: %w", errEnvironmentData)
	}
	return member, nil
}

func resolveCgroup(membership, mountinfo []byte) (cgroupLocation, error) {
	member, err := unifiedMembership(membership)
	if err != nil {
		return cgroupLocation{}, err
	}
	var best cgroupLocation
	var ambiguous bool
	for line := range strings.SplitSeq(string(mountinfo), "\n") {
		candidate, matches, parseErr := parseCgroupMount(line, member)
		if parseErr != nil {
			return cgroupLocation{}, parseErr
		}
		if !matches {
			continue
		}
		if len(candidate.mountRoot) > len(best.mountRoot) {
			best = candidate
			ambiguous = false
		} else if len(candidate.mountRoot) == len(best.mountRoot) {
			ambiguous = true
		}
	}
	if best.mountPoint == "" || ambiguous {
		return cgroupLocation{}, fmt.Errorf("cgroup mount unavailable or ambiguous: %w", errEnvironmentData)
	}
	return best, nil
}

func parseCgroupMount(line, member string) (cgroupLocation, bool, error) {
	left, right, ok := strings.Cut(line, " - ")
	if !ok {
		return cgroupLocation{}, false, nil
	}
	tail := strings.Fields(right)
	if len(tail) < 3 || tail[0] != "cgroup2" {
		return cgroupLocation{}, false, nil
	}
	fields := strings.Fields(left)
	if len(fields) < mountInfoFieldCount {
		return cgroupLocation{}, false, fmt.Errorf("cgroup mount fields: %w", errEnvironmentData)
	}
	root, point := unescapeMountField(fields[3]), unescapeMountField(fields[4])
	if !cleanAbsolute(root) || !cleanAbsolute(point) {
		return cgroupLocation{}, false, fmt.Errorf("cgroup mount path: %w", errEnvironmentData)
	}
	relative, err := filepath.Rel(root, member)
	if err != nil {
		return cgroupLocation{}, false, fmt.Errorf("resolve cgroup mount path: %w", err)
	}
	if !filepath.IsLocal(relative) {
		return cgroupLocation{}, false, nil
	}
	return cgroupLocation{memberPath: member, mountRoot: root, mountPoint: point, relative: relative}, true, nil
}

func cleanAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

func unescapeMountField(value string) string {
	return strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\").Replace(value)
}

func snapshotCurrentCgroup(ctx context.Context, proc *os.Root) (*CgroupSnapshot, error) {
	membership, err := readEnvironmentFile(ctx, proc, "self/cgroup")
	if err != nil {
		return nil, err
	}
	mountinfo, err := readEnvironmentFile(ctx, proc, "self/mountinfo")
	if err != nil {
		return nil, err
	}
	location, err := resolveCgroup(membership, mountinfo)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Join(location.mountPoint, location.relative))
	if err != nil {
		return nil, fmt.Errorf("open current cgroup: %w", err)
	}
	result, readErr := readCgroupSnapshot(ctx, root)
	if joinedErr := errors.Join(readErr, root.Close()); joinedErr != nil {
		return nil, fmt.Errorf("read and close current cgroup: %w", joinedErr)
	}
	result.MemberPath, result.MountRoot, result.MountPoint = location.memberPath, location.mountRoot, location.mountPoint
	return result, nil
}

func readCgroupSnapshot(ctx context.Context, root *os.Root) (*CgroupSnapshot, error) {
	var result CgroupSnapshot
	for _, item := range []struct {
		file        string
		destination *Limit
	}{
		{"memory.max", &result.MemoryMaxBytes},
		{"memory.high", &result.MemoryHighBytes},
		{"pids.max", &result.PidsMax},
	} {
		data, err := readEnvironmentFile(ctx, root, item.file)
		if err != nil {
			return nil, err
		}
		*item.destination, err = parseLimit(string(data))
		if err != nil {
			return nil, fmt.Errorf("cgroup %s: %w", item.file, err)
		}
	}
	for _, item := range []struct {
		file        string
		destination *uint64
	}{
		{"memory.current", &result.MemoryCurrentBytes}, {"pids.current", &result.PidsCurrent},
	} {
		current, err := readEnvironmentFile(ctx, root, item.file)
		if err != nil {
			return nil, err
		}
		*item.destination, err = strconv.ParseUint(strings.TrimSpace(string(current)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cgroup %s: %w", item.file, err)
		}
	}
	var err error
	result.CPUQuotaUS, result.CPUPeriodUS, err = readCPUQuota(ctx, root)
	if err != nil {
		return nil, err
	}
	result.CPUStat, err = readRequiredCounters(ctx, root, "cpu.stat",
		"usage_usec", "user_usec", "system_usec", "nr_periods", "nr_throttled", "throttled_usec")
	if err != nil {
		return nil, err
	}
	result.MemoryEvents, err = readRequiredCounters(ctx, root, "memory.events", "low", "high", "max", "oom", "oom_kill")
	if err != nil {
		return nil, err
	}
	local, err := readRequiredCounters(ctx, root, "cpu.stat.local", "throttled_usec")
	if err != nil {
		result.LocalThrottledError = err.Error()
	} else {
		value := local["throttled_usec"]
		result.LocalThrottledUS = &value
	}
	return &result, nil
}

func readCPUQuota(ctx context.Context, root *os.Root) (Limit, uint64, error) {
	data, err := readEnvironmentFile(ctx, root, "cpu.max")
	if err != nil {
		return Limit{}, 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) != kernelPairFields {
		return Limit{}, 0, fmt.Errorf("cgroup cpu.max fields: %w", errEnvironmentData)
	}
	quota, err := parseLimit(parts[0])
	if err != nil || (!quota.Unlimited && quota.Value == 0) {
		return Limit{}, 0, fmt.Errorf("cgroup CPU quota: %w", errors.Join(err, errEnvironmentData))
	}
	period, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || period == 0 {
		return Limit{}, 0, fmt.Errorf("cgroup CPU period: %w", errors.Join(err, errEnvironmentData))
	}
	return quota, period, nil
}

func readRequiredCounters(
	ctx context.Context, root *os.Root, name string, required ...string,
) (map[string]uint64, error) {
	data, err := readEnvironmentFile(ctx, root, name)
	if err != nil {
		return nil, err
	}
	values, err := parseCounters(data)
	if err != nil {
		return nil, fmt.Errorf("parse resource %s: %w", name, err)
	}
	for _, key := range required {
		if _, ok := values[key]; !ok {
			return nil, fmt.Errorf("missing resource %s counter %s: %w", name, key, errEnvironmentData)
		}
	}
	return values, nil
}

func parseCounters(data []byte) (map[string]uint64, error) {
	values := make(map[string]uint64)
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != kernelPairFields {
			return nil, fmt.Errorf("resource counter fields: %w", errEnvironmentData)
		}
		if _, duplicate := values[fields[0]]; duplicate {
			return nil, fmt.Errorf("duplicate resource counter %s: %w", fields[0], errEnvironmentData)
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("resource counter %s: %w", fields[0], err)
		}
		values[fields[0]] = value
	}
	return values, nil
}

func parseLimit(data string) (Limit, error) {
	if strings.TrimSpace(data) == "max" {
		return Limit{Unlimited: true}, nil
	}
	value, err := strconv.ParseUint(strings.TrimSpace(data), 10, 64)
	if err != nil {
		return Limit{}, fmt.Errorf("resource limit: %w", err)
	}
	return Limit{Value: value}, nil
}
