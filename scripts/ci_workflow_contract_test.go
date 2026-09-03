package main

import (
	"strings"
	"testing"
)

func TestCIWorkflowBuildAndSonarStepsDelegateToCICTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		workflow  string
		step      string
		command   string
		required  []string
		forbidden []string
	}{
		{
			name:      "Sonar token",
			workflow:  "../.github/workflows/build.yml",
			step:      "Check Sonar token",
			command:   "go run ./test/cmd/cictl sonar-mode",
			required:  []string{"SONAR_TOKEN_PRESENT: ${{ secrets.SONAR_TOKEN != '' }}"},
			forbidden: []string{"SONAR_TOKEN:", "shell: bash", "run: |", "mode=run", "skip-dependabot", "SONAR_TOKEN is required"},
		},
		{
			name:      "binary build",
			workflow:  "../.github/workflows/ci.yml",
			step:      "Build",
			command:   "go run ./test/cmd/cictl build --output /tmp/gobfd-build",
			forbidden: []string{"shell: bash", "run: |", "VERSION=", "GIT_COMMIT=", "BUILD_DATE=", "go build"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			workflow := readContractFile(t, test.workflow)
			step := namedWorkflowStep(t, workflow, test.step)
			wantRun := "        run: " + test.command + "\n"
			if strings.Count(step, wantRun) != 1 {
				t.Errorf("workflow step %q must contain exactly one %q", test.step, strings.TrimSpace(wantRun))
			}
			if got := strings.Count(step, "\n        run:"); got != 1 {
				t.Errorf("workflow step %q has %d run programs, want exactly one", test.step, got)
			}
			for _, marker := range test.required {
				if !strings.Contains(step, marker) {
					t.Errorf("workflow step %q lacks required marker %q", test.step, marker)
				}
			}
			for _, marker := range test.forbidden {
				if strings.Contains(step, marker) {
					t.Errorf("workflow step %q retains old shell marker %q", test.step, marker)
				}
			}
		})
	}
}

func TestCIWorkflowSupplyChainAndProtoStepsUseOneGoCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		step      string
		command   string
		required  []string
		forbidden []string
	}{
		{
			name:      "test tools",
			step:      "Install test tools",
			command:   "go run ./test/cmd/toolbootstrap podman-runtime",
			forbidden: []string{"run: |", "jq --version"},
		},
		{
			name:      "vulnerability audit",
			step:      "Vulnerability Audit",
			command:   "go run ./scripts/vuln-audit.go --report-dir reports/security",
			forbidden: []string{"run: |", "mkdir -p"},
		},
		{
			name:      "SBOMs",
			step:      "Generate separate runtime and tools SBOMs",
			command:   "go run ./test/cmd/cictl sbom --report-dir reports/security",
			required:  []string{"if: always()"},
			forbidden: []string{"run: |", "github.com/anchore/syft/cmd/syft", "test -s"},
		},
		{
			name:      "protobuf verification",
			step:      "Verify generated protobuf code",
			command:   "go run ./test/cmd/cictl proto-verify",
			forbidden: []string{"run: |", "mkdir -p", "go build", "export PATH", "buf generate", "git diff"},
		},
	}

	workflow := readContractFile(t, "../.github/workflows/ci.yml")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			step := namedWorkflowStep(t, workflow, test.step)
			wantRun := "        run: " + test.command + "\n"
			if strings.Count(step, wantRun) != 1 {
				t.Errorf("workflow step %q must contain exactly one %q", test.step, strings.TrimSpace(wantRun))
			}
			if got := strings.Count(step, "\n        run:"); got != 1 {
				t.Errorf("workflow step %q has %d run programs, want exactly one", test.step, got)
			}
			for _, marker := range test.required {
				if !strings.Contains(step, marker) {
					t.Errorf("workflow step %q lacks required marker %q", test.step, marker)
				}
			}
			for _, marker := range test.forbidden {
				if strings.Contains(step, marker) {
					t.Errorf("workflow step %q retains old shell marker %q", test.step, marker)
				}
			}
		})
	}
}

func namedWorkflowStep(t *testing.T, workflow, name string) string {
	t.Helper()

	marker := "      - name: " + name + "\n"
	start := strings.Index(workflow, marker)
	if start < 0 {
		t.Fatalf("workflow step %q is missing", name)
	}
	rest := workflow[start:]
	if end := strings.Index(rest[len(marker):], "\n      - name:"); end >= 0 {
		return rest[:len(marker)+end]
	}
	return rest
}
