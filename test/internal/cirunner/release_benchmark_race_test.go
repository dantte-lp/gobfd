package cirunner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const receiveRaceFixture = `goos: linux
goarch: amd64
pkg: github.com/dantte-lp/gobfd/internal/bfd
BenchmarkManagerDemux1000Sessions-2 100000 200 ns/op 0 B/op 0 allocs/op
BenchmarkRecvDecodeLookupEnqueue-2 100000 200 ns/op 0 B/op 0 allocs/op
PASS
`

func TestReleaseBenchmarkRaceFailureStopsCollectionAndRetainsStderr(t *testing.T) {
	t.Parallel()
	root, temporary := t.TempDir(), t.TempDir()
	frozen := strings.Repeat("a", 40)
	wantErr := errors.New("race detector rejected receive fixture")
	runner := &receiveRaceFailureRunner{failure: wantErr}
	runner.afterRun = func(spec CommandSpec) {
		if output, handled := receiveFixtureGitCommand(t, spec); handled {
			if spec.Stdout != nil {
				if _, err := io.WriteString(spec.Stdout, output); err != nil {
					t.Fatal(err)
				}
			}
			return
		}
		if spec.Name == "git" && slices.Contains(spec.Args, "HEAD^{commit}") {
			if _, err := io.WriteString(spec.Stdout, frozen+"\n"); err != nil {
				t.Fatal(err)
			}
		}
		if spec.Name == "go" {
			assertReceiveOverlay(t, spec)
			if !slices.Contains(spec.Args, "-race") || !slices.Contains(spec.Args, "-benchtime=100000x") {
				t.Fatalf("unsafe baseline reached timing collection: %q", spec.Args)
			}
			if _, err := io.WriteString(spec.Stderr, "WARNING: DATA RACE\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := collectBenchmarkBase(t.Context(), BenchmarkBaseOptions{
		Root: root, RunnerTemp: temporary, Ref: frozen,
		Output: "old.txt", Regex: ".", Runner: runner,
	}, "new.txt", frozen); !errors.Is(err, wantErr) {
		t.Fatalf("race failure did not stop collection: %v", err)
	}
	got := readTestFile(t, filepath.Join(root, "reports/benchmarks/pair-baseline-race.txt"))
	if !strings.Contains(got, "WARNING: DATA RACE") {
		t.Fatalf("race stderr was not retained: %q", got)
	}
	if last := runner.calls[len(runner.calls)-1]; !slices.Contains(last.args, "remove") {
		t.Fatalf("failed preflight did not remove baseline worktree: %+v", last)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed preflight retained temporary overlay/worktree: %v: %v", entries, err)
	}
}

func TestReleaseBenchmarkRaceRequiresBothReceiveFixtures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, output string
		valid        bool
	}{
		{name: "current", output: receiveRaceFixture, valid: true},
		{name: "historical_alias", output: strings.ReplaceAll(receiveRaceFixture,
			"RecvDecodeLookupEnqueue", "FullRecvPath"), valid: true},
		{name: "empty"},
		{name: "no_records", output: "PASS\n"},
		{name: "missing_decode", output: strings.ReplaceAll(receiveRaceFixture,
			"BenchmarkRecvDecodeLookupEnqueue-2", "BenchmarkOther-2")},
		{name: "missing_manager", output: strings.ReplaceAll(receiveRaceFixture,
			"BenchmarkManagerDemux1000Sessions-2", "BenchmarkOther-2")},
		{name: "invalid_record", output: strings.ReplaceAll(receiveRaceFixture, "200 ns/op", "-1 ns/op")},
		{name: "duplicate_record", output: receiveRaceFixture + receiveRaceFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, base := t.TempDir(), t.TempDir()
			runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
				if spec.Stdout != spec.Stderr || spec.WaitDelay != benchmarkCleanupLimit ||
					!slices.Contains(spec.Args, "-race") || !slices.Contains(spec.Args, "-run=^$") {
					t.Fatalf("race preflight lost its bounded diagnostic contract: %+v", spec)
				}
				if _, err := io.WriteString(spec.Stdout, tc.output); err != nil {
					t.Fatal(err)
				}
			}}
			err := checkReleaseBenchmarkRace(t.Context(), BenchmarkBaseOptions{Root: root, Runner: runner}, base)
			if (err == nil) != tc.valid {
				t.Fatalf("preflight error=%v, valid=%t", err, tc.valid)
			}
			wantCalls := 1
			if tc.valid {
				wantCalls = 2
			}
			if len(runner.calls) != wantCalls || runner.calls[0].dir != base ||
				(tc.valid && runner.calls[1].dir != root) {
				t.Fatalf("invalid preflight execution order: %+v", runner.calls)
			}
		})
	}
}

type receiveRaceFailureRunner struct {
	recordingSpecRunner

	failure error
}

func TestReleaseBenchmarkRaceRejectsCurrentFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("current receive fixture failed")
	runner := &recordingSpecRunner{failAt: 2, err: wantErr, afterRun: func(spec CommandSpec) {
		if _, err := io.WriteString(spec.Stdout, receiveRaceFixture); err != nil {
			t.Fatal(err)
		}
	}}
	if err := checkReleaseBenchmarkRace(t.Context(), BenchmarkBaseOptions{
		Root: t.TempDir(), Runner: runner,
	}, t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("current race failure was lost: %v", err)
	}
}

func (r *receiveRaceFailureRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	if err := r.recordingSpecRunner.RunCommand(ctx, spec); err != nil {
		return err
	}
	if spec.Name == "go" {
		return r.failure
	}
	return nil
}
