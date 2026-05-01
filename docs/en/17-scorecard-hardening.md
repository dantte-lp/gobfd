# OpenSSF Scorecard Hardening

Date: 2026-05-01

Scope: repository security posture for `dantte-lp/gobfd` with one active
maintainer.

## Current Score

| Source | Value |
|---|---:|
| Scorecard run | `25216591582` |
| Commit | `9fda579a2d0a02f9c2b52f4596c97872b68f1d9a` |
| Scorecard version | `v5.3.0`, commit `c22063e786c11f9dd714d777a687ff7c4599b600` |
| Overall score | `6.3` |

## Check Status

| Check | Score | Status | One-maintainer action |
|---|---:|---|---|
| Dependency-Update-Tool | 10 | Complete | Keep Dependabot enabled. |
| Dangerous-Workflow | 10 | Complete | Keep `pull_request_target` and unsafe `workflow_run` patterns out of CI. |
| Security-Policy | 10 | Complete | Keep `SECURITY.md` current. |
| Binary-Artifacts | 10 | Complete | Keep generated binaries out of git. |
| SAST | 10 | Complete | Keep CodeQL on every change. |
| Packaging | 10 | Complete | Keep release workflow, archives, packages, and images. |
| Fuzzing | 10 | Complete | Keep Go fuzz targets in protocol hot paths. |
| License | 10 | Complete | Keep canonical Apache-2.0 text. |
| CI-Tests | 10 | Complete | Keep required CI checks on pull requests. |
| Vulnerabilities | 9 | Action required | Triage `GO-2026-4736`; update, replace, or document non-impact in supported scanner policy. |
| Token-Permissions | 8 | Action required | Add top-level read-only permissions to every workflow; keep write scopes only at job level. |
| Pinned-Dependencies | 6 | Action required | Hash-pin or remove `downloadThenRun`, `pipCommand`, and `npmCommand` findings. |
| CII-Best-Practices | 0 | Action required | Register the project in OpenSSF Best Practices and add the badge after status is issued. |
| Signed-Releases | 0 | Action required | Publish release signatures and SLSA/in-toto provenance for release assets. |
| Branch-Protection | 0 | Action required | Disable force pushes and branch deletion first; defer blocking two-reviewer settings until a second maintainer exists. |
| Code-Review | 1 | Structural limit | Keep PR workflow; recruit an external reviewer before enforcing universal independent review. |
| Contributors | 0 | Structural limit | Treat as ecosystem signal; no synthetic remediation. |
| Maintained | 0 | Temporal limit | No remediation until repository age is over 90 days. |

## S9 Plan

| Sprint | Goal | Exit criteria | Release required |
|---|---|---|---:|
| S9.1 | Documentation truth table | Repository settings, release docs, and RFC capability wording match code and live GitHub state. | No |
| S9.2 | Workflow token hardening | Every workflow has top-level read-only permissions and scoped job-level writes. | No |
| S9.3 | Dependency pinning | Scorecard no longer reports unpinned `downloadThenRun`, `pipCommand`, or `npmCommand` findings. | No |
| S9.4 | Ruleset hardening | Force push and branch deletion are disabled for the default branch ruleset. | No |
| S9.5 | Best Practices badge | OpenSSF Best Practices project entry exists and README links to the issued badge. | No |
| S9.6 | Signed release pipeline | Future releases produce signatures and SLSA/in-toto provenance. | Yes, next functional or security release |

## Deferred Controls

| Control | Reason |
|---|---|
| Two required reviewers | Blocks a one-maintainer repository. |
| Required CODEOWNERS review | Blocks a one-maintainer repository until a second maintainer exists. |
| Latest-push approval | Blocks the only maintainer from updating their own pull request. |
| Contributor organization score | Requires real external contributors from multiple organizations. |

## Official Sources

- OpenSSF Scorecard checks:
  <https://github.com/ossf/scorecard/blob/c22063e786c11f9dd714d777a687ff7c4599b600/docs/checks.md>
- OpenSSF Best Practices:
  <https://www.bestpractices.dev/>
- GitHub repository rulesets:
  <https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets>
- GitHub Actions permissions:
  <https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions>
- SLSA provenance:
  <https://slsa.dev/spec/v1.0/provenance>
