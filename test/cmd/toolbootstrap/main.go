// Command toolbootstrap installs repository-pinned development tools without a shell.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/dantte-lp/gobfd/test/internal/toolbootstrap"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("tool bootstrap failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("usage: toolbootstrap <compose|podman-runtime>: %w", flag.ErrHelp)
	}
	switch arguments[0] {
	case "compose":
		return runCompose(ctx, arguments[1:])
	case "podman-runtime":
		return runPodmanRuntime(ctx, arguments[1:])
	default:
		return fmt.Errorf("unknown tool bootstrap command %q: %w", arguments[0], flag.ErrHelp)
	}
}

func runCompose(ctx context.Context, arguments []string) error {
	installDir, err := toolbootstrap.DefaultInstallDir()
	if err != nil {
		return fmt.Errorf("resolve default Compose install directory: %w", err)
	}
	flags := flag.NewFlagSet("compose", flag.ContinueOnError)
	directory := flags.String("install-dir", installDir, "provider installation directory")
	version := flags.String("version", toolbootstrap.ComposeVersion, "exact Docker Compose version")
	if parseErr := flags.Parse(arguments); parseErr != nil {
		return fmt.Errorf("parse Compose bootstrap flags: %w", parseErr)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected Compose bootstrap arguments: %w", flag.ErrHelp)
	}
	report, err := toolbootstrap.InstallCompose(ctx, toolbootstrap.ComposeOptions{
		InstallDir: *directory,
		Version:    *version,
	})
	if err != nil {
		return fmt.Errorf("install Compose provider: %w", err)
	}
	slog.Info("installed Docker Compose provider", "version", report.Version, "path", report.Path)
	return nil
}

func runPodmanRuntime(ctx context.Context, arguments []string) error {
	installDir, err := toolbootstrap.DefaultInstallDir()
	if err != nil {
		return fmt.Errorf("resolve default Podman install directory: %w", err)
	}
	flags := flag.NewFlagSet("podman-runtime", flag.ContinueOnError)
	directory := flags.String("install-dir", installDir, "provider installation directory")
	githubEnv := flags.String("github-env", os.Getenv("GITHUB_ENV"), "GitHub environment file")
	githubPath := flags.String("github-path", os.Getenv("GITHUB_PATH"), "GitHub executable path file")
	if parseErr := flags.Parse(arguments); parseErr != nil {
		return fmt.Errorf("parse Podman bootstrap flags: %w", parseErr)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected Podman bootstrap arguments: %w", flag.ErrHelp)
	}
	report, err := toolbootstrap.SetupPodmanRuntime(ctx, toolbootstrap.RuntimeOptions{
		InstallDir: *directory,
		GitHubEnv:  *githubEnv,
		GitHubPath: *githubPath,
	})
	if err != nil {
		return fmt.Errorf("set up Podman runtime: %w", err)
	}
	slog.Info("verified Podman runtime",
		"podman", report.Podman,
		"compose_version", report.Compose.Version,
		"compose_path", report.Compose.Path,
	)
	return nil
}
