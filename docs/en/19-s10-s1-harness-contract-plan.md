# S10.1 Harness Inventory and Contract Plan

![Sprint](https://img.shields.io/badge/Sprint-S10.1-1a73e8?style=for-the-badge)
![Scope](https://img.shields.io/badge/Scope-Harness%20Contract-34a853?style=for-the-badge)
![Runtime](https://img.shields.io/badge/Runtime-Podman-ea4335?style=for-the-badge)
![Status](https://img.shields.io/badge/Status-Planned-6f42c1?style=for-the-badge)

Detailed implementation plan for S10.1.

---

## 1. Goal

| Field | Value |
|---|---|
| Sprint | S10.1 |
| Goal | Define a worktree-safe, Podman-only E2E harness contract before adding new E2E scenarios. |
| Primary output | `make e2e-help`, `test/e2e/README.md`, harness inventory, artifact directory contract, and implementation notes for S10.2-S10.7. |
| Code behavior impact | None. |
| Release impact | No release. |
| Commit | `test(interop): define extended evidence harness` |

## 2. Architecture Decision

| Candidate | Decision | Rationale |
|---|---|---|
| Current shell + `podman-compose` runners | Keep as stack lifecycle layer. | Existing interop stacks require fixed networks, packet captures, FRR/BIRD/GoBGP services, and explicit cleanup. |
| Current Go interop tests | Keep as assertion layer. | Go tests already express protocol assertions, timeouts, Podman API actions, and pcap parsing. |
| Shared Go Podman API helper | Add. | `test/interop-bgp`, `test/interop-rfc`, and `test/interop-clab` duplicate exec/logs/pause/start helpers. |
| `testcontainers-go` | Defer for S10.1; allow later for isolated S10.2 core tests only. | Context7 confirms Podman provider support, but the current repo already depends on compose topologies, static IPs, packet capture containers, and vendor NOS profiles. |
| containerlab | Keep optional/manual for vendor profile only. | Containerlab documents Docker as default and Podman as experimental; public CI must not depend on licensed NOS images. |
| Host `go test` inside runner scripts | Remove from future S10 gates. | Project evidence requires Go commands through Podman. |
| Fixed `container_name: gobfd-dev` for worktree checks | Remove. | Parallel worktrees must use Compose-generated names under `COMPOSE_PROJECT_NAME`. |

## 3. Current Findings

| Area | Finding | Required S10.1 Action |
|---|---|---|
| Worktree safety | `deployments/compose/compose.dev.yml` previously used fixed `container_name: gobfd-dev`. | Remove the fixed name, default `COMPOSE_PROJECT_NAME` to the checkout directory, and expose `make dev-project` / `make dev-ps`. |
| Full-cycle interop runners | `test/interop-rfc/run.sh` and similar scripts run `go test` directly. | Route Go tests through a Podman command or make the shell runner lifecycle-only. |
| Podman API helpers | `podman_api_test.go` logic is duplicated across interop packages. | Plan a shared `test/internal/podmanapi` helper for S10.2 or S10.3. |
| Artifact output | Existing pcap files live inside capture containers or stack-local paths. | Define `reports/e2e/<target>/<timestamp>/` and copy logs/pcaps/test JSON there. |
| Target discoverability | `make interop*` targets exist; no S10 aggregate help target exists. | Add `make e2e-help` in S10.1. |
| Evidence format | `gotestsum` exists for unit reports; interop targets do not produce standard JSON/JUnit. | Standardize on Go `-json` and optional `gotestsum` output for S10 targets. |

## 4. File Plan

| Path | Action | Responsibility |
|---|---|---|
| `Makefile` | Modify | Add `e2e-help`; declare future S10 target names without weakening existing interop targets. |
| `test/e2e/README.md` | Create | Define harness contract, target matrix, artifact layout, skip policy, cleanup policy, and Podman-only command policy. |
| `test/e2e/targets.md` | Create | Inventory existing interop/integration targets with owner, runtime, inputs, outputs, cleanup, and S10 mapping. |
| `docs/en/18-s10-extended-e2e-interop.md` | Modify | Mark S10.1 plan file and refine the test-foundation decision. |
| `docs/ru/18-s10-extended-e2e-interop.md` | Modify | Russian translation of S10.1 linkage. |
| `docs/en/19-s10-s1-harness-contract-plan.md` | Create | Canonical detailed English S10.1 plan. |
| `docs/ru/19-s10-s1-harness-contract-plan.md` | Create | Russian translation. |
| `docs/en/README.md` | Modify | Add S10.1 plan to release planning index. |
| `docs/ru/README.md` | Modify | Add S10.1 plan to release planning index. |
| `docs/README.md` | Modify | Increment document count. |
| `CHANGELOG.md` | Modify | Record S10.1 plan under Unreleased. |
| `CHANGELOG.ru.md` | Modify | Record S10.1 plan under Unreleased. |

## 5. Task Plan

### Task 1 -- Worktree Baseline

- [x] Verify active branch.

```bash
git status --short --branch
```

Expected:

```text
## s10/s1-e2e-harness
```

- [x] Verify the dev project before trusting Makefile checks.

```bash
make dev-project
make up
make dev-ps
```

Expected:

```text
The Compose project name must match the active checkout slug, and `make dev-ps` must show a `dev` service for that project.
```

- [x] If a legacy fixed dev container exists, stop it before using S10.1 Makefile checks.

```bash
podman-compose -f deployments/compose/compose.dev.yml down
```

Expected:

```text
No fixed `gobfd-dev` container remains.
```

### Task 2 -- Add `e2e-help`

- [x] Modify `.PHONY` in `Makefile`.

Required names:

```makefile
e2e-help e2e-core e2e-routing e2e-rfc e2e-overlay e2e-linux e2e-vendor
```

- [x] Add `e2e-help`.

Required output:

```text
S10 E2E targets
  e2e-core      implemented: GoBFD daemon-to-daemon scenarios
  e2e-routing   implemented: FRR/BIRD3/GoBGP/ExaBGP aggregate
  e2e-rfc       implemented: RFC 7419/9384/9468/9747 aggregate
  e2e-overlay   implemented: VXLAN/Geneve backend boundary checks
  e2e-linux     planned: rtnetlink/kernel-bond/OVSDB/NM ownership checks
  e2e-vendor    planned: optional containerlab vendor profiles
```

- [x] Keep non-implemented S10 aggregate targets fail-closed.

Required behavior:

```bash
make e2e-linux
```

Expected:

```text
e2e-linux: planned in S10.5; not implemented in S10.1
```

Exit code:

```text
2
```

### Task 3 -- Create Harness Contract

- [x] Create `test/e2e/README.md`.

Required sections:

```markdown
# GoBFD E2E Harness Contract

## Runtime Policy
## Target Matrix
## Artifact Layout
## Cleanup Policy
## Skip Policy
## Worktree Safety
## Podman API Usage
## Packet Capture Policy
## Benchmark Policy
```

- [x] Define artifact layout.

Required path pattern:

```text
reports/e2e/<target>/<YYYYMMDDTHHMMSSZ>/
```

Required files:

```text
go-test.json
go-test.log
containers.json
containers.log
packets.pcapng
packets.csv
environment.json
summary.md
```

- [x] Define skip classes.

Required classes:

| Class | Meaning |
|---|---|
| `unsupported-host-capability` | Kernel, namespace, or Podman capability is absent. |
| `missing-image` | Required public image is unavailable. |
| `licensed-vendor-image` | Vendor image cannot be redistributed or pulled by CI. |
| `manual-only` | Scenario requires operator-owned infrastructure. |

### Task 4 -- Inventory Current Targets

- [x] Create `test/e2e/targets.md`.

Required target inventory:

| Current Target | S10 Target | Type |
|---|---|---|
| `make test-integration` | `e2e-core` input | in-process integration |
| `make interop` | `e2e-routing` input | 4-peer BFD interop |
| `make interop-bgp` | `e2e-routing` input | BGP+BFD interop |
| `make interop-rfc` | `e2e-rfc` input | RFC interop |
| `make interop-clab` | `e2e-vendor` input | vendor NOS profile |
| `make int-bgp-failover` | `e2e-routing` optional input | integration example |
| `make int-haproxy` | `e2e-core` optional input | integration example |
| `make int-observability` | `e2e-core` optional input | integration example |
| `make int-exabgp-anycast` | `e2e-routing` optional input | integration example |
| `make int-k8s` | future manual profile | cluster integration |

- [x] Record owner, runtime, network, inputs, outputs, cleanup, current gaps, and planned S10 target for every row.

### Task 5 -- Document Test Foundation Decision

- [x] Update `docs/en/18-s10-extended-e2e-interop.md`.

Required decision:

```text
S10 keeps the existing shell/compose stack lifecycle and Go assertion model.
S10 improves it with a shared Podman API helper, worktree-safe execution, standard artifacts, and Podman-only Go execution.
S10.2 uses the compose topology for daemon-to-daemon testing; testcontainers-go remains deferred.
```

- [x] Update `docs/ru/18-s10-extended-e2e-interop.md` with the same decision.

### Task 6 -- Update Indexes and Changelogs

- [x] Update `docs/README.md`, `docs/en/README.md`, and `docs/ru/README.md`.

Required count:

```text
Documents-25
```

- [x] Add the S10.1 plan to EN/RU release planning tables and Mermaid maps.

- [x] Update `CHANGELOG.md` and `CHANGELOG.ru.md` under Unreleased.

Required entry:

```text
S10.1 harness contract plan and target inventory for extended E2E evidence.
```

### Task 7 -- Verification

- [x] Run documentation lint in Podman.

Preferred command:

```bash
make lint-docs
```

Fallback command when the dev container is unavailable:

```bash
podman run --rm \
  -v "$PWD:/app:z" \
  -w /app \
  localhost/compose_dev:latest \
  markdownlint-cli2 "**/*.md" "#node_modules" "#vendor" "#reports" "#dist" "#build" "#docs/rfc"
```

- [x] Run diff whitespace check.

```bash
git diff --check
```

- [x] Run commitlint.

```bash
make lint-commit MSG='test(interop): define extended evidence harness'
```

If `make lint-commit` is blocked by the fixed dev container mount, run the equivalent command inside a one-off Podman container mounted to the current worktree.

### Task 8 -- Commit

- [x] Stage only S10.1 files.

```bash
git add \
  Makefile \
  CHANGELOG.md CHANGELOG.ru.md \
  docs/README.md docs/en/README.md docs/ru/README.md \
  docs/en/18-s10-extended-e2e-interop.md \
  docs/ru/18-s10-extended-e2e-interop.md \
  docs/en/19-s10-s1-harness-contract-plan.md \
  docs/ru/19-s10-s1-harness-contract-plan.md \
  test/e2e/README.md test/e2e/targets.md
```

- [x] Commit.

```bash
git commit -m 'test(interop): define extended evidence harness'
```

## 6. S10.1 Exit Criteria

| Gate | Required |
|---|---|
| Worktree-safe validation path documented | Yes |
| `make e2e-help` present | Yes |
| Future S10 targets fail closed | Yes |
| `test/e2e/README.md` present | Yes |
| `test/e2e/targets.md` present | Yes |
| EN/RU docs synchronized | Yes |
| Changelogs updated | Yes |
| Documentation lint pass | Yes |
| Commitlint pass | Yes |
| Conventional commit created | Yes |

---

*Last updated: 2026-05-01*
