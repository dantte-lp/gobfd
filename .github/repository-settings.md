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

## Required Settings

| Area | Required policy |
|---|---|
| Default branch | `master` |
| Pull requests | Require pull request before merge |
| Required checks | CI, security, docs lint, commitlint, vulnerability audit |
| Code owner review | Required after `.github/CODEOWNERS` is on `master` |
| Conversations | Require resolution before merge |
| Force pushes | Disabled on protected branches |
| Branch deletion | Disabled on protected branches |
| Secret scanning | Enabled |
| Push protection | Enabled when available |
| Dependabot alerts | Enabled |
| Dependabot security updates | Enabled |
| CodeQL | Enabled for Go |
| Private vulnerability reporting | Enabled |

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
