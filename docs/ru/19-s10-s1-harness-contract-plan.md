# План S10.1 Harness Inventory and Contract

![Sprint](https://img.shields.io/badge/Sprint-S10.1-1a73e8?style=for-the-badge)
![Scope](https://img.shields.io/badge/Scope-Harness%20Contract-34a853?style=for-the-badge)
![Runtime](https://img.shields.io/badge/Runtime-Podman-ea4335?style=for-the-badge)
![Status](https://img.shields.io/badge/Status-Implemented-brightgreen?style=for-the-badge)

Implementation record для S10.1.

---

## 1. Цель

| Поле | Значение |
|---|---|
| Sprint | S10.1 |
| Цель | Определить worktree-safe, Podman-only E2E harness contract до добавления новых E2E scenarios. |
| Primary output | `make e2e-help`, `test/e2e/README.md`, harness inventory, artifact directory contract и implementation notes для S10.2-S10.7. |
| Code behavior impact | Нет. |
| Release impact | Нет релиза. |
| Commit | `test(interop): define extended evidence harness` |

## 2. Architecture decision

| Candidate | Решение | Обоснование |
|---|---|---|
| Current shell + `podman-compose` runners | Сохранить как stack lifecycle layer. | Existing interop stacks требуют fixed networks, packet captures, FRR/BIRD/GoBGP services и explicit cleanup. |
| Current Go interop tests | Сохранить как assertion layer. | Go tests уже выражают protocol assertions, timeouts, Podman API actions и pcap parsing. |
| Shared Go Podman API helper | Defer после S10 close. | `test/interop-bgp`, `test/interop-rfc` и `test/interop-clab` всё ещё дублируют exec/logs/pause/start helpers; S10 сначала стандартизирует aggregate evidence. |
| `testcontainers-go` | Отложить для S10.1; разрешить позже только для isolated S10.2 core tests. | Context7 подтверждает Podman provider support, но текущий repo уже использует compose topologies, static IPs, packet capture containers и vendor NOS profiles. |
| containerlab | Оставить optional/manual только для vendor profile. | Containerlab документирует Docker как default и Podman как experimental; public CI не должен зависеть от licensed NOS images. |
| Host `go test` внутри runner scripts | Убрать из будущих S10 gates. | Project evidence требует Go commands через Podman. |
| Fixed `container_name: gobfd-dev` for worktree checks | Удалить. | Parallel worktrees должны использовать Compose-generated names под `COMPOSE_PROJECT_NAME`. |

## 3. Текущие findings

| Область | Finding | Required S10.1 action |
|---|---|---|
| Worktree safety | `deployments/compose/compose.dev.yml` раньше использовал fixed `container_name: gobfd-dev`. | Удалить fixed name, default `COMPOSE_PROJECT_NAME` к checkout directory и добавить `make dev-project` / `make dev-ps`. |
| Full-cycle interop runners | `test/interop-rfc/run.sh` и похожие scripts запускают `go test` напрямую. | Направить Go tests через Podman command или сделать shell runner lifecycle-only. |
| Podman API helpers | `podman_api_test.go` logic дублируется между interop packages. | Вести shared `test/internal/podmanapi` helper как post-S10 refactor. |
| Artifact output | Existing pcap files находятся внутри capture containers или stack-local paths. | Определить `reports/e2e/<target>/<timestamp>/` и копировать logs/pcaps/test JSON туда. |
| Target discoverability | `make interop*` targets существуют; S10 aggregate help target отсутствует. | Добавить `make e2e-help` в S10.1. |
| Evidence format | `gotestsum` есть для unit reports; interop targets не дают standard JSON/JUnit. | Стандартизировать Go `-json` и optional `gotestsum` output для S10 targets. |

## 4. File plan

| Path | Action | Responsibility |
|---|---|---|
| `Makefile` | Modify | Добавить `e2e-help`; объявить future S10 target names без ослабления существующих interop targets. |
| `test/e2e/README.md` | Create | Определить harness contract, target matrix, artifact layout, skip policy, cleanup policy и Podman-only command policy. |
| `test/e2e/targets.md` | Create | Инвентаризировать existing interop/integration targets с owner, runtime, inputs, outputs, cleanup и S10 mapping. |
| `docs/en/18-s10-extended-e2e-interop.md` | Modify | Отметить S10.1 plan file и уточнить test-foundation decision. |
| `docs/ru/18-s10-extended-e2e-interop.md` | Modify | Русский перевод S10.1 linkage. |
| `docs/en/19-s10-s1-harness-contract-plan.md` | Create | Каноничный подробный English S10.1 plan. |
| `docs/ru/19-s10-s1-harness-contract-plan.md` | Create | Русский перевод. |
| `docs/en/README.md` | Modify | Добавить S10.1 plan в release planning index. |
| `docs/ru/README.md` | Modify | Добавить S10.1 plan в release planning index. |
| `docs/README.md` | Modify | Увеличить document count. |
| `CHANGELOG.md` | Modify | Записать S10.1 plan в Unreleased. |
| `CHANGELOG.ru.md` | Modify | Записать S10.1 plan в Unreleased. |

## 5. Task plan

### Task 1 -- Worktree Baseline

- [x] Проверить active branch.

```bash
git status --short --branch
```

Expected:

```text
## s10/s1-e2e-harness
```

- [x] Проверить dev project перед доверием к Makefile checks.

```bash
make dev-project
make up
make dev-ps
```

Expected:

```text
Compose project name должен совпадать с active checkout slug, а `make dev-ps` должен показывать service `dev` для этого project.
```

- [x] Если legacy fixed dev container существует, остановить его до использования S10.1 Makefile checks.

```bash
podman-compose -f deployments/compose/compose.dev.yml down
```

Expected:

```text
Fixed `gobfd-dev` container отсутствует.
```

### Task 2 -- Add `e2e-help`

- [x] Изменить `.PHONY` в `Makefile`.

Required names:

```makefile
e2e-help e2e-core e2e-routing e2e-rfc e2e-overlay e2e-linux e2e-vendor
```

- [x] Добавить `e2e-help`.

Required output:

```text
S10 E2E targets
  e2e-core      implemented: GoBFD daemon-to-daemon scenarios
  e2e-routing   implemented: FRR/BIRD3/GoBGP/ExaBGP aggregate
  e2e-rfc       implemented: RFC 7419/9384/9468/9747 aggregate
  e2e-overlay   implemented: VXLAN/Geneve backend boundary checks
  e2e-linux     implemented: rtnetlink/kernel-bond/OVSDB/NM ownership checks
  e2e-vendor    implemented: optional containerlab vendor profile evidence
```

- [x] Оставить non-implemented S10 aggregate targets fail-closed.

Initial S10.1 required behavior:

```bash
make e2e-vendor
```

Expected S10.1 placeholder output:

```text
e2e-vendor placeholder message
```

Exit code:

```text
2
```

Current S10.6 required behavior:

```bash
make e2e-vendor
```

Expected artifacts:

```text
reports/e2e/vendor/<timestamp>/vendor-profiles.json
reports/e2e/vendor/<timestamp>/vendor-images.json
reports/e2e/vendor/<timestamp>/skip-summary.json
```

### Task 3 -- Create Harness Contract

- [x] Создать `test/e2e/README.md`.

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

- [x] Определить artifact layout.

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

- [x] Определить skip classes.

Required classes:

| Class | Meaning |
|---|---|
| `unsupported-host-capability` | Kernel, namespace или Podman capability отсутствует. |
| `missing-image` | Required public image unavailable. |
| `licensed-vendor-image` | Vendor image cannot be redistributed or pulled by CI. |
| `manual-only` | Scenario требует operator-owned infrastructure. |

### Task 4 -- Inventory Current Targets

- [x] Создать `test/e2e/targets.md`.

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

- [x] Записать owner, runtime, network, inputs, outputs, cleanup, current gaps и planned S10 target для каждой строки.

### Task 5 -- Document Test Foundation Decision

- [x] Обновить `docs/en/18-s10-extended-e2e-interop.md`.

Required decision:

```text
S10 keeps the existing shell/compose stack lifecycle and Go assertion model.
S10 improves it with worktree-safe execution, standard artifacts, Podman-only Go execution, and a deferred post-S10 Podman API helper extraction item.
S10.2 uses the compose topology for daemon-to-daemon testing; testcontainers-go remains deferred.
```

- [x] Обновить `docs/ru/18-s10-extended-e2e-interop.md` с тем же решением.

### Task 6 -- Update Indexes and Changelogs

- [x] Обновить `docs/README.md`, `docs/en/README.md` и `docs/ru/README.md`.

Current count after S10 closeout:

```text
Documents-26
```

- [x] Добавить S10.1 plan и S10 closeout analysis в EN/RU release planning tables and Mermaid maps.

- [x] Обновить `CHANGELOG.md` и `CHANGELOG.ru.md` в Unreleased.

Required entry:

```text
S10.1 harness contract plan and target inventory for extended E2E evidence.
```

### Task 7 -- Verification

- [x] Запустить documentation lint в Podman.

Preferred command:

```bash
make lint-docs
```

Fallback command when dev container is unavailable:

```bash
podman run --rm \
  -v "$PWD:/app:z" \
  -w /app \
  localhost/compose_dev:latest \
  markdownlint-cli2 "**/*.md" "#node_modules" "#vendor" "#reports" "#dist" "#build" "#docs/rfc"
```

- [x] Запустить diff whitespace check.

```bash
git diff --check
```

- [x] Запустить commitlint.

```bash
make lint-commit MSG='test(interop): define extended evidence harness'
```

Если `make lint-commit` заблокирован fixed dev container mount, запустить equivalent command внутри one-off Podman container, смонтированного на current worktree.

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
| Worktree-safe validation path documented | Да |
| `make e2e-help` present | Да |
| Future S10 targets fail closed | Да |
| `test/e2e/README.md` present | Да |
| `test/e2e/targets.md` present | Да |
| EN/RU docs synchronized | Да |
| Changelogs updated | Да |
| Documentation lint pass | Да |
| Commitlint pass | Да |
| Conventional commit created | Да |

---

*Последнее обновление: 2026-05-01*
