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
	"time"
)

func TestBenchmarkPairCollectsTenSamplesWithBalancedOrder(t *testing.T) {
	t.Parallel()
	root, temporary := t.TempDir(), t.TempDir()
	for _, name := range []string{"old.txt", "new.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("stale\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := filepath.Join(temporary, "gobfd-benchmark-base")
	var order []string
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		if spec.Name != "go" {
			return
		}
		order = append(order, spec.Dir)
		if spec.WaitDelay <= 0 || spec.WaitDelay > 30*time.Second {
			t.Errorf("benchmark pipe cleanup has no bounded WaitDelay: %s", spec.WaitDelay)
		}
		if !slices.Contains(spec.Args, "-count=1") || !slices.Contains(spec.Args, "-benchtime=100ms") ||
			!slices.Contains(spec.Args, "-run=^$") || slices.Contains(spec.Args, "-race") {
			t.Errorf("sample arguments do not select a bounded non-race benchmark: %q", spec.Args)
		}
		fixture := requiredBenchmarkFixture
		if spec.Dir == root {
			fixture = strings.ReplaceAll(fixture, "200 ns/op", "100 ns/op")
		}
		if _, err := io.WriteString(spec.Stdout, fixture); err != nil {
			t.Fatal(err)
		}
	}}
	if err := BenchmarkPair(t.Context(), BenchmarkBaseOptions{
		Root: root, RunnerTemp: temporary, Ref: "origin/main", Output: "old.txt",
		Regex: "^BenchmarkRequired$", Runner: runner,
	}, "new.txt"); err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{
		base, root, root, base, base, root, root, base, base, root,
		root, base, base, root, root, base, base, root, root, base,
	}
	if !slices.Equal(order, wantOrder) {
		t.Fatalf("sample order=%q, want %q", order, wantOrder)
	}
	for _, tc := range []struct{ name, timing string }{{"old.txt", "200 ns/op"}, {"new.txt", "100 ns/op"}} {
		path := filepath.Join(root, tc.name)
		data := readTestFile(t, path)
		if strings.Contains(data, "stale") || strings.Count(data, "BenchmarkRecvDecodeFSM-8") != 10 ||
			strings.Count(data, tc.timing) != 30 {
			t.Errorf("%s does not contain ten complete fresh samples: %q", tc.name, data)
		}
		assertExactMode(t, path, 0o644)
	}
	if len(runner.calls) != 23 || !slices.Contains(runner.calls[22].args, "remove") {
		t.Fatalf("base worktree was not created once and cleaned after twenty samples: %#v", runner.calls)
	}
}

func TestBenchmarkPairFailureAndEmptySampleCleanUp(t *testing.T) {
	t.Parallel()
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "empty"}[empty], func(t *testing.T) {
			t.Parallel()
			wantErr := errors.New("head sample failed")
			runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
				if spec.Name == "go" && !empty {
					if _, err := io.WriteString(spec.Stdout, requiredBenchmarkFixture); err != nil {
						t.Fatal(err)
					}
				}
			}}
			if !empty {
				runner.failAt, runner.err = 4, wantErr
			}
			err := BenchmarkPair(t.Context(), BenchmarkBaseOptions{
				Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
				Regex: "Benchmark", Runner: runner,
			}, "new.txt")
			if err == nil || (!empty && !errors.Is(err, wantErr)) {
				t.Fatalf("pair error=%v, want sample failure", err)
			}
			last := runner.calls[len(runner.calls)-1]
			if last.name != "git" || !slices.Contains(last.args, "remove") || len(runner.calls) > 5 {
				t.Fatalf("failed sample did not stop and clean up: %#v", runner.calls)
			}
		})
	}
}

func TestBenchmarkPairCleanupSurvivesCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runner := &cancelingSpecRunner{cancel: cancel}
	err := BenchmarkPair(ctx, BenchmarkBaseOptions{
		Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
		Regex: "Benchmark", Runner: runner,
	}, "new.txt")
	if !errors.Is(err, context.Canceled) || runner.calls != 4 || runner.cleanupContextErr != nil {
		t.Fatalf("canceled pair did not retain its error and detached cleanup: %v, %+v", err, runner)
	}
}

func TestBenchmarkPairBoundsStdoutBeforeCommandReturns(t *testing.T) {
	t.Parallel()
	const limit = 64 << 20
	for _, tc := range []struct {
		name      string
		sizes     []int
		wantCalls int
	}{
		{name: "single-write", sizes: []int{limit + 1}, wantCalls: 1},
		{name: "across-samples", sizes: []int{limit / 2, limit / 2, 1}, wantCalls: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, temporary := t.TempDir(), t.TempDir()
			runner := &oversizedBenchmarkRunner{
				base: filepath.Join(temporary, "gobfd-benchmark-base"), sizes: tc.sizes,
				payload: make([]byte, slices.Max(tc.sizes)),
			}
			err := BenchmarkPair(t.Context(), BenchmarkBaseOptions{
				Root: root, RunnerTemp: temporary, Ref: "origin/main", Output: "old.txt",
				Regex: "Benchmark", Runner: runner,
			}, "new.txt")
			if err == nil || runner.writeErr == nil {
				t.Errorf("oversized stdout write has no streaming error: pair=%v write=%v", err, runner.writeErr)
			}
			info, statErr := os.Stat(filepath.Join(root, "old.txt"))
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Size() > limit {
				t.Errorf("stdout size=%d exceeds the 64 MiB limit", info.Size())
			}
			if runner.goCalls != tc.wantCalls || !runner.cleaned {
				t.Errorf("overflow did not stop and clean up: calls=%d cleanup=%v", runner.goCalls, runner.cleaned)
			}
		})
	}
}

type oversizedBenchmarkRunner struct {
	base      string
	sizes     []int
	payload   []byte
	goCalls   int
	baseCalls int
	writeErr  error
	cleaned   bool
}

func (r *oversizedBenchmarkRunner) RunCommand(_ context.Context, spec CommandSpec) error {
	if spec.Name != "go" {
		r.cleaned = r.cleaned || slices.Contains(spec.Args, "remove")
		return nil
	}
	r.goCalls++
	if spec.Dir == r.base {
		size := r.sizes[r.baseCalls]
		r.baseCalls++
		_, r.writeErr = spec.Stdout.Write(r.payload[:size])
		return r.writeErr
	}
	_, err := io.WriteString(spec.Stdout, requiredBenchmarkFixture)
	return err
}

func TestBenchmarkPairCleansUpPartialWorktree(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("partial worktree failure")
	runner := &partialAddSpecRunner{err: wantErr}
	err := BenchmarkPair(t.Context(), BenchmarkBaseOptions{
		Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
		Regex: "Benchmark", Runner: runner,
	}, "new.txt")
	if !errors.Is(err, wantErr) || len(runner.calls) != 3 || runner.cleanupContextErr != nil {
		t.Fatalf("partial worktree cleanup failed: %v, %+v", err, runner)
	}
}

func TestBenchmarkPairRequiresTenValidRecordsPerPackageAndName(t *testing.T) {
	t.Parallel()
	const row = "BenchmarkShared-2 1 10 ns/op 0 B/op 0 allocs/op\n"
	ten := "pkg: example.com/a\n" + strings.Repeat(row, 10)
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{name: "ten", data: ten, valid: true},
		{name: "nine", data: "pkg: example.com/a\n" + strings.Repeat(row, 9)},
		{name: "eleven", data: ten + row},
		{name: "malformed", data: strings.Replace(ten, "10 ns/op", "NaN ns/op", 1)},
		{name: "no-records", data: "PASS\n"},
		{name: "missing-package", data: strings.Repeat(row, 10)},
		{name: "same-name-in-two-packages", data: ten + "pkg: example.com/b\n" + strings.Repeat(row, 10), valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "samples.txt")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := validateBenchmarkPairSamples(path); (err == nil) != tc.valid {
				t.Fatalf("sample validation=%v, want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestBenchmarkPairRejectsAliasedAndUnsafeOutputsBeforeTruncation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"same", "hardlink", "symlink", "escape", "empty"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			oldPath := filepath.Join(root, "old.txt")
			if err := os.WriteFile(oldPath, []byte("retain\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			newName := "new.txt"
			switch kind {
			case "same":
				newName = "old.txt"
			case "hardlink":
				if err := os.Link(oldPath, filepath.Join(root, newName)); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(oldPath, filepath.Join(root, newName)); err != nil {
					t.Fatal(err)
				}
			case "escape":
				newName = "../new.txt"
			case "empty":
				newName = ""
			}
			runner := &recordingSpecRunner{}
			if err := BenchmarkPair(t.Context(), BenchmarkBaseOptions{
				Root: root, RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
				Regex: "Benchmark", Runner: runner,
			}, newName); err == nil {
				t.Fatal("unsafe output was accepted")
			}
			if readTestFile(t, oldPath) != "retain\n" || len(runner.calls) != 0 {
				t.Fatal("unsafe output truncated the existing input or ran a command")
			}
		})
	}
}
