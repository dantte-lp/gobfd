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
	sonarProject := readContractFile(t, "../sonar-project.properties")

	requireContractStrings(t, "Makefile", makefile, []string{
		"lint-ci:\n\tgo run ./test/cmd/cictl lint\n",
		"go run ./test/cmd/repoquality markdown --root .",
		"go run ./test/cmd/repoquality commit --message",
		"go tool -modfile=tools/go.mod yamlfmt -lint .",
		"go tool -modfile=tools/go.mod misspell -error",
		"go run ./test/cmd/junitreport --root .",
	})
	for _, marker := range []string{"define run_lint_tag", "LINT_ENABLED_COUNT :=", "grep -RIl --include='*.go'"} {
		if strings.Contains(makefile, marker) {
			t.Errorf("Makefile retains shell-owned lint marker %q", marker)
		}
	}
	requireContractStrings(t, "CI workflow", workflow, []string{
		"go run ./test/cmd/repoquality markdown --root .",
		"go run ./test/cmd/cictl commit-policy",
		"go tool -modfile=tools/go.mod yamlfmt -lint .",
		"go run ./test/cmd/junitreport --root .",
	})
	requireContractStrings(t, "release workflow", releaseWorkflow, []string{
		"go run ./test/cmd/cictl release-test-report",
	})
	requireContractStrings(t, "Sonar project", sonarProject, []string{
		"internal/netio/listener_linux.go",
		"internal/netio/listener_other.go",
		"internal/netio/rawsock_other.go",
	})
	requireContractStrings(t, "repository settings", repositorySettings, []string{
		"Commit policy (PR title)",
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
			"setup-uv",
			"uv run",
			"junit2html",
			"yamllint",
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
		"      - name: Extract release notes from CHANGELOG.md\n" +
			"        env:\n          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n" +
			"        run: go run ./test/cmd/cictl release-notes\n",
		"      - name: Checkout immutable release verifier\n",
		"        working-directory: .release-verifier\n" +
			"        env:\n" +
			"          RELEASE_ARTIFACT_ROOT: ${{ github.workspace }}\n" +
			"        run: go run ./test/cmd/cictl release-artifacts\n",
		"      - name: Attach reports to release\n" +
			"        working-directory: .release-verifier\n" +
			"        env:\n" +
			"          RELEASE_ARTIFACT_ROOT: ${{ github.workspace }}\n" +
			"          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n" +
			"        run: go run ./test/cmd/cictl release-evidence\n",
		"      - name: Verify exact release draft\n" +
			"        working-directory: .release-verifier\n" +
			"        env:\n" +
			"          RELEASE_ARTIFACT_ROOT: ${{ github.workspace }}\n" +
			"          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n" +
			"        run: go run ./test/cmd/cictl release-verify\n",
		"      - name: Promote verified OCI aliases\n" +
			"        working-directory: .release-verifier\n" +
			"        env:\n" +
			"          RELEASE_ARTIFACT_ROOT: ${{ github.workspace }}\n" +
			"          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n" +
			"        run: go run ./test/cmd/cictl release-promote\n",
		"      - name: Publish verified release\n" +
			"        working-directory: .release-verifier\n" +
			"        env:\n" +
			"          RELEASE_ARTIFACT_ROOT: ${{ github.workspace }}\n" +
			"          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n" +
			"        run: go run ./test/cmd/cictl release-publish\n",
		"Refuse existing release, draft, or versioned OCI tag",
	})
	for _, forbidden := range []string{"--clobber", "--notes-file", " --notes "} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release workflow retains forbidden mutation marker %q", forbidden)
		}
	}
	upload := strings.LastIndex(workflow, "run: go run ./test/cmd/cictl release-evidence")
	preflight := strings.Index(workflow, "Refuse existing release, draft, or versioned OCI tag")
	notes := strings.Index(workflow, "Extract release notes from CHANGELOG.md")
	goreleaser := strings.Index(workflow, "Run GoReleaser")
	artifacts := strings.Index(workflow, "Record exact release asset matrix")
	ociEvidence := strings.Index(workflow, "Record exact release OCI evidence")
	reportsDownload := strings.Index(workflow, "Download reports archive")
	verification := strings.LastIndex(workflow, "Verify exact release draft")
	promotion := strings.LastIndex(workflow, "Promote verified OCI aliases")
	publication := strings.LastIndex(workflow, "run: go run ./test/cmd/cictl release-publish")
	if preflight < 0 || notes < preflight || goreleaser < notes || artifacts < goreleaser ||
		ociEvidence < artifacts || reportsDownload < ociEvidence || upload < reportsDownload ||
		verification < upload || promotion < verification || publication < promotion {
		t.Error("release ordering is not preflight, notes, draft, artifact and OCI evidence, " +
			"upload, verification, alias promotion, then publication")
	}
	if strings.LastIndex(workflow, "run: go run ./test/cmd/cictl release-") != publication {
		t.Error("publishing is not the final release command")
	}
	publicationStep := strings.LastIndex(workflow, "\n      - name: Publish verified release\n")
	if publicationStep < 0 || strings.LastIndex(workflow, "\n      - ") != publicationStep {
		t.Error("release publication is not the final workflow step")
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

func TestPythonToolingIsAbsent(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"../.python-version",
		"../pyproject.toml",
		"../uv.lock",
		"../.codespell-ignore",
		"../.yamllint.yaml",
		"../.github/scripts/install-uv.sh",
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("legacy Python quality path still exists: %s (%v)", path, err)
		}
	}
}

func requireContractStrings(t *testing.T, surface string, content string, required []string) {
	t.Helper()

	for _, marker := range required {
		if !strings.Contains(content, marker) {
			t.Errorf("%s lacks repository quality contract %q", surface, marker)
		}
	}
}
