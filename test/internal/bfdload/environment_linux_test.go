//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCgroupLocation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, member, mount, point, relative string
		valid                                bool
	}{
		{
			"host", "0::/agents/job\n", "39 37 0:27 / /sys/fs/cgroup rw - cgroup2 none rw\n",
			"/sys/fs/cgroup", "agents/job", true,
		},
		{
			"namespace", "0::/\n", "39 37 0:27 / /sys/fs/cgroup rw - cgroup2 none rw\n",
			"/sys/fs/cgroup", ".", true,
		},
		{
			"bind", "0::/agents/job\n", "39 37 0:27 /agents /cg rw shared:2 - cgroup2 none rw\n",
			"/cg", "job", true,
		},
		{
			"escaped", "0::/agents/job\n", "39 37 0:27 /agents /cg\\040space rw - cgroup2 none rw\n",
			"/cg space", "job", true,
		},
		{"prefix", "0::/agents2/job\n", "39 37 0:27 /agents /cg rw - cgroup2 none rw\n", "", "", false},
		{"outside", "0::/../sibling\n", "39 37 0:27 / /cg rw - cgroup2 none rw\n", "", "", false},
		{"v1", "3:cpu:/job\n", "39 37 0:27 / /cg rw - cgroup none rw\n", "", "", false},
		{"duplicate", "0::/a\n0::/b\n", "39 37 0:27 / /cg rw - cgroup2 none rw\n", "", "", false},
		{"ambiguous", "0::/a\n", "39 37 0:27 / /cg rw - cgroup2 none rw\n" +
			"40 37 0:27 / /cg2 rw - cgroup2 none rw\n", "", "", false},
		{"specific", "0::/agents/job\n", "39 37 0:27 / /cg rw - cgroup2 none rw\n" +
			"40 37 0:27 /agents /cg2 rw - cgroup2 none rw\n", "/cg2", "job", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveCgroup([]byte(tc.member), []byte(tc.mount))
			if (err == nil) != tc.valid {
				t.Fatalf("location=%+v error=%v, valid=%v", got, err, tc.valid)
			}
			if tc.valid && (got.mountPoint != tc.point || got.relative != tc.relative) {
				t.Fatalf("location=%+v, want %s/%s", got, tc.point, tc.relative)
			}
		})
	}
}

func TestKernelResourceParsers(t *testing.T) {
	t.Parallel()
	unlimited, err := parseLimit("max\n")
	if err != nil || !unlimited.Unlimited {
		t.Fatalf("unlimited=%v error=%v", unlimited, err)
	}
	zero, err := parseLimit("0\n")
	if err != nil || zero.Unlimited || zero.Value != 0 {
		t.Fatalf("zero limit=%v error=%v", zero, err)
	}
	for _, invalid := range []string{"", "-1", "18446744073709551616", "1 2"} {
		if _, parseErr := parseLimit(invalid); parseErr == nil {
			t.Fatalf("invalid limit accepted: %q", invalid)
		}
	}
	fields, err := parseCounters([]byte("usage_usec 17\nnr_throttled 0\n"))
	if err != nil || fields["usage_usec"] != 17 || fields["nr_throttled"] != 0 {
		t.Fatalf("counters=%v error=%v", fields, err)
	}
	for _, invalid := range []string{"max 2\nmax 3\n", "max\n", "max -1\n", "max 18446744073709551616\n"} {
		if _, err := parseCounters([]byte(invalid)); err == nil {
			t.Fatalf("invalid counters accepted: %q", invalid)
		}
	}
	if got, err := parseProcField([]byte("Rss: 123 kB\n"), "Rss", true); err != nil || got != 125952 {
		t.Fatalf("rss bytes=%d error=%v", got, err)
	}
	for _, invalid := range []string{
		"", "Rss: 3 MB\n", "Rss: -1 kB\n", "Rss: 18014398509481984 kB\n",
		"Rss: 1 kB\nRss: 2 kB\n",
	} {
		if _, err := parseProcField([]byte(invalid), "Rss", true); err == nil {
			t.Fatalf("invalid RSS accepted: %q", invalid)
		}
	}
}

func environmentRoot(t *testing.T, files map[string]string) *os.Root {
	t.Helper()
	dir := t.TempDir()
	for name, value := range files {
		file := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}

func TestResourceReadBound(t *testing.T) {
	t.Parallel()
	root := environmentRoot(t, map[string]string{"huge": strings.Repeat("x", maxEnvironmentFileSize+1)})
	if _, err := readEnvironmentFile(t.Context(), root, "huge"); !errors.Is(err, ErrEnvironmentLimit) {
		t.Fatalf("oversized resource error=%v", err)
	}
}

func TestDescriptorReadBound(t *testing.T) {
	t.Parallel()
	root := environmentRoot(t, map[string]string{"self/fd/0": "", "self/fd/1": "", "self/fd/2": ""})
	if _, err := countFDs(t.Context(), root, 2); !errors.Is(err, ErrEnvironmentLimit) {
		t.Fatalf("descriptor bound error=%v", err)
	}
	if got, err := countFDs(t.Context(), root, 3); err != nil || got != 3 {
		t.Fatalf("descriptor count=%d error=%v", got, err)
	}
}

func TestCgroupRequiredCounters(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"cpu.max": "200000 100000\n", "cpu.stat": "usage_usec 17\nuser_usec 10\nsystem_usec 7\n" +
			"nr_periods 2\nnr_throttled 1\nthrottled_usec 9\n",
		"memory.current": "12345\n", "memory.max": "2147483648\n", "memory.high": "max\n",
		"memory.events": "low 0\nhigh 2\nmax 3\noom 0\noom_kill 0\n",
		"pids.current":  "17\n", "pids.max": "512\n",
	}
	for _, broken := range []string{"", "localZero", "cpu.stat", "memory.events", "cpu.max"} {
		t.Run("broken="+broken, func(t *testing.T) {
			t.Parallel()
			fixture := maps.Clone(files)
			if broken == "localZero" {
				fixture["cpu.stat.local"] = "throttled_usec 0\n"
			} else if broken != "" {
				fixture[broken] = "unrelated 0\n"
			}
			got, err := readCgroupSnapshot(t.Context(), environmentRoot(t, fixture))
			if broken != "" && broken != "localZero" {
				if err == nil {
					t.Fatalf("missing required counters accepted: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.CPUQuotaUS.Unlimited || got.CPUQuotaUS.Value != 200000 || got.CPUPeriodUS != 100000 ||
				got.MemoryCurrentBytes != 12345 || got.MemoryMaxBytes.Unlimited || got.MemoryMaxBytes.Value != 2147483648 ||
				!got.MemoryHighBytes.Unlimited || got.CPUStat["throttled_usec"] != 9 || got.MemoryEvents["max"] != 3 ||
				got.PidsCurrent != 17 || got.PidsMax.Unlimited || got.PidsMax.Value != 512 {
				t.Fatalf("resource snapshot loses counters/units: %+v", got)
			}
			if broken == "localZero" {
				if got.LocalThrottledUS == nil || *got.LocalThrottledUS != 0 || got.LocalThrottledError != "" {
					t.Fatalf("known local zero lost: %+v", got)
				}
			} else if got.LocalThrottledUS != nil || got.LocalThrottledError == "" {
				t.Fatal("missing optional local throttling became a known zero")
			}
		})
	}
}

func TestCaptureEnvironment(t *testing.T) {
	t.Parallel()
	got, err := CaptureEnvironment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Process.RSSBytes == 0 || got.Process.Threads == 0 || got.Process.FDs == 0 ||
		got.Process.ObserverCPUAffinity == 0 || got.Process.PID != os.Getpid() ||
		got.Runtime.GOMAXPROCS != runtime.GOMAXPROCS(0) || got.Runtime.Version != runtime.Version() ||
		got.Runtime.Goroutines == 0 || got.ElapsedNS <= 0 || got.StartedAtUTC.IsZero() {
		t.Fatalf("unknown process/runtime measurements presented as zero: %+v", got)
	}
	if got.Cgroup == nil && got.CgroupError == "" {
		t.Fatal("missing cgroup snapshot has no explicit reason")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Environment
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Process.RSSBytes != got.Process.RSSBytes ||
		decoded.Runtime.MemoryLimitBytes != got.Runtime.MemoryLimitBytes {
		t.Fatal("resource JSON roundtrip loses measured counters")
	}
	t.Logf("pid=%d rss_bytes=%d threads=%d fds=%d GOMAXPROCS=%d capture_ns=%d cgroup_available=%v reason=%s",
		got.Process.PID, got.Process.RSSBytes, got.Process.Threads, got.Process.FDs, got.Runtime.GOMAXPROCS,
		got.ElapsedNS, got.Cgroup != nil, got.CgroupError)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CaptureEnvironment(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capture error=%v", err)
	}
}

func TestUnavailableCgroupIsExplicit(t *testing.T) {
	t.Parallel()
	proc := environmentRoot(t, map[string]string{
		"self/status": "Threads: 3\n", "self/smaps_rollup": "Rss: 7 kB\n",
		"self/fd/0": "", "self/cgroup": "2:cpu:/legacy\n", "self/mountinfo": "",
	})
	got, err := captureEnvironment(t.Context(), proc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Cgroup != nil || got.CgroupError == "" || got.Process.RSSBytes != 7168 {
		t.Fatalf("unavailable cgroup became a zero-valued success: %+v", got)
	}
}
