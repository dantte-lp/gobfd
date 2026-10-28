# Repository publication controls

Snapshot: **2026-10-29**, read through the GitHub repository and ruleset APIs.
Settings describe publication controls, not release qualification.
[Governance](../GOVERNANCE.md) defines maintainer authority and branch roles.

## Repository metadata

| Control | Observed value |
|:---|:---|
| Repository | `dantte-lp/gobfd`, public |
| Default branch | `master` |
| Merge methods | Merge, squash and rebase enabled |
| Auto-merge | Disabled |
| Delete head branches | Disabled |
| Discussions / Projects / Wiki | Enabled / Enabled / Disabled |
| Secret scanning / push protection | Enabled / Enabled |

## Active rulesets

| Ruleset | ID | Target | Enforced controls |
|:---|---:|:---|:---|
| `master-protection` | 13093259 | Default branch | Pull request, checks, deletion and non-fast-forward prevention |
| `release-protection` | 21655254 | `refs/heads/release/v*` | Pull request, checks, deletion and non-fast-forward prevention |
| `release-tags` | 21655273 | `refs/tags/v*` | Update and deletion prevention |

All three are active and have no bypass actors. Both branch rulesets allow
the three merge methods and dismiss stale approvals. Required approving
review count is zero; CODEOWNERS approval, latest-push approval and review
thread resolution are not required. Strict up-to-date checking is disabled.
The release ruleset does not enforce required status checks on branch creation.

Zero required approvals reflect the single-maintainer constraint. Retain
independent review evidence before merge. Raise the count and enable
CODEOWNERS requirements when another eligible maintainer can satisfy them
without bypass. Do not equate a ruleset setting with technical review.

## Required check contexts

The branch rulesets require these exact, case-sensitive contexts:

```text title="required-status-contexts"
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

## Publication procedure

1. Read live settings and exact source/target commits before integration.
2. Verify the applicable active ruleset before creating a release branch or tag.
3. Preserve source branches unless deletion is explicitly authorized.
4. Reject a failed or unreviewed candidate; retain its evidence and resolve
   the failure before further integration.
5. Verify the merged tree, target ref, parent history and checks by readback.
6. Follow [Release process](../docs/en/10-changelog.md) for immutable tags,
   complete draft assets and final publication.

Existing tags and supported lines are preserved. Branch naming uses the
repository's accepted `feat/`, `fix/`, `docs/`, `chore/`, `ci/` and `deps/`
prefixes. Repository rulesets, rather than the legacy branch-protection API,
are the observed enforcement mechanism.

## Read-only API inspection

```sh title="publication-control-inspection"
gh api repos/dantte-lp/gobfd/rulesets
gh api repos/dantte-lp/gobfd/rulesets/13093259
gh api repos/dantte-lp/gobfd/rulesets/21655254
gh api repos/dantte-lp/gobfd/rulesets/21655273
```

Inspect only selected non-secret metadata in reports. Recheck immutable-release
settings and required checks on the actual publication candidate; a dated
snapshot does not authorize mutation.
