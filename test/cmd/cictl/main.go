// Command cictl owns shell-free CI workflow operations.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], dependencies{}); err != nil {
		slog.Error("CI command failed", "error", err)
		os.Exit(1)
	}
}

type dependencies struct {
	getenv func(string) string
	now    func() time.Time
	runner cirunner.CommandRunner
}

func run(ctx context.Context, arguments []string, deps dependencies) error {
	if deps.getenv == nil {
		deps.getenv = os.Getenv
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.runner == nil {
		deps.runner = cirunner.ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr}
	}
	if len(arguments) == 0 {
		return fmt.Errorf("usage: cictl {sonar-mode|build}: %w", flag.ErrHelp)
	}

	switch arguments[0] {
	case "sonar-mode":
		return runSonarMode(arguments[1:], deps)
	case "build":
		return runBuild(ctx, arguments[1:], deps)
	default:
		return fmt.Errorf("unknown CI command %q: %w", arguments[0], flag.ErrHelp)
	}
}

func runSonarMode(arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("sonar-mode", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	actor := flags.String("actor", deps.getenv("GITHUB_ACTOR"), "GitHub workflow actor")
	output := flags.String("output", deps.getenv("GITHUB_OUTPUT"), "GitHub step output file")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse sonar-mode flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected sonar-mode arguments: %w", flag.ErrHelp)
	}
	if err := cirunner.SonarMode(cirunner.SonarOptions{
		Token:  deps.getenv("SONAR_TOKEN"),
		Actor:  *actor,
		Output: *output,
	}); err != nil {
		return fmt.Errorf("select Sonar workflow mode: %w", err)
	}
	return nil
}

func runBuild(ctx context.Context, arguments []string, deps dependencies) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sha := flags.String("sha", deps.getenv("GITHUB_SHA"), "Git commit SHA")
	output := flags.String("output", "/tmp/gobfd-build", "binary output directory")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse build flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected build arguments: %w", flag.ErrHelp)
	}
	if err := cirunner.Build(ctx, cirunner.BuildOptions{
		SHA:    *sha,
		Output: *output,
		Now:    deps.now,
		Runner: deps.runner,
	}); err != nil {
		return fmt.Errorf("build CI binaries: %w", err)
	}
	return nil
}
