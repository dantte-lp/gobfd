//go:build linux

package bfdload

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestReferenceAdmissionRejectsTighterAncestorsAndUnknowns(t *testing.T) {
	t.Parallel()
	leaf := CgroupSnapshot{
		MemberPath: "/agents.slice/workload", CPUQuotaUS: Limit{Value: 800000}, CPUPeriodUS: 100000,
		MemoryMaxBytes: Limit{Value: 8 << 30}, MemoryHighBytes: Limit{Unlimited: true},
	}
	parent := CgroupSnapshot{
		MemberPath: "/agents.slice", CPUQuotaUS: Limit{Value: 1000000}, CPUPeriodUS: 100000,
		MemoryMaxBytes: Limit{Unlimited: true}, MemoryHighBytes: Limit{Value: 40 << 30},
	}
	env := Environment{
		Process: ProcessSnapshot{ObserverCPUAffinity: 8},
		Runtime: RuntimeSnapshot{Version: "go1.27.3", GOOS: "linux", GOMAXPROCS: 8, GCPercent: 100},
	}
	if err := validateReferenceResources(env, []CgroupSnapshot{leaf, parent}); err != nil {
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
			if err := validateReferenceResources(e, chain); !errors.Is(err, ErrAdmission) {
				t.Fatalf("invalid reference envelope admitted: %v", err)
			}
		})
	}
}

func TestAdmissionChainReadsEveryAncestorAndFailsClosed(t *testing.T) {
	t.Parallel()
	files := make(map[string]string)
	for _, prefix := range []string{"agents/", "agents/workload/"} {
		for name, value := range map[string]string{
			"cpu.max": "800000 100000\n", "cpu.stat": "usage_usec 17\nuser_usec 10\nsystem_usec 7\n" +
				"nr_periods 2\nnr_throttled 0\nthrottled_usec 0\n",
			"memory.current": "12345\n", "memory.max": "8589934592\n", "memory.high": "max\n",
			"memory.events": "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n",
		} {
			files[prefix+name] = value
		}
	}
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
	if _, err := AdmitReferenceResources(t.Context()); !errors.Is(err, ErrAdmission) {
		t.Fatalf("source iteration runtime admitted reference load: %v", err)
	}
}
