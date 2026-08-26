package clabbootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	writeExecutable(t, fakeBin, "podman", "#!/bin/sh\nprintf 'stdout-value'\nprintf 'stderr-value' >&2\nexit 7\n")
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
	writeExecutable(
		t,
		fakeBin,
		"podman",
		"#!/bin/sh\n/usr/bin/head -c 1048577 /dev/zero\n",
	)
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
	writeExecutable(t, fakeBin, "podman", "#!/bin/sh\nwhile :; do :; done\n")
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

	runner, err := NewOSRunner(filepath.Join(string(filepath.Separator), "repo"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("create test OS runner: %v", err)
	}
	return runner
}

func writeExecutable(t *testing.T, directory, name, contents string) {
	t.Helper()

	if strings.ContainsRune(name, filepath.Separator) {
		t.Fatalf("invalid fake executable name %q", name)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake executable %s: %v", path, err)
	}
}
