package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type qualificationChild struct {
	process *os.Process
	done    chan struct{}
	state   *os.ProcessState
	err     error
	hash    string
}

func startQualificationChild(t *testing.T, binary, dir string, args ...string) *qualificationChild {
	t.Helper()
	if !filepath.IsAbs(binary) {
		t.Fatal("qualification requires an absolute prebuilt binary path")
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, file)
	if readErr := errors.Join(hashErr, file.Close()); readErr != nil {
		t.Fatal(readErr)
	}
	output, err := os.OpenFile(filepath.Join(dir, filepath.Base(binary)+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	child := &qualificationChild{done: make(chan struct{}), hash: hex.EncodeToString(hash.Sum(nil))}
	child.process, err = os.StartProcess(binary, append([]string{binary}, args...), &os.ProcAttr{
		Dir: dir, Env: append(os.Environ(), "STEPPATH="+filepath.Join(dir, "step")),
		Files: []*os.File{nil, output, output},
	})
	closeErr := output.Close()
	if err != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	go func() {
		child.state, child.err = child.process.Wait()
		close(child.done)
	}()
	t.Cleanup(func() { child.stop(t) })
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return child
}

func (c *qualificationChild) stop(t *testing.T) {
	t.Helper()
	select {
	case <-c.done:
	default:
		if err := c.process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		select {
		case <-c.done:
		case <-time.After(10 * time.Second):
			t.Error("qualification child did not join after SIGTERM")
			if err := c.process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			select {
			case <-c.done:
			case <-time.After(5 * time.Second):
				t.Fatal("qualification child did not join after SIGKILL")
			}
		}
	}
	if c.err != nil || c.state == nil || !c.state.Success() {
		t.Errorf("qualification child exit: %v %v", c.state, c.err)
	}
}

func awaitQualificationReady(t *testing.T, child *qualificationChild, probe func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var lastErr error
	for {
		probeCtx, probeCancel := context.WithTimeout(ctx, 300*time.Millisecond)
		lastErr = probe(probeCtx)
		probeCancel()
		if lastErr == nil {
			return
		}
		select {
		case <-child.done:
			t.Fatalf("qualification child exited before readiness: %v %v", child.state, child.err)
		case <-ctx.Done():
			t.Fatalf("qualification readiness: %v", lastErr)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
