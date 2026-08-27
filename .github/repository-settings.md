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
| Protection mechanism | Repository rulesets `master-protection` (ID `13093259`), `release-protection` (ID `21655254`), and `release-tags` (ID `21655273`) |
| Enforcement | All three rulesets are active |
| Bypass actors | None |
| Pull requests before merge | Required on `master` and `release/v*` |
| Required approving reviews | Zero while the repository has one eligible maintainer; raise to one when a second eligible maintainer is active |
| Stale review dismissal | Configured for future approving reviews |
| Code owner review | Not required |
| Latest reviewable push approval | Not required |
| Conversation resolution | Not required |
| Required status checks | All 11 exact contexts listed below on `master` and `release/v*` |
| Strict up-to-date branches | Not required |
| v0.6 release branch | `release/v0.6` does not yet exist |
| Release branch protection | Active `release-protection` ruleset (ID `21655254`) targets `refs/heads/release/v*`; deletion and non-fast-forward updates are prohibited |
| Release tag protection | Active `release-tags` ruleset (ID `21655273`) targets `refs/tags/v*`; deletion and updates are prohibited |
| Immutable releases | Enabled (`enabled=true`) |
| Branch protection API | Not configured; repository rulesets are the active control plane |
| OpenSSF Branch-Protection status | The last report predates the live deletion/non-fast-forward rulesets; approval, latest-push, CODEOWNERS, and two-reviewer tiers remain intentionally constrained by maintainer capacity |

## Required Settings

| Area | Required policy |
|---|---|
| Default branch | `master` |
| Stable branch roles | `master` is the latest accepted stable state; supported lines use `release/vMAJOR.MINOR` |
| Release branches | Create an active `release-protection` ruleset targeting `release/v*` before each new matching branch is created |
| Pull requests | Require a pull request for `master` and `release/v*`; while only one eligible maintainer exists, require independent review evidence but configure zero GitHub approving reviews so the author is not permanently blocked |
| Required checks | Require every exact context listed below on both stable-line rulesets |
| Release tags | Create an active `release-tags` ruleset targeting `v*` before each new matching tag is created, specifically before `v0.6.2`; preserve existing `v0.1.0` through `v0.6.1` tags unchanged and prohibit tag updates and deletion |
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
| `Branch-Protection` | Force-push and deletion prevention are active. Raise the approving-review count to one and require CODEOWNERS review when a second eligible maintainer can satisfy them without bypass. |
| `Code-Review` | Both branch rulesets require pull requests but configure zero approving reviews while the author is the only eligible maintainer. Record independent read-only review evidence before merge; enable one GitHub approval when a second eligible maintainer is active. |
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
