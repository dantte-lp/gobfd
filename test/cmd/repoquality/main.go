// Command repoquality checks repository-owned source and documentation policy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/repoquality"
)

var (
	errMarkdownViolations = errors.New("markdown policy violations")
	errCommitViolations   = errors.New("commit policy violations")
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("repository quality check failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("usage: repoquality <markdown|commit|gopls>: %w", flag.ErrHelp)
	}
	switch arguments[0] {
	case "markdown":
		return runMarkdown(ctx, arguments[1:])
	case "commit":
		return runCommit(arguments[1:])
	case "gopls":
		return runGopls(ctx, arguments[1:])
	default:
		return fmt.Errorf("unknown repository quality command %q: %w", arguments[0], flag.ErrHelp)
	}
}

type profileFlags []string

func (profiles *profileFlags) String() string {
	return strings.Join(*profiles, ",")
}

func (profiles *profileFlags) Set(value string) error {
	*profiles = append(*profiles, value)
	return nil
}

func runGopls(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("gopls", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	goos := flags.String("goos", "linux", "target GOOS")
	goarch := flags.String("goarch", "amd64", "target GOARCH")
	var profiles profileFlags
	flags.Var(&profiles, "profile", "build-tag profile; repeat for multiple profiles")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse gopls flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected gopls arguments: %w", flag.ErrHelp)
	}
	report, err := repoquality.CheckGopls(ctx, repoquality.GoplsOptions{
		Root:     *root,
		GOOS:     *goos,
		GOARCH:   *goarch,
		Profiles: profiles,
	})
	if err != nil {
		return fmt.Errorf("check gopls diagnostics: %w", err)
	}
	fmt.Fprintf(os.Stdout,
		"gopls-check: no diagnostics across %d tag profiles, %d package checks, and %d Go input checks; "+
			"GOOS=%s GOARCH=%s\n",
		report.ProfileCount, report.PackageCount, report.InputCount, *goos, *goarch,
	)
	return nil
}

func runMarkdown(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("markdown", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse markdown flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected markdown arguments: %w", flag.ErrHelp)
	}
	report, err := repoquality.CheckMarkdownTree(ctx, *root)
	if err != nil {
		return fmt.Errorf("check Markdown tree: %w", err)
	}
	for _, diagnostic := range report.Diagnostics {
		fmt.Fprintf(os.Stderr, "%s:%d %s %s\n", diagnostic.Path, diagnostic.Line, diagnostic.Rule, diagnostic.Message)
	}
	if len(report.Diagnostics) != 0 {
		return fmt.Errorf("%w: found %d", errMarkdownViolations, len(report.Diagnostics))
	}
	fmt.Fprintf(os.Stdout, "repoquality: checked %d Markdown files\n", len(report.Files))
	return nil
}

func runCommit(arguments []string) error {
	flags := flag.NewFlagSet("commit", flag.ContinueOnError)
	message := flags.String("message", "", "commit message")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse commit flags: %w", err)
	}
	if flags.NArg() != 0 || *message == "" {
		return fmt.Errorf("commit message is required: %w", flag.ErrHelp)
	}
	diagnostics := repoquality.CheckCommit(*message)
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(os.Stderr, "commit:%d %s %s\n", diagnostic.Line, diagnostic.Rule, diagnostic.Message)
	}
	if repoquality.HasErrors(diagnostics) {
		return fmt.Errorf("%w: found %d", errCommitViolations, len(diagnostics))
	}
	fmt.Fprintln(os.Stdout, "repoquality: commit message accepted")
	return nil
}
