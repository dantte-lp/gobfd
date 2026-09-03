package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

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
	afterRun func([]string)
}

func (r *specCommandRecorder) RunCommand(_ context.Context, spec cirunner.CommandSpec) error {
	r.names = append(r.names, spec.Name)
	if r.afterRun != nil {
		r.afterRun(spec.Args)
	}
	return nil
}
