package toolbootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const commandWaitDelay = 5 * time.Second

var errCommandNotAllowed = errors.New("bootstrap command is not allowlisted")

type outputRunner interface {
	Output(ctx context.Context, name string, arguments, environment []string) (string, error)
}

type execOutputRunner struct{}

func (execOutputRunner) Output(
	ctx context.Context,
	name string,
	arguments, environment []string,
) (string, error) {
	return commandOutput(ctx, name, arguments, environment)
}

func verifyJQ(ctx context.Context, runner outputRunner) error {
	if _, err := runner.Output(ctx, "jq", []string{"--version"}, nil); err != nil {
		return fmt.Errorf("verify jq runtime: %w", err)
	}
	return nil
}

func commandPath(name string) (string, error) {
	switch name {
	case "jq", "podman", "sudo":
	default:
		return "", fmt.Errorf("resolve bootstrap command %q: %w", name, errCommandNotAllowed)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve bootstrap command %q: %w", name, err)
	}
	return path, nil
}

func commandOutput(ctx context.Context, name string, arguments, environment []string) (string, error) {
	path := name
	if name == "jq" || name == "podman" {
		var err error
		path, err = commandPath(name)
		if err != nil {
			return "", err
		}
	}
	// #nosec G204 -- path is either the checksum-verified Compose file or a fixed allowlisted executable.
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = environment
	command.WaitDelay = commandWaitDelay
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run %s %s; output=%s: %w", name, strings.Join(arguments, " "),
			strings.TrimSpace(string(output)), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func runStreaming(ctx context.Context, name string, arguments, environment []string) error {
	path, err := commandPath(name)
	if err != nil {
		return err
	}
	// #nosec G204 -- path is resolved from the fixed allowlist and no shell is used.
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.WaitDelay = commandWaitDelay
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w", name, strings.Join(arguments, " "), err)
	}
	return nil
}
