package cirunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func benchmarkGateCSV(name, unit, base, head, delta string) string {
	return fmt.Sprintf(",old.txt,,new.txt,,,\n,%s,CI,%s,CI,vs base,P\n%s,%s,1%%,%s,1%%,%s,p=0.001 n=6\n",
		unit, unit, name, base, head, delta)
}

func benchmarkGateFixture(t *testing.T, csv string) (BenchmarkReportOptions, *recordingSpecRunner, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	input := []byte(strings.Repeat(requiredBenchmarkFixture, 6))
	for _, name := range []string{"old.txt", "new.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), input, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	summary := filepath.Join(t.TempDir(), "summary")
	if err := os.WriteFile(summary, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		text := "benchmark comparison\n"
		if containsArgument(spec.Args, "-format") {
			text = csv
		}
		if _, err := io.WriteString(spec.Stdout, text); err != nil {
			t.Fatal(err)
		}
		if containsArgument(spec.Args, "-format") {
			if _, err := io.WriteString(spec.Stderr, "fixture note\n"); err != nil {
				t.Fatal(err)
			}
		}
	}}
	warning := new(bytes.Buffer)
	return BenchmarkReportOptions{
		Root: root, Old: "old.txt", New: "new.txt", Text: "bench.txt", Markdown: "bench.md",
		HTML: "bench.html", JSON: "bench.json", CSV: "bench-regression/bench.csv",
		Notes: "bench-regression/notes.txt", StepSummary: summary, Warning: warning, Runner: runner,
	}, runner, warning
}

func requireBenchmarkGateArtifacts(t *testing.T, options BenchmarkReportOptions) {
	t.Helper()
	for _, name := range []string{options.Text, options.Markdown, options.HTML, options.JSON, options.CSV, options.Notes} {
		if got := readTestFile(t, filepath.Join(options.Root, name)); got == "" {
			t.Errorf("empty retained artifact %s", name)
		}
	}
}

func TestBenchmarkCriticalGatePreservesClassifierBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, benchmark, unit, base, head, delta string
		critical                                 bool
	}{
		{"unchanged", "RecvDecodeFSM-8", "sec/op", "2e-7", "2e-7", "~", false},
		{"improved", "RecvDecodeFSM-8", "sec/op", "2e-7", "1.6e-7", "-20.00%", false},
		{"noisy", "RecvDecodeFSM-8", "sec/op", "2e-7", "3e-7", "~", false},
		{"below_percent", "RecvDecodeFSM-8", "sec/op", "2e-7", "2.1998e-7", "+9.99%", false},
		{"below_floor", "RecvDecodeFSM-8", "sec/op", "8e-8", "8.8e-8", "+10.00%", false},
		{"noncritical_name", "RecvDecodeFSMExtra-8", "sec/op", "2e-7", "2.4e-7", "+20.00%", false},
		{"non_time", "RecvDecodeFSM-8", "allocs/op", "1", "2", "+100.00%", false},
		{"equal_percent_and_base_floor", "RecvDecodeFSM-8", "sec/op", "1e-7", "1.1e-7", "+10.00%", true},
		{"head_reaches_floor", "RecvDecodeFSM-8", "sec/op", "9e-8", "1e-7", "+11.11%", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			csv := benchmarkGateCSV(test.benchmark, test.unit, test.base, test.head, test.delta)
			options, _, _ := benchmarkGateFixture(t, csv)
			err := BenchmarkReport(t.Context(), options)
			if (err != nil) != test.critical {
				t.Errorf("critical=%t, error=%v", test.critical, err)
			}
			requireBenchmarkGateArtifacts(t, options)
		})
	}
}

func TestBenchmarkCriticalGateDoesNotDependOnWarningOrSummary(t *testing.T) {
	t.Parallel()
	options, _, _ := benchmarkGateFixture(t, benchmarkGateCSV("RecvDecodeFSM-8", "sec/op", "2e-7", "2.4e-7", "+20.00%"))
	options.Warning, options.SkipStepSummary, options.StepSummary = nil, true, ""
	if err := BenchmarkReport(t.Context(), options); err == nil {
		t.Error("critical report with nil warning and skipped summary succeeded")
	}
	requireBenchmarkGateArtifacts(t, options)
}

func TestBenchmarkCriticalGateAcceptsExistingInputBudgets(t *testing.T) {
	t.Parallel()
	for _, samples := range []int{6, 10, (2<<20)/len(requiredBenchmarkFixture) + 1} {
		t.Run(strconv.Itoa(samples), func(t *testing.T) {
			t.Parallel()
			options, _, _ := benchmarkGateFixture(t, benchmarkGateCSV("RecvDecodeFSM-8", "sec/op", "2e-7", "2e-7", "~"))
			data := []byte(strings.Repeat(requiredBenchmarkFixture, samples))
			for _, name := range []string{options.Old, options.New} {
				if err := os.WriteFile(filepath.Join(options.Root, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := BenchmarkReport(t.Context(), options); err != nil {
				t.Fatalf("valid raw input %d bytes rejected: %v", len(data), err)
			}
		})
	}
}

func TestBenchmarkCriticalGateRejectsInvalidRawBeforeBenchstat(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{
		"", " \n", "goos: linux\nPASS\n", requiredBenchmarkFixture + "not-a-benchmark-record\n",
		"BenchmarkBroken-8 one 200 ns/op\n", "BenchmarkBroken-8 0 200 ns/op\n",
		"BenchmarkBroken-8 1 -1 ns/op\n", "BenchmarkBroken-8 1 NaN ns/op\n",
		"BenchmarkBroken-8 1 200 ns/op 201 ns/op\n",
	} {
		for _, side := range []string{"old.txt", "new.txt"} {
			t.Run(fmt.Sprintf("%s/%x", side, invalid), func(t *testing.T) {
				t.Parallel()
				options, runner, _ := benchmarkGateFixture(t, benchmarkGateCSV("RecvDecodeFSM-8", "sec/op", "2e-7", "2e-7", "~"))
				if err := os.WriteFile(filepath.Join(options.Root, side), []byte(invalid), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := BenchmarkReport(t.Context(), options); err == nil {
					t.Error("invalid raw input succeeded")
				}
				if len(runner.calls) != 0 {
					t.Errorf("benchstat ran %d commands before raw validation", len(runner.calls))
				}
			})
		}
	}
}

type benchmarkGateRunnerFunc func(context.Context, CommandSpec) error

func (f benchmarkGateRunnerFunc) RunCommand(ctx context.Context, spec CommandSpec) error {
	return f(ctx, spec)
}

func TestBenchmarkCriticalGatePreservesFirstTextError(t *testing.T) {
	t.Parallel()
	for _, partial := range []string{"", "partial comparison\n"} {
		t.Run(fmt.Sprintf("partial_%t", partial != ""), func(t *testing.T) {
			t.Parallel()
			csv := benchmarkGateCSV("RecvDecodeFSM-8", "sec/op", "2e-7", "2e-7", "~")
			options, _, _ := benchmarkGateFixture(t, csv)
			want := errors.New("text command failed")
			calls := 0
			options.Runner = benchmarkGateRunnerFunc(func(_ context.Context, spec CommandSpec) error {
				calls++
				if containsArgument(spec.Args, "-format") {
					_, err := io.WriteString(spec.Stdout, csv)
					return err
				}
				_, err := io.WriteString(spec.Stdout, partial)
				return errors.Join(want, err)
			})
			if err := BenchmarkReport(t.Context(), options); !errors.Is(err, want) {
				t.Errorf("error=%v, want original text error", err)
			}
			if calls != 1 {
				t.Errorf("executed %d commands after text failure", calls)
			}
		})
	}
}

func TestBenchmarkCriticalGatePreservesWarningWriteErrors(t *testing.T) {
	t.Parallel()
	want := errors.New("warning writer failed")
	for _, test := range []struct {
		name            string
		at              int
		cause, expected error
	}{
		{"header", 1, want, want},
		{"row", 2, want, want},
		{"short_header", 1, nil, io.ErrShortWrite},
		{"short_row", 2, nil, io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options, _, _ := benchmarkGateFixture(t, benchmarkGateCSV("RecvDecodeFSM-8", "sec/op", "2e-7", "2.4e-7", "+20.00%"))
			options.Warning = &failOnWrite{failAt: test.at, err: test.cause}
			if err := BenchmarkReport(t.Context(), options); !errors.Is(err, test.expected) {
				t.Errorf("error=%v, want %v", err, test.expected)
			}
			requireBenchmarkGateArtifacts(t, options)
		})
	}
}

func TestBenchmarkCriticalGateSummaryFailureStillWritesWarnings(t *testing.T) {
	t.Parallel()
	csv := benchmarkGateCSV("RecvDecodeFSM-8", "sec/op", "2e-7", "2.4e-7", "+20.00%")
	options, runner, warning := benchmarkGateFixture(t, csv)
	original := runner.afterRun
	runner.afterRun = func(spec CommandSpec) {
		original(spec)
		if containsArgument(spec.Args, "-format") {
			if err := os.Remove(options.StepSummary); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(options.StepSummary, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	var pathError *os.PathError
	if err := BenchmarkReport(t.Context(), options); !errors.As(err, &pathError) {
		t.Errorf("error=%v, want summary IO error", err)
	}
	if !strings.Contains(warning.String(), "RecvDecodeFSM-8,+20.00%") {
		t.Error("summary failure suppressed critical warning")
	}
	requireBenchmarkGateArtifacts(t, options)
}

func TestBenchmarkCriticalGateArtifactFailurePreservesEarlierReports(t *testing.T) {
	t.Parallel()
	csv := benchmarkGateCSV("RecvDecodeFSM-8", "sec/op", "2e-7", "2.4e-7", "+20.00%")
	options, runner, _ := benchmarkGateFixture(t, csv)
	original := runner.afterRun
	runner.afterRun = func(spec CommandSpec) {
		original(spec)
		if containsArgument(spec.Args, "-format") {
			path := filepath.Join(options.Root, options.JSON)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	var pathError *os.PathError
	var linkError *os.LinkError
	err := BenchmarkReport(t.Context(), options)
	if !errors.Is(err, errInvalidConfig) && !errors.As(err, &pathError) && !errors.As(err, &linkError) {
		t.Errorf("error=%v, want artifact IO error", err)
	}
	for _, name := range []string{options.Markdown, options.HTML} {
		if readTestFile(t, filepath.Join(options.Root, name)) == "" {
			t.Errorf("earlier artifact %s lost", name)
		}
	}
}
