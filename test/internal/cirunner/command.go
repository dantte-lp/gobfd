package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec" //nolint:depguard // CI invokes only fixed commands assembled by this package.
)

var errCommandNotAllowed = errors.New("CI command is not allowlisted")

// CommandSpec describes one direct child process invocation.
type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
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
	case "buf", "git", "go":
	default:
		return fmt.Errorf("run CI command %q: %w", spec.Name, errCommandNotAllowed)
	}
	// #nosec G204 -- the executable is selected from the fixed allowlist above.
	command := exec.CommandContext(ctx, spec.Name, spec.Args...)
	command.Dir = spec.Dir
	command.Env = spec.Env
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
