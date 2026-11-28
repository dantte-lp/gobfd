package cirunner

import (
	"bytes"
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
	artifactNames := []string{"unit-report.xml", "unit-report.json", "coverage.out", "unit-report.html"}
	artifacts := make([]string, 0, len(artifactNames))
	for _, name := range artifactNames {
		path, pathErr := validateRootFile(root, name, "test artifact", false)
		if pathErr != nil {
			return pathErr
		}
		artifacts = append(artifacts, path)
	}
	for _, path := range artifacts {
		if err := resetArtifact(path, "test artifact"); err != nil {
			return err
		}
	}
	spec := CommandSpec{
		Name: "go",
		Args: []string{
			"tool", toolsModuleFlag, "gotestsum",
			"--junitfile", "unit-report.xml", "--jsonfile", "unit-report.json",
			formatFlag, "short-verbose", "--", buildVCSDisabledFlag, "./...", "-race", "-count=1",
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
		return fmt.Errorf("buf fetch command runner is required: %w", errInvalidConfig)
	}
	refspec := "+refs/heads/" + base + ":refs/remotes/origin/" + base
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "git", Args: []string{"fetch", "origin", refspec}, Dir: root,
	}); err != nil {
		return fmt.Errorf("fetch Buf base branch: %w", err)
	}
	return nil
}

// BufBreaking checks the current protobuf API against the pull request base branch.
func BufBreaking(ctx context.Context, root, base string, runner SpecRunner) error {
	root, rootErr := validateAbsoluteExistingDirectory(root, "repository root")
	if rootErr != nil {
		return rootErr
	}
	if err := validateGitHubBaseRef(base); err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("buf breaking command runner is required: %w", errInvalidConfig)
	}
	remoteRef := "refs/remotes/origin/" + base
	var revision bytes.Buffer
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "git", Args: []string{"rev-parse", "--verify", remoteRef + "^{commit}"}, Dir: root,
		Stdout: &revision,
	}); err != nil {
		return fmt.Errorf("resolve Buf base commit: %w", err)
	}
	commit, commitErr := validateCommitID(revision.String())
	if commitErr != nil {
		return commitErr
	}
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "buf", Args: []string{"breaking", "--against", ".git#commit=" + commit}, Dir: root,
	}); err != nil {
		return fmt.Errorf("check Buf compatibility: %w", err)
	}
	return nil
}

// CommitPolicy validates the pull request title through the repository quality command.
func CommitPolicy(ctx context.Context, root, title string, runner SpecRunner) error {
	root, err := validateAbsoluteExistingDirectory(root, "repository root")
	if err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("commit policy command runner is required: %w", errInvalidConfig)
	}
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "go",
		Args: []string{"run", "./test/cmd/repoquality", "commit", "--message", title},
		Dir:  root,
	}); err != nil {
		return fmt.Errorf("validate pull request title: %w", err)
	}
	return nil
}

func validateCommitID(output string) (string, error) {
	commit := strings.TrimSuffix(output, "\n")
	if (len(commit) != 40 && len(commit) != 64) || strings.ContainsAny(commit, "\r\n") {
		return "", fmt.Errorf("resolved Buf base commit must be one 40- or 64-hex ID: %w", errInvalidConfig)
	}
	for _, character := range commit {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return "", fmt.Errorf("resolved Buf base commit must be one 40- or 64-hex ID: %w", errInvalidConfig)
		}
	}
	return commit, nil
}

func validateGitHubBaseRef(value string) error {
	if value == "" || len(value) > githubBaseRefLimit || hasControl(value) ||
		value == "@" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") ||
		strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") ||
		strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " \\~^:?*[") {
		return fmt.Errorf("GITHUB_BASE_REF must be a safe branch name: %w", errInvalidConfig)
	}
	if hasUnsafeGitHubBaseRefComponent(value) {
		return fmt.Errorf("GITHUB_BASE_REF must be a safe branch name: %w", errInvalidConfig)
	}
	return nil
}

func hasUnsafeGitHubBaseRefComponent(value string) bool {
	for component := range strings.SplitSeq(value, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return true
		}
	}
	return false
}
