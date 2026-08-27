# GoBFD Release-Branch Versioning Design

- **Date:** 2026-08-27
- **Status:** Implemented; v0.6.4 published
- **Beads:** `gobfd-qj0.8.1.15`

**Scope:** v0.6 maintenance line and reusable release-branch policy

## Decision

GoBFD uses product-version release branches named
`release/vMAJOR.MINOR`. The first such branch is `release/v0.6`.

`release/v0.6` preserves GoBGP v3.37.0 and the v0.6 public contracts. It
remains available for reviewed critical correctness and security patches after
`dev` advances to v1 and GoBGP v4. The branch is not named after GoBGP because
dependencies are implementation details and may change within a compatible
product line.

The v0.6.2 Git tag was created from the exact qualified commit on
`release/v0.6`, but its workflow failed before a GitHub Release or assets were
created. The reviewed runner fix produced the immutable `v0.6.3` tag and a
complete draft, but fail-closed verification rejected its empty body before
publication. Both failed cuts remain unchanged. The release-notes fix produces
`v0.6.4` as the first publication from the maintained branch. A tag ruleset and
GitHub immutable releases enforce tag and asset identity after publication.
The branch remains the source line for later v0.6.x tags.

## Evidence and constraints

The live repository state checked on 2026-08-27 establishes these constraints:

- `master` is the protected default branch at merge commit
  `48ef04e158dbc923173f44932a4686747bf873ee`; the published immutable `v0.6.4`
  tag points to `b1c0bcd7d2e9abed00368b2082e34f521084c087` on `release/v0.6`.
- `dev` was fast-forwarded to the same reviewed release commit after
  publication; its push triggers no repository workflow.
- PR #63 merged the independently reviewed `dev` head
  `2e6335cb310f8654a74ca348916386a47cf33d87` after all 20 contexts completed.
- `.github/workflows/release.yml` runs only for pushed `v*` tags and creates the
  release from the tagged commit.
- active rulesets protect `master`, `release/v*`, and `v*`; the release branch
  ruleset was created before `release/v0.6`, and the tag ruleset was created
  before the first new matching tag, `v0.6.2`. Existing `v0.1.0` through
  `v0.6.1` tags predate that tag ruleset and remain unchanged.
- `ci.yml`, `security.yml`, and `e2e.yml` route pull requests into `release/v*`
  through the normal release gates.
- `build.yml` runs for every pull request, so its SonarQube check already covers
  a release-branch pull request.
- the Makefile's interactive Buf target defaults to `master`, while `ci.yml`
  already overrides it with the pull request's actual base branch. Release
  branch support must preserve that correct CI behavior rather than redesign
  the Buf gate.

GitHub documentation confirms that releases are based on tags, a new release
tag can target a branch or exact commit, and branch rulesets can target branch
name patterns. The repository continues to use its existing tag-triggered
GitHub Actions release workflow.

## Considered approaches

### 1. `release/v0.6` maintained product line — selected

Create one long-lived branch for the SemVer minor line. Cut `v0.6.2` and future
`v0.6.x` tags from reviewed commits on this branch.

Advantages:

- names the compatibility contract users consume rather than one dependency;
- supports critical and security backports after v1 development starts;
- matches SemVer patch-line ownership;
- generalizes to later supported lines such as `release/v1.0`.

Cost: CI filters, Buf comparison targets, rulesets, and forward-port policy must
be branch-aware.

### 2. `support/gobgp-v3` dependency line — rejected

This makes the branch identity depend on an internal component. It leaves the
GoBFD API/config compatibility boundary unclear and becomes misleading if a
compatible GoBGP v3 patch or another dependency changes. It does not establish
a reusable release policy.

### 3. Tags only or one branch per patch — rejected

Tags alone identify releases but provide no safe place for v0.6 backports once
`master` moves to v1. A separate `release/v0.6.2` branch for every patch creates
unnecessary permanent branches and does not identify the current head of the
maintained v0.6 line.

## Branch roles

| Ref | Role | Allowed content | Release source |
|---|---|---|---|
| `dev` | Integration for the next release line | Approved v1 and GoBGP v4 work | No |
| `master` | Protected default branch containing the latest accepted stable state | Reviewed integration or forward-ports | Only while it is the applicable release-line commit |
| `release/v0.6` | Maintained v0.6 compatibility line | Critical correctness, security, release engineering, and documentation fixes compatible with v0.6 | Yes, for `v0.6.x` |
| `fix/v0.6-*` | Short-lived patch branch | One reviewed v0.6 patch slice | No |
| `v0.6.x` | Immutable public identity | Exact qualified commit | GitHub Release trigger |

`dev` must not become a release source. A release candidate moves through the
normal reviewed path before a tag is created.

## Initial v0.6.2 cut

1. Complete the release-policy and release-preparation changes on an isolated
   branch and merge them into `dev` through review.
2. Re-run PR #63 checks after its final push and complete independent read-only
   review. While the author is the only eligible maintainer, keep the GitHub
   approval count at zero; do not merge while any required context or the
   independent review is incomplete.
3. Merge PR #63 into `master` without rewriting the published `dev` history.
   The merge method must preserve the reviewed commit ancestry.
4. Create and verify the `release/v*` branch ruleset before the new
   `release/v0.6` branch is created, and the `v*` tag ruleset before the new
   `v0.6.2` tag is created. Preserve the existing `v0.1.0` through `v0.6.1`
   tags unchanged. The tag ruleset restricts updates and deletions but does not
   restrict creation, so a new SemVer tag can be created once and cannot then
   be moved, deleted, or reused.
5. Create `release/v0.6` at the exact accepted v0.6.2 release commit. Record the
   commit SHA in Beads and release evidence, then verify that the pre-existing
   ruleset applies to the new branch.
6. Run the bounded local release preflight against that exact commit. Confirm
   the changelog contains a dated `0.6.2` section and that generated release
   notes are non-empty.
7. Create annotated tag `v0.6.2` at the same commit and push only that tag.
   Never move or reuse a published release tag.
8. Let `.github/workflows/release.yml` build into a draft GitHub Release. Attach
   the separately generated release report, verify the complete draft, and only
   then publish it. Verify the tag, GitHub Release, checksums, packages, OCI
   manifests, SBOM/provenance, and release reports before closing the
   milestone.

No tag is pushed while a required gate or independent review is incomplete. A failed
published release is fixed forward with a new patch version; its public tag is
not rewritten.

## Protection and CI contract

Create a repository ruleset for `release/v*` with at least the controls on
`master-protection`:

- pull request required;
- zero GitHub approving reviews while the PR author is the only eligible
  maintainer, with independent read-only review evidence required before merge;
- raise the GitHub approval count to one and dismiss stale approvals when a
  second eligible maintainer is active;
- force pushes and branch deletion prohibited;
- required status checks from the default branch plus `codeql`, `gosec`, and
  `PR-safe E2E`;
- no bypass actor added merely to cut a release.

Update pull-request branch filters so `ci.yml`, `security.yml`, and `e2e.yml`
run for `release/v*`. Keep scheduled work on the default branch unless a
release-specific schedule has an explicit need. Reconcile `master-protection`
to require the same security and PR-safe E2E checks so a failing normal gate is
not merely informational on either stable line. In particular, `gosec` must
return a failing exit status for findings, and the Trivy filesystem scan must
fail on HIGH or CRITICAL vulnerabilities while its SARIF upload remains an
`always()` evidence step.

Retain the existing CI behavior that passes `github.base_ref` to the Buf
breaking check. A pull request into `release/v0.6` therefore compares against
`release/v0.6`, not future v1 state. The local Makefile default remains
`master` for default-line development and can be overridden explicitly when a
maintainer validates a release worktree.

Required-check names must remain stable so both rulesets bind to checks that
actually run. Ruleset creation is verified through `gh api`; documentation is
not treated as proof of the live setting.

## Backport and forward-port policy

Every v0.6 patch starts from `release/v0.6` on `fix/v0.6-*` and reaches the
release branch through a reviewed pull request. Only changes compatible with
the v0.6 API, configuration, wire behavior, and GoBGP v3.37.0 boundary qualify.
Features and GoBGP v4 migration work remain on `dev`.

After a v0.6 fix is accepted, assess whether the defect exists on `master` or
`dev`:

- if present, forward-port it through a separate reviewed pull request;
- if absent or structurally replaced, record that conclusion in the Beads
  issue and do not create a no-op port;
- never merge future v1 work backward into `release/v0.6`.

This makes the release branch authoritative for v0.6 without allowing it to
silently fork untracked fixes from later lines.

## Versioning and changelog contract

- Public versions remain SemVer tags: `vMAJOR.MINOR.PATCH`.
- Release branch names contain only major and minor: `release/v0.6`.
- Patch releases increment only the patch component.
- Prereleases use the existing SemVer form, for example `v1.0.0-rc.1`; they do
  not create a new maintenance branch until that line is accepted for support.
- `CHANGELOG.md` on `release/v0.6` compares its Unreleased section with the
  latest v0.6 tag and links version sections to exact tags.
- Published changelog history is never rewritten. Later corrections are new
  entries in a new patch release.

## Immutable publication workflow

Enable GitHub immutable releases and protect `v*` tags before the first v0.6.2
tag is pushed. The release workflow must finish every mutation while the
release is a draft:

1. Configure GoReleaser `release.draft: true` and the documented release-notes
   mode `release.mode: keep-existing`. Before GoReleaser runs, require a
   canonical stable annotated SemVer tag that points directly to both the
   checked-out commit and the exact `release/vMAJOR.MINOR` branch head, then
   fail if any release, draft, or exact versioned OCI tag already exists for the cut.
   GoReleaser creates one new draft and uploads its artifacts and release
   notes. Existing-draft reuse and artifact or draft replacement remain
   disabled.
2. Publish only immutable versioned OCI refs. Download the already generated
   release-report artifact, checksum it together with the OCI receipt, and
   upload that evidence to the same draft without `--clobber`.
3. Query the draft through `gh api` and verify its tag, target commit, exact
   non-whitespace notes, exact asset names, checksums, SBOM/provenance,
   packages, and OCI results. Download the draft assets and verify their
   contents, not only their names. Require every declared GoReleaser artifact
   class, exactly one runnable descriptor for each supported OCI platform, and
   corresponding SPDX plus SLSA attestation evidence. Parse the actual
   platform-specific SPDX and current BuildKit SLSA v1 JSON payloads before
   alias promotion. Accept the default empty `builder.id` as a string and use
   the emitted `invocationId` field; predicate annotations alone are
   insufficient.
4. After all immutable evidence passes, promote the three mutable OCI aliases
   from the exact recorded index digests and verify them by digest. Publish the
   complete draft as the workflow's final GitHub Release mutation.

Remove the current post-publication `gh release upload --clobber` and release
notes edit. They are incompatible with immutable releases because publication
locks the tag and assets. A duplicate asset or incomplete draft fails closed
instead of replacing published evidence.

## Contract and documentation changes

Implementation updates:

- `AGENTS.md`: define branch roles, release-source rules, protection, backport,
  forward-port, and immutable-tag requirements;
- `.github/repository-settings.md`: document the live and required
  `release/v*` ruleset;
- `CONTRIBUTING.md`, `GOVERNANCE.md`, and EN/RU development and changelog
  guides: replace master-only contribution/release assumptions;
- EN/RU roadmaps: record v0.6 maintenance ownership and the v1 transition;
- CI workflows and Buf gate: make release-branch validation executable rather
  than documentation-only while retaining the existing PR-base comparison;
- GoReleaser and the release workflow: draft, attach, verify, then publish;
- repository rulesets/settings: protect `release/v*` branches and `v*` tags and
  enable immutable releases;
- Beads: keep release qualification and branch-policy evidence in
  `gobfd-qj0.8.1.15` and its relevant milestone dependencies.

## Validation

The implementation is accepted only with evidence that:

1. YAML and action syntax checks pass for every changed workflow.
2. A pull request targeting a `release/v*` test branch receives every required
   check name, or equivalent event/filter tests prove the routing before live
   branch creation.
3. `gh api` reports the intended branch and tag ruleset conditions,
   protections, and immutable-release setting.
4. `git merge-base --is-ancestor` proves the preserved v0.6.2 and v0.6.3 tag
   commits and the v0.6.4 recovery tag commit belong to `release/v0.6` and to
   reviewed stable history.
5. the release workflow extracts non-empty v0.6.4 recovery notes before the
   recovery tag push.
6. bounded local Go race/build, vulnerability, Buf, documentation, workflow,
   and release-dry-run gates pass on the exact release commit.
7. a release workflow contract check proves that no asset upload or release
   note edit follows publication.
8. post-publication GitHub API and registry queries resolve v0.6.4 assets and
   images to the recovery tag and expected immutable digests, while v0.6.2 and
   v0.6.3 remain unchanged as failed cuts.

Go specification and gopls checks remain mandatory when Go source or build
constraints change. This branch-policy slice does not invent a Go-language
change merely to exercise those tools; the release qualification still runs
the repository's normal Go gates against non-empty package input.

## Rollback and failure handling

Before a public tag exists, release preparation can be corrected through new
reviewed commits. No force-push or history rewrite is used.

After any v0.6.x release is published, its tag and assets are immutable release
evidence. A defect is handled on `fix/v0.6-*` and released as the next patch;
the previous tag is not moved, deleted, or reused. If a transient workflow step fails before
GoReleaser creates the draft, a rerun first proves that no release, draft, or
exact versioned OCI tag exists for the cut. Once a draft, release asset, or
versioned OCI tag exists, do not silently reuse, delete, or replace it: record
the failed cut for maintainer analysis and fix forward with the next patch
version. If the tagged workflow itself is defective, do not rewrite the tag. If
a ruleset or workflow filter is wrong, tag creation pauses until the live
configuration and a PR check run prove the repair.

### Observed failed-cut recovery

The annotated `v0.6.2` tag was created at
`48ef04e158dbc923173f44932a4686747bf873ee` after its exact-commit preflight.
Workflow run `33083358370` then failed in the test job before GoReleaser,
draft creation, asset upload, or OCI publication. The release test job wrote
smoke-build binaries into the checkout and lacked the repository-required uv,
Podman, and checksum-pinned Compose provider; the reports job shared the
Podman gap. These are workflow-environment defects, so rerunning the immutable
tag cannot correct them. The tag remains unchanged, the minimal runner parity
fix enters through `fix/v0.6-release-workflow`, and the recovery release is
`v0.6.3` as required by the fix-forward policy above.

The `v0.6.3` runner remediation passed its complete local qualification. Its
third workflow attempt retained successful test and lint jobs, completed the
reports and GoReleaser stages, created all expected draft assets and versioned
OCI manifests, and then failed closed because the draft body did not match the
non-empty extracted notes. GoReleaser v2.18 does not load `--release-notes`
when `changelog.disable` skips the changelog pipe. The draft was never
published, mutable aliases were not promoted, and the immutable tag is not
reused. The one-line changelog-pipe correction enters through
`fix/v0.6-release-notes`; the next recovery release is `v0.6.4`.

### Published recovery result

Release run `33101133019` completed successfully on its second attempt after
the sole transient OSV download failure was reproduced successfully with a
resource-bounded local audit. The immutable annotated `v0.6.4` tag points to
`b1c0bcd7d2e9abed00368b2082e34f521084c087`. GitHub Release `378036743` is
published, non-prerelease, and immutable; its exact CHANGELOG body, 12 assets,
checksums, two CycloneDX SBOMs, report archive, and API asset digests were
verified independently. The Debian Trixie index digest is
`sha256:6deccf688ef862da35c35fa3c8343ed6499fbf0d31d4b58226786dedc8b911e1` and
the Oracle Linux 10 index digest is
`sha256:eae22d81c6c8c31b9e7c969456203621d1f75b6fa1f17845b868208dbe4b4cef`;
both contain linux/amd64 and linux/arm64 manifests with linked attestations.

This design's release-line acceptance is complete. The broader maintenance
milestone remains open on separately tracked P1 review findings
`gobfd-qj0.8.1.8.8`, `.8.9`, and `.8.10`; they are not silently treated as
resolved by publication.
