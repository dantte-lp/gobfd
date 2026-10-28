package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec" //nolint:depguard // Execute fixed Make targets with isolated producer fixtures and real tee.
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
		{"save sink failure", "benchmark-save", "", "samples/local/benchmark-bfd.txt", "samples/local/meta.json", true, true},
		{
			"compare sink failure", "benchmark-compare", "",
			"reports/benchmarks/comparison.txt", "reports/benchmarks/comparison.html", true, true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for name, data := range map[string][]byte{"Makefile": makefile, "producer": []byte(benchmarkMakeProducer)} {
				if err := os.WriteFile(filepath.Join(root, name), data, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if test.sinkFailure {
				if err := os.MkdirAll(filepath.Join(root, test.output), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "make", "--no-print-directory", test.target,
				"EXEC=./producer", "BENCH_DIR=samples", "BENCH_VERSION=local", "REPORT_DIR=reports",
				"GIT_COMMIT=fixture", "BUILD_DATE=fixture", "VERSION=local")
			command.Dir = root
			command.Env = append(os.Environ(), "BENCHMARK_TEST_FAILURE="+test.failure, "MAKEFLAGS=", "MFLAGS=")
			output, runErr := command.CombinedOutput()
			if (runErr != nil) != test.wantError || ctx.Err() != nil {
				t.Errorf("make exit = %v, context = %v, want error %t; output:\n%s", runErr, ctx.Err(), test.wantError, output)
			}
			if test.failure != "" && !strings.Contains(string(output), "injected "+test.failure+" failure") {
				t.Errorf("producer failure missing from console:\n%s", output)
			}
			if test.blockedOutput != "" {
				if _, statErr := os.Stat(filepath.Join(root, test.blockedOutput)); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("failure published %s: %v", test.blockedOutput, statErr)
				}
			}
			if !test.sinkFailure {
				data, readErr := os.ReadFile(filepath.Join(root, test.output))
				if readErr != nil || !strings.Contains(string(data), "sample") || !strings.Contains(string(output), "sample") {
					t.Errorf("raw samples lost: file = %q, error = %v, console:\n%s", data, readErr, output)
				}
			}
			if !test.wantError && test.target == "benchmark-save" {
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
			if !test.wantError && test.target == "benchmark-compare" {
				data, readErr := os.ReadFile(filepath.Join(root, "reports/benchmarks/comparison.html"))
				if readErr != nil || !strings.Contains(string(data), "compare sample") ||
					!strings.HasSuffix(string(data), "</pre></body></html>\n") {
					t.Errorf("comparison HTML = %q, error = %v", data, readErr)
				}
			}
		})
	}
}

const benchmarkMakeProducer = `#!/bin/sh
case "$*" in
  'go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ ./internal/bfd/' | \
  'go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ -timeout=120s ./internal/bfd/') stage=bfd ;;
  'go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ ./...') stage=all ;;
  'go test -buildvcs=false -bench=. -benchmem -count=6 -run=^$ -timeout=120s ./internal/netio/') stage=netio ;;
  'go env GOVERSION') stage=version ;;
  'benchstat samples/baseline.txt samples/local/benchmark.txt') stage=compare ;;
  *) printf 'unexpected producer arguments: %s\n' "$*" >&2; exit 90 ;;
esac
if [ "$stage" = version ]; then printf 'go-fixture\n'; else printf '%s sample\n' "$stage"; fi
if [ "$BENCHMARK_TEST_FAILURE" = "$stage" ]; then
  printf 'injected %s failure\n' "$stage" >&2
  exit 23
fi
`
