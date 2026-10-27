// Command toolbootstrap installs repository-pinned development tools without a shell.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
		return fmt.Errorf("usage: toolbootstrap <compose|podman-runtime|podman-e2e|apparmor-denials>: %w", flag.ErrHelp)
	}
	switch arguments[0] {
	case "compose":
		return runCompose(ctx, arguments[1:])
	case "podman-runtime":
		return runPodmanRuntime(ctx, arguments[1:])
	case "podman-e2e", "apparmor-denials":
		if len(arguments) != 1 {
			return fmt.Errorf("unexpected E2E bootstrap arguments: %w", flag.ErrHelp)
		}
		commandCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if arguments[0] == "apparmor-denials" {
			if err := toolbootstrap.AppArmorDenials(commandCtx, os.Stdout); err != nil {
				return fmt.Errorf("report AppArmor denials: %w", err)
			}
			return nil
		}
		return runPodmanE2E(commandCtx)
	default:
		return fmt.Errorf("unknown tool bootstrap command %q: %w", arguments[0], flag.ErrHelp)
	}
}

func runPodmanE2E(ctx context.Context) error {
	installDir, err := toolbootstrap.DefaultInstallDir()
	if err != nil {
		return fmt.Errorf("resolve E2E runtime install directory: %w", err)
	}
	report, err := toolbootstrap.SetupPodmanE2E(ctx, toolbootstrap.RuntimeOptions{
		InstallDir: installDir, GitHubEnv: os.Getenv("GITHUB_ENV"), GitHubPath: os.Getenv("GITHUB_PATH"),
	})
	if err != nil {
		return fmt.Errorf("set up E2E runtime: %w", err)
	}
	slog.Info("verified E2E runtime", "podman", report.Podman,
		"runner_image_os", os.Getenv("ImageOS"), "runner_image_version", os.Getenv("ImageVersion"),
		"compose_version", report.Compose.Version, "compose_path", report.Compose.Path)
	return nil
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
	// Hosted runner images export ImageOS and ImageVersion; empty elsewhere.
	slog.Info("verified Podman runtime",
		"runner_image_os", os.Getenv("ImageOS"),
		"runner_image_version", os.Getenv("ImageVersion"),
		"podman", report.Podman,
		"compose_version", report.Compose.Version,
		"compose_path", report.Compose.Path,
	)
	return nil
}
