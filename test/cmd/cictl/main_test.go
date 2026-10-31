package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
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

func TestRunDispatchesReleaseReportModes(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("working directory unavailable")
	for _, mode := range []string{
		"release-test-report", "release-benchmarks", "release-benchmark-metadata",
		"release-benchmark-comparison", "release-reports-archive", "release-preflight",
		"release-notes", "release-pr-check",
	} {
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

func TestRunDispatchesReleasePleaseWithoutReadingTokenForUntrustedContext(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{{"release-please-pr"}, {"release-please-pr", "extra"}} {
		calls := 0
		err := run(t.Context(), arguments, dependencies{
			getwd: func() (string, error) { return t.TempDir(), nil },
			getenv: func(name string) string {
				if name == "GH_TOKEN" {
					t.Fatal("untrusted release automation read a token")
				}
				return ""
			},
			environ: func() []string { calls++; return nil },
		})
		if err == nil || strings.Contains(err.Error(), "unknown CI command") || calls != 0 {
			t.Fatalf("release-please-pr dispatch error = %v, environment reads = %d", err, calls)
		}
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

func TestRunDispatchesReleaseCloseoutFromImmutableVerifierEnvironment(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"release-promote", "release-publish"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := run(context.Background(), []string{mode}, dependencies{
				getenv: func(string) string { return "" },
				getwd:  func() (string, error) { return t.TempDir(), nil },
			})
			if err == nil || strings.Contains(err.Error(), "unknown CI command") {
				t.Fatalf("run(%s) error = %v, want dispatched verifier environment validation", mode, err)
			}
		})
	}
}

func TestRunDispatchesLint(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"lint"}, dependencies{
		getwd:      func() (string, error) { return t.TempDir(), nil },
		environ:    func() []string { return nil },
		specRunner: &specCommandRecorder{},
	})
	if err == nil || strings.Contains(err.Error(), "unknown CI command") {
		t.Fatalf("run(lint) error = %v, want dispatched lint input validation", err)
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
	for _, mode := range []string{
		"benchmark-run", "benchmark-base", "benchmark-pair", "benchmark-normalize", "benchmark-report",
	} {
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
	for _, mode := range []string{"benchmark-base", "benchmark-pair"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			wantErr := errors.New("stop after ref validation")
			runner := &specCommandRecorder{err: wantErr}
			root := t.TempDir()
			values := map[string]string{
				"BENCH_REGEX": "^BenchmarkRequired$", "GITHUB_BASE_REF": "release/v0.6", "RUNNER_TEMP": t.TempDir(),
			}
			if err := run(t.Context(), []string{mode}, dependencies{
				getwd: func() (string, error) { return root, nil }, getenv: func(name string) string { return values[name] },
				specRunner: runner,
			}); !errors.Is(err, wantErr) {
				t.Fatalf("run(%s) error=%v, want wrapped runner error", mode, err)
			}
			if len(runner.specs) != 1 {
				t.Fatalf("%s command count=%d, want 1", mode, len(runner.specs))
			}
			if got := runner.specs[0].Args[len(runner.specs[0].Args)-1]; got != "origin/release/v0.6" {
				t.Errorf("validated base ref=%q, want origin/release/v0.6", got)
			}
		})
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

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tools", "syft"), 0o755); err != nil {
		t.Fatal(err)
	}
	reportDir := filepath.Join(t.TempDir(), "reports", "security")
	runner := &specCommandRecorder{afterSpec: func(spec cirunner.CommandSpec) {
		if writeSyftCommandFixture(t, spec) {
			return
		}
		arguments := spec.Args
		for index, argument := range arguments {
			if argument == "--output" && index+1 < len(arguments) {
				_, output, _ := strings.Cut(arguments[index+1], "=")
				if err := os.WriteFile(output, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("write simulated SBOM: %v", err)
				}
				return
			}
		}
		t.Fatalf("SBOM command lacks output argument: %q", arguments)
	}}
	if err := run(context.Background(), []string{"sbom", "--report-dir", reportDir}, dependencies{
		specRunner: runner, getwd: func() (string, error) { return root, nil },
		environ: func() []string { return []string{"PATH=/sdk/bin", "GH_TOKEN=private"} },
	}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := len(runner.names); got != 12 {
		t.Errorf("SBOM command count = %d, want 12", got)
	}
}

func writeSyftCommandFixture(t *testing.T, spec cirunner.CommandSpec) bool {
	t.Helper()
	var data map[string]any
	switch spec.Name + " " + spec.Args[0] {
	case "go list":
		if spec.Args[len(spec.Args)-1] == toolprofile.SyftModule {
			data = map[string]any{"Path": toolprofile.SyftModule, "Version": "v1.55.0", "Sum": toolprofile.SyftSourceSum}
		} else {
			data = map[string]any{
				"Path": "github.com/dantte-lp/gobfd/tools/syft", "Main": true, "Dir": spec.Dir,
				"GoMod": filepath.Join(spec.Dir, "go.mod"),
			}
		}
	case "go build":
		for index, arg := range spec.Args {
			if arg == "-o" && index+1 < len(spec.Args) {
				if err := os.WriteFile(spec.Args[index+1], []byte("fixture"), 0o755); err != nil {
					t.Fatal(err)
				}
				return true
			}
		}
		t.Fatal("missing Syft build output")
	case "syft version":
		data = map[string]any{
			"application": "syft", "version": "1.55.0", "gitCommit": toolprofile.SyftSourceCommit, "gitDescription": "v1.55.0",
			"goVersion": runtime.Version(), "compiler": runtime.Compiler, "platform": runtime.GOOS + "/" + runtime.GOARCH,
		}
	default:
		return false
	}
	if err := json.NewEncoder(spec.Stdout).Encode(data); err != nil {
		t.Fatal(err)
	}
	return true
}

func TestRunDispatchesReleaseSyftWithRootAndWorkflowEnvironment(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tools", "syft"), 0o755); err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	path := filepath.Join(temp, "github-path")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &specCommandRecorder{afterSpec: func(spec cirunner.CommandSpec) {
		if !writeSyftCommandFixture(t, spec) {
			t.Fatalf("unexpected command %q", spec.Args)
		}
	}}
	values := map[string]string{"RUNNER_TEMP": temp, "GITHUB_PATH": path}
	if err := run(t.Context(), []string{"release-syft"}, dependencies{
		specRunner: runner,
		getwd:      func() (string, error) { return root, nil },
		getenv:     func(name string) string { return values[name] },
		environ:    func() []string { return []string{"PATH=/sdk/bin", "GITHUB_TOKEN=private"} },
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != filepath.Join(temp, "gobfd-syft", "bin")+"\n" {
		t.Fatalf("published Syft path = %q, %v", data, err)
	}
	for _, spec := range runner.specs {
		if strings.Contains(strings.Join(spec.Env, "\n"), "private") {
			t.Fatal("credential passed to Syft child")
		}
	}
}

func TestRunReleaseSyftCompilerFlagSelectsOuterAdapterWithoutHostGo(t *testing.T) {
	t.Parallel()
	runner := &specCommandRecorder{}
	rootCalls := 0
	err := run(t.Context(), []string{"release-syft", "--compiler-container"}, dependencies{
		specRunner: runner,
		getwd:      func() (string, error) { rootCalls++; return t.TempDir(), nil },
		getenv:     func(string) string { return "" },
		environ:    func() []string { return []string{"GH_TOKEN=never-forward"} },
	})
	// The test executable is not the workflow's run-scoped prebuilt controller.
	if err == nil || rootCalls != 1 || len(runner.specs) != 0 {
		t.Fatalf("outer adapter dispatch = %v, root calls=%d, host commands=%d", err, rootCalls, len(runner.specs))
	}
}

func TestRunReleaseSyftRejectsUnknownFlagsAndPositionalArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--unknown"}, {"--compiler-container", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			rootCalls := 0
			err := run(t.Context(), append([]string{"release-syft"}, args...), dependencies{
				getwd: func() (string, error) { rootCalls++; return "", nil },
			})
			if err == nil || rootCalls != 0 {
				t.Fatalf("invalid arguments reached the adapter: %v", err)
			}
		})
	}
}

func TestRunSourceToolFoundationRequiresClosedCompilerCommand(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		{"release-source-tools"},
		{"release-source-tools", "--unknown"},
		{"release-source-tools", "--compiler-container", "extra"},
		{"release-goreleaser", "extra"},
	} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			rootCalls := 0
			err := run(t.Context(), arguments, dependencies{
				getwd: func() (string, error) { rootCalls++; return "", nil },
			})
			if err == nil || rootCalls != 0 {
				t.Fatalf("invalid foundation command reached worker: %v", err)
			}
		})
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
	if got := len(runner.names); got != 5 {
		t.Errorf("protobuf command count = %d, want 5", got)
	}
}

func TestRunRejectsUnknownModeWithoutEnvironmentLeak(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"unknown", "sonar-mode", "sonar-skip-notice"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			environmentReads := 0
			runner := &commandRecorder{}
			specRunner := &specCommandRecorder{}
			err := run(t.Context(), []string{mode}, dependencies{
				getenv: func(string) string {
					environmentReads++
					return "secret"
				},
				stdout: &output, runner: runner, specRunner: specRunner,
			})
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("run(%s) error = %v, want usage error", mode, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Error("usage error contains an environment value")
			}
			if environmentReads != 0 || output.Len() != 0 || len(runner.names) != 0 || len(specRunner.specs) != 0 {
				t.Error("unknown or retired command read environment, emitted output or ran a child command")
			}
		})
	}
}

type commandRecorder struct {
	names []string
}

func (r *commandRecorder) Run(_ context.Context, name string, _ ...string) error {
	r.names = append(r.names, name)
	return nil
}

func TestRunPackageCommandsUseRepositoryRoot(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	runner := &specCommandRecorder{}
	deps := dependencies{
		getwd:      func() (string, error) { return "/repo", nil },
		environ:    func() []string { return []string{"PATH=" + bin} },
		specRunner: runner,
	}
	if err := run(context.Background(), []string{"package-lifecycle"}, deps); err != nil {
		t.Fatalf("run(package-lifecycle) error = %v", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("package-lifecycle ran %d commands, want 1", len(runner.specs))
	}
	if spec := runner.specs[0]; spec.Dir != "/repo" || !slices.Contains(spec.Env, "PATH="+bin) {
		t.Errorf("package-lifecycle spec dir %q env %v", spec.Dir, spec.Env)
	}
	for _, command := range []string{"package-build", "package-lifecycle"} {
		if err := run(context.Background(), []string{command, "extra"}, dependencies{specRunner: runner}); err == nil {
			t.Errorf("%s accepted an extra argument", command)
		}
	}
	// The owning cirunner package covers a fully verified publisher build.
	// Dispatcher fixtures must not bypass managed-tool prerequisites.
	runner.specs = nil
	err := run(context.Background(), []string{"package-build"}, deps)
	if err == nil || !strings.Contains(err.Error(), "pinned UPX is missing from PATH") || len(runner.specs) != 0 {
		t.Fatalf("package-build prerequisite error = %v, commands = %#v", err, runner.specs)
	}
}

type specCommandRecorder struct {
	names     []string
	specs     []cirunner.CommandSpec
	afterRun  func([]string)
	afterSpec func(cirunner.CommandSpec)
	err       error
}

func (r *specCommandRecorder) RunCommand(_ context.Context, spec cirunner.CommandSpec) error {
	r.names = append(r.names, spec.Name)
	r.specs = append(r.specs, spec)
	if r.afterRun != nil {
		r.afterRun(spec.Args)
	}
	if r.afterSpec != nil {
		r.afterSpec(spec)
	}
	return r.err
}
