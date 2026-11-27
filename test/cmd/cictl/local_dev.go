package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

func runDevEnsure(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("dev-ensure", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", deps.getenv("COMPOSE_PROJECT_NAME"), "Podman Compose project name")
	composeFile := flags.String("compose-file", "deployments/compose/compose.dev.yml", "Podman Compose file")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse dev-ensure flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected dev-ensure arguments: %w", flag.ErrHelp)
	}
	root, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve dev-ensure root: %w", err)
	}
	if err := cirunner.DevEnsure(ctx, cirunner.DevEnsureOptions{
		Root: root, Project: *project, ComposeFile: *composeFile,
		Environment: deps.environ(), Stdout: deps.stdout, Runner: deps.specRunner,
	}); err != nil {
		return fmt.Errorf("ensure development container: %w", err)
	}
	return nil
}

func runLocalBenchmark(ctx context.Context, arguments []string, deps dependencies) error {
	flagArgs, execPrefix := splitDashArguments(arguments)
	flags := flag.NewFlagSet("local-benchmark", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "benchmark.txt", "benchmark output file")
	pkg := flags.String("package", "./internal/bfd/", "Go package pattern")
	timeout := flags.String("timeout", "", "optional go test timeout")
	if err := flags.Parse(flagArgs); err != nil {
		return fmt.Errorf("parse local-benchmark flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected local-benchmark arguments: %w", flag.ErrHelp)
	}
	dir, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve local-benchmark directory: %w", err)
	}
	if err := cirunner.LocalBenchmark(ctx, cirunner.LocalBenchmarkOptions{
		Dir: dir, Output: *output, Package: *pkg, Timeout: *timeout,
		ExecPrefix: execPrefix, Stdout: deps.stdout, Stderr: deps.stderr, Runner: deps.prefixRunner,
	}); err != nil {
		return fmt.Errorf("run local benchmarks: %w", err)
	}
	return nil
}

func runLocalBenchmarkSave(ctx context.Context, arguments []string, deps dependencies) error {
	flagArgs, execPrefix := splitDashArguments(arguments)
	flags := flag.NewFlagSet("local-benchmark-save", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	benchDir := flags.String("bench-dir", "testdata/benchmarks", "benchmark archive root")
	version := flags.String("version", "", "benchmark archive version")
	commit := flags.String("commit", "unknown", "git commit")
	buildDate := flags.String("date", "", "build timestamp")
	if err := flags.Parse(flagArgs); err != nil {
		return fmt.Errorf("parse local-benchmark-save flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected local-benchmark-save arguments: %w", flag.ErrHelp)
	}
	dir, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve local-benchmark-save directory: %w", err)
	}
	if err := cirunner.LocalBenchmarkSave(ctx, cirunner.LocalBenchmarkSaveOptions{
		Dir: dir, BenchDir: *benchDir, Version: *version, Commit: *commit, BuildDate: *buildDate,
		ExecPrefix: execPrefix, Stdout: deps.stdout, Stderr: deps.stderr, Runner: deps.prefixRunner,
	}); err != nil {
		return fmt.Errorf("save local benchmarks: %w", err)
	}
	return nil
}

func runLocalBenchmarkCompare(ctx context.Context, arguments []string, deps dependencies) error {
	flagArgs, execPrefix := splitDashArguments(arguments)
	flags := flag.NewFlagSet("local-benchmark-compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	benchDir := flags.String("bench-dir", "testdata/benchmarks", "benchmark archive root")
	reportDir := flags.String("report-dir", "reports", "report output root")
	oldVer := flags.String("old", "baseline", "old benchmark version")
	newVer := flags.String("new", "", "new benchmark version")
	buildDate := flags.String("date", "", "comparison timestamp")
	if err := flags.Parse(flagArgs); err != nil {
		return fmt.Errorf("parse local-benchmark-compare flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected local-benchmark-compare arguments: %w", flag.ErrHelp)
	}
	dir, err := deps.getwd()
	if err != nil {
		return fmt.Errorf("resolve local-benchmark-compare directory: %w", err)
	}
	if err := cirunner.LocalBenchmarkCompare(ctx, cirunner.LocalBenchmarkCompareOptions{
		Dir: dir, BenchDir: *benchDir, ReportDir: *reportDir,
		Old: *oldVer, New: *newVer, BuildDate: *buildDate,
		ExecPrefix: execPrefix, Stdout: deps.stdout, Stderr: deps.stderr, Runner: deps.prefixRunner,
	}); err != nil {
		return fmt.Errorf("compare local benchmarks: %w", err)
	}
	return nil
}

func splitDashArguments(arguments []string) ([]string, []string) {
	for index, arg := range arguments {
		if arg == "--" {
			return arguments[:index], arguments[index+1:]
		}
	}
	return arguments, nil
}
