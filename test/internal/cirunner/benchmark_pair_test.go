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
	"testing/synctest"
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

func TestBenchmarkPairBoundsAndCancelsEverySample(t *testing.T) {
	t.Parallel()
	parent, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	var samples []context.Context
	runner := benchmarkGateRunnerFunc(func(ctx context.Context, spec CommandSpec) error {
		if len(samples) > 0 && !errors.Is(samples[len(samples)-1].Err(), context.Canceled) {
			t.Error("previous sample context was not canceled before the next command")
		}
		if spec.Name != "go" {
			return nil
		}
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) > 120*time.Second || ctx.Err() != nil {
			t.Errorf("sample context lacks its own active 120-second bound: deadline=%v bounded=%t", deadline, bounded)
		}
		samples = append(samples, ctx)
		if _, err := io.WriteString(spec.Stdout, requiredBenchmarkFixture); err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if err := BenchmarkPair(parent, BenchmarkBaseOptions{
		Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
		Regex: ".", Runner: runner,
	}, "new.txt"); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 20 {
		t.Fatalf("bounded sample contexts = %d, want 20", len(samples))
	}
	for index, sample := range samples {
		if !errors.Is(sample.Err(), context.Canceled) {
			t.Errorf("sample %d context was not canceled after command completion", index)
		}
	}
	if parent.Err() != nil {
		t.Fatalf("sample cancellation canceled the parent: %v", parent.Err())
	}
}

func TestBenchmarkPairDeadlineStopsSamplesAndCleansUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var samples int
		var cleaned bool
		runner := benchmarkGateRunnerFunc(func(ctx context.Context, spec CommandSpec) error {
			if spec.Name == "go" {
				samples++
				<-ctx.Done()
				return ctx.Err()
			}
			if slices.Contains(spec.Args, "remove") {
				deadline, bounded := ctx.Deadline()
				if ctx.Err() != nil || !bounded || !deadline.After(time.Now()) ||
					time.Until(deadline) > 30*time.Second {
					t.Error("expired sample did not receive live bounded cleanup context")
				}
				cleaned = true
			}
			return nil
		})
		err := BenchmarkPair(t.Context(), BenchmarkBaseOptions{
			Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: "origin/main", Output: "old.txt",
			Regex: ".", Runner: runner,
		}, "new.txt")
		if !errors.Is(err, context.DeadlineExceeded) || samples != 1 || !cleaned {
			t.Fatalf("expired sample = %v, attempts=%d cleanup=%t; want DeadlineExceeded, 1, true", err, samples, cleaned)
		}
	})
}

func TestReleaseBenchmarkPairRejectsWrongFrozenSource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, ref, frozen string
		cleanup           bool
	}{
		{name: "malformed", ref: "invalid", frozen: "invalid"},
		{name: "different_ref", ref: strings.Repeat("b", 40), frozen: strings.Repeat("a", 40)},
		{name: "wrong_checkout", ref: strings.Repeat("a", 40), frozen: strings.Repeat("a", 40), cleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
				if spec.Name == "go" {
					t.Error("unverified frozen source executed a benchmark")
				}
				if spec.Stdout != nil {
					if _, err := io.WriteString(spec.Stdout, strings.Repeat("b", 40)+"\n"); err != nil {
						t.Fatal(err)
					}
				}
			}}
			if err := collectBenchmarkBase(t.Context(), BenchmarkBaseOptions{
				Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: tc.ref,
				Output: "old.txt", Regex: ".", Runner: runner,
			}, "new.txt", tc.frozen); err == nil {
				t.Fatal("invalid frozen source was accepted")
			}
			if tc.cleanup {
				if len(runner.calls) != 3 || !slices.Contains(runner.calls[2].args, "remove") {
					t.Fatalf("unverified worktree was not cleaned: %+v", runner.calls)
				}
			} else if len(runner.calls) != 0 {
				t.Fatalf("invalid frozen ref executed commands: %+v", runner.calls)
			}
		})
	}
}

func TestReleaseBenchmarkPairRejectsDriftAfterCollection(t *testing.T) {
	t.Parallel()
	frozen := strings.Repeat("a", 40)
	var samples, checks int
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		var output string
		switch {
		case spec.Name == "git" && slices.Contains(spec.Args, "show"):
			output = receiveSourceFixture
		case spec.Name == "git" && slices.Contains(spec.Args, "add"):
			receiveFixtureGitCommand(t, spec)
		case spec.Name == "go" && slices.Contains(spec.Args, "-race"):
			assertReceiveOverlay(t, spec)
			output = receiveRaceFixture
		case spec.Name == "go":
			assertReceiveOverlay(t, spec)
			samples++
			output = requiredBenchmarkFixture
		case spec.Name == "git" && slices.Contains(spec.Args, "HEAD^{commit}"):
			checks++
			output = frozen + "\n"
			if samples == 20 {
				output = strings.Repeat("b", 40) + "\n"
			}
		}
		if spec.Stdout != nil {
			if _, err := io.WriteString(spec.Stdout, output); err != nil {
				t.Fatal(err)
			}
		}
	}}
	err := collectBenchmarkBase(t.Context(), BenchmarkBaseOptions{
		Root: t.TempDir(), RunnerTemp: t.TempDir(), Ref: frozen,
		Output: "old.txt", Regex: ".", Runner: runner,
	}, "new.txt", frozen)
	if !errors.Is(err, errInvalidConfig) || samples != 20 || checks != 2 {
		t.Fatalf("post-collection source drift accepted: samples=%d checks=%d error=%v", samples, checks, err)
	}
	if len(runner.calls) != 28 || !slices.Contains(runner.calls[27].args, "remove") {
		t.Fatalf("source drift did not clean its worktree: %+v", runner.calls)
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
