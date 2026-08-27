package repoquality

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckMarkdownTreeDiscoversExactSafeInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeMarkdownFixture(t, root, "README.md", "# Valid\n")
	writeMarkdownFixture(t, root, "docs/guide.md", "# Guide.\n")
	writeMarkdownFixture(t, root, "reports/ignored.md", "# Ignored.\n")
	writeMarkdownFixture(t, root, "reports-active/checked.md", "# Checked.\n")

	report, err := CheckMarkdownTree(context.Background(), root)
	if err != nil {
		t.Fatalf("CheckMarkdownTree() error = %v", err)
	}
	wantFiles := []string{"README.md", "docs/guide.md", "reports-active/checked.md"}
	if !slices.Equal(report.Files, wantFiles) {
		t.Fatalf("CheckMarkdownTree() files = %v, want %v", report.Files, wantFiles)
	}
	if len(report.Diagnostics) != 2 {
		t.Fatalf("CheckMarkdownTree() diagnostics = %#v, want two active-file violations", report.Diagnostics)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule != "MD026" {
			t.Fatalf("CheckMarkdownTree() diagnostic = %#v, want MD026", diagnostic)
		}
	}
}

func TestCheckMarkdownTreeFailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("empty discovery", func(t *testing.T) {
		t.Parallel()

		_, err := CheckMarkdownTree(context.Background(), t.TempDir())
		if !errors.Is(err, errNoMarkdownInputs) {
			t.Fatalf("CheckMarkdownTree() error = %v, want empty-discovery error", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeMarkdownFixture(t, root, "README.md", "# Valid\n")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := CheckMarkdownTree(ctx, root)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CheckMarkdownTree() error = %v, want context.Canceled", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeMarkdownFixture(t, root, "target.txt", "# Valid\n")
		if err := os.Symlink("target.txt", filepath.Join(root, "linked.md")); err != nil {
			t.Fatalf("create symlink: %v", err)
		}
		_, err := CheckMarkdownTree(context.Background(), root)
		if !errors.Is(err, errMarkdownNonRegular) {
			t.Fatalf("CheckMarkdownTree() error = %v, want non-regular error", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeMarkdownFixture(t, root, "large.md", strings.Repeat("x", maxMarkdownBytes+1))
		_, err := CheckMarkdownTree(context.Background(), root)
		if !errors.Is(err, errMarkdownTooLarge) {
			t.Fatalf("CheckMarkdownTree() error = %v, want size error", err)
		}
	})
}

func writeMarkdownFixture(t *testing.T, root, name, content string) {
	t.Helper()

	fullPath := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatalf("create fixture parent: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
