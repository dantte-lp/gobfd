# План закрытия блокеров S11 Release

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Цель:** закрыть оставшиеся S11 release blockers с evidence по CI, vendor lab и release gate.

**Архитектура:** локальный release gate остаётся Podman-only. GitHub Actions сохраняет строгие required checks для maintainer и push workflows; Dependabot-triggered workflows используют модель Dependabot secrets и deterministic skip/pass behavior, когда optional secrets недоступны.

**Tech Stack:** Go 1.26, Podman, Podman Compose, GitHub Actions, Dependabot, SonarQube Cloud, containerlab-style vendor NOS profiles, Arista cEOS, Nokia SR Linux, SONiC-VS, VyOS, FRRouting.

---

## Source Validation

| Source | Constraint |
|---|---|
| GitHub Dependabot on Actions | Dependabot-triggered workflows не получают обычные Actions secrets; workflows получают Dependabot secrets. |
| GitHub Dependabot options | `commit-message.prefix` формирует PR title и commit prefix; значение, заканчивающееся на `)`, получает вставленный colon. |
| SonarQube Cloud GitHub Actions | `SONAR_TOKEN` обязателен для authenticated scans. |
| GitHub Actions workflow syntax | `workflow_dispatch` choice inputs, `github.event_name` conditions и `always()` artifact upload являются valid workflow primitives. |
| Arista MCP EOS User Guide | BGP BFD использует `neighbor bfd`; EVPN VXLAN BFD использует `bfd vtep evpn` под VXLAN Tunnel Interface. |
| containerlab cEOS docs | cEOS использует `/sbin/init`, CEOS environment variables и `/mnt/flash` startup-config mount. |
| containerlab SR Linux docs | SR Linux поддерживает public container images и CLI-format startup configuration. |
| Nokia SR Linux BFD docs | BFD timer values задаются в microseconds; BFD может быть включён для BGP failure detection. |
| VyOS BFD docs | BFD peers и BGP neighbor BFD настраиваются через `protocols bfd` и `protocols bgp neighbor ... bfd`. |
| FRRouting BFD docs | BFD peers используют millisecond receive/transmit intervals и поддерживают JSON operational output. |

## Blocker Matrix

| ID | Blocker | Current State | Required Closeout |
|---|---|---|---|
| B1 | Dependabot PR title lint | PR `#42` начинается с `deps:` и нарушает project commitlint type allowlist. | Future Dependabot titles используют `chore(deps):`; текущий PR title редактируется или PR пересоздаётся. |
| B2 | SonarQube on Dependabot PRs | Dependabot не читает normal Actions `SONAR_TOKEN`; Sonar падает до merge. | Sonar job запускается при доступном token, skip только для Dependabot без token, fail для non-Dependabot без token. |
| B3 | Remote PR-safe E2E evidence | Local Podman gate проходит; remote evidence должна быть актуальна после CI config changes. | PR-safe workflow перезапущен, artifacts uploaded. |
| B4 | Nightly E2E evidence | Local `e2e-routing`, `e2e-rfc`, `e2e-linux` проходят; remote scheduled/manual proof pending. | `workflow_dispatch profile=nightly` запущен и записан. |
| B5 | Vendor E2E evidence | Local vendor profile evidence есть; public image availability может drift. | Bootstrap проверяет Nokia SR Linux, SONiC-VS, VyOS, FRR; Arista остаётся operator-image gated; vendor profile формирует pass/skip evidence. |
| B6 | Strict GoBGP vulnerability gates | `GO-2026-4736` в allowlist; strict gates fail by policy до fixed upstream. | Release gate использует controlled allowlist; strict gate остаётся policy blocker, не release blocker, до fixed upstream. |
| B7 | Styled HTML reports | Report contract есть; renderer не реализован. | Optional S11 follow-up; не required для release при complete raw artifacts. |
| B8 | Backend decision | Owner-specific overlay backends остаются fail-closed. | S12/S13 decision record выбирает следующий backend после green release confidence. |

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
