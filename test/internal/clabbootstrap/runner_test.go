package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const clabFakeMode = "GOBFD_CLABBOOTSTRAP_FAKE_MODE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(clabFakeMode); mode != "" {
		os.Exit(runClabFakeCommand(mode))
	}
	os.Exit(m.Run())
}

func TestOSRunnerRejectsNonAllowlistedExecutable(t *testing.T) {
	t.Parallel()

	runner := newTestOSRunner(t)
	_, err := runner.Run(t.Context(), Command{Executable: "sh"})
	if !errors.Is(err, errCommandNotAllowed) {
		t.Fatalf("runner error = %v, want errors.Is(errCommandNotAllowed)", err)
	}
}

func TestOSRunnerPreservesStreamsAndExitStatus(t *testing.T) {
	fakeBin := t.TempDir()
	installFakeCommand(t, fakeBin, "podman", "streams")
	t.Setenv("PATH", fakeBin)
	runner := newTestOSRunner(t)

	result, err := runner.Run(t.Context(), Command{Executable: executablePodman})
	if err != nil {
		t.Fatalf("run fake podman: %v", err)
	}
	if result.ExitCode != 7 || result.Stdout != "stdout-value" || result.Stderr != "stderr-value" {
		t.Fatalf("runner result = %#v", result)
	}
}

func TestOSRunnerRejectsOversizedOutput(t *testing.T) {
	fakeBin := t.TempDir()
	installFakeCommand(t, fakeBin, "podman", "oversized")
	t.Setenv("PATH", fakeBin)
	runner := newTestOSRunner(t)

	result, err := runner.Run(t.Context(), Command{Executable: executablePodman})
	if !errors.Is(err, errCommandOutputSize) {
		t.Fatalf("runner error = %v, want errors.Is(errCommandOutputSize)", err)
	}
	if len(result.Stdout) != maxCommandOutput {
		t.Fatalf("bounded stdout length = %d, want %d", len(result.Stdout), maxCommandOutput)
	}
}

func TestOSRunnerPreservesContextCancellation(t *testing.T) {
	fakeBin := t.TempDir()
	installFakeCommand(t, fakeBin, "podman", "block")
	t.Setenv("PATH", fakeBin)
	runner := newTestOSRunner(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := runner.Run(ctx, Command{Executable: executablePodman})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runner error = %v, want errors.Is(context.DeadlineExceeded)", err)
	}
}

func newTestOSRunner(t *testing.T) *OSRunner {
	t.Helper()

	runner, err := NewOSRunner(
		filepath.Join(string(filepath.Separator), "repo"),
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("create test OS runner: %v", err)
	}
	return runner
}

func installFakeCommand(t *testing.T, directory, name, mode string) {
	t.Helper()

	if strings.ContainsRune(name, filepath.Separator) {
		t.Fatalf("invalid fake executable name %q", name)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	if err := os.Symlink(executable, filepath.Join(directory, name)); err != nil {
		t.Fatalf("install fake executable %s: %v", name, err)
	}
	if mode == "" {
		t.Fatal("fake command mode is empty")
	}
	t.Setenv(clabFakeMode, mode)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
}

func runClabFakeCommand(mode string) int {
	write := func(stream *os.File, value []byte) int {
		if _, err := stream.Write(value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 125
		}
		return 0
	}
	switch mode {
	case "streams":
		if code := write(os.Stdout, []byte("stdout-value")); code != 0 {
			return code
		}
		if code := write(os.Stderr, []byte("stderr-value")); code != 0 {
			return code
		}
		return 7
	case "oversized":
		return write(os.Stdout, make([]byte, maxCommandOutput+1))
	case "block":
		for {
			time.Sleep(time.Hour)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown clab bootstrap fake mode %q\n", mode)
		return 125
	}
}
