package cirunner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCoverageResetsFixedArtifactsBeforeCommandFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifacts := []string{"unit-report.xml", "unit-report.json", "coverage.out", "unit-report.html"}
	for _, name := range artifacts {
		if err := os.WriteFile(filepath.Join(root, name), []byte("stale\n"), 0o600); err != nil {
			t.Fatalf("seed stale %s: %v", name, err)
		}
	}
	wantErr := errors.New("tests failed")
	err := TestCoverage(context.Background(), root, &recordingSpecRunner{failAt: 1, err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("TestCoverage() error = %v, want wrapped command failure", err)
	}
	for _, name := range artifacts {
		path := filepath.Join(root, name)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read reset %s: %v", name, readErr)
		}
		if len(data) != 0 {
			t.Errorf("%s retains stale content %q", name, data)
		}
		assertExactMode(t, path, 0o644)
	}
}

func TestCoverageRejectsNonregularArtifact(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "target"), filepath.Join(root, "unit-report.xml")); err != nil {
		t.Fatalf("create report symlink: %v", err)
	}
	runner := &recordingSpecRunner{}
	if err := TestCoverage(context.Background(), root, runner); err == nil {
		t.Fatal("TestCoverage() error = nil, want nonregular artifact rejection")
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner received %d calls for a nonregular artifact", len(runner.calls))
	}
}

func TestBufFetchBaseUsesExactFullRefspec(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := "+refs/heads/topic,#"
	runner := &recordingSpecRunner{}
	if err := BufFetchBase(context.Background(), root, base, runner); err != nil {
		t.Fatalf("BufFetchBase() error = %v", err)
	}
	want := []specInvocation{{
		name: "git",
		args: []string{
			"fetch", "origin",
			"+refs/heads/" + base + ":refs/remotes/origin/" + base,
		},
		dir: root,
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Buf fetch calls = %#v, want %#v", runner.calls, want)
	}
}

func TestBufBreakingResolvesBaseToCommit(t *testing.T) {
	t.Parallel()

	for _, sha := range []string{strings.Repeat("a", 40), strings.Repeat("b", 64)} {
		t.Run(strconv.Itoa(len(sha)), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			base := "+refs/heads/topic,#"
			runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
				if spec.Name == "git" {
					if spec.Stdout == nil {
						t.Fatal("git rev-parse stdout is not captured")
					}
					if _, err := io.WriteString(spec.Stdout, sha+"\n"); err != nil {
						t.Fatalf("write simulated commit ID: %v", err)
					}
				}
			}}
			if err := BufBreaking(context.Background(), root, base, runner); err != nil {
				t.Fatalf("BufBreaking() error = %v", err)
			}
			want := []specInvocation{
				{
					name: "git",
					args: []string{"rev-parse", "--verify", "refs/remotes/origin/" + base + "^{commit}"},
					dir:  root,
				},
				{
					name: "buf",
					args: []string{"breaking", "--against", ".git#commit=" + sha},
					dir:  root,
				},
			}
			if !reflect.DeepEqual(runner.calls, want) {
				t.Errorf("Buf breaking calls = %#v, want %#v", runner.calls, want)
			}
		})
	}
}

func TestBufBreakingRejectsInvalidResolvedCommit(t *testing.T) {
	t.Parallel()

	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		if spec.Stdout != nil {
			if _, err := io.WriteString(spec.Stdout, strings.Repeat("a", 40)+"\nsecond\n"); err != nil {
				t.Fatalf("write invalid simulated commit ID: %v", err)
			}
		}
	}}
	err := BufBreaking(context.Background(), t.TempDir(), "main", runner)
	if err == nil {
		t.Fatal("BufBreaking() error = nil, want multi-line commit rejection")
	}
	if len(runner.calls) != 1 {
		t.Errorf("runner calls = %d, want only git rev-parse", len(runner.calls))
	}
}
