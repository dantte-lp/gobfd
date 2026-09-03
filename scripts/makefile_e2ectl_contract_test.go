package main

import (
	"strings"
	"testing"
)

func TestMakefileReportPipelinesDelegateToE2ECTL(t *testing.T) {
	t.Parallel()

	makefile := readContractFile(t, "../Makefile")
	for _, forbidden := range []string{"bash -o pipefail", "$(DC) exec dev bash"} {
		if strings.Contains(makefile, forbidden) {
			t.Errorf("Makefile retains Bash entrypoint %q", forbidden)
		}
	}
	for target, runnerTarget := range map[string]string{
		"e2e-core-testcontainers":          "core",
		"int-bgp-failover-testcontainers":  "bgp-fast-failover",
		"int-haproxy-testcontainers":       "haproxy-health",
		"int-observability-testcontainers": "observability",
	} {
		recipe := makeTargetRecipe(t, makefile, target)
		want := "go run ./test/cmd/e2ectl " + runnerTarget
		if strings.Count(recipe, want) != 1 {
			t.Errorf("Makefile target %s must delegate exactly once to %q", target, want)
		}
	}
	for field := range strings.FieldsSeq(makefile[:strings.Index(makefile, "# === Lifecycle ===")]) {
		if field == "shell" {
			t.Error("Makefile retains shell in .PHONY")
		}
	}
	if strings.Contains(makefile, "\nshell:") {
		t.Error("Makefile retains interactive shell target")
	}
}

func makeTargetRecipe(t *testing.T, makefile, target string) string {
	t.Helper()

	start := strings.Index(makefile, "\n"+target+":")
	if start < 0 {
		t.Fatalf("Makefile target %s is missing", target)
	}
	rest := makefile[start+1:]
	end := strings.Index(rest, "\n\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
