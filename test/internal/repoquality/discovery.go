package repoquality

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

const maxMarkdownBytes = 2 << 20

const ignoredMarkdownRootList = ",.git,.venv,.worktrees,bin,build,dist,docs/rfc,docs/tmp,node_modules,reports,vendor,"

var (
	errNoMarkdownInputs   = errors.New("no Markdown files")
	errMarkdownNonRegular = errors.New("markdown input is not a regular file")
	errMarkdownTooLarge   = errors.New("markdown input exceeds the size limit")
	errMarkdownChanged    = errors.New("markdown input changed during bounded read")
)

// FileDiagnostic associates a policy diagnostic with its repository-relative
// Markdown path.
type FileDiagnostic struct {
	Diagnostic

	Path string
}

// MarkdownReport contains the deterministic nonempty input set and all policy
// violations found under one repository root.
type MarkdownReport struct {
	Files       []string
	Diagnostics []FileDiagnostic
}

// CheckMarkdownTree discovers and checks the repository's Markdown inputs.
func CheckMarkdownTree(ctx context.Context, rootPath string) (MarkdownReport, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return MarkdownReport{}, fmt.Errorf("open Markdown root: %w", err)
	}
	report := MarkdownReport{}
	walker := markdownWalker{root: root, report: &report}
	walkErr := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		return walker.visit(ctx, name, entry, walkErr)
	})
	closeErr := root.Close()
	if joined := errors.Join(walkErr, closeErr); joined != nil {
		return MarkdownReport{}, fmt.Errorf("discover Markdown inputs: %w", joined)
	}
	if len(report.Files) == 0 {
		return MarkdownReport{}, fmt.Errorf("discover Markdown inputs: %w", errNoMarkdownInputs)
	}
	slices.Sort(report.Files)
	slices.SortFunc(report.Diagnostics, func(left, right FileDiagnostic) int {
		if order := strings.Compare(left.Path, right.Path); order != 0 {
			return order
		}
		if left.Line != right.Line {
			return left.Line - right.Line
		}
		return strings.Compare(left.Rule, right.Rule)
	})
	return report, nil
}

type markdownWalker struct {
	root   *os.Root
	report *MarkdownReport
}

func (walker markdownWalker) visit(ctx context.Context, name string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return fmt.Errorf("walk Markdown path %s: %w", name, walkErr)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("check Markdown discovery context: %w", contextErr)
	}
	clean := path.Clean(name)
	if entry.IsDir() && ignoredMarkdownPath(clean) {
		return fs.SkipDir
	}
	if entry.IsDir() || !strings.EqualFold(path.Ext(clean), ".md") {
		return nil
	}
	data, readErr := readMarkdownFile(walker.root, clean)
	if readErr != nil {
		return readErr
	}
	walker.report.Files = append(walker.report.Files, clean)
	for _, diagnostic := range CheckMarkdown(clean, data) {
		walker.report.Diagnostics = append(
			walker.report.Diagnostics,
			FileDiagnostic{Diagnostic: diagnostic, Path: clean},
		)
	}
	return nil
}

func ignoredMarkdownPath(name string) bool {
	for ignored := range strings.SplitSeq(strings.Trim(ignoredMarkdownRootList, ","), ",") {
		if name == ignored || strings.HasPrefix(name, ignored+"/") {
			return true
		}
	}
	return false
}

func readMarkdownFile(root *os.Root, name string) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("lstat Markdown file %s: %w", name, err)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s has mode %s", errMarkdownNonRegular, name, before.Mode())
	}
	if before.Size() > maxMarkdownBytes {
		return nil, fmt.Errorf("%w: %s has size %d", errMarkdownTooLarge, name, before.Size())
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open Markdown file %s: %w", name, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxMarkdownBytes+1))
	opened, statErr := file.Stat()
	closeErr := file.Close()
	after, afterErr := root.Lstat(name)
	if err := errors.Join(readErr, statErr, closeErr, afterErr); err != nil {
		return nil, fmt.Errorf("read Markdown file %s: %w", name, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("%w: %s", errMarkdownChanged, name)
	}
	if len(data) > maxMarkdownBytes {
		return nil, fmt.Errorf("%w: %s content has %d bytes", errMarkdownTooLarge, name, len(data))
	}
	return data, nil
}
