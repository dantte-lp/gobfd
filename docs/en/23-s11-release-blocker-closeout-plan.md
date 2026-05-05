# S11 Release Blocker Closeout Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining S11 release blockers with source-backed CI, vendor-lab, and release-gate evidence.

**Architecture:** The release gate remains Podman-only for local evidence. GitHub Actions keeps required checks strict for maintainer and push workflows, while Dependabot-triggered workflows use GitHub's Dependabot secret model and deterministic skip/pass behavior when optional secrets are unavailable.

**Tech Stack:** Go 1.26, Podman, Podman Compose, GitHub Actions, Dependabot, SonarQube Cloud, containerlab-style vendor NOS profiles, Arista cEOS, Nokia SR Linux, SONiC-VS, VyOS, FRRouting.

---

## Source Validation

| Source | Constraint |
|---|---|
| GitHub Dependabot on Actions | Dependabot-triggered workflows do not receive normal Actions secrets; workflows receive Dependabot secrets instead. |
| GitHub Dependabot options | `commit-message.prefix` becomes the PR title and commit prefix; a value ending in `)` receives an inserted colon. |
| SonarQube Cloud GitHub Actions | `SONAR_TOKEN` is required for authenticated scans. |
| GitHub Actions workflow syntax | `workflow_dispatch` choice inputs, `github.event_name` conditions, and `always()` artifact upload are valid workflow primitives. |
| Arista MCP EOS User Guide | BGP BFD uses `neighbor bfd`; EVPN VXLAN BFD uses `bfd vtep evpn` under the VXLAN Tunnel Interface. |
| containerlab cEOS docs | cEOS uses `/sbin/init` plus CEOS environment variables and `/mnt/flash` startup-config mounting. |
| containerlab SR Linux docs | SR Linux supports public container images and CLI-format startup configuration. |
| Nokia SR Linux BFD docs | BFD timer values are configured in microseconds; BFD can be enabled for BGP failure detection. |
| VyOS BFD docs | BFD peers and BGP neighbor BFD are configured under `protocols bfd` and `protocols bgp neighbor ... bfd`. |
| FRRouting BFD docs | BFD peers use millisecond receive/transmit intervals and support JSON operational output. |

## Blocker Matrix

| ID | Blocker | Current State | Required Closeout |
|---|---|---|---|
| B1 | Dependabot PR title lint | PR `#42` title starts with `deps:` and violates project commitlint type allowlist. | Future Dependabot titles use `chore(deps):`; current PR title is edited or regenerated. |
| B2 | SonarQube on Dependabot PRs | Dependabot cannot read normal Actions `SONAR_TOKEN`; Sonar fails before merge. | Sonar job runs when token is available, skips only Dependabot without token, and fails for non-Dependabot missing token. |
| B3 | Remote PR-safe E2E evidence | Local Podman gate passes; remote evidence must be current after CI config changes. | PR-safe workflow is rerun and artifacts are uploaded. |
| B4 | Nightly E2E evidence | Local `e2e-routing`, `e2e-rfc`, `e2e-linux` pass; remote scheduled/manual proof is pending. | `workflow_dispatch profile=nightly` is run and recorded. |
| B5 | Vendor E2E evidence | Local vendor profile evidence exists; public image availability can drift. | Bootstrap verifies Nokia SR Linux, SONiC-VS, VyOS, FRR; Arista remains operator-image gated; vendor profile produces pass/skip evidence. |
| B6 | Strict GoBGP vulnerability gates | `GO-2026-4736` is allowlisted; strict gates fail by policy until upstream ships a fixed version. | Release gate uses controlled allowlist; strict gate remains documented as policy blocker, not release blocker, until a fixed upstream exists. |
| B7 | Styled HTML reports | Report contract exists; renderer is not implemented. | Optional S11 follow-up; not required for release if raw artifacts remain complete. |
| B8 | Backend decision | Owner-specific overlay backends remain fail-closed. | S12/S13 decision record selects next backend after release confidence is green. |

## Sprint Tasks

### Task 1: Normalize Dependabot PR Titles

**Files:**
- Modify: `.github/dependabot.yml`
- Verify: `.commitlintrc.yaml`

- [ ] Update Go module dependency updates to use `commit-message.prefix: "chore(deps)"`.
- [ ] Update Docker dependency updates to use `commit-message.prefix: "build(deps)"`.
- [ ] Keep GitHub Actions dependency updates on `commit-message.prefix: "ci"`.
- [ ] Edit PR `#42` title to `chore(deps): bump google.golang.org/grpc from 1.80.0 to 1.81.0 in the go-minor-patch group`.
- [ ] Run `make lint-commit MSG='chore(deps): bump google.golang.org/grpc from 1.80.0 to 1.81.0 in the go-minor-patch group'`.

### Task 2: Make Sonar Dependabot-Safe

**Files:**
- Modify: `.github/workflows/build.yml`
- Verify: `sonar-project.properties`

- [ ] Add a preflight step that classifies Sonar mode as `run` or `skip-dependabot`.
- [ ] Keep missing `SONAR_TOKEN` fatal for non-Dependabot actors.
- [ ] Run the Sonar scan only when the preflight mode is `run`.
- [ ] Add a skip evidence step for Dependabot without `SONAR_TOKEN`.
- [ ] Keep `sonar.projectKey`, `sonar.organization`, and coverage path unchanged.
- [ ] Run `actionlint .github/workflows/build.yml` inside the Podman dev container.

### Task 3: Refresh Remote PR Evidence

**Files:**
- Modify: `docs/en/21-s11-full-e2e-interop-plan.md`
- Modify: `docs/ru/21-s11-full-e2e-interop-plan.md`

- [ ] Push the S11 closeout branch.
- [ ] Open a PR with a Conventional Commit title.
- [ ] Confirm `Build & test`, `Lint (Go)`, `Vulnerability audit`, `Buf`, `Trivy filesystem scan`, `SonarQube`, and `PR-safe E2E` are green.
- [ ] Trigger `E2E Evidence` with `profile=nightly`.
- [ ] Record run IDs and artifact names.

### Task 4: Refresh Vendor Lab Evidence

**Files:**
- Verify: `test/interop-clab/bootstrap.py`
- Verify: `test/interop-clab/run.sh`
- Verify: `test/e2e/vendor/profiles.json`
- Modify: `docs/en/05-interop.md`
- Modify: `docs/ru/05-interop.md`

- [ ] Run `python3 test/interop-clab/bootstrap.py --dry-run -v`.
- [ ] Run `python3 test/interop-clab/bootstrap.py -v --skip-pull` when images are already present.
- [ ] Run `make e2e-vendor`.
- [ ] Run `make interop-clab` when at least one vendor image is available.
- [ ] Record pass/skip per vendor without claiming native SONiC BFD or generic VXLAN backend support.

### Task 5: Release-Gate Decision

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `CHANGELOG.ru.md`
- Modify: `docs/en/implementation-plan.md`
- Modify: `docs/ru/implementation-plan.md`

- [ ] Run `make verify`.
- [ ] Run `make e2e-core e2e-routing e2e-rfc e2e-overlay e2e-linux`.
- [ ] Run `make e2e-vendor`.
- [ ] Run a GoReleaser snapshot through Podman.
- [ ] Decide whether the next tag is required. Documentation-only and CI-only closeout does not require a new release tag unless package artifacts or published behavior change.

## Closure Criteria

| Criterion | Required Result |
|---|---|
| Main worktree | Clean and synced with `origin/master`. |
| Open old branches | Only active Dependabot and active S11 closeout branches remain. |
| Required PR checks | Green or documented non-required skip. |
| Local release gate | `make verify` passes in Podman. |
| E2E release confidence | Core, routing, RFC, overlay, Linux, and vendor reports exist with pass/skip summaries. |
| Documentation | `docs/en` canonical plan and `docs/ru` translation declare the same blockers and decisions. |
