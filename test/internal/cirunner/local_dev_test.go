package cirunner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDevEnsureScenarios(t *testing.T) {
	t.Parallel()

	t.Run("existing matching mount starts without rebuild", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		runner := &fakeDevEnsureRunner{containerID: "abc123\n", mountSource: root}
		var stdout bytes.Buffer
		err := DevEnsure(t.Context(), DevEnsureOptions{
			Root: root, Project: "proj", ComposeFile: "deployments/compose/compose.dev.yml",
			Stdout: &stdout, Runner: runner,
		})
		if err != nil {
			t.Fatalf("DevEnsure() error = %v", err)
		}
		if len(runner.calls) != 3 {
			t.Fatalf("calls = %d, want 3", len(runner.calls))
		}
		wantLast := []string{
			"compose", "-p", "proj", "-f", "deployments/compose/compose.dev.yml", "up", "-d", "--no-build", "dev",
		}
		if !slices.Equal(runner.calls[2].Args, wantLast) {
			t.Fatalf("final args = %q, want %q", runner.calls[2].Args, wantLast)
		}
	})

	t.Run("mount mismatch force recreates", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		runner := &fakeDevEnsureRunner{containerID: "abc123\n", mountSource: "/stale/worktree"}
		var stdout bytes.Buffer
		err := DevEnsure(t.Context(), DevEnsureOptions{
			Root: root, Project: "proj", ComposeFile: "compose.dev.yml",
			Stdout: &stdout, Runner: runner,
		})
		if err != nil {
			t.Fatalf("DevEnsure() error = %v", err)
		}
		if !strings.Contains(stdout.String(), "force recreating") {
			t.Fatalf("stdout = %q, want recreate notice", stdout.String())
		}
		wantLast := []string{
			"compose", "-p", "proj", "-f", "compose.dev.yml", "up", "-d", "--no-build", "--force-recreate", "dev",
		}
		if !slices.Equal(runner.calls[2].Args, wantLast) {
			t.Fatalf("final args = %q, want %q", runner.calls[2].Args, wantLast)
		}
	})

	t.Run("missing container builds when image absent", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		runner := &fakeDevEnsureRunner{imageMissing: true}
		var stdout bytes.Buffer
		err := DevEnsure(t.Context(), DevEnsureOptions{
			Root: root, Project: "proj", ComposeFile: "compose.dev.yml",
			Stdout: &stdout, Runner: runner,
		})
		if err != nil {
			t.Fatalf("DevEnsure() error = %v", err)
		}
		wantLast := []string{"compose", "-p", "proj", "-f", "compose.dev.yml", "up", "-d", "--build", "dev"}
		if !slices.Equal(runner.calls[len(runner.calls)-1].Args, wantLast) {
			t.Fatalf("final args = %q, want %q", runner.calls[len(runner.calls)-1].Args, wantLast)
		}
	})
}

type fakeDevEnsureRunner struct {
	calls        []CommandSpec
	containerID  string
	mountSource  string
	imageMissing bool
}

func (f *fakeDevEnsureRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	f.calls = append(f.calls, CommandSpec{Name: spec.Name, Args: slices.Clone(spec.Args)})
	switch {
	case slices.Contains(spec.Args, "ps"):
		_, err := io.WriteString(spec.Stdout, f.containerID)
		return err
	case slices.Contains(spec.Args, "inspect"):
		_, err := io.WriteString(spec.Stdout, f.mountSource+"\n")
		return err
	case len(spec.Args) >= 2 && spec.Args[0] == "image" && spec.Args[1] == "exists":
		if f.imageMissing {
			return errors.New("image missing")
		}
		return nil
	default:
		return nil
	}
}

func TestLocalBenchmarkStripsEnvAssignmentsAndRejectsBareEnvPrefix(t *testing.T) {
	t.Parallel()

	t.Run("default compose exec prefix merges leading env assignment", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		runner := &fakePrefixRunner{output: "BenchmarkPacket-2 1 10 ns/op\n"}
		execPrefix := []string{
			"COMPOSE_PROJECT_NAME=gobfd-wt",
			"podman", "compose", "-p", "gobfd-wt", "-f", "deployments/compose/compose.dev.yml", "exec", "-T", "dev",
		}
		err := LocalBenchmark(t.Context(), LocalBenchmarkOptions{
			Dir:         dir,
			Output:      "benchmark.txt",
			Package:     "./internal/bfd/",
			ExecPrefix:  execPrefix,
			Environment: []string{"PATH=/usr/bin", "COMPOSE_PROJECT_NAME=stale"},
			Runner:      runner,
		})
		if err != nil {
			t.Fatalf("LocalBenchmark() error = %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(runner.calls))
		}
		got := runner.calls[0]
		if got.Name != "podman" {
			t.Fatalf("command Name = %q, want podman", got.Name)
		}
		wantArgs := []string{
			"compose", "-p", "gobfd-wt", "-f", "deployments/compose/compose.dev.yml", "exec", "-T", "dev",
			"go", "test", "-buildvcs=false", "-bench=.", "-benchmem", "-count=6", "-run=^$", "./internal/bfd/",
		}
		if !slices.Equal(got.Args, wantArgs) {
			t.Fatalf("command Args = %q, want %q", got.Args, wantArgs)
		}
		wantEnv := []string{"PATH=/usr/bin", "COMPOSE_PROJECT_NAME=gobfd-wt"}
		if !slices.Equal(got.Env, wantEnv) {
			t.Fatalf("command Env = %q, want %q", got.Env, wantEnv)
		}
	})

	t.Run("bare env assignments without command fail closed", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		runner := &fakePrefixRunner{}
		err := LocalBenchmark(t.Context(), LocalBenchmarkOptions{
			Dir:        dir,
			Output:     "benchmark.txt",
			Package:    "./internal/bfd/",
			ExecPrefix: []string{"COMPOSE_PROJECT_NAME=gobfd-wt", "EXTRA_FLAG=1"},
			Runner:     runner,
		})
		if !errors.Is(err, errInvalidConfig) {
			t.Fatalf("LocalBenchmark() error = %v, want %v", err, errInvalidConfig)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "benchmark.txt")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected output file created on invalid prefix: %v", statErr)
		}
	})
}

func TestRenderLocalComparisonHTMLEscapesInputs(t *testing.T) {
	t.Parallel()

	rendered := renderLocalComparisonHTML(
		`v0.6.0"><script>alert(1)</script>`,
		`v1.0.0&beta`,
		`2026-11-27<tag>`,
		"BenchmarkX <10ns & \"fast\"\n",
	)
	if strings.Contains(rendered, "<script>") || strings.Contains(rendered, "<tag>") ||
		strings.Contains(rendered, "<10ns") {
		t.Fatalf("renderLocalComparisonHTML left unescaped HTML:\n%s", rendered)
	}
	for _, escaped := range []string{
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`v1.0.0&amp;beta`,
		`2026-11-27&lt;tag&gt;`,
		`BenchmarkX &lt;10ns &amp; &#34;fast&#34;`,
	} {
		if !strings.Contains(rendered, escaped) {
			t.Fatalf("renderLocalComparisonHTML missing escaped fragment %q:\n%s", escaped, rendered)
		}
	}
}

type fakePrefixRunner struct {
	calls  []CommandSpec
	output string
}

func (f *fakePrefixRunner) RunPrefix(_ context.Context, spec CommandSpec) error {
	f.calls = append(f.calls, CommandSpec{
		Name: spec.Name, Args: slices.Clone(spec.Args), Dir: spec.Dir, Env: slices.Clone(spec.Env),
	})
	if spec.Stdout != nil && f.output != "" {
		_, err := io.WriteString(spec.Stdout, f.output)
		return err
	}
	return nil
}
