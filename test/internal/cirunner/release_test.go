package cirunner

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReleaseBuildUsesTagVersionAndFixedTargets(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	err := ReleaseBuild(context.Background(), ReleaseBuildOptions{
		RefName: "v0.6.2", SHA: strings.Repeat("a", 40), Output: filepath.Join(t.TempDir(), "build"),
		Now:    func() time.Time { return time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC) },
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("ReleaseBuild() error = %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("release build calls = %d, want 4", len(runner.calls))
	}
	for _, call := range runner.calls {
		if call.name != "go" || !containsArgument(call.args, "-buildvcs=false") {
			t.Errorf("release build call = %#v", call)
		}
		if !strings.Contains(strings.Join(call.args, " "), "internal/version.Version=0.6.2") {
			t.Errorf("release build lacks tag version: %#v", call.args)
		}
	}
}

func TestReleaseTestReportRunsPinnedGotestsumAndRendersHTML(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		junit := argumentAfter(t, spec.Args, "--junitfile")
		jsonReport := argumentAfter(t, spec.Args, "--jsonfile")
		if err := os.WriteFile(filepath.Join(spec.Dir, junit), []byte(
			`<testsuites tests="1"><testsuite name="unit" tests="1"><testcase name="ok"/></testsuite></testsuites>`), 0o644); err != nil {
			t.Fatalf("write simulated JUnit: %v", err)
		}
		if err := os.WriteFile(filepath.Join(spec.Dir, jsonReport), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write simulated test JSON: %v", err)
		}
	}}
	if err := ReleaseTestReport(context.Background(), root, runner); err != nil {
		t.Fatalf("ReleaseTestReport() error = %v", err)
	}
	want := []specInvocation{{
		name: "go",
		args: []string{
			"tool", "-modfile=tools/go.mod", "gotestsum",
			"--junitfile", "reports/tests/unit-report.xml",
			"--jsonfile", "reports/tests/unit-report.json",
			"--format", "short-verbose", "--", "-buildvcs=false", "./...", "-race", "-count=1",
		},
		dir: root,
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("release test calls = %#v, want %#v", runner.calls, want)
	}
	if data, err := os.ReadFile(filepath.Join(root, "reports/tests/unit-report.html")); err != nil ||
		!strings.Contains(string(data), "unit") {
		t.Errorf("rendered JUnit HTML = %q, %v", data, err)
	}
}

func TestReleaseBenchmarksPreserveRawAndCombinedEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var console strings.Builder
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		if _, err := io.WriteString(spec.Stdout, requiredBenchmarkFixture); err != nil {
			t.Fatalf("write benchmark fixture: %v", err)
		}
	}}
	if err := ReleaseBenchmarks(context.Background(), root, "v0.6.2", &console, runner); err != nil {
		t.Fatalf("ReleaseBenchmarks() error = %v", err)
	}
	want := []specInvocation{
		{name: "go", args: []string{"test", "-buildvcs=false", "-bench=.", "-benchmem", "-count=6", "-run=^$", "-timeout=120s", "./internal/bfd/"}, dir: root},
		{name: "go", args: []string{"test", "-buildvcs=false", "-bench=.", "-benchmem", "-count=6", "-run=^$", "-timeout=120s", "./internal/netio/"}, dir: root},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("release benchmark calls = %#v, want %#v", runner.calls, want)
	}
	combined, err := os.ReadFile(filepath.Join(root, "testdata/benchmarks/v0.6.2/benchmark.txt"))
	if err != nil || strings.Count(string(combined), "BenchmarkRecvDecodeFSM") != 2 {
		t.Errorf("combined benchmark evidence = %q, %v", combined, err)
	}
	if strings.Count(console.String(), "BenchmarkRecvDecodeFSM") != 2 {
		t.Errorf("benchmark console output did not preserve tee behavior: %q", console.String())
	}
}

func TestReleaseBenchmarksPrevalidateAndClearFixedEvidence(t *testing.T) {
	t.Parallel()

	t.Run("command failure clears every target", func(t *testing.T) {
		root := t.TempDir()
		directory := filepath.Join(root, "testdata/benchmarks/v0.6.2")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"benchmark-bfd.txt", "benchmark-netio.txt", "benchmark.txt"} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte("stale\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		wantErr := errors.New("benchmark failed")
		runner := &recordingSpecRunner{failAt: 1, err: wantErr}
		if err := ReleaseBenchmarks(context.Background(), root, "v0.6.2", io.Discard, runner); !errors.Is(err, wantErr) {
			t.Fatalf("ReleaseBenchmarks() error = %v, want %v", err, wantErr)
		}
		for _, name := range []string{"benchmark-bfd.txt", "benchmark-netio.txt", "benchmark.txt"} {
			data, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil || len(data) != 0 {
				t.Errorf("failed benchmark retained %s = %q, %v", name, data, err)
			}
		}
	})

	t.Run("nonregular target is rejected before mutation", func(t *testing.T) {
		root := t.TempDir()
		directory := filepath.Join(root, "testdata/benchmarks/v0.6.2")
		if err := os.MkdirAll(filepath.Join(directory, "benchmark.txt"), 0o755); err != nil {
			t.Fatal(err)
		}
		bfd := filepath.Join(directory, "benchmark-bfd.txt")
		if err := os.WriteFile(bfd, []byte("preserve\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runner := &recordingSpecRunner{}
		if err := ReleaseBenchmarks(context.Background(), root, "v0.6.2", io.Discard, runner); err == nil {
			t.Fatal("ReleaseBenchmarks() error = nil, want nonregular target rejection")
		}
		if data, err := os.ReadFile(bfd); err != nil || string(data) != "preserve\n" {
			t.Errorf("prevalidation failure changed earlier target = %q, %v", data, err)
		}
		if len(runner.calls) != 0 {
			t.Errorf("runner received %d calls after prevalidation failure", len(runner.calls))
		}
	})
}

func TestReleaseBenchmarksRejectVersionPathTraversal(t *testing.T) {
	t.Parallel()

	runner := &recordingSpecRunner{}
	err := ReleaseBenchmarks(context.Background(), t.TempDir(), "..", io.Discard, runner)
	if err == nil {
		t.Fatal("ReleaseBenchmarks() error = nil, want version path rejection")
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner received %d calls for unsafe release version", len(runner.calls))
	}
}

func TestReleaseBenchmarkMetadataIsStructured(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		if _, err := io.WriteString(spec.Stdout, "go1.27.0\n"); err != nil {
			t.Fatalf("write Go version: %v", err)
		}
	}}
	err := ReleaseBenchmarkMetadata(context.Background(), ReleaseMetadataOptions{
		Root: root, Version: "v0.6.2", SHA: strings.Repeat("b", 40),
		Now: func() time.Time { return time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC) }, Runner: runner,
	})
	if err != nil {
		t.Fatalf("ReleaseBenchmarkMetadata() error = %v", err)
	}
	if !reflect.DeepEqual(runner.calls, []specInvocation{{name: "go", args: []string{"env", "GOVERSION"}, dir: root}}) {
		t.Errorf("Go version calls = %#v", runner.calls)
	}
	var metadata struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
		Go      string `json:"go"`
		Count   int    `json:"count"`
	}
	data, readErr := os.ReadFile(filepath.Join(root, "testdata/benchmarks/v0.6.2/meta.json"))
	if readErr != nil || json.Unmarshal(data, &metadata) != nil {
		t.Fatalf("read release metadata: %v", readErr)
	}
	if metadata.Version != "v0.6.2" || metadata.Commit != strings.Repeat("b", 8) ||
		metadata.Date != "2026-09-03T12:00:00Z" || metadata.Go != "go1.27.0" || metadata.Count != 6 {
		t.Errorf("release metadata = %#v", metadata)
	}
}

func TestReleaseBenchmarkComparisonPublishesTextHTMLAndJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	versionDir := filepath.Join(root, "testdata/benchmarks/v0.6.2")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "testdata/benchmarks/baseline.txt"), filepath.Join(versionDir, "benchmark.txt")} {
		if err := os.WriteFile(path, []byte(requiredBenchmarkFixture), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	csv := `,baseline.txt,,benchmark.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
RecvDecodeFSM-8,2e-7,1%,2.1e-7,1%,+5.00%,p=0.01 n=6
`
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		output := "benchmark comparison\n"
		if containsArgument(spec.Args, "-format") {
			output = csv
		}
		if _, err := io.WriteString(spec.Stdout, output); err != nil {
			t.Fatalf("write benchstat output: %v", err)
		}
	}}
	generatedAt := time.Date(2026, time.September, 3, 12, 34, 56, 0, time.FixedZone("test", 3*60*60))
	if err := ReleaseBenchmarkComparison(context.Background(), ReleaseComparisonOptions{
		Root: root, Version: "v0.6.2", Now: func() time.Time { return generatedAt }, Runner: runner,
	}); err != nil {
		t.Fatalf("ReleaseBenchmarkComparison() error = %v", err)
	}
	for _, name := range []string{"comparison-vs-baseline.txt", "comparison-vs-baseline.html", "comparison-vs-baseline.json"} {
		if info, err := os.Stat(filepath.Join(root, "reports/benchmarks", name)); err != nil || info.Size() == 0 {
			t.Errorf("comparison artifact %s missing or empty: %v", name, err)
		}
	}
	htmlReport, err := os.ReadFile(filepath.Join(root, "reports/benchmarks/comparison-vs-baseline.html"))
	if err != nil || !strings.Contains(string(htmlReport), "baseline vs v0.6.2") ||
		!strings.Contains(string(htmlReport), "2026-09-03T09:34:56Z") {
		t.Errorf("release comparison HTML lacks version/time context: %q, %v", htmlReport, err)
	}
	var structured struct {
		Version     string `json:"version"`
		GeneratedAt string `json:"generated_at"`
	}
	data, err := os.ReadFile(filepath.Join(root, "reports/benchmarks/comparison-vs-baseline.json"))
	if err != nil || json.Unmarshal(data, &structured) != nil {
		t.Fatalf("read structured release comparison: %v", err)
	}
	if structured.Version != "v0.6.2" || structured.GeneratedAt != "2026-09-03T09:34:56Z" {
		t.Errorf("release comparison context = %#v", structured)
	}
}

func TestReleaseBenchmarkComparisonClearsStaleOutputsWithoutBaseline(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "reports/benchmarks")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	artifacts := []string{
		"comparison-vs-baseline.txt", "comparison-vs-baseline.md", "comparison-vs-baseline.html",
		"comparison-vs-baseline.json", "comparison-vs-baseline.csv", "comparison-vs-baseline-notes.txt",
	}
	for _, name := range artifacts {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ReleaseBenchmarkComparison(context.Background(), ReleaseComparisonOptions{
		Root: root, Version: "v0.6.2", Runner: &recordingSpecRunner{},
	}); err != nil {
		t.Fatalf("ReleaseBenchmarkComparison() error = %v", err)
	}
	for _, name := range artifacts {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || len(data) != 0 {
			t.Errorf("absent baseline retained %s = %q, %v", name, data, err)
		}
	}
}

func TestReleaseReportsArchiveContainsReportsAndBenchmarkEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for path, data := range map[string]string{
		"reports/tests/unit-report.xml":                  "junit\n",
		"testdata/benchmarks/v0.6.2/benchmark-bfd.txt":   "bfd\n",
		"testdata/benchmarks/v0.6.2/benchmark-netio.txt": "netio\n",
		"testdata/benchmarks/v0.6.2/benchmark.txt":       "combined\n",
		"testdata/benchmarks/v0.6.2/meta.json":           "{}\n",
	} {
		absolute := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(root, "gobfd-v0.6.2-reports.tar.gz")
	if err := ReleaseReportsArchive(root, "v0.6.2"); err != nil {
		t.Fatalf("ReleaseReportsArchive() error = %v", err)
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	decompressor, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(decompressor)
	entries := map[string]bool{}
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		entries[header.Name] = true
	}
	if err := decompressor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"reports/tests/unit-report.xml", "reports/benchmarks/benchmark-bfd.txt",
		"reports/benchmarks/benchmark-netio.txt", "reports/benchmarks/benchmark.txt",
		"reports/benchmarks/meta.json",
	} {
		if !entries[name] {
			t.Errorf("reports archive lacks %s", name)
		}
	}
}

func TestOpenRootedRegularFileRejectsReplacementOutsideRoot(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "evidence.txt")
	if err := os.WriteFile(path, []byte("expected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if file, err := openRootedRegularFile(root, "evidence.txt", expected, "test evidence"); err == nil {
		_ = file.Close()
		t.Fatal("openRootedRegularFile() error = nil, want external replacement rejection")
	}
}

func TestOpenReportArchiveFileRejectsSiblingDirectoryReplacement(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	reports := filepath.Join(repository, "reports")
	originalDirectory := filepath.Join(reports, "subdir")
	siblingDirectory := filepath.Join(repository, "sibling")
	for _, directory := range []string{originalDirectory, siblingDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	siblingFile := filepath.Join(siblingDirectory, "evidence.txt")
	if err := os.WriteFile(siblingFile, []byte("evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(siblingFile)
	if err != nil {
		t.Fatal(err)
	}
	parkedDirectory := filepath.Join(reports, "parked")
	if err := os.Rename(originalDirectory, parkedDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "sibling"), originalDirectory); err != nil {
		t.Fatal(err)
	}
	reportsRoot, err := os.OpenRoot(reports)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reportsRoot.Close() }()
	if file, err := openReportArchiveFile(reportsRoot, filepath.Join("subdir", "evidence.txt"), expected); err == nil {
		_ = file.Close()
		t.Fatal("openReportArchiveFile() error = nil, want sibling directory escape rejection")
	}
}

func argumentAfter(t *testing.T, arguments []string, marker string) string {
	t.Helper()
	for index, argument := range arguments {
		if argument == marker && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	t.Fatalf("arguments %q lack %s value", arguments, marker)
	return ""
}
