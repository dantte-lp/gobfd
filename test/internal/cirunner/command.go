package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // CI invokes only fixed commands assembled by this package.
)

var errCommandNotAllowed = errors.New("CI command is not allowlisted")

// CommandSpec describes one direct child process invocation.
type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
	// Executable binds an allowlisted command to an already verified file.
	Executable *os.File
	// Stdin overrides the child process's standard input.
	Stdin io.Reader
	// Stdout and Stderr override the runner streams for artifact-producing commands.
	Stdout io.Writer
	Stderr io.Writer
}

// SpecRunner executes a typed command without a shell.
type SpecRunner interface {
	RunCommand(ctx context.Context, spec CommandSpec) error
}

// RunCommand executes a typed direct child command and preserves its output streams.
func (r ExecRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	switch spec.Name {
	case "buf", "gh", "git", "go", "upx", "xz":
	default:
		return fmt.Errorf("run CI command %q: %w", spec.Name, errCommandNotAllowed)
	}
	executable := spec.Name
	var extraFiles []*os.File
	if spec.Executable != nil {
		if spec.Name != "upx" {
			return fmt.Errorf("bind executable for CI command %q: %w", spec.Name, errInvalidConfig)
		}
		info, err := spec.Executable.Stat()
		if err != nil {
			return fmt.Errorf("inspect bound UPX executable: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("bound UPX executable has mode %s: %w", info.Mode(), errInvalidConfig)
		}
		executable = "/proc/self/fd/3"
		extraFiles = []*os.File{spec.Executable}
	} else if spec.Name == "upx" {
		return fmt.Errorf("UPX command requires a bound executable: %w", errInvalidConfig)
	}
	// #nosec G204 -- the executable is selected from the fixed allowlist above.
	command := exec.CommandContext(ctx, executable, spec.Args...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	command.ExtraFiles = extraFiles
	command.Stdin = spec.Stdin
	command.Stdout = r.Stdout
	if spec.Stdout != nil {
		command.Stdout = spec.Stdout
	}
	command.Stderr = r.Stderr
	if spec.Stderr != nil {
		command.Stderr = spec.Stderr
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s command: %w", spec.Name, err)
	}
	return nil
}
