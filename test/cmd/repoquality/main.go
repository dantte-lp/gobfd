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
	errArtifactViolations    = errors.New("tracked artifact policy violations")
	errAttributionViolations = errors.New("AI attribution policy violations")
	errMarkdownViolations    = errors.New("markdown policy violations")
	errCommitViolations      = errors.New("commit policy violations")
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("repository quality check failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("usage: repoquality <artifacts|attribution|markdown|commit|gopls|code>: %w", flag.ErrHelp)
	}
	switch arguments[0] {
	case "artifacts":
		return runArtifacts(ctx, arguments[1:])
	case "attribution":
		return runAttribution(ctx, arguments[1:])
	case "markdown":
		return runMarkdown(ctx, arguments[1:])
	case "commit":
		return runCommit(arguments[1:])
	case "gopls":
		return runGopls(ctx, arguments[1:])
	case "code":
		return runCode(ctx, arguments[1:])
	default:
		return fmt.Errorf("unknown repository quality command %q: %w", arguments[0], flag.ErrHelp)
	}
}

func runArtifacts(ctx context.Context, arguments []string) error {
	return runRootPolicyCheck(ctx, arguments, rootPolicySpec{
		name:         "artifacts",
		failureLabel: "check tracked repository artifacts",
		unitLabel:    "tracked files",
		violationErr: errArtifactViolations,
		evaluate: func(checkCtx context.Context, root string) (int, []repoquality.FileDiagnostic, error) {
			report, err := repoquality.CheckGitTrackedFiles(checkCtx, root)
			return len(report.Files), report.Diagnostics, err
		},
	})
}

func runAttribution(ctx context.Context, arguments []string) error {
	return runRootPolicyCheck(ctx, arguments, rootPolicySpec{
		name:         "attribution",
		failureLabel: "check commit and pull-request AI attribution",
		unitLabel:    "attribution surfaces",
		violationErr: errAttributionViolations,
		evaluate: func(checkCtx context.Context, root string) (int, []repoquality.FileDiagnostic, error) {
			diagnostics, err := repoquality.CheckGitRecentAttribution(checkCtx, repoquality.AttributionOptions{
				Root:            root,
				BaseSHA:         os.Getenv("PR_BASE_SHA"),
				HeadSHA:         os.Getenv("PR_HEAD_SHA"),
				PullRequestBody: os.Getenv("PR_BODY"),
			})
			if err != nil {
				return 0, nil, fmt.Errorf("inspect git and PR attribution: %w", err)
			}
			return 2, diagnostics, nil
		},
	})
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

type rootPolicySpec struct {
	name         string
	failureLabel string
	unitLabel    string
	violationErr error
	evaluate     func(ctx context.Context, root string) (int, []repoquality.FileDiagnostic, error)
}

func runRootPolicyCheck(ctx context.Context, arguments []string, spec rootPolicySpec) error {
	flags := flag.NewFlagSet(spec.name, flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse %s flags: %w", spec.name, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected %s arguments: %w", spec.name, flag.ErrHelp)
	}
	checkedCount, diagnostics, err := spec.evaluate(ctx, *root)
	if err != nil {
		return fmt.Errorf("%s: %w", spec.failureLabel, err)
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(os.Stderr, "%s:%d %s %s\n", diagnostic.Path, diagnostic.Line, diagnostic.Rule, diagnostic.Message)
	}
	if len(diagnostics) != 0 {
		return fmt.Errorf("%w: found %d", spec.violationErr, len(diagnostics))
	}
	fmt.Fprintf(os.Stdout, "repoquality: checked %d %s\n", checkedCount, spec.unitLabel)
	return nil
}

func runMarkdown(ctx context.Context, arguments []string) error {
	return runRootPolicyCheck(ctx, arguments, rootPolicySpec{
		name:         "markdown",
		failureLabel: "check Markdown tree",
		unitLabel:    "Markdown files",
		violationErr: errMarkdownViolations,
		evaluate: func(checkCtx context.Context, root string) (int, []repoquality.FileDiagnostic, error) {
			report, err := repoquality.CheckMarkdownTree(checkCtx, root)
			return len(report.Files), report.Diagnostics, err
		},
	})
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
