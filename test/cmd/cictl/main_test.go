package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
