# Repository Settings

## Current Metadata

| Setting | Value |
|---|---|
| Repository | `dantte-lp/gobfd` |
| Visibility | Public |
| Default branch | `master` |
| Merge commits | Enabled |
| Squash merge | Enabled |
| Rebase merge | Enabled |
| Auto-merge | Disabled |
| Delete head branches | Disabled |
| Discussions | Enabled |
| Projects | Enabled |
| Wiki | Disabled |
| Dependabot security updates | Enabled |
| Secret scanning | Enabled |
| Push protection | Enabled |

## Current Protection State

| Area | Current state |
|---|---|
| Protection mechanism | Repository ruleset `master-protection` (ID `13093259`) on the default branch; it is the only live ruleset |
| Enforcement | `master-protection` is active |
| Bypass actors | None |
| Pull requests before merge | Required |
| Required approving reviews | 1 |
| Stale review dismissal | Required |
| Code owner review | Not required |
| Latest reviewable push approval | Not required |
| Conversation resolution | Not required |
| Required status checks | `Build & test`, `Lint (Go)`, `Vulnerability audit`, `Buf`, `SonarQube`, `Trivy filesystem scan` |
| Strict up-to-date branches | Not required |
| v0.6 release branch | `release/v0.6` does not yet exist |
| Release branch protection | No `release-protection` ruleset exists |
| Release tag protection | No `release-tags` ruleset exists |
| Immutable releases | Disabled (`enabled=false`, `enforced_by_owner=false`) |
| Branch protection API | Not configured; repository rulesets are the active control plane |
| OpenSSF Branch-Protection status | Scorecard reports gaps for force push/deletion, up-to-date branches, latest-push approval, CODEOWNERS review, and two-reviewer tier |

## Required Settings

| Area | Required policy |
|---|---|
| Default branch | `master` |
| Stable branch roles | `master` is the latest accepted stable state; supported lines use `release/vMAJOR.MINOR` |
| Release branches | Create an active `release-protection` ruleset targeting `release/v*` before any matching branch exists |
| Pull requests | Require a pull request and one approving review, with stale approvals dismissed, for `master` and `release/v*` |
| Required checks | Require every exact context listed below on both stable-line rulesets |
| Release tags | Create an active `release-tags` ruleset targeting `v*` before any matching release tag exists; prohibit tag updates and deletion |
| Immutable releases | Enable immutable releases, complete and verify assets in a draft, and publish only as the final mutation |
| Code owner review | Required only after at least two active maintainers can satisfy review policy |
| Conversations | Require resolution before merge when maintainer capacity allows it |
| Force pushes | Disabled on protected branches |
| Branch deletion | Disabled on protected branches |
| Secret scanning | Enabled |
| Push protection | Enabled when available |
| Dependabot alerts | Enabled |
| Dependabot security updates | Enabled |
| CodeQL | Enabled for Go |
| Private vulnerability reporting | Enabled |

The required status-check contexts are exact and case-sensitive:

- `Build & test`
- `Lint (Go)`
- `Vulnerability audit`
- `Buf`
- `SonarQube`
- `Trivy filesystem scan`
- `Lint (docs)`
- `Commit policy (PR title)`
- `codeql`
- `gosec`
- `PR-safe E2E`

## One-Maintainer Constraints

| Scorecard check | Policy |
|---|---|
| `Branch-Protection` | Enable force-push and deletion prevention immediately; defer two-reviewer and CODEOWNERS requirements until a second maintainer exists. |
| `Code-Review` | Use pull requests for traceability; recruit an external reviewer before making all merges review-mandatory. |
| `Contributors` | Treat the score as an ecosystem signal, not a repository misconfiguration. |
| `Maintained` | No remediation until the repository is older than 90 days; keep weekly maintenance activity visible. |

## Recommended Settings

| Area | Recommended policy |
|---|---|
| Issues | Enabled with issue forms only |
| Discussions | Optional; enable only when maintainer capacity exists |
| Wiki | Disabled; documentation source is `docs/en` and `docs/ru` |
| Projects | Optional |
| Releases | GitHub Actions release workflow only |
| Branch naming | `feat/*`, `fix/*`, `docs/*`, `chore/*`, `ci/*`, `deps/*` |
| Delete head branches | Enabled after merge |
| Linear history | Optional; keep disabled while merge commits are allowed |

## Sources

- GitHub issue and pull request templates:
  <https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/about-issue-and-pull-request-templates>
- GitHub security policy:
  <https://docs.github.com/en/code-security/getting-started/adding-a-security-policy-to-your-repository>
- GitHub CODEOWNERS:
  <https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners>
- GitHub repository security:
  <https://docs.github.com/en/code-security/getting-started/securing-your-repository>
