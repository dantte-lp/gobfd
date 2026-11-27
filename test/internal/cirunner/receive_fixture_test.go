package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const receiveSourceFixture = `package fixture
func BenchmarkRecvDecodeLookupEnqueue(b *testing.B) {
	var pkt bfd.ControlPacket

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		consume(&pkt)
	}
}
`

func receiveFixtureGitCommand(t *testing.T, spec CommandSpec) (string, bool) {
	t.Helper()
	if spec.Name != "git" {
		return "", false
	}
	switch {
	case slices.Contains(spec.Args, "add"):
		base := spec.Args[len(spec.Args)-2]
		for _, directory := range []string{spec.Dir, base} {
			path := filepath.Join(directory, "internal/bfd/bench_test.go")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(receiveSourceFixture), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	case slices.Contains(spec.Args, "show"):
		return receiveSourceFixture, true
	case slices.Contains(spec.Args, "remove"):
		if err := os.RemoveAll(spec.Args[len(spec.Args)-1]); err != nil {
			t.Fatal(err)
		}
	default:
		return "", false
	}
	return "", true
}

func assertReceiveOverlay(t *testing.T, spec CommandSpec) {
	t.Helper()
	if len(spec.Args) < 2 || !strings.HasPrefix(spec.Args[1], "-overlay=") {
		t.Fatalf("release test command omitted its ownership overlay: %+v", spec)
	}
	var overlay map[string]map[string]string
	data, err := os.ReadFile(strings.TrimPrefix(spec.Args[1], "-overlay="))
	if err != nil || json.Unmarshal(data, &overlay) != nil || len(overlay["Replace"]) != 2 {
		t.Fatalf("missing symmetric receive overlay: %s: %v", data, err)
	}
	for original, replacement := range overlay["Replace"] {
		if readTestFile(t, original) != receiveSourceFixture ||
			!strings.Contains(readTestFile(t, replacement), "for b.Loop() {\n\t\tvar pkt") {
			t.Fatal("overlay changed original source or omitted the owned decode fixture")
		}
	}
}

func TestReceiveFixtureOwnsEachDecodedPacket(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"BenchmarkRecvDecodeLookupEnqueue", "BenchmarkFullRecvPath"} {
		source := []byte(strings.ReplaceAll(receiveSourceFixture, "BenchmarkRecvDecodeLookupEnqueue", name))
		original := bytes.Clone(source)
		got, err := ownedReceiveFixture(source)
		if err != nil || !bytes.Contains(got, []byte("for b.Loop() {\n\t\tvar pkt bfd.ControlPacket\n")) ||
			!bytes.Equal(source, original) {
			t.Fatalf("fixture correction lost per-iteration ownership or modified input: %s: %v", got, err)
		}
		if _, err := ownedReceiveFixture(got); err == nil {
			t.Fatal("unknown/already changed fixture was silently accepted")
		}
	}
}

func TestReceiveFixtureRejectsUnknownShape(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"package fixture\n",
		strings.ReplaceAll(receiveSourceFixture, "BenchmarkRecvDecodeLookupEnqueue", "BenchmarkOther"),
		strings.ReplaceAll(receiveSourceFixture, "var pkt", "var other"),
		receiveSourceFixture + strings.TrimPrefix(receiveSourceFixture, "package fixture\n"),
		"invalid source",
	} {
		if _, err := ownedReceiveFixture([]byte(source)); err == nil {
			t.Fatalf("unrecognized fixture accepted: %q", source)
		}
	}
}

func TestReceiveFixtureRejectsUncommittedSourceAndCleansWorktree(t *testing.T) {
	t.Parallel()
	root, temporary := t.TempDir(), t.TempDir()
	frozen := strings.Repeat("a", 40)
	runner := &recordingSpecRunner{afterRun: func(spec CommandSpec) {
		output, handled := receiveFixtureGitCommand(t, spec)
		if slices.Contains(spec.Args, "show") {
			output += "// committed blob differs\n"
		}
		if !handled && spec.Name == "git" {
			output = frozen + "\n"
		}
		if spec.Name == "go" {
			t.Fatal("uncommitted receive source reached benchmark execution")
		}
		if spec.Stdout != nil {
			if _, err := io.WriteString(spec.Stdout, output); err != nil {
				t.Fatal(err)
			}
		}
	}}
	err := collectBenchmarkBase(t.Context(), BenchmarkBaseOptions{
		Root: root, RunnerTemp: temporary, Ref: frozen, Output: "old.txt", Regex: ".", Runner: runner,
	}, "new.txt", frozen)
	if !errors.Is(err, errInvalidConfig) {
		t.Fatalf("dirty fixture was accepted: %v", err)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("source rejection retained temporary worktree/overlay: %v: %v", entries, err)
	}
}

func TestReceiveFixtureRunnerPreservesOtherCommands(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("command failed")
	args := []string{"test", "-race", "./internal/bfd/"}
	runner := &recordingSpecRunner{failAt: 2, err: wantErr, afterRun: func(spec CommandSpec) {
		if !slices.Equal(spec.Args, []string{"test", "-overlay=/fixture/map.json", "-race", "./internal/bfd/"}) {
			t.Fatalf("test overlay missing: %q", spec.Args)
		}
	}}
	wrapped := receiveFixtureRunner{runner: runner, overlay: "/fixture/map.json"}
	if err := wrapped.RunCommand(t.Context(), CommandSpec{Name: "go", Args: args}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"test", "-race", "./internal/bfd/"}) {
		t.Fatal("overlay wrapper mutated caller arguments")
	}
	if err := wrapped.RunCommand(context.Background(), CommandSpec{Name: "git", Args: args}); !errors.Is(err, wantErr) ||
		!slices.Equal(runner.calls[1].args, args) {
		t.Fatalf("non-test command or error changed: %v", err)
	}
}
