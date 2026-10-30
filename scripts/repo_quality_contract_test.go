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

func TestV1ReleaseUsesOnlyDebianOCIAndKeepsRPM(t *testing.T) {
	t.Parallel()

	configuration := readContractFile(t, "../.goreleaser.yml")
	requireContractStrings(t, "GoReleaser configuration", configuration, []string{
		"  - id: gobfd-debian-trixie\n",
		"    formats:\n      - deb\n      - rpm\n",
	})
	for _, path := range []string{
		"../.goreleaser.yml",
		"../.github/workflows/e2e.yml",
		"../test/package-lifecycle/lifecycle_test.go",
		"../test/internal/cirunner/release_preflight.go",
		"../test/internal/cirunner/release_oci.go",
		"../test/internal/cirunner/release_evidence.go",
		"../test/internal/cirunner/release_promote.go",
		"../test/internal/cirunner/release_publish.go",
	} {
		if strings.Contains(strings.ToLower(readContractFile(t, path)), "oracle") {
			t.Errorf("%s retains the retired Oracle Linux OCI product", path)
		}
	}
	if _, err := os.Stat("../deployments/docker/Containerfile.release.oraclelinux"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("retired Oracle Linux recipe: stat error = %v, want not exist", err)
	}
}

func TestReleasePublishesVerifiedDraftLast(t *testing.T) {
	t.Parallel()

	configuration := readContractFile(t, "../.goreleaser.yml")
	release := "\n" + topLevelYAMLSection(t, configuration, "release")
	requireContractStrings(t, "GoReleaser release configuration", release, []string{
		"\n  draft: true\n",
		"\n  prerelease: auto\n",
		"\n  make_latest: false\n",
		"\n  mode: keep-existing\n",
		"\n  skip_upload: true\n",
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
	for _, step := range []struct {
		name, command string
		verifier      bool
		authenticated bool
	}{
		{name: "Refuse existing release, draft, or versioned OCI tag", command: "release-preflight", authenticated: true},
		{name: "Extract release notes from CHANGELOG.md", command: "release-notes", authenticated: true},
		{
			name:    "Verify merged release PR and bind its URL to release notes",
			command: "release-pr-check", authenticated: true,
		},
		{name: "Record exact release asset matrix", command: "release-artifacts", verifier: true},
		{name: "Upload exact release asset matrix", command: "release-assets-upload", verifier: true, authenticated: true},
		{name: "Record exact release OCI evidence", command: "release-oci-evidence", verifier: true},
		{name: "Attach reports to release", command: "release-evidence", verifier: true, authenticated: true},
		{name: "Verify exact release draft", command: "release-verify", verifier: true, authenticated: true},
		{name: "Promote verified OCI aliases", command: "release-promote", verifier: true, authenticated: true},
		{name: "Publish verified release", command: "release-publish", verifier: true, authenticated: true},
	} {
		var required []string
		if step.verifier {
			required = append(required,
				"        working-directory: .release-verifier\n",
				"          RELEASE_ARTIFACT_ROOT: ${{ github.workspace }}\n",
			)
		}
		if step.authenticated {
			required = append(required, "          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n")
		}
		if step.command == "release-promote" {
			required = append(required, "        if: ${{ !contains(github.ref_name, '-') }}\n")
		}
		assertWorkflowStep(t, workflow, step.name, "go run ./test/cmd/cictl "+step.command,
			required, []string{"run: |", "run: >", "shell:"}, "embedded shell program")
	}
	assertWorkflowStep(t, workflow, "Finalize shellless packages", "go -C tools run ./cmd/packagefinalize",
		nil, []string{"run: |", "run: >", "shell:"}, "embedded shell program")
	requireContractStrings(t, "GoReleaser workflow step", namedWorkflowStep(t, workflow, "Run GoReleaser"), []string{
		"        uses: goreleaser/goreleaser-action@",
		"          args: release --clean --parallelism=1 --release-notes=release-notes.md\n",
	})
	requireContractStrings(t, "immutable release verifier checkout",
		namedWorkflowStep(t, workflow, "Checkout immutable release verifier"), []string{
			"          ref: ${{ github.sha }}\n",
			"          path: .release-verifier\n",
			"          persist-credentials: false\n",
		})
	for _, forbidden := range []string{"--clobber", "--notes-file", " --notes "} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release workflow retains forbidden mutation marker %q", forbidden)
		}
	}
	previous := -1
	for _, name := range []string{
		"Refuse existing release, draft, or versioned OCI tag",
		"Extract release notes from CHANGELOG.md",
		"Verify merged release PR and bind its URL to release notes",
		"Run GoReleaser",
		"Finalize shellless packages",
		"Checkout immutable release verifier",
		"Record exact release asset matrix",
		"Upload exact release asset matrix",
		"Record exact release OCI evidence",
		"Download reports archive",
		"Attach reports to release",
		"Verify exact release draft",
		"Promote verified OCI aliases",
		"Publish verified release",
	} {
		marker := "      - name: " + name + "\n"
		position := strings.Index(workflow, marker)
		if count := strings.Count(workflow, marker); count != 1 || position <= previous {
			t.Errorf("release stage %q count/order = %d/%d after %d; want one later stage", name, count, position, previous)
		}
		previous = position
	}
	publication := strings.LastIndex(workflow, "run: go run ./test/cmd/cictl release-publish")
	if strings.LastIndex(workflow, "run: go run ./test/cmd/cictl release-") != publication {
		t.Error("publishing is not the final release command")
	}
	publicationStep := strings.LastIndex(workflow, "\n      - name: Publish verified release\n")
	if publicationStep < 0 || strings.LastIndex(workflow, "\n      - ") != publicationStep {
		t.Error("release publication is not the final workflow step")
	}
	if strings.Contains(namedWorkflowStep(t, workflow, "Publish verified release"), "\n        if:") {
		t.Error("release publication must follow successful verification for both stable and alpha channels")
	}
}

func topLevelYAMLSection(t *testing.T, configuration, name string) string {
	t.Helper()

	var section strings.Builder
	found := false
	for line := range strings.SplitSeq(configuration, "\n") {
		if !found {
			found = line == name+":"
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '#' {
			break
		}
		section.WriteString(line)
		section.WriteByte('\n')
	}
	if !found {
		t.Fatalf("top-level YAML section %q is missing", name)
	}
	return section.String()
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
