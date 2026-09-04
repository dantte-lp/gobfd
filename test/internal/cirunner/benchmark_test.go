package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const requiredBenchmarkFixture = `goos: linux
goarch: amd64
pkg: github.com/dantte-lp/gobfd/internal/bfd
BenchmarkRecvDecodeLookupEnqueue-8  6  200 ns/op  0 B/op  0 allocs/op
BenchmarkRecvDecodeFSM-8            6  200 ns/op  0 B/op  0 allocs/op
BenchmarkTxMarshalJitter-8          6  200 ns/op  0 B/op  0 allocs/op
PASS
`

func TestBenchmarkRunUsesFixedArgumentsAndFreshOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("set repository root fixture mode: %v", err)
	}
	output := filepath.Join(root, "new.txt")
	if err := os.WriteFile(output, []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("seed stale benchmark output: %v", err)
	}
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		if _, err := io.WriteString(spec.Stdout, requiredBenchmarkFixture); err != nil {
			t.Fatalf("write benchmark output: %v", err)
		}
	}}
	if err := BenchmarkRun(context.Background(), BenchmarkRunOptions{
		Root: root, Output: "new.txt", Regex: "^BenchmarkRequired$", Runner: runner,
	}); err != nil {
		t.Fatalf("BenchmarkRun() error = %v", err)
	}
	want := []specInvocation{{
		name: "go",
		args: []string{
			"test", "-buildvcs=false", "-bench=^BenchmarkRequired$", "-benchmem", "-count=6",
			"-run=^$", "-timeout=120s", "./internal/bfd/", "./internal/netio/",
		},
		dir: root,
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("benchmark invocation = %#v, want %#v", runner.calls, want)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read benchmark output: %v", err)
	}
	if string(got) != requiredBenchmarkFixture {
		t.Errorf("benchmark output = %q, want fresh output", got)
	}
	assertExactMode(t, output, 0o644)
	assertExactMode(t, root, 0o700)
}

func TestBenchmarkBaseAlwaysRemovesTemporaryWorktree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runnerTemp := t.TempDir()
	wantErr := errors.New("benchmark failed")
	runner := &recordingSpecRunner{failAt: 3, err: wantErr}
	err := BenchmarkBase(context.Background(), BenchmarkBaseOptions{
		Root: root, RunnerTemp: runnerTemp, Ref: "origin/main", Output: "old.txt",
		Regex: "^BenchmarkRequired$", Runner: runner,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("BenchmarkBase() error = %v, want wrapped benchmark error", err)
	}
	base := filepath.Join(runnerTemp, "gobfd-benchmark-base")
	want := []specInvocation{
		{
			name: "git",
			args: []string{"-c", "safe.directory=" + root, "check-ref-format", "--branch", "origin/main"},
			dir:  root,
		},
		{
			name: "git",
			args: []string{"-c", "safe.directory=" + root, "worktree", "add", "--detach", base, "origin/main"},
			dir:  root,
		},
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-bench=^BenchmarkRequired$", "-benchmem", "-count=6",
				"-run=^$", "-timeout=120s", "./internal/bfd/", "./internal/netio/",
			},
			dir: base,
		},
		{
			name: "git",
			args: []string{"-c", "safe.directory=" + root, "worktree", "remove", base},
			dir:  root,
		},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("base benchmark invocations = %#v, want %#v", runner.calls, want)
	}
}

func TestBenchmarkBaseCleansUpAfterPartialWorktreeAddFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("partial worktree add failure")
	runner := &partialAddSpecRunner{err: wantErr}
	err := BenchmarkBase(context.Background(), BenchmarkBaseOptions{
		Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
		Regex: "^BenchmarkRequired$", Runner: runner,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("BenchmarkBase() error = %v, want wrapped add error", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("partial-add command count = %d, want validation, add, cleanup", len(runner.calls))
	}
	cleanup := runner.calls[2]
	if cleanup.name != "git" || !reflect.DeepEqual(cleanup.args[len(cleanup.args)-3:], []string{
		"worktree", "remove", runner.worktree,
	}) {
		t.Errorf("partial-add cleanup = %#v, want git worktree remove", cleanup)
	}
	if runner.cleanupContextErr != nil {
		t.Errorf("partial-add cleanup context error = %v, want detached context", runner.cleanupContextErr)
	}
}

func TestBenchmarkBaseCleanupSurvivesBenchmarkCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	runner := &cancelingSpecRunner{cancel: cancel}
	err := BenchmarkBase(ctx, BenchmarkBaseOptions{
		Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
		Regex: "^BenchmarkRequired$", Runner: runner,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BenchmarkBase() error = %v, want context cancellation", err)
	}
	if runner.cleanupContextErr != nil {
		t.Errorf("cleanup context error = %v, want cancellation detached", runner.cleanupContextErr)
	}
}

func TestNormalizeBenchmarksRenamesExactAliasesAndRequiresStableSet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldPath := filepath.Join(root, "old.txt")
	newPath := filepath.Join(root, "new.txt")
	legacy := strings.NewReplacer(
		"BenchmarkRecvDecodeLookupEnqueue-8", "BenchmarkFullRecvPath-8",
		"BenchmarkRecvDecodeFSM-8", "BenchmarkFullRecvPathCodec-8",
		"BenchmarkTxMarshalJitter-8", "BenchmarkFullTxPath-8",
	).Replace(requiredBenchmarkFixture) + "BenchmarkFullRecvPathExtra-8  6  1 ns/op\n"
	for _, path := range []string{oldPath, newPath} {
		if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
			t.Fatalf("write legacy benchmark fixture: %v", err)
		}
	}
	if err := NormalizeBenchmarks(root, []string{"old.txt", "new.txt"}); err != nil {
		t.Fatalf("NormalizeBenchmarks() error = %v", err)
	}
	for _, path := range []string{oldPath, newPath} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read normalized benchmark fixture: %v", err)
		}
		text := string(got)
		for _, stable := range []string{
			"BenchmarkRecvDecodeLookupEnqueue-8",
			"BenchmarkRecvDecodeFSM-8",
			"BenchmarkTxMarshalJitter-8",
		} {
			if !strings.Contains(text, stable) {
				t.Errorf("normalized benchmark output lacks %q", stable)
			}
		}
		if !strings.Contains(text, "BenchmarkFullRecvPathExtra-8") {
			t.Error("normalization changed a non-alias benchmark name")
		}
		assertExactMode(t, path, 0o644)
	}
}

func TestNormalizeBenchmarksRejectsMissingMandatoryBenchmark(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"old.txt", "new.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("BenchmarkOther-8  6  1 ns/op\n"), 0o644); err != nil {
			t.Fatalf("write incomplete benchmark fixture: %v", err)
		}
	}
	err := NormalizeBenchmarks(root, []string{"old.txt", "new.txt"})
	if err == nil || !strings.Contains(err.Error(), "RecvDecodeLookupEnqueue") {
		t.Fatalf("NormalizeBenchmarks() error = %v, want mandatory benchmark context", err)
	}
}

func TestBenchmarkReportWritesEscapedStructuredArtifactsAndSummary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"old.txt", "new.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(requiredBenchmarkFixture), 0o644); err != nil {
			t.Fatalf("write benchmark input: %v", err)
		}
	}
	summary := filepath.Join(t.TempDir(), "step-summary")
	if err := os.WriteFile(summary, nil, 0o600); err != nil {
		t.Fatalf("create step summary: %v", err)
	}
	csvOutput := `,old.txt,,new.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
RecvDecodeFSM-8,2e-7,1%,2.4e-7,1%,+20.00%,p=0.001 n=6
Other-8,2e-9,1%,2.3e-9,1%,+15.00%,p=0.002 n=6
Tiny-8,5e-10,1%,6e-10,1%,+20.00%,p=0.003 n=6
Boundary-8,2e-9,1%,2.2e-9,1%,+10.00%,p=0.004 n=6
geomean,1e-8,,1.1e-8,,+10.00%,

,old.txt,,new.txt,,,
,B/op,CI,B/op,CI,vs base,P
RecvDecodeFSM-8,0,0%,0,0%,~,
geomean,0,,0,,?,
`
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		if len(spec.Args) == 0 {
			t.Fatal("benchstat command lacks arguments")
		}
		if containsArgument(spec.Args, "-format") {
			if _, err := io.WriteString(spec.Stdout, csvOutput); err != nil {
				t.Fatalf("write benchstat CSV: %v", err)
			}
			if _, err := io.WriteString(spec.Stderr, "G3: benchmark samples are sparse\n"); err != nil {
				t.Fatalf("write benchstat notes: %v", err)
			}
			return
		}
		if _, err := io.WriteString(spec.Stdout, "name old new delta\nUnsafe<& +20%\n"); err != nil {
			t.Fatalf("write benchstat text: %v", err)
		}
	}}
	var warning bytes.Buffer
	options := BenchmarkReportOptions{
		Root: root, Old: "old.txt", New: "new.txt", Markdown: "bench-report.md",
		HTML: "bench-comparison.html", JSON: "bench-comparison.json",
		CSV: "bench-regression/bench-csv.txt", Notes: "bench-regression/benchstat-notes.txt",
		StepSummary: summary, Warning: &warning, Runner: runner,
	}
	withoutSummary := options
	withoutSummary.StepSummary = ""
	if err := BenchmarkReport(context.Background(), withoutSummary); err == nil {
		t.Fatal("BenchmarkReport() with default empty step summary error = nil, want rejection")
	}
	if got := len(runner.calls); got != 0 {
		t.Fatalf("benchstat invocation count after rejected empty summary = %d, want 0", got)
	}
	if err := BenchmarkReport(context.Background(), options); err != nil {
		t.Fatalf("BenchmarkReport() error = %v", err)
	}
	if got := len(runner.calls); got != 2 {
		t.Fatalf("benchstat invocation count = %d, want 2", got)
	}
	for _, call := range runner.calls {
		if call.name != "go" || call.dir != root {
			t.Errorf("benchstat invocation = %#v, want Go command in repository root", call)
		}
	}

	markdown := readTestFile(t, filepath.Join(root, "bench-report.md"))
	if !strings.Contains(markdown, "## Benchmark comparison") || !strings.Contains(markdown, "Unsafe<& +20%") {
		t.Errorf("Markdown report lacks benchstat text: %q", markdown)
	}
	htmlReport := readTestFile(t, filepath.Join(root, "bench-comparison.html"))
	if strings.Contains(htmlReport, "Unsafe<&") || !strings.Contains(htmlReport, "Unsafe&lt;&amp; +20%") {
		t.Errorf("HTML report did not escape benchstat text: %q", htmlReport)
	}

	var structured struct {
		SchemaVersion string `json:"schema_version"`
		Rows          []struct {
			Unit           string  `json:"unit"`
			Name           string  `json:"name"`
			Base           float64 `json:"base"`
			Head           float64 `json:"head"`
			Delta          string  `json:"delta"`
			Significance   string  `json:"significance"`
			Classification string  `json:"regression_classification"`
		} `json:"comparison_rows"`
		Notes []string `json:"notes"`
	}
	structuredJSON := readTestFile(t, filepath.Join(root, "bench-comparison.json"))
	if strings.Contains(structuredJSON, `"version"`) || strings.Contains(structuredJSON, `"generated_at"`) {
		t.Errorf("generic PR benchmark report gained release context: %s", structuredJSON)
	}
	if err := json.Unmarshal([]byte(structuredJSON), &structured); err != nil {
		t.Fatalf("decode structured benchmark report: %v", err)
	}
	if structured.SchemaVersion != "gobfd.benchmark-comparison.v1" {
		t.Errorf("schema version = %q, want v1", structured.SchemaVersion)
	}
	if len(structured.Rows) != 5 {
		t.Fatalf("comparison row count = %d, want 5", len(structured.Rows))
	}
	if got := []string{
		structured.Rows[0].Classification,
		structured.Rows[1].Classification,
		structured.Rows[2].Classification,
		structured.Rows[3].Classification,
		structured.Rows[4].Classification,
	}; !reflect.DeepEqual(got, []string{"critical", "reported", "none", "reported", "none"}) {
		t.Errorf("regression classifications = %v", got)
	}
	if structured.Rows[3].Delta != "+10.00%" {
		t.Errorf("exact boundary delta = %q, want +10.00%%", structured.Rows[3].Delta)
	}
	if !reflect.DeepEqual(structured.Notes, []string{"G3: benchmark samples are sparse"}) {
		t.Errorf("structured notes = %v", structured.Notes)
	}

	stepSummary := readTestFile(t, summary)
	if !strings.Contains(stepSummary, "Report-only benchmark regressions") ||
		!strings.Contains(stepSummary, "`Other-8`") ||
		!strings.Contains(stepSummary, "`Boundary-8`") ||
		!strings.Contains(stepSummary, "benchstat notes") {
		t.Errorf("step summary lacks regression evidence: %q", stepSummary)
	}
	if !strings.Contains(warning.String(), "::warning::Critical benchmark regression detected (>=10%)") ||
		!strings.Contains(warning.String(), "RecvDecodeFSM-8,+20.00%,p=0.001 n=6") {
		t.Errorf("workflow warning lacks critical regression evidence: %q", warning.String())
	}
	for _, path := range []string{
		"bench-report.md", "bench-comparison.html", "bench-comparison.json",
		"bench-regression/bench-csv.txt", "bench-regression/benchstat-notes.txt",
	} {
		assertExactMode(t, filepath.Join(root, path), 0o644)
	}
}

func TestWriteBenchmarkWarningsReturnsRowWriteError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("warning output unavailable")
	output := &failOnWrite{failAt: 2, err: wantErr}
	err := writeBenchmarkWarnings(output, benchmarkComparison{ComparisonRows: []benchmarkComparisonRow{{
		Name: "BenchmarkRecvDecodeFSM-8", RegressionClassification: "critical",
	}}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeBenchmarkWarnings() error = %v, want wrapped row write error", err)
	}
}

func TestBenchmarkReportClearsStaleArtifactsBeforeBenchstatFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"old.txt", "new.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(requiredBenchmarkFixture), 0o644); err != nil {
			t.Fatalf("write benchmark input: %v", err)
		}
	}
	for _, name := range []string{
		"bench-report.md", "bench-comparison.html", "bench-comparison.json",
		"bench-regression/bench-csv.txt", "bench-regression/benchstat-notes.txt",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create artifact parent: %v", err)
		}
		if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
			t.Fatalf("seed stale artifact: %v", err)
		}
	}
	summary := filepath.Join(t.TempDir(), "summary")
	if err := os.WriteFile(summary, nil, 0o600); err != nil {
		t.Fatalf("create step summary: %v", err)
	}
	wantErr := errors.New("benchstat unavailable")
	runner := &recordingSpecRunner{failAt: 2, err: wantErr, afterRun: func(spec CommandSpec) {
		_, _ = io.WriteString(spec.Stdout, "text output\n")
	}}
	err := BenchmarkReport(context.Background(), BenchmarkReportOptions{
		Root: root, Old: "old.txt", New: "new.txt", Markdown: "bench-report.md",
		HTML: "bench-comparison.html", JSON: "bench-comparison.json",
		CSV: "bench-regression/bench-csv.txt", Notes: "bench-regression/benchstat-notes.txt",
		StepSummary: summary, Runner: runner,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("BenchmarkReport() error = %v, want wrapped benchstat error", err)
	}
	for _, name := range []string{
		"bench-report.md", "bench-comparison.html", "bench-comparison.json",
		"bench-regression/bench-csv.txt", "bench-regression/benchstat-notes.txt",
	} {
		if got := readTestFile(t, filepath.Join(root, name)); got != "" {
			t.Errorf("failed report retained stale %s content %q", name, got)
		}
	}
}

func TestValidateStepSummaryRejectsRelativeAndSymlinkPaths(t *testing.T) {
	t.Parallel()

	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "summary")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("create summary target: %v", err)
	}
	linkDir := filepath.Join(t.TempDir(), "summary-link")
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Fatalf("create summary parent symlink: %v", err)
	}
	for _, path := range []string{"summary", filepath.Join(linkDir, "summary")} {
		if err := validateStepSummary(path); err == nil {
			t.Errorf("validateStepSummary(%q) error = nil, want unsafe path rejection", path)
		}
	}
}

func TestBenchmarkOperationsRejectControlCharactersAndEscapingPaths(t *testing.T) {
	root := t.TempDir()
	runner := &recordingSpecRunner{}
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "benchmark regex control",
			run: func() error {
				return BenchmarkRun(context.Background(), BenchmarkRunOptions{
					Root: root, Output: "new.txt", Regex: "Benchmark\nOther", Runner: runner,
				})
			},
		},
		{
			name: "base ref control",
			run: func() error {
				return BenchmarkBase(context.Background(), BenchmarkBaseOptions{
					Root: root, RunnerTemp: t.TempDir(), Ref: "origin/main\n--force",
					Output: "old.txt", Regex: "Benchmark", Runner: runner,
				})
			},
		},
		{
			name: "output escapes root",
			run: func() error {
				return BenchmarkRun(context.Background(), BenchmarkRunOptions{
					Root: root, Output: "../new.txt", Regex: "Benchmark", Runner: runner,
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil {
				t.Fatal("operation error = nil, want unsafe input rejection")
			}
		})
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner received %d calls for unsafe inputs", len(runner.calls))
	}
}

func containsArgument(arguments []string, value string) bool {
	return slices.Contains(arguments, value)
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}

type cancelingSpecRunner struct {
	calls             int
	cancel            context.CancelFunc
	cleanupContextErr error
}

func (r *cancelingSpecRunner) RunCommand(ctx context.Context, _ CommandSpec) error {
	r.calls++
	if r.calls == 3 {
		r.cancel()
		return context.Canceled
	}
	if r.calls == 4 {
		r.cleanupContextErr = ctx.Err()
	}
	return nil
}

type partialAddSpecRunner struct {
	calls             []specInvocation
	err               error
	worktree          string
	cleanupContextErr error
}

type failOnWrite struct {
	writes int
	failAt int
	err    error
}

func (w *failOnWrite) Write(data []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return len(data), nil
}

func (r *partialAddSpecRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	r.calls = append(r.calls, specInvocation{
		name: spec.Name,
		args: append([]string(nil), spec.Args...),
		dir:  spec.Dir,
	})
	if len(r.calls) == 2 {
		r.worktree = spec.Args[len(spec.Args)-2]
		if err := os.Mkdir(r.worktree, 0o755); err != nil {
			return err
		}
		return r.err
	}
	if len(r.calls) == 3 {
		r.cleanupContextErr = ctx.Err()
	}
	return nil
}
