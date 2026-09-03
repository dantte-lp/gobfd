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
		name         string
		tokenPresent string
		actor        string
		want         string
	}{
		{name: "token available", tokenPresent: "true", actor: "developer", want: "mode=run\n"},
		{name: "Dependabot without token", tokenPresent: "false", actor: "dependabot[bot]", want: "mode=skip-dependabot\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "github-output")
			if err := os.WriteFile(output, []byte("existing=value\n"), 0o600); err != nil {
				t.Fatalf("seed GitHub output: %v", err)
			}
			if err := SonarMode(SonarOptions{TokenPresent: test.tokenPresent, Actor: test.actor, Output: output}); err != nil {
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
		})
	}
}

func TestSonarModeFailsClosedWithoutToken(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(output, []byte("existing=value\n"), 0o600); err != nil {
		t.Fatalf("seed GitHub output: %v", err)
	}
	err := SonarMode(SonarOptions{TokenPresent: "false", Actor: "developer", Output: output})
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

func TestSonarModeRejectsMissingOrInvalidPresence(t *testing.T) {
	t.Parallel()

	for _, present := range []string{"", "TRUE", "1", "yes", "unexpected"} {
		t.Run(present, func(t *testing.T) {
			t.Parallel()

			output := filepath.Join(t.TempDir(), "github-output")
			if err := os.WriteFile(output, nil, 0o600); err != nil {
				t.Fatalf("create GitHub output: %v", err)
			}
			err := SonarMode(SonarOptions{TokenPresent: present, Actor: "dependabot[bot]", Output: output})
			if err == nil {
				t.Fatal("SonarMode() error = nil, want invalid presence error")
			}
			if !strings.Contains(err.Error(), "SONAR_TOKEN_PRESENT") {
				t.Errorf("SonarMode() error = %q, want input context", err)
			}
			got, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatalf("read GitHub output: %v", readErr)
			}
			if len(got) != 0 {
				t.Errorf("GitHub output changed for invalid presence: %q", got)
			}
		})
	}
}

func TestSonarModeWrapsOutputErrors(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing", "github-output")
	err := SonarMode(SonarOptions{TokenPresent: "true", Output: missing})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SonarMode() error = %v, want wrapped os.ErrNotExist", err)
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

func TestExecRunnerRejectsCommandsOutsideCIAllowlist(t *testing.T) {
	t.Parallel()

	err := (ExecRunner{}).RunCommand(context.Background(), CommandSpec{Name: "true"})
	if err == nil {
		t.Fatal("RunCommand() error = nil, want command allowlist error")
	}
	if !strings.Contains(err.Error(), "not allowlisted") {
		t.Errorf("RunCommand() error = %q, want allowlist context", err)
	}
}

func TestExecRunnerAllowlistIncludesGitHubCLI(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (ExecRunner{}).RunCommand(ctx, CommandSpec{Name: "gh", Args: []string{"--version"}})
	if errors.Is(err, errCommandNotAllowed) {
		t.Fatalf("RunCommand(gh) error = %v, want deliberately allowlisted executable", err)
	}
}

func TestSBOMRunsPinnedScansAndValidatesArtifacts(t *testing.T) {
	t.Parallel()

	reportDir := filepath.Join(t.TempDir(), "reports", "security")
	runner := &recordingSpecRunner{
		afterRun: func(spec CommandSpec) {
			output := outputArgument(t, spec.Args)
			if err := os.WriteFile(output, []byte("{}\n"), 0o600); err != nil {
				t.Fatalf("write simulated SBOM: %v", err)
			}
		},
	}
	if err := SBOM(context.Background(), SBOMOptions{ReportDir: reportDir, Runner: runner}); err != nil {
		t.Fatalf("SBOM() error = %v", err)
	}

	want := []specInvocation{
		{
			name: "go",
			args: []string{
				"run", "github.com/anchore/syft/cmd/syft@v1.51.0", "scan", "file:go.mod",
				"--override-default-catalogers", "go-module-file-cataloger", "--quiet",
				"--output", "cyclonedx-json=" + filepath.Join(reportDir, "runtime-sbom.cdx.json"),
			},
		},
		{
			name: "go",
			args: []string{
				"run", "github.com/anchore/syft/cmd/syft@v1.51.0", "scan", "file:tools/go.mod",
				"--override-default-catalogers", "go-module-file-cataloger", "--quiet",
				"--output", "cyclonedx-json=" + filepath.Join(reportDir, "tools-sbom.cdx.json"),
			},
		},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("SBOM invocations = %#v, want %#v", runner.calls, want)
	}
	assertExactMode(t, reportDir, 0o755)
	assertExactMode(t, filepath.Join(reportDir, "runtime-sbom.cdx.json"), 0o644)
	assertExactMode(t, filepath.Join(reportDir, "tools-sbom.cdx.json"), 0o644)
}

func TestSBOMRejectsMissingEmptyAndNonregularArtifacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		write func(*testing.T, string)
	}{
		{name: "missing", write: func(*testing.T, string) {}},
		{name: "empty", write: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatalf("write empty artifact: %v", err)
			}
		}},
		{name: "nonregular", write: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove prepared artifact: %v", err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("create directory artifact: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
				test.write(t, outputArgument(t, spec.Args))
			}}
			err := SBOM(context.Background(), SBOMOptions{
				ReportDir: filepath.Join(t.TempDir(), "reports", "security"),
				Runner:    runner,
			})
			if err == nil {
				t.Fatal("SBOM() error = nil, want invalid artifact error")
			}
			if !strings.Contains(err.Error(), "runtime SBOM artifact") {
				t.Errorf("SBOM() error = %q, want artifact context", err)
			}
			if got := len(runner.calls); got != 1 {
				t.Errorf("runner calls = %d, want stop after invalid first artifact", got)
			}
		})
	}
}

func TestSBOMRejectsStaleRegularArtifactWhenScannerDoesNotRewrite(t *testing.T) {
	t.Parallel()

	reportDir := filepath.Join(t.TempDir(), "reports", "security")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("create report directory: %v", err)
	}
	stale := filepath.Join(reportDir, "runtime-sbom.cdx.json")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("seed stale runtime SBOM: %v", err)
	}
	runner := &recordingSpecRunner{}
	err := SBOM(context.Background(), SBOMOptions{ReportDir: reportDir, Runner: runner})
	if err == nil {
		t.Fatal("SBOM() error = nil, want stale artifact failure")
	}
	if !strings.Contains(err.Error(), "runtime SBOM artifact") {
		t.Errorf("SBOM() error = %q, want runtime artifact context", err)
	}
	if got := len(runner.calls); got != 1 {
		t.Errorf("runner calls = %d, want stop after no-op runtime scan", got)
	}
}

func TestSBOMWrapsScannerFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("scanner failed")
	err := SBOM(context.Background(), SBOMOptions{
		ReportDir: filepath.Join(t.TempDir(), "reports", "security"),
		Runner:    &recordingSpecRunner{failAt: 1, err: wantErr},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SBOM() error = %v, want wrapped scanner error", err)
	}
	if !strings.Contains(err.Error(), "generate runtime SBOM") {
		t.Errorf("SBOM() error = %q, want scanner context", err)
	}
}

func TestSBOMRejectsUnsafeReportDirectoryWithoutRunningScanner(t *testing.T) {
	t.Parallel()

	symlink := filepath.Join(t.TempDir(), "reports-link")
	if err := os.Symlink(t.TempDir(), symlink); err != nil {
		t.Fatalf("create report directory symlink: %v", err)
	}
	for _, reportDir := range []string{"", ".", "reports/../security", filepath.Join(symlink, "security")} {
		t.Run(reportDir, func(t *testing.T) {
			t.Parallel()

			runner := &recordingSpecRunner{}
			err := SBOM(context.Background(), SBOMOptions{ReportDir: reportDir, Runner: runner})
			if err == nil {
				t.Fatal("SBOM() error = nil, want unsafe report directory error")
			}
			if len(runner.calls) != 0 {
				t.Errorf("runner received %d calls for unsafe report directory", len(runner.calls))
			}
		})
	}
}

func TestProtoVerifyRunsFixedCommandsWithChildOnlyPATH(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runnerTemp := t.TempDir()
	runner := &recordingSpecRunner{}
	if err := ProtoVerify(context.Background(), ProtoOptions{
		Root:        root,
		RunnerTemp:  runnerTemp,
		Environment: []string{"KEEP=value", "PATH=/first", "PATH=/usr/bin"},
		Runner:      runner,
	}); err != nil {
		t.Fatalf("ProtoVerify() error = %v", err)
	}

	binDir := filepath.Join(runnerTemp, "gobfd-proto-tools", "bin")
	want := []specInvocation{
		{
			name: "go",
			args: []string{"build", "-modfile=tools/go.mod", "-o", filepath.Join(binDir, "protoc-gen-go"),
				"google.golang.org/protobuf/cmd/protoc-gen-go"},
			dir: root,
		},
		{
			name: "go",
			args: []string{"build", "-modfile=tools/go.mod", "-o", filepath.Join(binDir, "protoc-gen-connect-go"),
				"connectrpc.com/connect/cmd/protoc-gen-connect-go"},
			dir: root,
		},
		{
			name: "buf",
			args: []string{"generate"},
			dir:  root,
			env:  []string{"KEEP=value", "PATH=" + binDir + string(os.PathListSeparator) + "/usr/bin"},
		},
		{
			name: "git",
			args: []string{"diff", "--exit-code", "--", "pkg/bfdpb"},
			dir:  root,
		},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("protobuf invocations = %#v, want %#v", runner.calls, want)
	}
	assertExactMode(t, filepath.Join(runnerTemp, "gobfd-proto-tools"), 0o755)
	assertExactMode(t, binDir, 0o755)
}

func TestProtoVerifyRejectsUnsafePathsWithoutRunningCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	symlink := filepath.Join(t.TempDir(), "runner-temp-link")
	if err := os.Symlink(t.TempDir(), symlink); err != nil {
		t.Fatalf("create runner temp symlink: %v", err)
	}
	symlinkTarget := t.TempDir()
	if err := os.Mkdir(filepath.Join(symlinkTarget, "child"), 0o755); err != nil {
		t.Fatalf("create nested runner temp: %v", err)
	}
	symlinkParent := filepath.Join(t.TempDir(), "runner-temp-parent-link")
	if err := os.Symlink(symlinkTarget, symlinkParent); err != nil {
		t.Fatalf("create runner temp parent symlink: %v", err)
	}
	tests := []struct {
		name       string
		root       string
		runnerTemp string
	}{
		{name: "relative root", root: ".", runnerTemp: t.TempDir()},
		{name: "filesystem root repository", root: string(filepath.Separator), runnerTemp: t.TempDir()},
		{name: "missing root", root: filepath.Join(t.TempDir(), "missing"), runnerTemp: t.TempDir()},
		{name: "symlink repository ancestor", root: filepath.Join(symlinkParent, "child"), runnerTemp: t.TempDir()},
		{name: "relative runner temp", root: root, runnerTemp: "runner-temp"},
		{name: "filesystem root runner temp", root: root, runnerTemp: string(filepath.Separator)},
		{name: "symlink runner temp", root: root, runnerTemp: symlink},
		{name: "symlink runner temp ancestor", root: root, runnerTemp: filepath.Join(symlinkParent, "child")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &recordingSpecRunner{}
			err := ProtoVerify(context.Background(), ProtoOptions{
				Root: test.root, RunnerTemp: test.runnerTemp, Environment: []string{"PATH=/usr/bin"}, Runner: runner,
			})
			if err == nil {
				t.Fatal("ProtoVerify() error = nil, want unsafe path error")
			}
			if len(runner.calls) != 0 {
				t.Errorf("runner received %d calls for unsafe paths", len(runner.calls))
			}
		})
	}
}

func TestProtoVerifyWrapsCommandFailureAndStops(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("buf failed")
	runner := &recordingSpecRunner{failAt: 3, err: wantErr}
	err := ProtoVerify(context.Background(), ProtoOptions{
		Root: t.TempDir(), RunnerTemp: t.TempDir(), Environment: []string{"PATH=/usr/bin"}, Runner: runner,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ProtoVerify() error = %v, want wrapped buf error", err)
	}
	if !strings.Contains(err.Error(), "generate protobuf code") {
		t.Errorf("ProtoVerify() error = %q, want generation context", err)
	}
	if got := len(runner.calls); got != 3 {
		t.Errorf("runner calls = %d, want stop at third call", got)
	}
}

func outputArgument(t *testing.T, arguments []string) string {
	t.Helper()

	for index, argument := range arguments {
		if argument == "--output" && index+1 < len(arguments) {
			_, output, found := strings.Cut(arguments[index+1], "=")
			if found {
				return output
			}
		}
	}
	t.Fatalf("command arguments lack SBOM output: %q", arguments)
	return ""
}

func assertExactMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %#o, want %#o", path, got, want)
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

type specInvocation struct {
	name       string
	args       []string
	dir        string
	env        []string
	executable bool
}

type recordingSpecRunner struct {
	calls    []specInvocation
	failAt   int
	err      error
	afterRun func(CommandSpec)
}

func (r *recordingSpecRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	r.calls = append(r.calls, specInvocation{
		name: spec.Name,
		args: append([]string(nil), spec.Args...),
		dir:  spec.Dir,
		env:  append([]string(nil), spec.Env...),
	})
	if r.failAt != 0 && len(r.calls) == r.failAt {
		return r.err
	}
	if r.afterRun != nil {
		r.afterRun(spec)
	}
	return nil
}
