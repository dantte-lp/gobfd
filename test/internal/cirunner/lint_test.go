package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLintRunsBoundedBaseAndTaggedProfiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	profiles := defaultLintProfiles()
	for _, profile := range profiles {
		path := filepath.Join(root, profile.tag+".go")
		content := "//go:build " + profile.tag + "\n\npackage fixture\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write tagged fixture %s: %v", profile.tag, err)
		}
	}
	prebuiltFixture := filepath.Join(t.TempDir(), "golangci-lint")
	if err := os.WriteFile(prebuiltFixture, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write prebuilt linter fixture: %v", err)
	}
	runner := &lintTestRunner{}
	err := Lint(context.Background(), LintOptions{
		Root: root, Environment: []string{
			"PATH=/bin", "GOMAXPROCS=99", "GOMEMLIMIT=off", "GOLANGCI_LINT=" + prebuiltLintPath,
		},
		Runner:        runner,
		Containerized: func() bool { return true },
		PrebuiltStat:  func(string) (os.FileInfo, error) { return os.Lstat(prebuiltFixture) },
	})
	if err != nil {
		t.Fatalf("Lint() error = %v", err)
	}
	if got, want := len(runner.calls), 8+2*len(profiles); got != want {
		t.Fatalf("lint command count = %d, want %d", got, want)
	}
	for index, call := range runner.calls {
		if call.Name != "go" && call.Name != prebuiltLintPath {
			t.Errorf("lint command %d executable = %q, want go or %s", index, call.Name, prebuiltLintPath)
		}
		if call.Dir != root {
			t.Errorf("lint command %d directory = %q, want %q", index, call.Dir, root)
		}
		for _, value := range []string{"GOMAXPROCS=2", "GOMEMLIMIT=1500MiB"} {
			if !slices.Contains(call.Env, value) {
				t.Errorf("lint command %d environment lacks %q", index, value)
			}
		}
	}
}

func TestLintRefusesHostExecution(t *testing.T) {
	t.Parallel()

	err := Lint(context.Background(), LintOptions{
		Root: t.TempDir(), Runner: &lintTestRunner{}, Containerized: func() bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "container-only") {
		t.Fatalf("Lint() error = %v, want container-only refusal", err)
	}
}

func TestCountBuildTagFilesSkipsOnlyRootVendor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, path := range []string{"vendor/root.go", "test/e2e/vendor/nested.go"} {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte("//go:build e2e_vendor\n\npackage fixture\n"), 0o600); err != nil {
			t.Fatalf("write tagged fixture: %v", err)
		}
	}
	got, err := countBuildTagFiles(root, "e2e_vendor")
	if err != nil {
		t.Fatalf("countBuildTagFiles() error = %v", err)
	}
	if got != 1 {
		t.Fatalf("countBuildTagFiles() = %d, want 1", got)
	}
}

func TestResolveLintCommandAutoSelectsPrebuiltExecutable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executablePath := filepath.Join(dir, "golangci-lint")
	if err := os.WriteFile(executablePath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	nonExecPath := filepath.Join(dir, "nonexec")
	if err := os.WriteFile(nonExecPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write non-executable fixture: %v", err)
	}

	gotPrebuilt, err := resolveLintCommand([]string{"PATH=/bin"}, func(string) (os.FileInfo, error) {
		return os.Lstat(executablePath)
	})
	if err != nil || gotPrebuilt.name != prebuiltLintPath || len(gotPrebuilt.arguments) != 0 {
		t.Fatalf("resolveLintCommand(prebuilt) = %+v, %v; want %s", gotPrebuilt, err, prebuiltLintPath)
	}

	gotFallback, err := resolveLintCommand([]string{"PATH=/bin"}, func(string) (os.FileInfo, error) {
		return os.Lstat(nonExecPath)
	})
	if err != nil || gotFallback.name != "go" {
		t.Fatalf("resolveLintCommand(non-executable) = %+v, %v; want go tool fallback", gotFallback, err)
	}

	gotMissing, err := resolveLintCommand([]string{"PATH=/bin"}, func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	})
	if err != nil || gotMissing.name != "go" {
		t.Fatalf("resolveLintCommand(missing) = %+v, %v; want go tool fallback", gotMissing, err)
	}

	gotExplicitExec, err := resolveLintCommand(
		[]string{"GOLANGCI_LINT=" + prebuiltLintPath},
		func(path string) (os.FileInfo, error) {
			if path != prebuiltLintPath {
				return nil, fmt.Errorf("unexpected stat path %q", path)
			}
			return os.Lstat(executablePath)
		},
	)
	if err != nil || gotExplicitExec.name != prebuiltLintPath {
		t.Fatalf("resolveLintCommand(explicit executable) = %+v, %v; want %s", gotExplicitExec, err, prebuiltLintPath)
	}

	if _, nonExecErr := resolveLintCommand(
		[]string{"GOLANGCI_LINT=" + prebuiltLintPath},
		func(string) (os.FileInfo, error) { return os.Lstat(nonExecPath) },
	); !errors.Is(nonExecErr, errInvalidConfig) {
		t.Fatalf("resolveLintCommand(explicit non-executable) error = %v, want %v", nonExecErr, errInvalidConfig)
	}

	if _, missingExplicitErr := resolveLintCommand(
		[]string{"GOLANGCI_LINT=" + prebuiltLintPath},
		func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	); !errors.Is(missingExplicitErr, errInvalidConfig) {
		t.Fatalf("resolveLintCommand(explicit missing) error = %v, want %v", missingExplicitErr, errInvalidConfig)
	}
}

type lintTestRunner struct {
	calls []CommandSpec
}

func (runner *lintTestRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	runner.calls = append(runner.calls, CommandSpec{
		Name: spec.Name, Args: slices.Clone(spec.Args), Dir: spec.Dir, Env: slices.Clone(spec.Env),
	})
	arguments := strings.Join(spec.Args, " ")
	switch {
	case spec.Name == prebuiltLintPath && strings.HasPrefix(arguments, "config verify"):
		return nil
	case strings.HasPrefix(arguments, "list -buildvcs=false"):
		_, err := io.WriteString(spec.Stdout, "example.invalid/input.go\n")
		return err
	case strings.HasPrefix(arguments, "list -modfile=tools/go.mod -m"):
		_, err := io.WriteString(spec.Stdout, "v2.13.1\n")
		return err
	case spec.Name == prebuiltLintPath && strings.HasPrefix(arguments, "version"):
		_, err := io.WriteString(spec.Stdout, "2.13.1\n")
		return err
	case spec.Name == prebuiltLintPath && strings.HasPrefix(arguments, "linters"):
		var output strings.Builder
		output.WriteString("Enabled by your configuration linters:\n")
		for index := range lintEnabledCount {
			fmt.Fprintf(&output, "linter%d: fixture\n", index)
		}
		output.WriteString("Disabled by your configuration linters:\n")
		_, err := io.WriteString(spec.Stdout, output.String())
		return err
	case spec.Name == prebuiltLintPath && strings.HasPrefix(arguments, "run"):
		return nil
	default:
		return fmt.Errorf("unexpected lint command: %s", arguments)
	}
}
