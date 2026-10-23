//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

type failingOutput struct{ err error }

func (w failingOutput) Write([]byte) (int, error) { return 0, w.err }

func TestEnvironmentCommand(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := run(t.Context(), []string{"environment"}, &output); err != nil {
		t.Fatal(err)
	}
	var report bfdload.Environment
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Process.RSSBytes == 0 || report.Runtime.Version == "" || report.ElapsedNS <= 0 {
		t.Fatalf("command produced no measured environment: %+v", report)
	}
	for _, args := range [][]string{nil, {"unknown"}, {"environment", "extra"}} {
		if err := run(t.Context(), args, &output); err == nil {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
	writeErr := errors.New("output unavailable")
	if err := run(t.Context(), []string{"environment"}, failingOutput{writeErr}); !errors.Is(err, writeErr) {
		t.Fatalf("lost output failure: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, []string{"environment"}, &output); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestEnvironmentSignalHelper(_ *testing.T) {
	if os.Args[len(os.Args)-1] != "bfdload-signal-helper" {
		return
	}
	os.Args = []string{os.Args[0], "environment"}
	main()
}

func TestEnvironmentCommandSignalWithBlockedOutput(t *testing.T) {
	t.Parallel()
	const pipeCapacity = 4096
	var output bytes.Buffer
	if err := run(t.Context(), []string{"environment"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := errors.Join(reader.Close(), writer.Close()); closeErr != nil {
					t.Error(closeErr)
				}
			})
			capacity, err := unix.FcntlInt(writer.Fd(), unix.F_SETPIPE_SZ, pipeCapacity)
			if err != nil {
				t.Fatal(err)
			}
			if capacity >= output.Len() {
				t.Skipf("snapshot %d bytes does not exceed pipe capacity %d", output.Len(), capacity)
			}
			t.Logf("snapshot_bytes=%d pipe_capacity=%d", output.Len(), capacity)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child, err := os.StartProcess(executable,
				[]string{executable, "-test.run=^TestEnvironmentSignalHelper$", "bfdload-signal-helper"},
				&os.ProcAttr{Files: []*os.File{os.Stdin, writer, os.Stderr}})
			if err != nil {
				t.Fatal(err)
			}
			var state *os.ProcessState
			var waitErr error
			done := make(chan struct{})
			go func() {
				state, waitErr = child.Wait()
				close(done)
			}()
			t.Cleanup(func() {
				select {
				case <-done:
				default:
					if killErr := child.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
						t.Error(killErr)
					}
					<-done
				}
				if waitErr != nil {
					t.Error(waitErr)
				}
			})
			if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var prefix [1]byte
			if _, err := io.ReadFull(reader, prefix[:]); err != nil {
				t.Fatal(err)
			}
			if prefix[0] != '{' {
				t.Fatalf("child did not start its JSON report: %q", prefix)
			}
			if err := child.Signal(sig); err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
				if waitErr != nil {
					t.Fatal(waitErr)
				}
				status, ok := state.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != sig {
					t.Fatalf("child status = %v, want termination by %s", state, sig)
				}
			case <-timer.C:
				t.Fatalf("%s did not terminate the command while stdout was blocked", sig)
			}
		})
	}
}
