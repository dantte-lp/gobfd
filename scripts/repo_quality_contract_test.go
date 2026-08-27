package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryQualityGatesHaveNoNodeRuntime(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	entrypoint := filepath.Join(repositoryRoot, "test", "cmd", "repoquality", "main.go")
	if info, statErr := os.Stat(entrypoint); statErr != nil {
		t.Errorf("Go quality entrypoint is missing: %v", statErr)
	} else if !info.Mode().IsRegular() || info.Size() == 0 {
		t.Errorf("Go quality entrypoint is not a nonempty regular file: %s", entrypoint)
	}

	for _, path := range []string{
		"../package.json",
		"../package-lock.json",
		"../.commitlintrc.yaml",
		"../.markdownlint-cli2.yaml",
		"../.cspell.json",
	} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("obsolete Node quality file still exists: %s (%v)", path, statErr)
		}
	}

	makefile := readContractFile(t, "../Makefile")
	workflow := readContractFile(t, "../.github/workflows/ci.yml")
	releaseWorkflow := readContractFile(t, "../.github/workflows/release.yml")
	repositorySettings := readContractFile(t, "../.github/repository-settings.md")
	containerfile := readContractFile(t, "../deployments/docker/Containerfile.dev")
	pythonProject := readContractFile(t, "../pyproject.toml")
	sonarProject := readContractFile(t, "../sonar-project.properties")

	requireContractStrings(t, "Makefile", makefile, []string{
		"go run ./test/cmd/repoquality markdown --root .",
		"go run ./test/cmd/repoquality commit --message",
		"uv run --frozen --no-default-groups --group quality -- codespell",
	})
	requireContractStrings(t, "CI workflow", workflow, []string{
		"go run ./test/cmd/repoquality markdown --root .",
		"go run ./test/cmd/repoquality commit --message \"$PR_TITLE\"",
		"uv run --frozen --no-build --no-default-groups --group quality -- codespell",
	})
	if got := strings.Count(workflow, "uv run --frozen --no-build"); got != 3 {
		t.Errorf("CI workflow has %d locked no-build uv runs, want 3", got)
	}
	requireContractStrings(t, "release workflow", releaseWorkflow, []string{
		"uv sync --frozen --no-build --no-default-groups --group quality",
		"uv run --frozen --no-build --no-default-groups --group quality",
	})
	requireContractStrings(t, "Sonar project", sonarProject, []string{
		"internal/netio/listener_linux.go",
		"internal/netio/listener_other.go",
		"internal/netio/rawsock_other.go",
	})
	requireContractStrings(t, "repository settings", repositorySettings, []string{
		"Commit policy (PR title)",
	})
	requireContractStrings(t, "Python quality project", pythonProject, []string{
		`"codespell==2.4.3",`,
		"[tool.codespell]",
		"ignore-words = \".codespell-ignore\"",
	})

	for _, surface := range []struct {
		name    string
		content string
	}{
		{name: "Makefile", content: makefile},
		{name: "CI workflow", content: workflow},
		{name: "repository settings", content: repositorySettings},
		{name: "development Containerfile", content: containerfile},
	} {
		for _, forbidden := range []string{
			"setup-node",
			"nodejs",
			"npm ",
			"npx ",
			"markdownlint-cli2",
			"cspell",
			"commitlint",
		} {
			if strings.Contains(surface.content, forbidden) {
				t.Errorf("%s retains Node quality marker %q", surface.name, forbidden)
			}
		}
	}
}

func TestReleaseBranchesReceiveRequiredWorkflows(t *testing.T) {
	t.Parallel()

	for workflow, wantOccurrences := range map[string]int{
		"../.github/workflows/ci.yml":       2,
		"../.github/workflows/security.yml": 2,
		"../.github/workflows/e2e.yml":      1,
	} {
		content := readContractFile(t, workflow)
		marker := `branches: [master, main, "release/v*"]`
		if got := strings.Count(content, marker); got != wantOccurrences {
			t.Errorf("%s has %d release/v* event filters, want %d", workflow, got, wantOccurrences)
		}
	}
}

func TestUVInstallerRequiresHTTPSAcrossRedirects(t *testing.T) {
	t.Parallel()

	installer := readContractFile(t, "../.github/scripts/install-uv.sh")
	requireContractStrings(t, "uv installer", installer, []string{
		"--proto '=https'",
		"--proto-redir '=https'",
	})
}

func requireContractStrings(t *testing.T, surface string, content string, required []string) {
	t.Helper()

	for _, marker := range required {
		if !strings.Contains(content, marker) {
			t.Errorf("%s lacks repository quality contract %q", surface, marker)
		}
	}
}
