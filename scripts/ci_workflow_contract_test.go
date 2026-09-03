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

func TestCIWorkflowResidualShellStepsUseOneGoCommand(t *testing.T) {
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
			name:      "Sonar skip notice",
			workflow:  "../.github/workflows/build.yml",
			step:      "SonarQube skipped for Dependabot",
			command:   "go run ./test/cmd/cictl sonar-skip-notice",
			required:  []string{"if: steps.sonar-token.outputs.mode == 'skip-dependabot'"},
			forbidden: []string{"run: >", "echo ", "Dependabot SONAR_TOKEN"},
		},
		{
			name:      "coverage test",
			workflow:  "../.github/workflows/ci.yml",
			step:      "Test with coverage",
			command:   "go run ./test/cmd/cictl test-coverage",
			forbidden: []string{"run: |", "gotestsum", "\\\\"},
		},
		{
			name:      "JUnit HTML report",
			workflow:  "../.github/workflows/ci.yml",
			step:      "Generate HTML test report",
			command:   "go run ./test/cmd/junitreport --root . --input unit-report.xml --output unit-report.html",
			required:  []string{"if: always()"},
			forbidden: []string{"run: >", "run: |"},
		},
		{
			name:      "Buf base fetch",
			workflow:  "../.github/workflows/ci.yml",
			step:      "Fetch base branch for buf breaking",
			command:   "go run ./test/cmd/cictl buf-fetch-base",
			required:  []string{"if: github.event_name == 'pull_request'"},
			forbidden: []string{"${{ github.event.pull_request.base.ref }}", "git fetch", "run: |", "run: >"},
		},
		{
			name:      "Buf compatibility",
			workflow:  "../.github/workflows/ci.yml",
			step:      "buf breaking",
			command:   "go run ./test/cmd/cictl buf-breaking",
			required:  []string{"if: github.event_name == 'pull_request'"},
			forbidden: []string{"${{ github.event.pull_request.base.ref }}", "buf breaking --against", "run: |", "run: >"},
		},
		{
			name:      "commit policy",
			workflow:  "../.github/workflows/ci.yml",
			step:      "Validate PR title",
			command:   "go run ./test/cmd/cictl commit-policy",
			required:  []string{"PR_TITLE: ${{ github.event.pull_request.title }}"},
			forbidden: []string{"$PR_TITLE", "repoquality commit", "run: |", "run: >"},
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
					t.Errorf("workflow step %q retains shell marker %q", test.step, marker)
				}
			}
		})
	}
}

func TestCIWorkflowBenchmarkStepsUseGoOwner(t *testing.T) {
	t.Parallel()

	workflow := readContractFile(t, "../.github/workflows/ci.yml")
	tests := []struct {
		step    string
		command string
	}{
		{step: "Run benchmarks (PR)", command: "go run ./test/cmd/cictl benchmark-run --output new.txt"},
		{
			step:    "Run benchmarks (base)",
			command: "go run ./test/cmd/cictl benchmark-base --output old.txt",
		},
		{
			step:    "Normalize renamed benchmarks and validate inputs",
			command: "go run ./test/cmd/cictl benchmark-normalize --old old.txt --new new.txt",
		},
		{step: "Compare benchmarks", command: "go run ./test/cmd/cictl benchmark-report"},
	}
	for _, test := range tests {
		t.Run(test.step, func(t *testing.T) {
			t.Parallel()

			step := namedWorkflowStep(t, workflow, test.step)
			wantRun := "        run: " + test.command + "\n"
			if strings.Count(step, wantRun) != 1 {
				t.Errorf("workflow step %q must contain exactly one %q", test.step, strings.TrimSpace(wantRun))
			}
			for _, marker := range []string{"run: |", "run: >", "awk ", "sed ", "grep ", "git config --global"} {
				if strings.Contains(step, marker) {
					t.Errorf("workflow step %q retains shell marker %q", test.step, marker)
				}
			}
			if test.step == "Run benchmarks (base)" {
				for _, marker := range []string{"${{", "--ref"} {
					if strings.Contains(step, marker) {
						t.Errorf("workflow base benchmark step exposes ref marker %q to the shell", marker)
					}
				}
			}
		})
	}

	for _, removedStep := range []string{
		"Create base branch worktree",
		"Generate HTML comparison",
		"Check for regressions (>10%)",
	} {
		if strings.Contains(workflow, "      - name: "+removedStep+"\n") {
			t.Errorf("workflow retains superseded benchmark step %q", removedStep)
		}
	}
	upload := namedWorkflowStep(t, workflow, "Upload benchmark artifacts")
	for _, artifact := range []string{
		"old.txt", "new.txt", "bench-report.md", "bench-comparison.html", "bench-comparison.json",
		"bench-regression/bench-csv.txt", "bench-regression/benchstat-notes.txt",
	} {
		if !strings.Contains(upload, artifact) {
			t.Errorf("benchmark artifact upload lacks %q", artifact)
		}
	}
}

func TestReleaseWorkflowTestAndReportStepsUseGoOwners(t *testing.T) {
	t.Parallel()

	workflow := readContractFile(t, "../.github/workflows/release.yml")
	tests := []struct {
		step    string
		command string
	}{
		{step: "Build", command: "go run ./test/cmd/cictl release-build"},
		{step: "Install test tools", command: "go run ./test/cmd/toolbootstrap podman-runtime"},
		{step: "Install report tools", command: "go run ./test/cmd/toolbootstrap podman-runtime"},
		{step: "Run tests with JUnit output", command: "go run ./test/cmd/cictl release-test-report"},
		{step: "Run benchmarks", command: "go run ./test/cmd/cictl release-benchmarks"},
		{step: "Generate benchmark metadata", command: "go run ./test/cmd/cictl release-benchmark-metadata"},
		{step: "Compare with baseline", command: "go run ./test/cmd/cictl release-benchmark-comparison"},
		{step: "Create reports archive", command: "go run ./test/cmd/cictl release-reports-archive"},
	}
	for _, test := range tests {
		t.Run(test.step, func(t *testing.T) {
			t.Parallel()

			step := namedWorkflowStep(t, workflow, test.step)
			wantRun := "        run: " + test.command + "\n"
			if strings.Count(step, wantRun) != 1 {
				t.Errorf("release step %q must contain exactly one %q", test.step, strings.TrimSpace(wantRun))
			}
			if got := strings.Count(step, "\n        run:"); got != 1 {
				t.Errorf("release step %q has %d run programs, want exactly one", test.step, got)
			}
			for _, marker := range []string{
				"run: |", "run: >", "shell: bash", "mkdir ", " tee ", " cat ", " cp ", "tar -",
				"VERSION=", "GIT_COMMIT=", "BUILD_DATE=", "go build", "go test", "benchstat",
			} {
				if strings.Contains(step, marker) {
					t.Errorf("release step %q retains shell marker %q", test.step, marker)
				}
			}
		})
	}

	reportsUpload := namedWorkflowStep(t, workflow, "Upload reports archive")
	for _, marker := range []string{"name: release-reports", "path: gobfd-*-reports.tar.gz", "retention-days: 90"} {
		if !strings.Contains(reportsUpload, marker) {
			t.Errorf("release reports upload lacks %q", marker)
		}
	}
}

func TestReleaseWorkflowImmutablePreflightUsesOneGoCommand(t *testing.T) {
	t.Parallel()

	workflow := readContractFile(t, "../.github/workflows/release.yml")
	step := namedWorkflowStep(t, workflow, "Refuse existing release, draft, or versioned OCI tag")
	if got := strings.Count(step, "        run: go run ./test/cmd/cictl release-preflight\n"); got != 1 {
		t.Errorf("release preflight command count = %d, want 1", got)
	}
	for _, marker := range []string{
		"run: |", "shell: bash", "git rev-parse", "gh api", "jq ", "grep ", "printf ", "$GITHUB_", "${{ github.",
	} {
		if strings.Contains(step, marker) {
			t.Errorf("release preflight retains shell-owned marker %q", marker)
		}
	}
	if !strings.Contains(step, "GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}") {
		t.Error("release preflight no longer inherits GH_TOKEN")
	}
}

func TestReleaseWorkflowNotesUseOneGoCommand(t *testing.T) {
	t.Parallel()

	workflow := readContractFile(t, "../.github/workflows/release.yml")
	step := namedWorkflowStep(t, workflow, "Extract release notes from CHANGELOG.md")
	if got := strings.Count(step, "        run: go run ./test/cmd/cictl release-notes\n"); got != 1 {
		t.Errorf("release notes command count = %d, want 1", got)
	}
	for _, marker := range []string{
		"run: |", "gh api", "jq ", "awk ", "grep ", "$GITHUB_", "${{ github.",
	} {
		if strings.Contains(step, marker) {
			t.Errorf("release notes step retains shell-owned marker %q", marker)
		}
	}
	if !strings.Contains(step, "GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}") {
		t.Error("release notes no longer inherits GH_TOKEN")
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
