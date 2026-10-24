//go:build linux

package bfdload

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceAdmissionRejectsTighterAncestorsAndUnknowns(t *testing.T) {
	t.Parallel()
	leaf := CgroupSnapshot{
		MemberPath: "/agents.slice/workload", CPUQuotaUS: Limit{Value: 800000}, CPUPeriodUS: 100000,
		MemoryMaxBytes: Limit{Value: 8 << 30}, MemoryHighBytes: Limit{Unlimited: true},
		PidsMax: Limit{Unlimited: true},
	}
	parent := CgroupSnapshot{
		MemberPath: "/agents.slice", CPUQuotaUS: Limit{Value: 1000000}, CPUPeriodUS: 100000,
		MemoryMaxBytes: Limit{Unlimited: true}, MemoryHighBytes: Limit{Value: 40 << 30},
		PidsMax: Limit{Unlimited: true},
	}
	env := Environment{
		Process: ProcessSnapshot{ObserverCPUAffinity: 8},
		Runtime: RuntimeSnapshot{Version: "go1.27.3", GOOS: "linux", GOMAXPROCS: 8, GCPercent: 100},
	}
	if err := validateReferenceResources(env, []CgroupSnapshot{leaf, parent}, 1064); err != nil {
		t.Fatalf("approved visible envelope rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Environment, *[]CgroupSnapshot)
	}{
		{"no_ancestors", func(_ *Environment, chain *[]CgroupSnapshot) { *chain = nil }},
		{"wrong_gomaxprocs", func(e *Environment, _ *[]CgroupSnapshot) { e.Runtime.GOMAXPROCS = 2 }},
		{"wrong_compiler", func(e *Environment, _ *[]CgroupSnapshot) { e.Runtime.Version = "go1.27.2" }},
		{"wrong_gc", func(e *Environment, _ *[]CgroupSnapshot) { e.Runtime.GCPercent = 50 }},
		{"affinity", func(e *Environment, _ *[]CgroupSnapshot) { e.Process.ObserverCPUAffinity = 7 }},
		{"child_cpu", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[0].CPUQuotaUS.Value = 200000 }},
		{"child_memory", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[0].MemoryMaxBytes.Value = 2 << 30 }},
		{"unbounded_child", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[0].MemoryMaxBytes.Unlimited = true }},
		{"ancestor_cpu", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[1].CPUQuotaUS.Value = 799999 }},
		{"ancestor_high", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[1].MemoryHighBytes.Value = 7 << 30 }},
		{"ancestor_max", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[1].MemoryMaxBytes = Limit{Value: 7 << 30} }},
		{"zero_period", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[1].CPUPeriodUS = 0 }},
		{"quota_overflow", func(_ *Environment, c *[]CgroupSnapshot) { (*c)[0].CPUPeriodUS = math.MaxUint64 }},
		{"missing_middle", func(_ *Environment, c *[]CgroupSnapshot) {
			(*c)[0].MemberPath = "/agents.slice/hidden/workload"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := env
			chain := []CgroupSnapshot{leaf, parent}
			tc.change(&e, &chain)
			if err := validateReferenceResources(e, chain, 1064); !errors.Is(err, ErrAdmission) {
				t.Fatalf("invalid reference envelope admitted: %v", err)
			}
		})
	}
}

func TestAdmissionChainReadsEveryAncestorAndFailsClosed(t *testing.T) {
	t.Parallel()
	files := admissionFiles()
	chain, err := readAdmissionChain(t.Context(), environmentRoot(t, files), "/agents/workload")
	if err != nil || len(chain) != 2 || chain[0].MemberPath != "/agents/workload" || chain[1].MemberPath != "/agents" {
		t.Fatalf("incomplete actual file traversal: %+v: %v", chain, err)
	}
	for _, member := range []string{"/", "/agents/../outside", "/missing/workload"} {
		if _, err := readAdmissionChain(t.Context(), environmentRoot(t, files), member); err == nil {
			t.Fatalf("unverifiable membership accepted: %q", member)
		}
	}
	delete(files, "agents/cpu.max")
	if _, err := readAdmissionChain(t.Context(), environmentRoot(t, files), "/agents/workload"); err == nil {
		t.Fatal("missing ancestor cap ignored")
	}
	files["agents/cpu.max"] = strings.Repeat("9", 100)
	if _, err := readAdmissionChain(t.Context(), environmentRoot(t, files), "/agents/workload"); err == nil {
		t.Fatal("malformed ancestor cap ignored")
	}
}

func TestReferenceAdmissionRejectsIterationRuntime(t *testing.T) {
	t.Parallel()
	environment, err := CaptureEnvironment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if environment.Runtime.GOMAXPROCS != 2 {
		t.Skip("requires the bounded source iteration runner")
	}
	if _, err := AdmitReferenceResources(t.Context(), 1000); !errors.Is(err, ErrAdmission) {
		t.Fatalf("source iteration runtime admitted reference load: %v", err)
	}
}

func TestReferenceAdmissionRejectsThreadExhaustion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file, value string
	}{
		{"leaf_cap", "agents/workload/pids.max", "512\n"},
		{"ancestor_cap", "agents/pids.max", "512\n"},
		{"ancestor_usage", "agents/pids.current", "4000\n"},
		{"missing", "agents/pids.max", ""},
		{"missing_current", "agents/workload/pids.current", ""},
		{"malformed", "agents/pids.current", "max\n"},
		{"malformed_max", "agents/pids.max", "-1\n"},
		{"zero_max", "agents/pids.max", "0\n"},
		{"current_above_max", "agents/pids.current", "5000\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := admissionFiles()
			files[tc.file] = tc.value
			if tc.value == "" {
				delete(files, tc.file)
			}
			chain, err := readAdmissionChain(t.Context(), environmentRoot(t, files), "/agents/workload")
			if err != nil {
				return
			}
			env := Environment{
				Process: ProcessSnapshot{ObserverCPUAffinity: 8},
				Runtime: RuntimeSnapshot{Version: "go1.27.3", GOOS: "linux", GOMAXPROCS: 8, GCPercent: 100},
			}
			if err := validateReferenceResources(env, chain, 1064); !errors.Is(err, ErrAdmission) {
				t.Fatalf("1000 pinned sessions admitted without task headroom: %v", err)
			}
		})
	}
}

func admissionFiles() map[string]string {
	files := make(map[string]string)
	for _, prefix := range []string{"agents/", "agents/workload/"} {
		for name, value := range map[string]string{
			"cpu.max": "800000 100000\n", "cpu.stat": "usage_usec 17\nuser_usec 10\nsystem_usec 7\n" +
				"nr_periods 2\nnr_throttled 0\nthrottled_usec 0\n",
			"memory.current": "12345\n", "memory.max": "8589934592\n", "memory.high": "max\n",
			"memory.events": "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n",
			"pids.current":  "10\n", "pids.max": "4096\n",
		} {
			files[prefix+name] = value
		}
	}
	return files
}

func TestReferenceTaskBudgetBoundaries(t *testing.T) {
	t.Parallel()
	for _, sessions := range []int{1000, 5000} {
		t.Run(strconv.Itoa(sessions), func(t *testing.T) {
			t.Parallel()
			tasks := uint64(sessions) + baselineTaskMargin
			for _, tc := range []struct {
				name      string
				current   uint64
				limit     Limit
				wantError bool
			}{
				{"exact", 10, Limit{Value: tasks + 10}, false},
				{"one_short", 10, Limit{Value: tasks + 9}, true},
				{"overflow", math.MaxUint64, Limit{Value: math.MaxUint64}, true},
				{"near_max", math.MaxUint64 - tasks, Limit{Value: math.MaxUint64}, false},
				{"unlimited", 10, Limit{Unlimited: true}, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					chain := []CgroupSnapshot{{
						MemberPath: "/workload", CPUQuotaUS: Limit{Value: 800000}, CPUPeriodUS: 100000,
						MemoryMaxBytes: Limit{Value: 8 << 30}, MemoryHighBytes: Limit{Unlimited: true},
						PidsCurrent: tc.current, PidsMax: tc.limit,
					}}
					err := validateReferenceLimits(chain, tasks)
					if (err != nil) != tc.wantError || (err != nil && !errors.Is(err, ErrAdmission)) {
						t.Fatalf("task budget %d: %v", tasks, err)
					}
				})
			}
		})
	}
}

func TestReferenceAdmissionRetainsTaskBudget(t *testing.T) {
	t.Parallel()
	for _, sessions := range []int{1000, 5000} {
		admission, admissionErr := AdmitReferenceResources(t.Context(), sessions)
		if admissionErr != nil && !errors.Is(admissionErr, ErrAdmission) {
			t.Fatal(admissionErr)
		}
		encoded, err := json.Marshal(admission)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ResourceAdmission
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.AdditionalTaskBudget != uint64(sessions)+baselineTaskMargin {
			t.Fatalf("lost admission budget for %d sessions: %s", sessions, encoded)
		}
	}
	for _, sessions := range []int{-1, 0, sourcePortCapacity + 1, math.MaxInt} {
		if _, err := AdmitReferenceResources(t.Context(), sessions); !errors.Is(err, ErrAdmission) {
			t.Fatalf("invalid session count %d admitted: %v", sessions, err)
		}
	}
}
