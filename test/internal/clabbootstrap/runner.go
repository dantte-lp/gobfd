package clabbootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"time"
)

const maxCommandOutput = 1 << 20

const commandWaitDelay = 5 * time.Second

var (
	errCommandNotAllowed = errors.New("bootstrap command is not allowlisted")
	errCommandOutputSize = errors.New("bootstrap command output exceeds limit")
)

// OSRunner executes allowlisted commands without a shell.
type OSRunner struct {
	projectRoot string
	logger      *slog.Logger
}

// NewOSRunner returns an allowlisted operating-system command runner.
func NewOSRunner(projectRoot string, logger *slog.Logger) (*OSRunner, error) {
	if !filepath.IsAbs(projectRoot) || logger == nil {
		return nil, fmt.Errorf("create bootstrap OS runner for %q: %w", projectRoot, errInvalidBootstrapOptions)
	}
	return &OSRunner{projectRoot: projectRoot, logger: logger}, nil
}

// Run executes command with context cancellation and bounded output capture.
func (runner *OSRunner) Run(ctx context.Context, command Command) (Result, error) {
	executable, err := runner.resolveExecutable(command.Executable, command.DryRun)
	if err != nil {
		return Result{}, err
	}
	runner.logger.DebugContext(ctx, "bootstrap command", "executable", executable, "arguments", command.Arguments)
	if command.DryRun {
		runner.logger.InfoContext(ctx, "bootstrap dry run", "executable", executable, "arguments", command.Arguments)
		return Result{}, nil
	}

	stdout := newLimitedBuffer(maxCommandOutput)
	stderr := newLimitedBuffer(maxCommandOutput)
	// #nosec G204 -- executable is resolved from the fixed allowlist or repository run.sh and no shell is used.
	process := exec.CommandContext(ctx, executable, command.Arguments...)
	process.Dir = command.Directory
	process.Stdout = stdout
	process.Stderr = stderr
	process.WaitDelay = commandWaitDelay
	runErr := process.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if stdout.overflow || stderr.overflow {
		return result, fmt.Errorf("capture %s output: %w", command.Executable, errCommandOutputSize)
	}
	if runErr == nil {
		return result, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, fmt.Errorf("run %s with context: %w", command.Executable, ctxErr)
	}
	if exitError, ok := errors.AsType[*exec.ExitError](runErr); ok {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("start or wait for %s: %w", command.Executable, runErr)
}

func (runner *OSRunner) resolveExecutable(name string, dryRun bool) (string, error) {
	runScript := filepath.Join(runner.projectRoot, "test", "interop-clab", "run.sh")
	if name == runScript {
		return runScript, nil
	}
	switch name {
	case executableContainerlab, executablePodman, "go", "7z", "unsquashfs", "xorriso":
	default:
		return "", fmt.Errorf("resolve bootstrap executable %q: %w", name, errCommandNotAllowed)
	}
	if dryRun {
		return name, nil
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve bootstrap executable %q: %w", name, err)
	}
	return resolved, nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining < len(data) {
		buffer.overflow = true
		if remaining <= 0 {
			return written, nil
		}
		data = data[:remaining]
	}
	_, _ = buffer.buffer.Write(data)
	return written, nil
}

func (buffer *limitedBuffer) String() string {
	return buffer.buffer.String()
}
