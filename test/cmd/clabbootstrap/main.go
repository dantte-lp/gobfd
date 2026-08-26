// Command clabbootstrap prepares the repository vendor-interoperability lab.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dantte-lp/gobfd/test/internal/clabbootstrap"
)

var errUnexpectedArguments = errors.New("unexpected positional arguments")

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr))
}

func run(ctx context.Context, arguments []string, stderr io.Writer) int {
	projectRoot, err := repositoryRoot()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "clabbootstrap: %v\n", err)
		return 2
	}
	options, verbose, parseErr := parseOptions(projectRoot, arguments, stderr)
	if parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	options.Logger = logger
	runner, err := clabbootstrap.NewOSRunner(projectRoot, logger)
	if err != nil {
		logger.ErrorContext(ctx, "create bootstrap runner", "error", err)
		return 1
	}
	if err := clabbootstrap.Run(ctx, options, runner); err != nil {
		logger.ErrorContext(ctx, "containerlab bootstrap failed", "error", err)
		return 1
	}
	logger.InfoContext(ctx, "containerlab bootstrap complete")
	return 0
}

func parseOptions(projectRoot string, arguments []string, stderr io.Writer) (clabbootstrap.Options, bool, error) {
	options := clabbootstrap.DefaultOptions(projectRoot)
	flags := flag.NewFlagSet("clabbootstrap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	verbose := flags.Bool("v", false, "enable debug logging")
	flags.BoolVar(verbose, "verbose", false, "enable debug logging")
	flags.StringVar(&options.Archives.Arista, "arista-image", "", "path to cEOS archive")
	flags.StringVar(&options.Tags.Arista, "arista-tag", options.Tags.Arista, "tag for imported cEOS image")
	flags.StringVar(&options.Archives.Cisco, "cisco-image", "", "path to XRd archive")
	flags.StringVar(&options.Tags.Cisco, "cisco-tag", options.Tags.Cisco, "tag for imported XRd image")
	flags.StringVar(&options.VyOSISO, "vyos-iso", "", "path to local VyOS ISO")
	flags.StringVar(&options.VyOSVersion, "vyos-version", options.VyOSVersion, "VyOS rolling version for ISO fallback")
	flags.StringVar(&options.Tags.Nokia, "nokia-tag", options.Tags.Nokia, "Nokia SR Linux tag")
	flags.StringVar(&options.Tags.FRR, "frr-tag", options.Tags.FRR, "FRRouting tag")
	flags.StringVar(&options.Tags.Sonic, "sonic-tag", options.Tags.Sonic, "SONiC-VS tag")
	flags.StringVar(&options.Tags.VyOS, "vyos-tag", options.Tags.VyOS, "public VyOS image tag")
	flags.BoolVar(&options.SkipBuild, "skip-build", false, "skip GoBFD image build")
	flags.BoolVar(&options.SkipPull, "skip-pull", false, "skip images already present")
	flags.BoolVar(&options.Deploy, "deploy", false, "run run.sh --up-only after preparation")
	flags.BoolVar(&options.Test, "test", false, "run the full interop-clab runner after preparation")
	flags.BoolVar(&options.DryRun, "dry-run", false, "show commands without executing them")
	flags.IntVar(&options.Jobs, "jobs", options.Jobs, "maximum parallel image pulls")
	if err := flags.Parse(arguments); err != nil {
		return clabbootstrap.Options{}, false, fmt.Errorf("parse bootstrap flags: %w", err)
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "clabbootstrap: unexpected arguments: %v\n", flags.Args())
		return clabbootstrap.Options{}, false, fmt.Errorf("parse bootstrap flags: %w", errUnexpectedArguments)
	}
	return options, *verbose, nil
}

func repositoryRoot() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("require repository-root go.mod in %s: %w", root, err)
	}
	return root, nil
}
