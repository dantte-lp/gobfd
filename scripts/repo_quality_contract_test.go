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

func TestReleasePublishesVerifiedDraftLast(t *testing.T) {
	t.Parallel()

	configuration := readContractFile(t, "../.goreleaser.yml")
	requireContractStrings(t, "GoReleaser configuration", configuration, []string{
		"release:\n  draft: true\n  mode: keep-existing\n",
	})
	for _, forbidden := range []string{
		"use_existing_draft: true",
		"replace_existing_draft: true",
		"replace_existing_artifacts: true",
		"\n      - latest\n",
		"\n      - debian-trixie\n",
		"\n      - oraclelinux10\n",
	} {
		if strings.Contains(configuration, forbidden) {
			t.Errorf("GoReleaser configuration enables forbidden retry behavior %q", forbidden)
		}
	}

	workflow := readContractFile(t, "../.github/workflows/release.yml")
	requireContractStrings(t, "release workflow", workflow, []string{
		"gh release upload \"$GITHUB_REF_NAME\" \\",
		"Refuse existing release, draft, or versioned OCI tag",
		"Verify exact release draft",
		"expected-release-assets.txt",
		"expected-release-tag-object.txt",
		"expected-checksummed-assets.txt",
		"release-image-digests.txt",
		"release-evidence-checksums.txt",
		"gh release download \"$GITHUB_REF_NAME\"",
		"sha256sum --check --strict checksums.txt",
		"Promote verified OCI aliases",
		"gh release edit \"$GITHUB_REF_NAME\" --draft=false",
	})
	for _, forbidden := range []string{"--clobber", "--notes-file", " --notes "} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release workflow retains forbidden mutation marker %q", forbidden)
		}
	}
	upload := strings.LastIndex(workflow, "gh release upload \"$GITHUB_REF_NAME\"")
	preflight := strings.Index(workflow, "Refuse existing release, draft, or versioned OCI tag")
	goreleaser := strings.Index(workflow, "Run GoReleaser")
	verification := strings.LastIndex(workflow, "Verify exact release draft")
	promotion := strings.LastIndex(workflow, "Promote verified OCI aliases")
	publication := strings.LastIndex(workflow, "gh release edit \"$GITHUB_REF_NAME\" --draft=false")
	if preflight < 0 || goreleaser < preflight || upload < goreleaser ||
		verification < upload || promotion < verification || publication < promotion {
		t.Error("release ordering is not preflight, draft, upload, verification, alias promotion, then publication")
	}
	if strings.LastIndex(workflow, "gh release ") != publication {
		t.Error("publishing is not the final gh release mutation")
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
