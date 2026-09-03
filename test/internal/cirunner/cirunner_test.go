package cirunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSonarModeAppendsOnlySelectedMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		actor string
		want  string
	}{
		{name: "token available", token: "do-not-persist-this-token", actor: "developer", want: "mode=run\n"},
		{name: "Dependabot without token", actor: "dependabot[bot]", want: "mode=skip-dependabot\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "github-output")
			if err := os.WriteFile(output, []byte("existing=value\n"), 0o600); err != nil {
				t.Fatalf("seed GitHub output: %v", err)
			}
			if err := SonarMode(SonarOptions{Token: test.token, Actor: test.actor, Output: output}); err != nil {
				t.Fatalf("SonarMode() error = %v", err)
			}
			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatalf("read GitHub output: %v", err)
			}
			want := "existing=value\n" + test.want
			if string(got) != want {
				t.Errorf("GitHub output = %q, want %q", got, want)
			}
			if test.token != "" && strings.Contains(string(got), test.token) {
				t.Error("GitHub output contains the Sonar token")
			}
		})
	}
}

func TestSonarModeFailsClosedWithoutToken(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(output, []byte("existing=value\n"), 0o600); err != nil {
		t.Fatalf("seed GitHub output: %v", err)
	}
	err := SonarMode(SonarOptions{Actor: "developer", Output: output})
	if err == nil {
		t.Fatal("SonarMode() error = nil, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "SONAR_TOKEN is required for non-Dependabot SonarQube scans") {
		t.Errorf("SonarMode() error = %q, want contextual policy error", err)
	}
	got, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("read GitHub output: %v", readErr)
	}
	if string(got) != "existing=value\n" {
		t.Errorf("GitHub output changed on failure: %q", got)
	}
}

func TestSonarModeWrapsOutputErrors(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing", "github-output")
	err := SonarMode(SonarOptions{Token: "secret", Output: missing})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SonarMode() error = %v, want wrapped os.ErrNotExist", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Error("SonarMode() error contains the Sonar token")
	}
}

func TestBuildRunsFixedCommandsWithMetadata(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "build")
	runner := &recordingRunner{}
	now := time.Date(2026, time.September, 3, 14, 5, 6, 0, time.FixedZone("test", 3*60*60))
	err := Build(context.Background(), BuildOptions{
		SHA:    "AbCd0123deadBEEF",
		Output: output,
		Now:    func() time.Time { return now },
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	ldflags := "-ldflags=-s -w " +
		"-X github.com/dantte-lp/gobfd/internal/version.Version=ci-AbCd0123 " +
		"-X github.com/dantte-lp/gobfd/internal/version.GitCommit=AbCd0123 " +
		"-X github.com/dantte-lp/gobfd/internal/version.BuildDate=2026-09-03T11:05:06Z"
	names := []string{"gobfd", "gobfdctl", "gobfd-haproxy-agent", "gobfd-exabgp-bridge"}
	want := make([]invocation, 0, len(names))
	for _, name := range names {
		want = append(want, invocation{
			name: "go",
			args: []string{
				"build",
				"-buildvcs=false",
				ldflags,
				"-o",
				filepath.Join(output, name),
				"./cmd/" + name,
			},
		})
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("build invocations = %#v, want %#v", runner.calls, want)
	}
	info, statErr := os.Stat(output)
	if statErr != nil {
		t.Fatalf("stat output directory: %v", statErr)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("output directory mode = %#o, want 0755", got)
	}
}

func TestBuildCreatesOutputWithExactModeDespiteUmask(t *testing.T) {
	oldUmask := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(oldUmask) })

	output := filepath.Join(t.TempDir(), "build")
	err := Build(context.Background(), BuildOptions{
		SHA:    "01234567",
		Output: output,
		Now:    func() time.Time { return time.Unix(0, 0) },
		Runner: &recordingRunner{},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	info, statErr := os.Stat(output)
	if statErr != nil {
		t.Fatalf("stat output directory: %v", statErr)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("output directory mode = %#o, want 0755", got)
	}
}

func TestBuildRejectsInvalidSHA(t *testing.T) {
	t.Parallel()

	for _, sha := range []string{"", "0123456", "0123456g", "01234567xyz"} {
		sha := sha
		t.Run(sha, func(t *testing.T) {
			t.Parallel()

			runner := &recordingRunner{}
			err := Build(context.Background(), BuildOptions{
				SHA:    sha,
				Output: filepath.Join(t.TempDir(), "build"),
				Runner: runner,
			})
			if !errors.Is(err, ErrInvalidSHA) {
				t.Fatalf("Build() error = %v, want ErrInvalidSHA", err)
			}
			if len(runner.calls) != 0 {
				t.Errorf("runner received %d calls for invalid SHA", len(runner.calls))
			}
		})
	}
}

func TestBuildWrapsRunnerErrorWithBinary(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("runner failed")
	runner := &recordingRunner{failAt: 2, err: wantErr}
	err := Build(context.Background(), BuildOptions{
		SHA:    "0123456789abcdef",
		Output: filepath.Join(t.TempDir(), "build"),
		Runner: runner,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Build() error = %v, want wrapped runner error", err)
	}
	if !strings.Contains(err.Error(), "build gobfdctl") {
		t.Errorf("Build() error = %q, want binary context", err)
	}
	if got := len(runner.calls); got != 2 {
		t.Errorf("runner calls = %d, want stop after second call", got)
	}
}

type invocation struct {
	name string
	args []string
}

type recordingRunner struct {
	calls  []invocation
	failAt int
	err    error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, invocation{name: name, args: append([]string(nil), args...)})
	if r.failAt != 0 && len(r.calls) == r.failAt {
		return r.err
	}
	return nil
}
