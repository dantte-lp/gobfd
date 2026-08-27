# GoBFD v0.6 Maintenance Line Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish protected `release/v0.6` maintenance ownership for GoBGP v3.37.0 and publish v0.6.2 from its exact reviewed commit without blocking future v1/GoBGP v4 development on `dev`.

**Architecture:** Repository-owned contract tests make branch filters and draft-first immutable publication fail closed. The policy implementation first reaches `dev` and PR #63, then the reviewed stable commit reaches `master`; only afterward do GitHub rulesets, immutable-release settings, `release/v0.6`, and `v0.6.2` get created in that order. Later v0.6 fixes enter through short-lived `fix/v0.6-*` branches and are forward-ported separately when applicable.

**Tech Stack:** Git and Git worktrees, GitHub Actions, GitHub REST/GraphQL through `gh api`, GoReleaser v2.18.0, Go 1.27.0 contract tests, Buf, gopls v0.23.0, Beads.

---

## File map

| File | Responsibility |
|---|---|
| `scripts/repo_quality_contract_test.go` | Executable branch-filter and immutable-release workflow contract |
| `.github/workflows/ci.yml` | Routine required checks for default and release PRs |
| `.github/workflows/security.yml` | CodeQL and gosec routing for release PRs |
| `.github/workflows/e2e.yml` | PR-safe E2E routing for release PRs |
| `.github/workflows/release.yml` | Extract notes, build a draft, attach reports, verify, publish last |
| `.goreleaser.yml` | Keep GitHub Release as a draft until the workflow finishes validation |
| `AGENTS.md` | Maintainer-enforced branch, release, backport, and tag contract |
| `.github/repository-settings.md` | Document intended and live rulesets and immutable-release state |
| `CONTRIBUTING.md` | Contributor source/target branch rules |
| `GOVERNANCE.md` | Maintainer release authority and supported-line ownership |
| `docs/en/09-development.md`, `docs/ru/09-development.md` | Bilingual development and backport workflow |
| `docs/en/10-changelog.md`, `docs/ru/10-changelog.md` | Bilingual release-branch and tag procedure |
| `docs/en/roadmap.md`, `docs/ru/roadmap.md` | v0.6 maintenance versus v1 development boundary |
| `CHANGELOG.md` | Dated v0.6.2 release notes and comparison links |
| `docs/superpowers/specs/2026-08-27-release-branch-versioning-design.md` | Approved design source of truth |
| `docs/superpowers/plans/2026-08-27-release-v06-maintenance-line.md` | Executable implementation checklist |

Do not add a new helper, test file, dependency, or tool. Extend the existing
repository-quality contract test only where it directly guards this release
slice.

### Task 1: Make release-branch workflow routing executable

**Files:**
- Modify: `scripts/repo_quality_contract_test.go`
- Modify: `.github/workflows/ci.yml:3-7`
- Modify: `.github/workflows/security.yml:3-7`
- Modify: `.github/workflows/e2e.yml:3-5`

- [ ] **Step 1: Add the failing branch-filter contract**

Before editing the Go test, check the Go 1.27 language specification sections
for range clauses and string literals at <https://go.dev/ref/spec>. The test
must use only APIs and syntax admitted by the module's exact Go 1.27 baseline.

Add a focused test beside `TestRepositoryQualityGatesHaveNoNodeRuntime`:

```go
func TestReleaseBranchesReceiveRequiredWorkflows(t *testing.T) {
	t.Parallel()

	for workflow, wantOccurrences := range map[string]int{
		"../.github/workflows/ci.yml":      2,
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
```

- [ ] **Step 2: Prove the test fails on the current branch filters**

Run:

```bash
GOMAXPROCS=4 GOMEMLIMIT=8GiB go test -race -count=1 ./scripts \
  -run '^TestReleaseBranchesReceiveRequiredWorkflows$'
```

Expected: FAIL for `ci.yml`, `security.yml`, and `e2e.yml`.

- [ ] **Step 3: Add `release/v*` only to relevant pull-request filters**

Use this exact flow-style value in all three files:

```yaml
branches: [master, main, "release/v*"]
```

Also add it to the `push` filters of `ci.yml` and `security.yml`, because the
stable release branch must receive post-merge evidence. Do not broaden
scheduled or manual E2E behavior.

- [ ] **Step 4: Prove the focused contract passes**

Run the Step 2 command again.

Expected: PASS with one non-empty Go package tested.

- [ ] **Step 5: Validate workflow syntax without starting remote jobs**

Run:

```bash
uv run --frozen --no-default-groups --group quality -- \
  yamllint -c .yamllint.yaml .github/workflows/ci.yml \
  .github/workflows/security.yml .github/workflows/e2e.yml
git diff --check
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit the routing slice**

```bash
git add scripts/repo_quality_contract_test.go .github/workflows/ci.yml \
  .github/workflows/security.yml .github/workflows/e2e.yml
git commit -m "ci(release): validate maintained release branches"
```

### Task 2: Make immutable release publication draft-first

**Files:**
- Modify: `scripts/repo_quality_contract_test.go`
- Modify: `.goreleaser.yml:65-66`
- Modify: `.github/workflows/release.yml:180-232`

- [ ] **Step 1: Add the failing immutable-publication contract**

Extend `scripts/repo_quality_contract_test.go` with a test that reads
`.goreleaser.yml` and `release.yml` and requires these markers:

```go
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
		"release-image-digests.txt",
		"gh release edit \"$GITHUB_REF_NAME\" --draft=false",
	})
	for _, forbidden := range []string{"--clobber", "--notes-file", " --notes "} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release workflow retains forbidden mutation marker %q", forbidden)
		}
	}
	upload := strings.LastIndex(workflow, "gh release upload \"$GITHUB_REF_NAME\"")
	verification := strings.LastIndex(workflow, "Verify exact release draft")
	publication := strings.LastIndex(workflow, "gh release edit \"$GITHUB_REF_NAME\" --draft=false")
	if upload < 0 || verification < upload || publication < verification {
		t.Error("release ordering is not upload, exact verification, then publication")
	}
	if strings.LastIndex(workflow, "gh release ") != publication {
		t.Error("publishing is not the final gh release mutation")
	}
}
```

- [ ] **Step 2: Prove the immutable-publication contract fails**

Run:

```bash
GOMAXPROCS=4 GOMEMLIMIT=8GiB go test -race -count=1 ./scripts \
  -run '^TestReleasePublishesVerifiedDraftLast$'
```

Expected: FAIL because `draft: true` and final publish are absent and
`--clobber` is present.

- [ ] **Step 3: Configure GoReleaser to retain a draft**

Change the release block to:

```yaml
release:
  draft: true
  mode: keep-existing
```

Do not enable `use_existing_draft`, `replace_existing_draft`, or
`replace_existing_artifacts`. Before the GoReleaser action, add a named
`Refuse existing release, draft, or versioned OCI tag` step. Query
`repository.release(tagName:)` through `gh api graphql` so both drafts and
published releases are visible. Require a non-null repository and an explicit
null release result; any GitHub, GraphQL, or JSON error is fatal.

Then query every GHCR package-version page through:

```bash
existing_tags="$(gh api --paginate \
  '/users/dantte-lp/packages/container/gobfd/versions?per_page=100' \
  --slurp | jq -r 'flatten | .[].metadata.container.tags[]?' | LC_ALL=C sort -u)"
version="${GITHUB_REF_NAME#v}"
for tag in "$version" "$version-debian-trixie" "$version-oraclelinux10"; do
  if grep -Fxq "$tag" <<<"$existing_tags"; then
    echo "versioned OCI tag already exists: $tag" >&2
    exit 1
  fi
done
```

An API error is fatal. This makes partial GitHub or GHCR publication fail closed
instead of pretending a duplicate upload is idempotent.

- [ ] **Step 4: Fail closed when changelog notes are absent**

In `release.yml`, replace the fallback note generation with a non-zero exit.
Require at least one non-whitespace character. The tag must have an exact dated
section in `CHANGELOG.md`; a generic link to `master` is not a release note.

- [ ] **Step 5: Build the exact asset manifest and OCI digest receipt**

Peel the remote tag through the GitHub Git API and compare it with
`git rev-parse HEAD`. Query each released OCI reference with
`docker buildx imagetools inspect --raw`, require both `linux/amd64` and
`linux/arm64`, and write the calculated index SHA-256 values to
`release-image-digests.txt`.

Build `expected-release-assets.txt` from `dist/artifacts.json` by selecting the
GoReleaser artifact types `Archive`, `Linux Package`, `Checksum`, and `SBOM`,
first requiring that every one of those four classes is present. Then append
the exact report archive and OCI digest receipt names. Normalize it with
`LC_ALL=C sort -u`.

Use this fail-closed outline:

```bash
expected_commit="$(git rev-parse HEAD)"
tag_ref="$(gh api "repos/${GITHUB_REPOSITORY}/git/ref/tags/${GITHUB_REF_NAME}")"
tag_type="$(jq -r '.object.type' <<<"$tag_ref")"
tag_sha="$(jq -r '.object.sha' <<<"$tag_ref")"
if [ "$tag_type" = tag ]; then
  tag_object="$(gh api "repos/${GITHUB_REPOSITORY}/git/tags/${tag_sha}")"
  tag_type="$(jq -r '.object.type' <<<"$tag_object")"
  tag_sha="$(jq -r '.object.sha' <<<"$tag_object")"
fi
test "$tag_type" = commit
test "$tag_sha" = "$expected_commit"

jq -r '
  .[] |
  select(.type == "Archive" or .type == "Linux Package" or
    .type == "Checksum" or .type == "SBOM") |
  .path | split("/")[-1]
' dist/artifacts.json >"$RUNNER_TEMP/expected-release-assets.txt"
printf 'gobfd-%s-reports.tar.gz\nrelease-image-digests.txt\n' \
  "$GITHUB_REF_NAME" >>"$RUNNER_TEMP/expected-release-assets.txt"
LC_ALL=C sort -u -o "$RUNNER_TEMP/expected-release-assets.txt" \
  "$RUNNER_TEMP/expected-release-assets.txt"
```

For each of
`ghcr.io/dantte-lp/gobfd:${GITHUB_REF_NAME#v}-debian-trixie` and
`ghcr.io/dantte-lp/gobfd:${GITHUB_REF_NAME#v}-oraclelinux10`, save the raw
manifest, exclude only descriptors explicitly annotated as
`attestation-manifest`, and require the remaining sorted descriptor list to
equal exactly one `linux/amd64` and one `linux/arm64` line. Do not deduplicate
the list because duplicate runnable descriptors must fail. Record `sha256sum`
of the raw bytes with the full image reference and require exactly two receipt
lines.

- [ ] **Step 6: Attach, verify the complete draft, and publish last**

Retain the report download. Upload both the report and the completed OCI digest
receipt without replacement:

```bash
gh release upload "$GITHUB_REF_NAME" \
  gobfd-*-reports.tar.gz release-image-digests.txt
```

Remove the post-GoReleaser release-notes edit because GoReleaser already
receives `--release-notes=release-notes.md` while the release is a draft.

Sort the names returned by `gh release view ... --json assets` and require an
exact `diff` against `expected-release-assets.txt`, not a subset. The named
workflow step must be `Verify exact release draft`. It additionally requires a
non-whitespace body exactly equal to `release-notes.md` after shell
trailing-newline normalization, the exact tag, the already verified peeled
commit, every required artifact class, and both recorded multi-platform image
digests.

The final outline is:

```bash
release_json="$(gh release view "$GITHUB_REF_NAME" \
  --json isDraft,tagName,body,assets)"
jq -e --arg tag "$GITHUB_REF_NAME" '
  .isDraft == true and
  .tagName == $tag and
  (.body | length > 0) and
  ([.assets[].name] | any(endswith(".sbom.json")))
' <<<"$release_json"
jq -r '.assets[].name' <<<"$release_json" | LC_ALL=C sort -u \
  >"$RUNNER_TEMP/actual-release-assets.txt"
diff -u "$RUNNER_TEMP/expected-release-assets.txt" \
  "$RUNNER_TEMP/actual-release-assets.txt"
gh release edit "$GITHUB_REF_NAME" --draft=false --latest
```

The `gh release edit --draft=false` command is the final release mutation.

- [ ] **Step 7: Run focused and syntax gates**

```bash
GOMAXPROCS=4 GOMEMLIMIT=8GiB go test -race -count=1 ./scripts \
  -run '^(TestReleasePublishesVerifiedDraftLast|TestRepositoryQualityGatesHaveNoNodeRuntime)$'
uv run --frozen --no-default-groups --group quality -- \
  yamllint -c .yamllint.yaml .github/workflows/release.yml .goreleaser.yml
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 8: Commit the immutable release slice**

```bash
git add scripts/repo_quality_contract_test.go .goreleaser.yml \
  .github/workflows/release.yml
git commit -m "ci(release): publish verified immutable drafts"
```

### Task 3: Update the repository release contract and bilingual documentation

**Files:**
- Modify: `AGENTS.md`
- Modify: `.github/repository-settings.md`
- Modify: `CONTRIBUTING.md`
- Modify: `GOVERNANCE.md`
- Modify: `docs/en/09-development.md`
- Modify: `docs/ru/09-development.md`
- Modify: `docs/en/10-changelog.md`
- Modify: `docs/ru/10-changelog.md`
- Modify: `docs/en/roadmap.md`
- Modify: `docs/ru/roadmap.md`
- Modify: `docs/superpowers/specs/2026-08-27-release-branch-versioning-design.md`

- [ ] **Step 1: Add the normative branch roles to `AGENTS.md`**

Add one `## Release branches` section that states:

- `dev` integrates the next line and is never tagged for a stable release;
- `master` is the latest accepted stable/default state;
- supported lines use `release/vMAJOR.MINOR`;
- `release/v0.6` retains GoBGP v3.37.0 and v0.6 public contracts;
- fixes use `fix/vMAJOR.MINOR-*`, enter through reviewed PRs, and are
  forward-ported separately when applicable;
- the release ruleset exists before each new matching branch, and the tag
  ruleset exists before each new matching tag, specifically before `v0.6.2`;
  existing `v0.1.0` through `v0.6.1` tags remain unchanged;
- a tag points to the exact reviewed release-branch commit and is never moved,
  deleted, or reused;
- draft assets are complete and verified before immutable publication.

- [ ] **Step 2: Reconcile repository settings documentation**

Separate `Current Protection State` from `Required Settings`. Before the live
API mutation, identify `release-protection`, `release-tags`, and immutable
releases as pending. List the exact required status contexts:

```text
Build & test
Lint (Go)
Vulnerability audit
Buf
SonarQube
Trivy filesystem scan
Lint (docs)
Commit policy (PR title)
codeql
gosec
PR-safe E2E
```

Do not claim a pending ruleset is already live.

- [ ] **Step 3: Update contributor and governance rules**

Change the master-only feature-branch instruction in `CONTRIBUTING.md`:

- features start from `dev` and target `dev`;
- v0.6 maintenance fixes start from and target `release/v0.6`;
- release preparation follows the applicable supported line.

In `GOVERNANCE.md`, record the supported-line model, immutable draft-first
authority, and required forward-port assessment.

- [ ] **Step 4: Update EN/RU development and changelog guides in lockstep**

Document the same branch roles, patch workflow, tag source, no-rewrite policy,
and GitHub Actions draft publication in both languages. Replace the current
`git push origin master --tags` example with separate, explicit branch and tag
pushes that cannot publish unrelated local tags.

- [ ] **Step 5: Update both roadmaps without claiming publication**

State that v0.6.x is maintained on `release/v0.6` with GoBGP v3.37.0 and that
v1/GoBGP v4 development follows on `dev`. Keep v0.6.2 `In progress` until the
tag and GitHub Release are verified.

- [ ] **Step 6: Reconcile the approved design metadata**

Add the missing blank line after its metadata block and change its status to
`Approved; implementation in progress`. Do not change the accepted decision.

- [ ] **Step 7: Run documentation gates**

```bash
go run ./test/cmd/repoquality markdown --root .
uv run --frozen --no-default-groups --group quality -- codespell \
  AGENTS.md CONTRIBUTING.md GOVERNANCE.md .github/repository-settings.md \
  docs/en/09-development.md docs/ru/09-development.md \
  docs/en/10-changelog.md docs/ru/10-changelog.md \
  docs/en/roadmap.md docs/ru/roadmap.md \
  docs/superpowers/specs/2026-08-27-release-branch-versioning-design.md
git diff --check
```

Expected: 0 diagnostics.

- [ ] **Step 8: Commit the contract slice**

```bash
git add AGENTS.md .github/repository-settings.md CONTRIBUTING.md GOVERNANCE.md \
  docs/en/09-development.md docs/ru/09-development.md \
  docs/en/10-changelog.md docs/ru/10-changelog.md \
  docs/en/roadmap.md docs/ru/roadmap.md \
  docs/superpowers/specs/2026-08-27-release-branch-versioning-design.md
git commit -m "docs(release): define v0.6 maintenance ownership"
```

### Task 4: Prepare the exact v0.6.2 changelog section

**Files:**
- Modify: `CHANGELOG.md:8-135`
- Modify: `CHANGELOG.ru.md:8-134`
- Test: `.github/workflows/release.yml` release-note extraction command

- [ ] **Step 1: Convert current Unreleased content into v0.6.2**

Keep an empty `## [Unreleased]` at the top. Add:

```markdown
## [0.6.2] - 2026-08-27
```

Place every current v0.6.2 entry under that heading without rewriting earlier
published sections.

- [ ] **Step 2: Update comparison links**

Use:

```markdown
[Unreleased]: https://github.com/dantte-lp/gobfd/compare/v0.6.2...HEAD
[0.6.2]: https://github.com/dantte-lp/gobfd/compare/v0.6.1...v0.6.2
```

Use the same targets under the localized `[Не выпущено]` and `[0.6.2]`
labels in `CHANGELOG.ru.md`. Leave every existing published link unchanged.

- [ ] **Step 3: Exercise the workflow's exact extraction logic locally**

Run the `awk` program from `.github/workflows/release.yml` with
`VERSION=0.6.2` into a `mktemp -d` directory. Assert the result is non-empty,
contains `### Changed`, and contains no `## [0.6.1]` heading. Remove only that
exact temporary directory after the assertions.

Expected: all assertions pass and the extracted file contains only v0.6.2
notes.

- [ ] **Step 4: Run changelog quality gates**

```bash
go run ./test/cmd/repoquality markdown --root .
uv run --frozen --no-default-groups --group quality -- codespell CHANGELOG.md
git diff --check
```

Expected: 0 diagnostics.

- [ ] **Step 5: Commit release preparation**

```bash
git add CHANGELOG.md
git commit -m "chore(release): prepare v0.6.2"
```

### Task 5: Qualify the implementation branch locally

**Files:**
- Verify only; do not add unrelated fixes
- Update: Beads `gobfd-qj0.8.1.15`

- [ ] **Step 1: Verify non-empty Go package input and the Go toolchain**

```bash
go version
go list ./... | tee /tmp/gobfd-release-v06-packages.txt
test -s /tmp/gobfd-release-v06-packages.txt
```

Expected: Go 1.27.0 and a non-empty package list. Remove the exact temporary
file after recording its line count in Beads.

- [ ] **Step 2: Run bounded local race and contract tests**

```bash
GOMAXPROCS=4 GOMEMLIMIT=8GiB go test -p=4 ./... -race -count=1
```

Expected: all discovered packages pass.

- [ ] **Step 3: Run gopls against the changed Go contract test and repository profiles**

This slice modifies `scripts/repo_quality_contract_test.go`, and the maintainer
explicitly requires gopls. Use the pinned development container or the already
pinned local tool with resource limits:

```bash
GOMAXPROCS=4 GOMEMLIMIT=8GiB sh ./scripts/gopls-check.sh
GOMAXPROCS=4 GOMEMLIMIT=8GiB GOPLS_GOOS=darwin \
  GOPLS_TAGS=dependencyinventory_generate sh ./scripts/gopls-check.sh
```

Expected: non-zero package/input counts and zero gopls diagnostics.

- [ ] **Step 4: Run build, configuration, and release snapshot gates**

Run the four-binary build first:

```bash
GOMAXPROCS=4 GOMEMLIMIT=8GiB make -o dev-ensure EXEC= build
```

This invokes the existing four-binary target with the local exact Go 1.27.0
toolchain and resource limits without implicitly building or starting the much
larger Compose development image.

Obtain the repository-pinned GoReleaser v2.18.0 archive and `checksums.txt`
from its GitHub Release into an exact `mktemp -d` directory. Verify only the
selected archive line with `sha256sum --check`, extract the binary there, and
record `goreleaser --version`. Do not use the stale host v2.15.4 binary. Obtain
Syft v1.51.0 the same way and put only that checksum-verified binary on the
temporary `PATH`.

The release configuration enables UPX, which GoReleaser otherwise silently
skips when the command is absent. Download the upstream
`upx-4.2.2-amd64_linux.tar.xz` release asset and require SHA-256
`915c8e844f835de03b9cc311ff185aedec79d757aee9d7133a528b9e89c463bb` before
extraction. Require `upx --version` to report 4.2.2 so the local snapshot
matches the exact fail-closed runner prerequisite.

Run:

```bash
GOMAXPROCS=4 GOMEMLIMIT=8GiB "$RELEASE_TOOL_DIR/goreleaser" check
GOMAXPROCS=4 GOMEMLIMIT=8GiB "$RELEASE_TOOL_DIR/goreleaser" release \
  --snapshot --clean --skip=publish,docker --parallelism=1
```

Expected: config validation, four binary builds, archives, checksums, SBOMs,
DEB/RPM packaging, and `dist/artifacts.json` succeed without publishing.

Use the snapshot output to build both release Containerfiles for local amd64
with Podman under collision-free labels and tags derived from the exact commit.
Require both tags to be absent before the build, record the complete pre-build
image-ID set, and capture each result through a unique `--iidfile`. Verify the
tag, ID, ownership label, architecture, user, entrypoint, and base before
cleanup. Remove only the two new tags with `podman image rm --no-prune` and
without `--force` when their result IDs were absent from the pre-build set. If
a collision-free tag resolves to an ID that already existed, remove only that
new tag with `podman untag` and preserve the image. Pass
`--default-mounts-file=/dev/null` to isolate build steps from host subscription
mounts. In particular, a host certificate mounted directly at
`/etc/ssl/certs/ca-certificates.crt` prevents Debian's `ca-certificates` package
from atomically replacing that path and is not part of the GitHub release
environment. This covers Debian Trixie and Oracle Linux 10 locally without
creating a remote manifest or using Alpine.

- [ ] **Step 5: Run remaining release-relevant existing gates**

```bash
go mod tidy -diff
go mod verify
go run ./scripts/vuln-audit.go
"$BUF_TOOL_DIR/buf" lint
go run ./test/cmd/repoquality markdown --root .
uv run --frozen --no-default-groups --group quality -- \
  yamllint -c .yamllint.yaml .github/workflows .goreleaser.yml
git diff --check
git status --short --branch
```

Expected: all gates pass and the worktree is clean. Do not run a new interop
topology because this slice changes no protocol or container behavior.

Obtain Buf v1.72.0 and its `sha256.txt` from the upstream GitHub Release in an
exact temporary directory and verify only the selected Linux x86-64 artifact
before using `BUF_TOOL_DIR`. Do not use a stale host Buf binary.

- [ ] **Step 6: Record qualification in Beads**

Append exact commands, versions, package counts, commit SHAs, and results to
`gobfd-qj0.8.1.15`. Keep the task `IN_PROGRESS` because remote review, rulesets,
branch creation, and publication remain.

### Task 6: Independently review and integrate into `dev`

**Files:**
- Review the complete `dev..docs/release-v06-policy` diff
- Update: local and remote `dev`
- Update: PR #63 through its existing `dev` head

- [ ] **Step 1: Run independent code/spec review on the exact head**

Review the workflow ordering, shell quoting, required check names, EN/RU parity,
and changelog extraction. Reject P0/P1 defects before merge. Do not broaden the
review into unrelated project improvements.

The independent review must also confirm that required security contexts are
blocking: `gosec` runs without `-no-fail`, Trivy uses `exit-code: "1"` for its
HIGH/CRITICAL filesystem scan, and both SARIF uploads retain `if: always()`.

- [ ] **Step 2: Merge locally into `dev` without rewriting history**

From the root worktree, verify both worktrees are clean, then:

```bash
git switch dev
git merge --no-ff docs/release-v06-policy \
  -m "merge: establish v0.6 maintenance line"
```

Expected: one merge commit and no unresolved paths.

- [ ] **Step 3: Run focused post-merge checks**

Repeat Task 5 Steps 2–5 on the exact merge commit. Record the SHA.

- [ ] **Step 4: Push only `dev` and inspect PR #63 through GitHub API**

```bash
git push origin dev
gh pr view 63 --repo dantte-lp/gobfd \
  --json headRefOid,mergeStateStatus,reviewDecision,statusCheckRollup,reviews
```

Expected: PR head equals local `dev`; new checks start for that exact SHA.

- [ ] **Step 5: Wait for every required check and current approval**

Use `gh api graphql` to verify exhaustive check and review state. Do not merge
while any required check is pending/failing or while `reviewDecision` is not
`APPROVED`. A stale approval from an earlier SHA is not evidence.

### Task 7: Merge the accepted stable commit and establish live protections

**Files:**
- Update: GitHub PR #63 and `master`
- Update: GitHub rulesets and immutable-release setting through `gh api`
- Modify after live verification: `.github/repository-settings.md`
- Update: Beads `gobfd-qj0.8.1.15`

- [ ] **Step 1: Merge PR #63 only after the Task 6 gate**

Use a merge commit so reviewed `dev` ancestry remains visible. Fetch and verify
that `origin/master` contains the reviewed PR head.

- [ ] **Step 2: Reconcile `master-protection` with the declared checks**

Read the live ruleset first. Update it through `gh api` to add deletion and
non-fast-forward protection and the exact required contexts listed in Task 3.
Preserve its PR-review policy and do not add bypass actors.

- [ ] **Step 3: Create `release-protection` before the branch**

POST a branch ruleset matching `refs/heads/release/v*` with:

- deletion and non-fast-forward protection;
- one approving PR review with stale approvals dismissed;
- the exact Task 3 required status contexts;
- no bypass actors.

Build the payload with `jq -n` and pipe it directly to
`gh api --method POST repos/dantte-lp/gobfd/rulesets --input -`. Its normalized
shape must be:

```json
{
  "name": "release-protection",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/release/v*"],
      "exclude": []
    }
  },
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": false,
        "require_extra_approval_for_unattributed_changes": true,
        "allowed_merge_methods": ["merge", "squash", "rebase"],
        "required_reviewers": []
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": false,
        "do_not_enforce_on_create": true,
        "required_status_checks": [
          {"context": "Build & test"},
          {"context": "Lint (Go)"},
          {"context": "Vulnerability audit"},
          {"context": "Buf"},
          {"context": "SonarQube"},
          {"context": "Trivy filesystem scan"},
          {"context": "Lint (docs)"},
          {"context": "Commit policy (PR title)"},
          {"context": "codeql"},
          {"context": "gosec"},
          {"context": "PR-safe E2E"}
        ]
      }
    }
  ]
}
```

`do_not_enforce_on_create: true` is limited to bootstrap of a new matching
branch. It does not bypass checks on later updates.

Read it back through `gh api` and compare its normalized JSON to the intended
policy.

- [ ] **Step 4: Create `release-tags` before the new v0.6.2 tag**

POST a tag ruleset matching `refs/tags/v*` that blocks deletion and
all updates without blocking creation of a new SemVer tag. Read it back and
verify the exact condition. Its rules contain only `deletion` and `update`; do
not add a `creation` rule. Preserve existing `v0.1.0` through `v0.6.1` tags
unchanged. The normalized payload is:

```json
{
  "name": "release-tags",
  "target": "tag",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {
    "ref_name": {
      "include": ["refs/tags/v*"],
      "exclude": []
    }
  },
  "rules": [
    {"type": "deletion"},
    {"type": "update"}
  ]
}
```

- [ ] **Step 5: Enable immutable releases through the documented endpoint**

```bash
gh api --method PUT repos/dantte-lp/gobfd/immutable-releases
gh api repos/dantte-lp/gobfd/immutable-releases
```

Expected GET result: `enabled: true`.

- [ ] **Step 6: Update settings documentation with live IDs and state**

On a short-lived documentation branch from the accepted stable commit, replace
pending markers in `.github/repository-settings.md` with verified ruleset IDs,
conditions, required checks, and immutable-release state. Merge it through the
normal reviewed path before tagging; do not claim unverified settings.

### Task 8: Create `release/v0.6`, tag, and publish v0.6.2

**Files:**
- Create remote branch: `release/v0.6`
- Create annotated tag: `v0.6.2`
- Update: GitHub Release and GHCR through the approved workflow
- Update: Beads milestones after evidence

- [ ] **Step 1: Resolve the exact release commit**

Fetch `master`, resolve the accepted commit containing the policy/settings
documentation, and verify it is descended from the reviewed PR #63 head. Store
the full SHA in Beads before creating refs.

- [ ] **Step 2: Create and push the maintained branch**

Create local `release/v0.6` at the exact SHA, push only that branch, then use
`gh api` to verify the remote ref and that `release-protection` applies. Do not
create the branch in `/root`; any worktree for it belongs under repository
`.worktrees/`.

- [ ] **Step 3: Re-run exact-commit release preflight**

Create `.worktrees/release-v0.6` from the existing branch. Run Task 5 against
that exact commit and repeat the release-note extraction. Stop on any failure.

- [ ] **Step 4: Create and push only the annotated v0.6.2 tag**

```bash
git tag -a v0.6.2 <FULL_RELEASE_SHA> -m "Release v0.6.2"
git push origin refs/tags/v0.6.2
```

Verify the peeled tag commit equals `release/v0.6` and that the tag ruleset is
active. Do not use `--tags`.

- [ ] **Step 5: Monitor the release workflow without consuming unrelated CI**

Use `gh run list`, `gh run view`, and `gh api` for the tag-triggered run. Do not
start duplicate workflows. The release must remain a draft until all assets and
the report are verified, then become published and immutable.

- [ ] **Step 6: Verify public release evidence**

Through GitHub API and GHCR, verify:

- tag and release target the recorded full SHA;
- release is non-draft and non-prerelease;
- notes are the v0.6.2 changelog section;
- checksums, archives, DEB/RPM packages, SBOMs, and report archive exist;
- Debian Trixie and Oracle Linux 10 manifests exist for the exact v0.6.2 tags;
- immutable releases remains enabled.

- [ ] **Step 7: Close release tracking only after evidence is complete**

Update `gobfd-qj0.8.1.15`, the v0.6.2 qualification task, and milestone with
exact SHAs, run IDs, asset/digest evidence, and any time-bounded advisory
exception. Close only tasks whose acceptance criteria are fully satisfied.

- [ ] **Step 8: Clean only owned worktrees and transient resources**

After all commits are integrated and refs verified, remove the clean
`docs-release-v06-policy` and `release-v0.6` worktrees through
`git worktree remove` after resolving and checking each exact path and branch.
Do not run repository-wide `git worktree prune`; report any unrelated stale
entry instead. Do not delete branches, tags, unrelated caches, containers,
images, or volumes.
