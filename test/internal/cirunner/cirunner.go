// Package cirunner owns the shell-free commands used by CI workflow steps.
package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // CI invokes only the fixed Go compiler command below.
	"path/filepath"
	"time"
)

const (
	buildOutputMode      = 0o755
	buildVCSDisabledFlag = "-buildvcs=false"
	dockerCommand        = "docker"
	buildxSubcommand     = "buildx"
	imagetoolsSubcommand = "imagetools"
	inspectSubcommand    = "inspect"
	formatFlag           = "--format"
)

var (
	// ErrInvalidSHA reports a GITHUB_SHA that cannot identify a Git commit.
	ErrInvalidSHA    = errors.New("invalid Git commit SHA")
	errInvalidConfig = errors.New("invalid CI runner configuration")
)

// SonarOptions supplies the inputs used to select the Sonar workflow mode.
type SonarOptions struct {
	TokenPresent string
	Actor        string
	Output       string
}

// SonarMode appends the selected Sonar policy mode to the GitHub output file.
func SonarMode(options SonarOptions) error {
	var mode string
	switch options.TokenPresent {
	case "true":
		mode = "run"
	case "false":
		if options.Actor != "dependabot[bot]" {
			return fmt.Errorf("SONAR_TOKEN is required for non-Dependabot SonarQube scans: %w", errInvalidConfig)
		}
		mode = "skip-dependabot"
	default:
		return fmt.Errorf("SONAR_TOKEN_PRESENT must be exactly true or false: %w", errInvalidConfig)
	}
	if options.Output == "" {
		return fmt.Errorf("GITHUB_OUTPUT is required for Sonar mode: %w", errInvalidConfig)
	}

	output, err := os.OpenFile(options.Output, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT for append: %w", err)
	}
	line := "mode=" + mode + "\n"
	written, writeErr := io.WriteString(output, line)
	if writeErr == nil && written != len(line) {
		writeErr = io.ErrShortWrite
	}
	closeErr := output.Close()
	if writeErr != nil {
		return fmt.Errorf("append Sonar mode to GITHUB_OUTPUT: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close GITHUB_OUTPUT after append: %w", closeErr)
	}
	return nil
}

// CommandRunner executes an argument vector without a shell.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// ExecRunner executes commands using os/exec.
type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes one command and preserves its output streams.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = r.Stdout
	command.Stderr = r.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s command: %w", name, err)
	}
	return nil
}

// BuildOptions supplies build metadata, output location, and test seams.
type BuildOptions struct {
	Version string
	SHA     string
	Output  string
	Now     func() time.Time
	Runner  CommandRunner
}

// Build compiles the four supported binaries with deterministic command lines.
func Build(ctx context.Context, options BuildOptions) error {
	shortSHA, err := validateSHA(options.SHA)
	if err != nil {
		return err
	}
	if options.Output == "" {
		return fmt.Errorf("build output directory is required: %w", errInvalidConfig)
	}
	if options.Runner == nil {
		return fmt.Errorf("build command runner is required: %w", errInvalidConfig)
	}
	if err := os.MkdirAll(options.Output, buildOutputMode); err != nil {
		return fmt.Errorf("create build output directory %s: %w", options.Output, err)
	}
	if err := os.Chmod(options.Output, buildOutputMode); err != nil {
		return fmt.Errorf("set build output directory %s mode 0755: %w", options.Output, err)
	}

	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	buildDate := now().UTC().Format(time.RFC3339)
	version := "ci-" + shortSHA
	if options.Version != "" {
		version = options.Version
	}
	ldflags := fmt.Sprintf(
		"-s -w -X github.com/dantte-lp/gobfd/internal/version.Version=%s "+
			"-X github.com/dantte-lp/gobfd/internal/version.GitCommit=%s "+
			"-X github.com/dantte-lp/gobfd/internal/version.BuildDate=%s",
		version,
		shortSHA,
		buildDate,
	)
	for _, name := range []string{"gobfd", "gobfdctl", "gobfd-haproxy-agent", "gobfd-exabgp-bridge"} {
		arguments := []string{
			"build",
			buildVCSDisabledFlag,
			"-ldflags=" + ldflags,
			"-o",
			filepath.Join(options.Output, name),
			"./cmd/" + name,
		}
		if err := options.Runner.Run(ctx, "go", arguments...); err != nil {
			return fmt.Errorf("build %s: %w", name, err)
		}
	}
	return nil
}

func validateSHA(sha string) (string, error) {
	const shortCommitSHACharacterCount = 8

	if len(sha) < shortCommitSHACharacterCount {
		return "", fmt.Errorf("GITHUB_SHA must contain at least 8 hexadecimal characters: %w", ErrInvalidSHA)
	}
	for _, character := range sha {
		if character >= '0' && character <= '9' ||
			character >= 'a' && character <= 'f' ||
			character >= 'A' && character <= 'F' {
			continue
		}
		return "", fmt.Errorf("GITHUB_SHA contains a non-hexadecimal character: %w", ErrInvalidSHA)
	}
	return sha[:shortCommitSHACharacterCount], nil
}
