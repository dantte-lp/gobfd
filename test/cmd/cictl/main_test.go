package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

func TestRunResidualCIModesUseFixedInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	values := map[string]string{"GITHUB_BASE_REF": "release/v0.6"}
	tests := []struct {
		name     string
		mode     string
		wantName string
		wantArgs []string
	}{
		{
			name: "coverage", mode: "test-coverage", wantName: "go",
			wantArgs: []string{
				"tool", "-modfile=tools/go.mod", "gotestsum",
				"--junitfile", "unit-report.xml", "--jsonfile", "unit-report.json",
				"--format", "short-verbose", "--", "-buildvcs=false", "./...", "-race", "-count=1",
				"-coverprofile=coverage.out", "-covermode=atomic",
			},
		},
		{
			name: "Buf fetch", mode: "buf-fetch-base", wantName: "git",
			wantArgs: []string{
				"fetch", "origin",
				"+refs/heads/release/v0.6:refs/remotes/origin/release/v0.6",
			},
		},
		{
			name: "commit policy", mode: "commit-policy", wantName: "go",
			wantArgs: []string{
				"run", "./test/cmd/repoquality", "commit", "--message", "fix(ci): preserve $title literally",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &specCommandRecorder{}
			err := run(context.Background(), []string{test.mode}, dependencies{
				getenv: func(name string) string {
					if name == "PR_TITLE" {
						return "fix(ci): preserve $title literally"
					}
					return values[name]
				},
				getwd:      func() (string, error) { return root, nil },
				specRunner: runner,
			})
			if err != nil {
				t.Fatalf("run(%q) error = %v", test.mode, err)
			}
			if len(runner.specs) != 1 {
				t.Fatalf("run(%q) command count = %d, want 1", test.mode, len(runner.specs))
			}
			if runner.specs[0].Name != test.wantName {
				t.Errorf("run(%q) command = %q, want %q", test.mode, runner.specs[0].Name, test.wantName)
			}
			if !reflect.DeepEqual(runner.specs[0].Args, test.wantArgs) {
				t.Errorf("run(%q) arguments = %q, want %q", test.mode, runner.specs[0].Args, test.wantArgs)
			}
			if runner.specs[0].Dir != root {
				t.Errorf("run(%q) directory = %q, want %q", test.mode, runner.specs[0].Dir, root)
			}
			if runner.specs[0].Stdout != nil || runner.specs[0].Stderr != nil {
				t.Errorf("run(%q) overrides child output streams", test.mode)
			}
		})
	}

	wantErr := errors.New("command failed")
	err := run(context.Background(), []string{"test-coverage"}, dependencies{
		getwd:      func() (string, error) { return root, nil },
		specRunner: &specCommandRecorder{err: wantErr},
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("run(test-coverage) error = %v, want wrapped command failure", err)
	}

	runner := &specCommandRecorder{}
	err = run(context.Background(), []string{"buf-fetch-base"}, dependencies{
		getenv:     func(string) string { return "release/v0.6\n--upload-pack=bad" },
		getwd:      func() (string, error) { return root, nil },
		specRunner: runner,
	})
	if err == nil {
		t.Error("run(buf-fetch-base) accepted a control character in GITHUB_BASE_REF")
	}
	if len(runner.specs) != 0 {
		t.Errorf("run(buf-fetch-base) ran %d commands for unsafe GITHUB_BASE_REF", len(runner.specs))
	}
}

func TestRunSonarSkipNoticePreservesMessage(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := run(context.Background(), []string{"sonar-skip-notice"}, dependencies{stdout: &output}); err != nil {
		t.Fatalf("run(sonar-skip-notice) error = %v", err)
	}
	want := "Skipping SonarQube scan because this run was triggered by Dependabot " +
		"and no Dependabot SONAR_TOKEN secret is available.\n"
	if output.String() != want {
		t.Errorf("Sonar skip message = %q, want %q", output.String(), want)
	}
}

func TestRunDispatchesReleaseReportModes(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("working directory unavailable")
	for _, mode := range []string{
		"release-test-report", "release-benchmarks", "release-benchmark-metadata",
		"release-benchmark-comparison", "release-reports-archive", "release-preflight",
		"release-notes",
	} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := run(context.Background(), []string{mode}, dependencies{
				getwd: func() (string, error) { return "", wantErr },
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("run(%q) error = %v, want working-directory error", mode, err)
			}
		})
	}
}

func TestRunDispatchesReleaseUPXFromWorkflowEnvironment(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"release-upx"}, dependencies{
		getenv: func(string) string { return "" },
	})
	if err == nil || strings.Contains(err.Error(), "unknown CI command") {
		t.Fatalf("run(release-upx) error = %v, want dispatched environment validation", err)
	}
}

func TestRunDispatchesReleaseArtifactsFromImmutableVerifierEnvironment(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"release-artifacts"}, dependencies{
		getenv: func(string) string { return "" },
	})
	if err == nil || strings.Contains(err.Error(), "unknown CI command") {
		t.Fatalf("run(release-artifacts) error = %v, want dispatched verifier environment validation", err)
	}
}

func TestRunDispatchesReleaseOCIEvidenceFromWorkflowEnvironment(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"release-oci-evidence"}, dependencies{
		getenv: func(string) string { return "" },
	})
	if err == nil || strings.Contains(err.Error(), "unknown CI command") {
		t.Fatalf("run(release-oci-evidence) error = %v, want dispatched environment validation", err)
	}
}

func TestRunDispatchesReleaseEvidenceFromWorkflowEnvironment(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"release-evidence"}, dependencies{
		getenv: func(string) string { return "" },
	})
	if err == nil || strings.Contains(err.Error(), "unknown CI command") {
		t.Fatalf("run(release-evidence) error = %v, want dispatched environment validation", err)
	}
}

func TestRunDispatchesReleaseVerifyFromImmutableVerifierEnvironment(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"release-verify"}, dependencies{
		getenv: func(string) string { return "" },
		getwd:  func() (string, error) { return t.TempDir(), nil },
	})
	if err == nil || strings.Contains(err.Error(), "unknown CI command") {
		t.Fatalf("run(release-verify) error = %v, want dispatched verifier environment validation", err)
	}
}

func TestRunReleaseBuildReadsTagAndSHA(t *testing.T) {
	t.Parallel()

	runner := &commandRecorder{}
	output := filepath.Join(t.TempDir(), "build")
	values := map[string]string{
		"GITHUB_REF_NAME": "v0.6.2",
		"GITHUB_SHA":      strings.Repeat("a", 40),
	}
	err := run(context.Background(), []string{"release-build", "--output", output}, dependencies{
		getenv: func(name string) string { return values[name] },
		now:    func() time.Time { return time.Unix(0, 0) },
		runner: runner,
	})
	if err != nil {
		t.Fatalf("run(release-build) error = %v", err)
	}
	if len(runner.names) != 4 {
		t.Errorf("release build command count = %d, want 4", len(runner.names))
	}
}

func TestRunDispatchesBenchmarkModes(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("working directory unavailable")
	for _, mode := range []string{"benchmark-run", "benchmark-base", "benchmark-normalize", "benchmark-report"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := run(context.Background(), []string{mode}, dependencies{
				getwd: func() (string, error) { return "", wantErr },
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("run(%q) error = %v, want working-directory error", mode, err)
			}
		})
	}
}

func TestRunBenchmarkBaseDerivesOriginRefFromGitHubEnvironment(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("stop after ref validation")
	runner := &specCommandRecorder{err: wantErr}
	root := t.TempDir()
	runnerTemp := t.TempDir()
	values := map[string]string{
		"BENCH_REGEX":     "^BenchmarkRequired$",
		"GITHUB_BASE_REF": "release/v0.6",
		"RUNNER_TEMP":     runnerTemp,
	}
	err := run(context.Background(), []string{"benchmark-base", "--output", "old.txt"}, dependencies{
		getwd:      func() (string, error) { return root, nil },
		getenv:     func(name string) string { return values[name] },
		specRunner: runner,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("run(benchmark-base) error = %v, want wrapped runner error", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("benchmark-base command count = %d, want 1", len(runner.specs))
	}
	wantRef := "origin/release/v0.6"
	if got := runner.specs[0].Args[len(runner.specs[0].Args)-1]; got != wantRef {
		t.Errorf("validated base ref = %q, want %q", got, wantRef)
	}
}

func TestRunSonarModeReadsGitHubEnvironment(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatalf("create GitHub output: %v", err)
	}
	values := map[string]string{
		"SONAR_TOKEN_PRESENT": "true",
		"GITHUB_ACTOR":        "developer",
		"GITHUB_OUTPUT":       output,
	}
	err := run(context.Background(), []string{"sonar-mode"}, dependencies{
		getenv: func(name string) string {
			if name == "SONAR_TOKEN" {
				t.Fatal("cictl read the raw SONAR_TOKEN")
			}
			return values[name]
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("read GitHub output: %v", readErr)
	}
	if string(got) != "mode=run\n" {
		t.Errorf("GitHub output = %q, want mode=run", got)
	}
}

func TestRunBuildReadsGitHubSHAAndOutputFlag(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "build")
	runner := &commandRecorder{}
	err := run(context.Background(), []string{"build", "--output", output}, dependencies{
		getenv: func(name string) string {
			if name == "GITHUB_SHA" {
				return "0123456789abcdef"
			}
			return ""
		},
		now:    func() time.Time { return time.Unix(0, 0) },
		runner: runner,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := len(runner.names); got != 4 {
		t.Errorf("build command count = %d, want 4", got)
	}
}

func TestRunSBOMUsesReportDirectoryFlag(t *testing.T) {
	t.Parallel()

	reportDir := filepath.Join(t.TempDir(), "reports", "security")
	runner := &specCommandRecorder{afterRun: func(arguments []string) {
		for index, argument := range arguments {
			if argument == "--output" && index+1 < len(arguments) {
				output := strings.TrimPrefix(arguments[index+1], "cyclonedx-json=")
				if err := os.WriteFile(output, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("write simulated SBOM: %v", err)
				}
				return
			}
		}
		t.Fatalf("SBOM command lacks output argument: %q", arguments)
	}}
	if err := run(context.Background(), []string{"sbom", "--report-dir", reportDir}, dependencies{
		specRunner: runner,
	}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := len(runner.names); got != 2 {
		t.Errorf("SBOM command count = %d, want 2", got)
	}
}

func TestRunProtoVerifyReadsRunnerEnvironment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runnerTemp := t.TempDir()
	runner := &specCommandRecorder{}
	if err := run(context.Background(), []string{"proto-verify"}, dependencies{
		getenv: func(name string) string {
			if name == "RUNNER_TEMP" {
				return runnerTemp
			}
			return ""
		},
		getwd:      func() (string, error) { return root, nil },
		environ:    func() []string { return []string{"PATH=/usr/bin"} },
		specRunner: runner,
	}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := len(runner.names); got != 4 {
		t.Errorf("protobuf command count = %d, want 4", got)
	}
}

func TestRunRejectsUnknownModeWithoutEnvironmentLeak(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"unknown"}, dependencies{
		getenv: func(string) string { return "secret" },
	})
	if err == nil {
		t.Fatal("run() error = nil, want usage error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Error("usage error contains an environment value")
	}
}

type commandRecorder struct {
	names []string
}

func (r *commandRecorder) Run(_ context.Context, name string, _ ...string) error {
	r.names = append(r.names, name)
	return nil
}

type specCommandRecorder struct {
	names    []string
	specs    []cirunner.CommandSpec
	afterRun func([]string)
	err      error
}

func (r *specCommandRecorder) RunCommand(_ context.Context, spec cirunner.CommandSpec) error {
	r.names = append(r.names, spec.Name)
	r.specs = append(r.specs, spec)
	if r.afterRun != nil {
		r.afterRun(spec.Args)
	}
	return r.err
}
