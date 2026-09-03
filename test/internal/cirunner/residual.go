package cirunner

import (
	"context"
	"fmt"
	"strings"
)

const githubBaseRefLimit = 1024

// TestCoverage runs the pinned gotestsum tool with the CI coverage contract.
func TestCoverage(ctx context.Context, root string, runner SpecRunner) error {
	root, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("coverage command runner is required: %w", errInvalidConfig)
	}
	spec := CommandSpec{
		Name: "go",
		Args: []string{
			"tool", "-modfile=tools/go.mod", "gotestsum",
			"--junitfile", "unit-report.xml", "--jsonfile", "unit-report.json",
			"--format", "short-verbose", "--", "-buildvcs=false", "./...", "-race", "-count=1",
			"-coverprofile=coverage.out", "-covermode=atomic",
		},
		Dir: root,
	}
	if err := runner.RunCommand(ctx, spec); err != nil {
		return fmt.Errorf("run tests with coverage: %w", err)
	}
	return nil
}

// BufFetchBase fetches the pull request base branch without shell interpolation.
func BufFetchBase(ctx context.Context, root, base string, runner SpecRunner) error {
	root, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	if err := validateGitHubBaseRef(base); err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("Buf fetch command runner is required: %w", errInvalidConfig)
	}
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "git", Args: []string{"fetch", "origin", base}, Dir: root,
	}); err != nil {
		return fmt.Errorf("fetch Buf base branch: %w", err)
	}
	return nil
}

// BufBreaking checks the current protobuf API against the pull request base branch.
func BufBreaking(ctx context.Context, root, base string, runner SpecRunner) error {
	root, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	if err := validateGitHubBaseRef(base); err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("Buf breaking command runner is required: %w", errInvalidConfig)
	}
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "buf", Args: []string{"breaking", "--against", ".git#branch=origin/" + base}, Dir: root,
	}); err != nil {
		return fmt.Errorf("check Buf compatibility: %w", err)
	}
	return nil
}

func validateGitHubBaseRef(value string) error {
	if value == "" || len(value) > githubBaseRefLimit || hasControl(value) ||
		value == "@" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") ||
		strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") ||
		strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " \\~^:?*[") {
		return fmt.Errorf("GITHUB_BASE_REF must be a safe branch name: %w", errInvalidConfig)
	}
	for component := range strings.SplitSeq(value, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return fmt.Errorf("GITHUB_BASE_REF must be a safe branch name: %w", errInvalidConfig)
		}
	}
	return nil
}
