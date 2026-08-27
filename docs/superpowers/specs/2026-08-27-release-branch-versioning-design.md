# GoBFD Release-Branch Versioning Design

- **Date:** 2026-08-27
- **Status:** Approved; implementation in progress
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

The v0.6.2 Git tag and GitHub Release are created from the exact qualified
commit on `release/v0.6`. A tag ruleset and GitHub immutable releases enforce
the tag and asset identity after publication. The branch is the maintained
source line from which later v0.6.x tags are cut.

## Evidence and constraints

The live repository state checked on 2026-08-27 establishes these constraints:

- `master` is the protected default branch and currently contains v0.6.1.
- PR #63 proposes `dev` into `master`. All reported checks are successful, but
  the PR still requires an approving review.
- `.github/workflows/release.yml` runs only for pushed `v*` tags and creates the
  release from the tagged commit.
- the active `master-protection` ruleset targets only the default branch;
  `release/v*` is not currently protected.
- `ci.yml`, `security.yml`, and `e2e.yml` limit pull requests to `master` or
  `main`. Without updating those filters, a pull request into `release/v0.6`
  would not receive the normal release gates.
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
2. Re-run PR #63 checks after its final push and obtain the required approving
   review. Do not merge while its review decision is unresolved.
3. Merge PR #63 into `master` without rewriting the published `dev` history.
   The merge method must preserve the reviewed commit ancestry.
4. Create and verify the `release/v*` branch ruleset before the new
   `release/v0.6` branch is created, and the `v*` tag ruleset before the new
   `v0.6.2` tag is created. Preserve the existing `v0.1.0` through `v0.6.1`
   tags unchanged; do not move, delete, or reuse them.
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

No tag is pushed while a required gate or review is incomplete. A failed
published release is fixed forward with a new patch version; its public tag is
not rewritten.

## Protection and CI contract

Create a repository ruleset for `release/v*` with at least the controls on
`master-protection`:

- pull request required;
- one approving review with stale approvals dismissed;
- force pushes and branch deletion prohibited;
- required status checks from the default branch plus `codeql`, `gosec`, and
  `PR-safe E2E`;
- no bypass actor added merely to cut a release.

Update pull-request branch filters so `ci.yml`, `security.yml`, and `e2e.yml`
run for `release/v*`. Keep scheduled work on the default branch unless a
release-specific schedule has an explicit need. Reconcile `master-protection`
to require the same security and PR-safe E2E checks so a failing normal gate is
not merely informational on either stable line.

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
   mode `release.mode: keep-existing`. Before GoReleaser runs, fail if any
   release, draft, or exact versioned OCI tag already exists for the cut.
   GoReleaser creates one new draft and uploads its artifacts and release
   notes. Existing-draft reuse and artifact or draft replacement remain
   disabled.
2. Download the already generated release-report artifact and upload it to the
   same draft without `--clobber`.
3. Query the draft through `gh api` and verify its tag, target commit, exact
   non-whitespace notes, exact asset names, checksums, SBOM/provenance,
   packages, and OCI results. Require every declared GoReleaser artifact class
   and exactly one runnable descriptor for each supported OCI platform while
   excluding only explicit attestation descriptors.
4. Publish the complete draft as the workflow's final GitHub mutation.

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
4. `git merge-base --is-ancestor` proves the v0.6.2 tag commit belongs to
   `release/v0.6` and to the reviewed stable history.
5. the release workflow extracts non-empty v0.6.2 notes before the tag push.
6. bounded local Go race/build, vulnerability, Buf, documentation, workflow,
   and release-dry-run gates pass on the exact release commit.
7. a release workflow contract check proves that no asset upload or release
   note edit follows publication.
8. post-publication GitHub API and registry queries resolve assets and images
   to the released tag and expected immutable digests.

Go specification and gopls checks remain mandatory when Go source or build
constraints change. This branch-policy slice does not invent a Go-language
change merely to exercise those tools; the release qualification still runs
the repository's normal Go gates against non-empty package input.

## Rollback and failure handling

Before a public tag exists, release preparation can be corrected through new
reviewed commits. No force-push or history rewrite is used.

After `v0.6.2` is published, the tag and assets are immutable release evidence.
A defect is handled on `fix/v0.6-*` and released as `v0.6.3`; the previous tag
is not moved, deleted, or reused. If a transient workflow step fails before
GoReleaser creates the draft, a rerun first proves that no release, draft, or
exact versioned OCI tag exists for the cut. Once a draft, release asset, or
versioned OCI tag exists, do not silently reuse, delete, or replace it: record
the failed cut for maintainer analysis and fix forward with the next patch
version. If the tagged workflow itself is defective, do not rewrite the tag. If
a ruleset or workflow filter is wrong, tag creation pauses until the live
configuration and a PR check run prove the repair.
