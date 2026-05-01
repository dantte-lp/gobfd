# План реализации -- `gobfd`

- **Метод:** поэтапный gated lifecycle с короткими implementation sprint.
- **Область:** production-oriented BFD daemon, CLI, control-plane API и
  interop-среда для Linux networking stacks.
- **Каденс:** 8 спринтов по 2 недели до `v0.5.0`, затем release hardening,
  Scorecard hardening, extended E2E evidence и сопровождение.
- **Стандарты:** [Keep a Changelog 1.1.0], [Conventional Commits 1.0.0],
  [Semantic Versioning 2.0.0], [Compose Specification], [Containerfile.5],
  [.containerignore.5], [containers.conf.5].

[Keep a Changelog 1.1.0]: https://keepachangelog.com/en/1.1.0/
[Conventional Commits 1.0.0]: https://www.conventionalcommits.org/en/v1.0.0/
[Semantic Versioning 2.0.0]: https://semver.org/spec/v2.0.0.html
[Compose Specification]: https://github.com/compose-spec/compose-spec/blob/main/00-overview.md
[Containerfile.5]: https://github.com/containers/common/blob/main/docs/Containerfile.5.md
[.containerignore.5]: https://github.com/containers/common/blob/main/docs/containerignore.5.md
[containers.conf.5]: https://github.com/containers/common/blob/main/docs/containers.conf.5.md

---

## 1. Решения

| ID | Решение | Обоснование |
|---|---|---|
| ADR-1 | Только Podman-based Go toolchain. | Raw-socket тесты, interop stacks и версии анализаторов должны быть воспроизводимы без Go на хосте. |
| ADR-2 | Allowlist для `golangci-lint` v2. | `default: none` делает изменения анализаторов явными и reviewable. |
| ADR-3 | Поддерживать `CHANGELOG.md` и `CHANGELOG.ru.md`. | Пользовательские изменения должны быть curated для людей, а не сгенерированы из git log. |
| ADR-4 | SemVer tags только для released module versions. | Released versions immutable; исправления выходят только новой версией. |
| ADR-5 | Conventional Commits для commits и PR titles. | Commit type напрямую мапится на release notes и SemVer impact. |
| ADR-6 | Compose files следуют Compose Specification. | Interop stacks требуют portable service, network, volume и profile semantics. |
| ADR-7 | Containerfiles являются native Podman/Buildah inputs. | Используются `Containerfile`, `.containerignore` и минимальные capabilities вместо Docker-only assumptions. |
| ADR-8 | Source-backed design validation. | Claims по standards, vendor behavior и library config проверяются по актуальным primary или official docs. |

## 2. Quality Gates

| Gate | Tool | Trigger |
|---|---|---|
| Go build | `make build` | Каждое изменение кода. |
| Unit и package tests | `make test` | Каждое изменение кода; всегда внутри Podman. |
| Go language diagnostics | `make gopls-check` | Каждое изменение кода; official `gopls` diagnostics внутри Podman. |
| Go static analysis | `make lint` | Каждое изменение кода; `golangci-lint` v2 allowlist. |
| Proto lint | `make proto-lint` | Изменения API и `make verify`. |
| Vulnerability audit | `make vulncheck` | Изменения зависимостей и `make verify`. |
| Markdown | `make lint-md` | Изменения документации. |
| YAML | `make lint-yaml` | Compose, CI, config и docs tooling изменения. |
| Spelling | `make lint-spell` | Documentation и public API text изменения. |
| Commit message | `make lint-commit MSG='type(scope): subject'` | Перед commit или PR title review. |
| RFC interop | `make interop-rfc` | Protocol behavior changes. |
| BGP interop | `make interop-bgp` | GoBGP, route withdrawal и integration changes. |

Host-команды `go test`, `go build`, `go vet`, `golangci-lint` и `buf` не
являются evidence проекта. Используются Makefile targets, работающие через
Podman.

## 3. Текущий Snapshot

| Область | Статус | Evidence / Gap |
|---|---|---|
| Core build/test/lint | Green | `make verify` проходит в Podman и включает `gopls-check`, `golangci-lint`, doc lint, proto lint и vulnerability audit. |
| RFC 7419 / 9384 / 9468 interop | Green | `make interop-rfc-test` проходит эти сценарии при запущенном RFC stack. |
| RFC 9747 Echo interop | Green | `make interop-rfc-test` проверяет Echo `Up`, UDP 3785 packet capture, Echo failure и recovery. |
| Code/docs consistency | Partial | Canonical EN/RU docs требуют post-`v0.5.2` release synchronization; `docs/ru/codebase-consistency-audit.md` фиксирует remaining gaps. |
| Control-plane API | Partial | Proto/session snapshots и `gobfdctl` output показывают Echo, Micro-BFD, VXLAN и Geneve; generic `AddSession` и `gobfdctl session add` намеренно остаются single-hop/multi-hop до dedicated transport-specific APIs. |
| Interface monitoring | Green on Linux | `internal/netio` подписывается на rtnetlink `RTMGRP_LINK`, а `Manager.HandleInterfaceEvent` переводит сессии в Down до истечения detection timer. eBPF отложен; см. `docs/ru/linux-netlink-ebpf-research.md`. |
| Linux Micro-BFD enforcement | Partial | Есть per-member RFC 7130 sessions, aggregate state, dry-run actuator wiring, explicit Linux kernel-bond sysfs, native OVSDB bonded-port enforcement и NetworkManager D-Bus bond port activation с operator-selected owner policy. |
| Linux VXLAN/Geneve dataplane coexistence | Partial | Реализован explicit `userspace-udp` backend для dedicated endpoints; reserved `kernel`, `ovs`, `ovn`, `cilium`, `calico`, `nsx` имена fail closed до owner-specific integrations. |
| Auth wiring | Green for static per-session keys | YAML sessions, gRPC `AddSession` и `gobfdctl session add` подключают RFC 5880 auth в TX/RX, snapshots показывают auth type, missing raw wire bytes rejected, receive sequence knowledge resets after 2x Detection Time. Dynamic key rotation отложен. |
| pkg.go.dev command page | Green | `v0.5.2` индексируется на pkg.go.dev, Apache-2.0 определяется, `cmd/gobfd` имеет command documentation. |
| Documentation standards | Green | Keep a Changelog, SemVer, commitlint, canonical `docs/en` и `docs/ru`, а также doc lint gates присутствуют; temporary research files исключены из published Markdown corpus. |
| Extended E2E / interop evidence | Green | S10.1-S10.7 реализуют Podman-only evidence для core daemon, routing interop, RFC behavior, Linux dataplane ownership, overlay boundaries, optional vendor profiles и CI artifacts. |

## 4. Спринты

### Sprint Close Protocol

Каждый sprint закрывается небольшим reviewable commit после fresh evidence:

1. Обновить план и оба changelog с результатом sprint.
2. Запустить `make verify` внутри Podman.
3. Запустить `make lint-commit MSG='type(scope): subject'` для planned commit.
4. Закоммитить с Conventional Commit subject:
   `type(scope): short imperative summary`.
5. Если sprint меняет protocol behavior, запустить соответствующий interop gate
   перед commit и записать pass/fail evidence в sprint notes.

### Phase 1 -- Tooling and Documentation Baseline

| # | Output | Exit |
|---|---|---|
| **S1** | Podman-only analyzer stack, doc lint configs, commitlint config, `.containerignore`, canonical plan. | Done. Commit: `chore(lint): establish podman quality gates`. |
| **S1.1** | Full-corpus spelling dictionary. | Future: expand `make lint-spell` to all published EN docs without mass false positives. |
| **S1.2** | Optional function-order cleanup. | Future: enable `funcorder` only after legacy files are split or reordered cleanly. |

### Phase 2 -- Protocol Correctness

| # | Output | Exit |
|---|---|---|
| **S2** | RFC 9747 Echo fix with packet-level evidence. | Done. Commit: `fix(bfd): complete rfc9747 echo interop`. |
| **S3** | Auth wire integration audit and fixes. | Done for static keys. Commits: `fix(bfd): wire declarative authentication`, `fix(bfd): harden auth wire verification`, `fix(bfd): reset auth sequence window`, `feat(api): accept auth key material`, `feat(cli): add auth session flags`. |
| **S4** | Interface monitor implementation. | Done: Linux rtnetlink link-down events drive immediate BFD `Down` / `Path Down`; cilium/ebpf deferred. Commit: `feat(netio): react to link state events`. |
| **S4.1** | Code/docs/tooling consistency close-out. | Done: Linux build context for `gopls-check`, README RFC alignment, repo-wide consistency audit. Commit: `chore(lint): harden gopls diagnostics gate`. |

### Phase 3 -- Control Plane and Operations

| # | Output | Exit |
|---|---|---|
| **S5** | API/CLI coverage for Echo, Micro-BFD, VXLAN, Geneve. | Partial: vocabulary/snapshots/output done; dedicated create flows remain. Commit: `feat(api): expose advanced session type vocabulary`. |
| **S5.1** | State mutation consistency. | Done: `SetAdminDown` serialized through session goroutine. Commit: `fix(bfd): serialize admin-down transition`. |
| **S6** | Production security posture. | Done. Commit: `docs(security): define production hardening policy`. |
| **S6.1** | Linux advanced BFD applicability close-out. | Done: RFC docs, config examples and audit notes aligned with actuator and overlay limits. Commit: `docs(linux): document advanced bfd applicability`. |
| **S7** | Independent production integration assets. | Done for generic runbooks, Kubernetes hardening, Prometheus rules, FRR/GoBGP examples, Micro-BFD actuator policy/config wiring, kernel-bond, OVS CLI fallback, OVSDB research, native OVSDB, NetworkManager D-Bus and overlay backend model. Commit: `feat(examples): add production integration assets`. |
| **S7.1e** | Native OVSDB backend. | Done: `backend: ovs` selects OVSDB-backed LAG enforcement with configurable `ovsdb_endpoint`; `OVSLAGBackend` remains fallback/diagnostics. Commit: `feat(netio): add ovsdb lag backend`. |
| **S7.1f** | Optional NetworkManager backend. | Done: NetworkManager D-Bus backend handles active and available bond port profiles. Commit: `feat(netio): add networkmanager lag backend`. |
| **S7.2** | Overlay dataplane backend model. | Done: `userspace-udp` is explicit; reserved backend names fail closed; reconciliation reuses runtime overlay backend. Commit: `feat(netio): add overlay backend model`. |

### Phase 4 -- Release Readiness

| # | Output | Exit |
|---|---|---|
| **S8** | `v0.5.0` release readiness, without v1 bump. | Done: release dry-run, changelog, SemVer tag plan, docs layout and package artifacts prepared. Commit: `chore(release): prepare v0.5.0`. |
| **S8.1** | `v0.5.2` pkg.go.dev close-out. | Done: command package docs and canonical Apache-2.0 license text restored pkg.go.dev command and license rendering. Commits: `docs(docs): document pkg.go.dev command pages`, `fix(docs): restore pkg.go.dev license detection`. |
| **S9** | Documentation and Scorecard hardening. | Done: post-release doc drift закрыт, non-canonical Markdown удалён, one-maintainer Scorecard constraints задокументированы, repository hardening work разделён на follow-up actions без нового релиза. Commit: `docs: sync scorecard and release documentation`. |

### Phase 5 -- Extended Evidence

| # | Output | Exit |
|---|---|---|
| **S10** | Extended E2E and interoperability evidence. | Done: S10.1-S10.7 задают и реализуют Podman-only evidence targets, standard `reports/e2e/<target>/<timestamp>/` artifacts, PR-safe/nightly/manual CI gates, vendor profile skip evidence и benchmark policy separation. Closeout: `docs/ru/20-s10-closeout-analysis.md`. Commits: `docs(interop): plan s10 extended interop evidence`, `test(interop): define extended evidence harness`, `test(interop): add core daemon scenarios`, `test(interop): aggregate routing interop evidence`, `test(interop): verify rfc and overlay boundaries`, `test(netio): add linux dataplane ownership checks`, `test(interop): document vendor interop profiles`, `ci(interop): publish extended evidence artifacts`. |
| **S11** | Full E2E and interoperability execution. | Planned: `docs/ru/21-s11-full-e2e-interop-plan.md` задаёт shared Podman API helper extraction, full local E2E runs, vendor NOS pass/skip evidence, styled HTML reports, remote CI evidence и owner-backend decision gate. |

## 5. Definition of Done

1. Behavior changes have failing tests or manual reproduction before the fix.
2. All Go/proto/analyzer commands run through Podman.
3. `make verify` passes unless a known failing gate and reason are documented.
4. Protocol behavior references the relevant RFC section; vendor behavior uses
   primary vendor docs.
5. User-facing changes update `CHANGELOG.md` and, when appropriate,
   `CHANGELOG.ru.md`.
6. Commit message and PR title follow Conventional Commits.
7. Released artifacts follow SemVer; released tags are never rewritten.
8. Container changes respect Compose Specification and Containerfile /
   `.containerignore` behavior.

## 6. Risk Register

| ID | Risk | L | I | Mitigation |
|---|---|---|---|---|
| R1 | Echo interop regression. | M | H | Keep RFC 9747 interop gate and packet capture evidence. |
| R2 | Analyzer expansion creates noisy gates. | M | M | Keep `golangci-lint` allowlist explicit; add exclusions only with comments. |
| R3 | Interop requires Podman socket access from a container. | M | H | Use `security_opt: label=disable` for dev container only; avoid `privileged`. |
| R4 | GoBGP vulnerability allowlist becomes stale. | M | H | Track allowlist expiry and keep GoBGP gRPC on localhost/trusted networks until upstream fix. |
| R5 | README/pkg.go.dev overclaim feature readiness. | M | M | Treat interop and API surface as source of truth before release notes. |
| R6 | Micro-BFD is mistaken for universal Linux LAG enforcement. | M | H | Keep docs explicit: enforcement works only for selected kernel-bond, OVSDB and NetworkManager ownership paths. |
| R7 | VXLAN/Geneve userspace sockets conflict with kernel/OVS/Cilium/Calico dataplane. | M | H | `userspace-udp` is explicit and reserved backend names fail closed until owner-specific integrations exist. |
