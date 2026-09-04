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
		want := "$(EXEC) $(E2ECTL_BIN) " + runnerTarget
		if strings.Count(recipe, want) != 1 {
			t.Errorf("Makefile target %s must delegate exactly once to %q", target, want)
		}
		if !strings.Contains(recipe, "e2ectl-build") {
			t.Errorf("Makefile target %s does not depend on e2ectl-build", target)
		}
		if strings.Contains(recipe, "go run ./test/cmd/e2ectl") {
			t.Errorf("Makefile target %s uses go run and cannot preserve the exact child exit status", target)
		}
	}
	buildRecipe := makeTargetRecipe(t, makefile, "e2ectl-build")
	for _, required := range []string{
		"e2ectl-build: dev-ensure",
		"$(EXEC) go build -trimpath -o $(E2ECTL_BIN) ./test/cmd/e2ectl",
	} {
		if !strings.Contains(buildRecipe, required) {
			t.Errorf("Makefile e2ectl-build target lacks %q", required)
		}
	}
	beforeLifecycle, _, found := strings.Cut(makefile, "# === Lifecycle ===")
	if !found {
		t.Fatal("Makefile lifecycle marker is missing")
	}
	for field := range strings.FieldsSeq(beforeLifecycle) {
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

	lines := strings.Split(makefile, "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, target+":") {
			continue
		}
		end := index + 1
		for end < len(lines) && (lines[end] == "" || strings.HasPrefix(lines[end], "\t")) {
			end++
		}
		return strings.Join(lines[index:end], "\n")
	}
	t.Fatalf("Makefile target %s is missing", target)
	return ""
}
