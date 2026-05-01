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
| Protection mechanism | Repository ruleset `master-protection` on default branch |
| Enforcement | Active |
| Bypass actors | None |
| Pull requests before merge | Required |
| Required approving reviews | 1 |
| Stale review dismissal | Required |
| Code owner review | Not required |
| Latest reviewable push approval | Not required |
| Conversation resolution | Not required |
| Required status checks | Build and test, Go lint, vulnerability audit, Buf, SonarQube, Trivy filesystem scan |
| Strict up-to-date branches | Not required |
| Branch protection API | Not configured; repository rulesets are the active control plane |
| OpenSSF Branch-Protection status | Scorecard reports gaps for force push/deletion, up-to-date branches, latest-push approval, CODEOWNERS review, and two-reviewer tier |

## Required Settings

| Area | Required policy |
|---|---|
| Default branch | `master` |
| Pull requests | Require pull request before merge |
| Required checks | CI, security, docs lint, commitlint, vulnerability audit |
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
