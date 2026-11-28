package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // Execute fixed Make targets with isolated producer fixtures and compiled cictl.
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMakeBenchmarkPreservesCommandFailure(t *testing.T) {
	t.Parallel()

	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(makefile), "SHELL := bash") || strings.Contains(string(makefile), ".SHELLFLAGS") {
		t.Fatal("Makefile retains Bash shell configuration")
	}
	cictlBin := buildCICTLForMakeTest(t)

	for _, test := range []struct {
		name, target, failure, output, blockedOutput string
		wantError, sinkFailure                       bool
	}{
		{"bfd failure", "benchmark", "bfd", "benchmark.txt", "", true, false},
		{"bfd success", "benchmark", "", "benchmark.txt", "", false, false},
		{"all failure", "benchmark-all", "all", "benchmark.txt", "", true, false},
		{"all success", "benchmark-all", "", "benchmark.txt", "", false, false},
		{
			"save bfd failure", "benchmark-save", "bfd",
			"samples/local/benchmark-bfd.txt", "samples/local/meta.json", true, false,
		},
		{
			"save netio failure", "benchmark-save", "netio",
			"samples/local/benchmark-netio.txt", "samples/local/meta.json", true, false,
		},
		{
			"save version failure", "benchmark-save", "version",
			"samples/local/benchmark.txt", "samples/local/meta.json", true, false,
		},
		{"save success", "benchmark-save", "", "samples/local/benchmark.txt", "", false, false},
		{
			"compare failure", "benchmark-compare", "compare",
			"reports/benchmarks/comparison.txt", "reports/benchmarks/comparison.html", true, false,
		},
		{"compare success", "benchmark-compare", "", "reports/benchmarks/comparison.txt", "", false, false},
		{"bfd sink failure", "benchmark", "", "benchmark.txt", "", true, true},
		{
			"save sink failure", "benchmark-save", "",
			"samples/local/benchmark-bfd.txt", "samples/local/meta.json", true, true,
		},
		{
			"compare sink failure", "benchmark-compare", "",
			"reports/benchmarks/comparison.txt", "reports/benchmarks/comparison.html", true, true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if writeErr := os.WriteFile(filepath.Join(root, "Makefile"), makefile, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			self, execErr := os.Executable()
			if execErr != nil {
				t.Fatal(execErr)
			}
			if linkErr := os.Symlink(self, filepath.Join(root, "producer")); linkErr != nil {
				t.Fatal(linkErr)
			}
			fakeBin := setupBenchmarkMakeFakePodman(t, self)
			if test.sinkFailure {
				if mkdirErr := os.MkdirAll(filepath.Join(root, test.output), 0o700); mkdirErr != nil {
					t.Fatal(mkdirErr)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "make", "--no-print-directory", test.target,
				"CI_CTL="+cictlBin, "EXEC=./producer", "BENCH_DIR=samples", "BENCH_VERSION=local",
				"REPORT_DIR=reports", "GIT_COMMIT=fixture", "BUILD_DATE=fixture", "VERSION=local")
			command.Dir = root
			command.Env = benchmarkMakeEnv(fakeBin, root, test.failure, "")
			output, runErr := command.CombinedOutput()
			if (runErr != nil) != test.wantError || ctx.Err() != nil {
				t.Errorf(
					"make exit = %v, context = %v, want error %t; output:\n%s",
					runErr, ctx.Err(), test.wantError, output,
				)
			}
			verifyBenchmarkMakeSubtest(t, root, test.target, test.failure, test.output,
				test.blockedOutput, test.wantError, test.sinkFailure, output)
		})
	}
}

func TestMakeBenchmarkReportRunsInDevContainer(t *testing.T) {
	t.Parallel()

	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if writeErr := os.WriteFile(filepath.Join(root, "Makefile"), makefile, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	dryRun := exec.CommandContext(
		t.Context(), "make", "--no-print-directory", "-n", "benchmark-report",
		"COMPOSE_PROJECT_NAME=dev-contract",
	)
	dryRun.Dir = root
	output, err := dryRun.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run benchmark-report: %v\n%s", err, output)
	}
	rendered := string(output)
	for _, required := range []string{
		`dev-ensure --project "dev-contract"`,
		`exec -T dev env GCC_VERSION="gcc (Debian trixie)" go run ./test/cmd/benchreport --`,
		`"bench-results/bench-go.txt"`,
		`"testdata/benchmarks/v0.4.0/meta.json"`,
		`"scripts/report-template.html"`,
		`"reports/benchmarks/cross-comparison.html"`,
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("benchmark-report dry-run lacks %q:\n%s", required, rendered)
		}
	}
	if strings.Contains(rendered, root) {
		t.Errorf("benchmark-report passed host root path %q into container:\n%s", root, rendered)
	}
}

func TestMakeBenchmarkDefaultExecEnvPrefix(t *testing.T) {
	t.Parallel()

	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cictlBin := buildCICTLForMakeTest(t)
	fakeBin := setupBenchmarkMakeFakePodman(t, self)

	for _, test := range []struct {
		target, output string
	}{
		{"benchmark", "benchmark.txt"},
		{"benchmark-all", "benchmark.txt"},
		{"benchmark-save", "samples/local/benchmark.txt"},
		{"benchmark-compare", "reports/benchmarks/comparison.txt"},
	} {
		t.Run(test.target, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if writeErr := os.WriteFile(filepath.Join(root, "Makefile"), makefile, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(
				ctx, "make", "--no-print-directory", test.target,
				"CI_CTL="+cictlBin, "COMPOSE_PROJECT_NAME=bench-default-proj",
				"BENCH_DIR=samples", "BENCH_VERSION=local", "REPORT_DIR=reports",
				"GIT_COMMIT=fixture", "BUILD_DATE=fixture", "VERSION=local",
			)
			command.Dir = root
			command.Env = benchmarkMakeEnv(fakeBin, root, "", "bench-default-proj")
			output, runErr := command.CombinedOutput()
			if runErr != nil || ctx.Err() != nil {
				t.Fatalf("make %s failed: %v (ctx=%v)\n%s", test.target, runErr, ctx.Err(), output)
			}
			if _, statErr := os.Stat(filepath.Join(root, ".dev-ensure-ran")); statErr != nil {
				t.Fatalf("make %s did not run dev-ensure prerequisite: %v", test.target, statErr)
			}
			verifyBenchmarkMakeSubtest(t, root, test.target, "", test.output, "", false, false, output)
		})
	}
}

func setupBenchmarkMakeFakePodman(t *testing.T, self string) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(binDir, "podman")); err != nil {
		t.Fatal(err)
	}
	return binDir
}

func benchmarkMakeEnv(fakeBin, root, failure, expectedProject string) []string {
	base := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "COMPOSE_PROJECT_NAME=") ||
			strings.HasPrefix(entry, "MAKEFLAGS=") || strings.HasPrefix(entry, "MFLAGS=") {
			continue
		}
		base = append(base, entry)
	}
	return append(
		base,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		benchmarkMakeProducerEnabled+"=1",
		"BENCHMARK_TEST_ROOT="+root,
		"BENCHMARK_TEST_FAILURE="+failure,
		"BENCHMARK_EXPECT_PROJECT="+expectedProject,
		"MAKEFLAGS=",
		"MFLAGS=",
	)
}

func buildCICTLForMakeTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cictl")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", bin, "./test/cmd/cictl")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile cictl for Make test: %v\n%s", err, output)
	}
	return bin
}

func verifyBenchmarkMakeSubtest(
	t *testing.T,
	root, target, failure, outputPath, blockedOutput string,
	wantError, sinkFailure bool,
	output []byte,
) {
	t.Helper()
	if failure != "" && !strings.Contains(string(output), "injected "+failure+" failure") {
		t.Errorf("producer failure missing from console:\n%s", output)
	}
	if blockedOutput != "" {
		if _, statErr := os.Stat(filepath.Join(root, blockedOutput)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("failure published %s: %v", blockedOutput, statErr)
		}
	}
	if !sinkFailure {
		data, readErr := os.ReadFile(filepath.Join(root, outputPath))
		if readErr != nil || !strings.Contains(string(data), "sample") || !strings.Contains(string(output), "sample") {
			t.Errorf("raw samples lost: file = %q, error = %v, console:\n%s", data, readErr, output)
		}
	}
	if !wantError && target == "benchmark-save" {
		data, readErr := os.ReadFile(filepath.Join(root, "samples/local/meta.json"))
		var metadata struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Go      string `json:"go"`
		}
		if readErr != nil || json.Unmarshal(data, &metadata) != nil || metadata.Version != "local" ||
			metadata.Commit != "fixture" || metadata.Go != "go-fixture" {
			t.Errorf("saved metadata = %q, error = %v", data, readErr)
		}
	}
	if !wantError && target == "benchmark-compare" {
		data, readErr := os.ReadFile(filepath.Join(root, "reports/benchmarks/comparison.html"))
		if readErr != nil || !strings.Contains(string(data), "compare sample") ||
			!strings.HasSuffix(string(data), "</pre></body></html>\n") {
			t.Errorf("comparison HTML = %q, error = %v", data, readErr)
		}
	}
}

const benchmarkMakeProducerEnabled = "GOBFD_BENCHMARK_MAKE_PRODUCER"

func runBenchmarkMakeProducer() int {
	return runBenchmarkMakeProducerArgs(os.Args[1:])
}

func runBenchmarkMakePodman() int {
	args := os.Args[1:]
	if wantProj := os.Getenv("BENCHMARK_EXPECT_PROJECT"); wantProj != "" {
		if gotProj := os.Getenv("COMPOSE_PROJECT_NAME"); gotProj != wantProj {
			_, _ = fmt.Fprintf(os.Stderr, "COMPOSE_PROJECT_NAME = %q, want %q\n", gotProj, wantProj)
			return 92
		}
	}
	root := os.Getenv("BENCHMARK_TEST_ROOT")
	switch {
	case len(args) == 9 && args[0] == "compose" && args[5] == "ps" && args[6] == "--all":
		_, _ = fmt.Fprintln(os.Stdout, "dev-cid")
		return 0
	case len(args) == 4 && args[0] == "inspect" && args[1] == "dev-cid" && args[2] == "--format":
		_, _ = fmt.Fprintln(os.Stdout, root)
		return 0
	case len(args) == 9 && args[0] == "compose" && args[5] == "up" && args[6] == "-d":
		if writeErr := os.WriteFile(filepath.Join(root, ".dev-ensure-ran"), []byte("ok\n"), 0o600); writeErr != nil {
			_, _ = fmt.Fprintln(os.Stderr, "write dev-ensure marker:", writeErr)
			return 93
		}
		return 0
	case len(args) > 8 && args[0] == "compose" && args[5] == "exec" && args[6] == "-T" && args[7] == "dev":
		return runBenchmarkMakeProducerArgs(args[8:])
	default:
		_, _ = fmt.Fprintln(os.Stderr, "unexpected podman arguments:", strings.Join(args, " "))
		return 94
	}
}

func runBenchmarkMakeProducerArgs(rawArgs []string) int {
	arguments := strings.Join(rawArgs, " ")
	stage := benchmarkMakeProducerStage(arguments)
	if stage == "" {
		if _, err := fmt.Fprintln(os.Stderr, "unexpected producer arguments:", arguments); err != nil {
			return 91
		}
		return 90
	}
	output := stage + " sample"
	if stage == "version" {
		output = "go-fixture"
	}
	if _, err := fmt.Fprintln(os.Stdout, output); err != nil {
		return 91
	}
	if os.Getenv("BENCHMARK_TEST_FAILURE") == stage {
		if _, err := fmt.Fprintln(os.Stderr, "injected "+stage+" failure"); err != nil {
			return 91
		}
		return 23
	}
	return 0
}

func benchmarkMakeProducerStage(arguments string) string {
	switch arguments {
	case "go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ ./internal/bfd/",
		"go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ -timeout=120s ./internal/bfd/":
		return "bfd"
	case "go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ ./...":
		return "all"
	case "go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ -timeout=120s ./internal/netio/":
		return "netio"
	case "go env GOVERSION":
		return "version"
	case "benchstat samples/baseline.txt samples/local/benchmark.txt":
		return "compare"
	default:
		return ""
	}
}
