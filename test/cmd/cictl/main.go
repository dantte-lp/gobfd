// Command cictl owns shell-free CI workflow operations.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], dependencies{}); err != nil {
		slog.Error("CI command failed", "error", err)
		os.Exit(1)
	}
}

type dependencies struct {
	getenv     func(string) string
	getwd      func() (string, error)
	environ    func() []string
	now        func() time.Time
	runner     cirunner.CommandRunner
	specRunner cirunner.SpecRunner
	stdout     io.Writer
}

func run(ctx context.Context, arguments []string, deps dependencies) error {
	if deps.getenv == nil {
		deps.getenv = os.Getenv
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.getwd == nil {
		deps.getwd = os.Getwd
	}
	if deps.environ == nil {
		deps.environ = os.Environ
	}
	if deps.runner == nil {
		deps.runner = cirunner.ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr}
	}
	if deps.specRunner == nil {
		deps.specRunner = cirunner.ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr}
	}
	if deps.stdout == nil {
		deps.stdout = os.Stdout
	}
	if len(arguments) == 0 {
		return fmt.Errorf(
			"usage: cictl {sonar-mode|build|sbom|proto-verify|"+
				"benchmark-run|benchmark-base|benchmark-normalize|benchmark-report}: %w",
			flag.ErrHelp,
		)
	}

	switch arguments[0] {
	case "sonar-mode":
		return runSonarMode(arguments[1:], deps)
	case "build":
		return runBuild(ctx, arguments[1:], deps)
	case "sbom":
		return runSBOM(ctx, arguments[1:], deps)
	case "proto-verify":
		return runProtoVerify(ctx, arguments[1:], deps)
	case "benchmark-run":
		return runBenchmark(ctx, arguments[1:], deps)
	case "benchmark-base":
		return runBenchmarkBase(ctx, arguments[1:], deps)
	case "benchmark-normalize":
		return runBenchmarkNormalize(arguments[1:], deps)
	case "benchmark-report":
		return runBenchmarkReport(ctx, arguments[1:], deps)
	default:
		return fmt.Errorf("unknown CI command %q: %w", arguments[0], flag.ErrHelp)
	}
}

func runBenchmark(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("benchmark-run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "new.txt", "raw benchmark output")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse benchmark-run flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected benchmark-run arguments: %w", flag.ErrHelp)
	}
	root, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if err := cirunner.BenchmarkRun(ctx, cirunner.BenchmarkRunOptions{
		Root: root, Output: *output, Regex: deps.getenv("BENCH_REGEX"), Runner: deps.specRunner,
	}); err != nil {
		return fmt.Errorf("run head benchmarks: %w", err)
	}
	return nil
}

func runBenchmarkBase(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("benchmark-base", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "old.txt", "raw base benchmark output")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse benchmark-base flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected benchmark-base arguments: %w", flag.ErrHelp)
	}
	root, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if err := cirunner.BenchmarkBase(ctx, cirunner.BenchmarkBaseOptions{
		Root: root, RunnerTemp: deps.getenv("RUNNER_TEMP"),
		Ref: "origin/" + deps.getenv("GITHUB_BASE_REF"), Output: *output,
		Regex: deps.getenv("BENCH_REGEX"), Runner: deps.specRunner,
	}); err != nil {
		return fmt.Errorf("run base benchmarks: %w", err)
	}
	return nil
}

func runBenchmarkNormalize(arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("benchmark-normalize", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	oldName := flags.String("old", "old.txt", "old benchmark input")
	newName := flags.String("new", "new.txt", "new benchmark input")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse benchmark-normalize flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected benchmark-normalize arguments: %w", flag.ErrHelp)
	}
	root, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if err := cirunner.NormalizeBenchmarks(root, []string{*oldName, *newName}); err != nil {
		return fmt.Errorf("normalize benchmark inputs: %w", err)
	}
	return nil
}

func runBenchmarkReport(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("benchmark-report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	oldName := flags.String("old", "old.txt", "old benchmark input")
	newName := flags.String("new", "new.txt", "new benchmark input")
	markdown := flags.String("markdown", "bench-report.md", "Markdown report")
	htmlReport := flags.String("html", "bench-comparison.html", "HTML report")
	jsonReport := flags.String("json", "bench-comparison.json", "structured JSON report")
	csvReport := flags.String("csv", "bench-regression/bench-csv.txt", "benchstat CSV output")
	notes := flags.String("notes", "bench-regression/benchstat-notes.txt", "benchstat notes")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse benchmark-report flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected benchmark-report arguments: %w", flag.ErrHelp)
	}
	root, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if err := cirunner.BenchmarkReport(ctx, cirunner.BenchmarkReportOptions{
		Root: root, Old: *oldName, New: *newName, Markdown: *markdown, HTML: *htmlReport,
		JSON: *jsonReport, CSV: *csvReport, Notes: *notes,
		StepSummary: deps.getenv("GITHUB_STEP_SUMMARY"), Warning: deps.stdout, Runner: deps.specRunner,
	}); err != nil {
		return fmt.Errorf("generate benchmark comparison reports: %w", err)
	}
	return nil
}

func runSBOM(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("sbom", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reportDir := flags.String("report-dir", "reports/security", "SBOM report directory")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse SBOM flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected SBOM arguments: %w", flag.ErrHelp)
	}
	if err := cirunner.SBOM(ctx, cirunner.SBOMOptions{
		ReportDir: *reportDir,
		Runner:    deps.specRunner,
	}); err != nil {
		return fmt.Errorf("generate CI SBOMs: %w", err)
	}
	return nil
}

func runProtoVerify(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("proto-verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse proto-verify flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected proto-verify arguments: %w", flag.ErrHelp)
	}
	root, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if err := cirunner.ProtoVerify(ctx, cirunner.ProtoOptions{
		Root:        root,
		RunnerTemp:  deps.getenv("RUNNER_TEMP"),
		Environment: deps.environ(),
		Runner:      deps.specRunner,
	}); err != nil {
		return fmt.Errorf("verify generated protobuf code: %w", err)
	}
	return nil
}

func runSonarMode(arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("sonar-mode", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	actor := flags.String("actor", deps.getenv("GITHUB_ACTOR"), "GitHub workflow actor")
	output := flags.String("output", deps.getenv("GITHUB_OUTPUT"), "GitHub step output file")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse sonar-mode flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected sonar-mode arguments: %w", flag.ErrHelp)
	}
	if err := cirunner.SonarMode(cirunner.SonarOptions{
		TokenPresent: deps.getenv("SONAR_TOKEN_PRESENT"),
		Actor:        *actor,
		Output:       *output,
	}); err != nil {
		return fmt.Errorf("select Sonar workflow mode: %w", err)
	}
	return nil
}

func runBuild(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sha := flags.String("sha", deps.getenv("GITHUB_SHA"), "Git commit SHA")
	output := flags.String("output", "/tmp/gobfd-build", "binary output directory")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse build flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected build arguments: %w", flag.ErrHelp)
	}
	if err := cirunner.Build(ctx, cirunner.BuildOptions{
		SHA:    *sha,
		Output: *output,
		Now:    deps.now,
		Runner: deps.runner,
	}); err != nil {
		return fmt.Errorf("build CI binaries: %w", err)
	}
	return nil
}
