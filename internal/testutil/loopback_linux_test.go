//go:build linux

package testutil

import (
	"errors"
	"os"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLoopbackBFDLockSerializesProcesses(t *testing.T) {
	const busyExit = 42
	if slices.Contains(os.Args, "gobfd-loopback-lock-probe") {
		namespace, err := os.Open("/proc/self/ns/net")
		if err != nil {
			t.Fatal(err)
		}
		operation := unix.LOCK_EX
		if slices.Contains(os.Args, "nonblocking") {
			operation |= unix.LOCK_NB
		}
		lockErr := unix.Flock(int(namespace.Fd()), operation)
		if closeErr := namespace.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if errors.Is(lockErr, unix.EWOULDBLOCK) {
			os.Exit(busyExit)
		}
		if lockErr != nil {
			t.Fatal(lockErr)
		}
		return
	}
	probe := func(t *testing.T, want int) {
		t.Helper()
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		arguments := []string{
			executable, "-test.run=^TestLoopbackBFDLockSerializesProcesses$", "-test.timeout=30s",
			"--", "gobfd-loopback-lock-probe",
		}
		if want == busyExit {
			arguments = append(arguments, "nonblocking")
		}
		child, err := os.StartProcess(executable, arguments, &os.ProcAttr{Files: []*os.File{nil, os.Stdout, os.Stderr}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if killErr := child.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				t.Error(killErr)
			}
		})
		state, err := child.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if state.ExitCode() != want {
			t.Fatalf("competing process exit = %d, want %d", state.ExitCode(), want)
		}
	}
	t.Run("held", func(t *testing.T) {
		LockLoopbackBFD(t)
		probe(t, busyExit)
	})
	t.Run("released", func(t *testing.T) { probe(t, 0) })
}
