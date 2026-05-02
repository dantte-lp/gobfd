# Аудит консистентности кодовой базы

Дата: 2026-05-01

Область: структура репозитория, README, changelog-и, English/Russian docs,
public API, CLI, configuration schema, Makefile gates и независимая
production-применимость сетевых сценариев.

Источники evidence:
- локальный код и тесты в `cmd/`, `internal/`, `pkg/`, `api/`, `configs/`,
  `deployments/` и `test/`;
- документация `README.md`, `docs/`, `CHANGELOG.md`, `CHANGELOG.ru.md`;
- generic public scenarios: Linux routing hosts, BGP fast failover,
  EVPN/VXLAN и Geneve overlays, Kubernetes host-network daemon deployment,
  Cilium/Calico-style BGP environments, partner edge failover и DCI-style
  fast-failover expectations;
- OVSDB API research note: `docs/ru/ovsdb-api-research.md`;
- официальные и первичные документы для `gopls`, NetworkManager D-Bus,
  `godbus/dbus`, `cilium/ebpf`, Arista EOS BFD behavior и Open vSwitch OVSDB
  behavior;
- Linux advanced BFD applicability note:
  `docs/ru/linux-advanced-bfd-applicability.md`.

## Executive Summary

Реализация опережает часть operator-facing поверхности. Daemon и YAML
configuration поддерживают advanced session families, описанные RFC docs.
Public API vocabulary, snapshots и `gobfdctl` formatting показывают эти
семейства, но `AddSession` и `gobfdctl session add` остаются ограничены
single-hop и multi-hop до dedicated transport-specific configuration APIs.

Ключевой оставшийся gap для независимого production use -- не BFD packet
engine, а operator-facing surface: dedicated advanced API/CLI create flows,
owner-specific overlay backends и broader interop evidence для production
dataplane owners.

## Матрица консистентности

| Область | Реальность кода | Реальность документации | Статус |
|---|---|---|---|
| RFC 5880 auth | YAML, gRPC, CLI, snapshots, sequence reset и hardening реализованы для static per-session keys. | Config и changelog описывают static key material; dynamic rotation отложен. | Consistent |
| Single-hop / multi-hop | API и CLI create только `single-hop` и `multi-hop` sessions. | CLI docs соответствуют поведению. | Consistent |
| Echo / Micro-BFD / VXLAN / Geneve | Daemon, config, reconcile paths, codecs, receivers и tests существуют. | RFC/config docs описывают implemented support. | Consistent |
| Linux Micro-BFD enforcement | Есть per-member sessions, aggregate state tracking, actuator policy, dry-run config wiring, explicit kernel-bond sysfs backend, native OVSDB bonded-port backend, NetworkManager D-Bus backend и OVS CLI fallback type. | RFC/config docs разделяют detect/report, dry-run policy, kernel-bond/OVS enforcement with `allow-external`, OVSDB endpoint config и NetworkManager D-Bus owner policy. | Partial |
| Linux VXLAN/Geneve dataplane coexistence | `userspace-udp` backend bind-ит `localAddr:4789` и `localAddr:6081`; reserved kernel/OVS/OVN/Cilium/Calico/NSX backend names fail closed. | RFC/config docs описывают explicit backend ownership и future non-userspace integration scope. | Partial |
| Advanced API vocabulary | Proto enum, server mappings, snapshots и CLI output знают Echo, Micro-BFD, VXLAN и Geneve. Generic `AddSession` rejects these types until dedicated APIs exist. | Plan разделяет vocabulary/snapshot exposure и advanced create flows. | Partial |
| Unsolicited BFD | Manager auto-creates passive sessions behind explicit policy. | Config и RFC docs описывают opt-in behavior. | Consistent |
| Interface monitor | Linux rtnetlink monitor переводит sessions on link-down. Non-Linux имеет stub behavior. | S4 research doc и implementation plan описывают Linux scope. | Consistent |
| `gopls-check` | Gate работает в Linux build context и падает на diagnostics. | Plan объявляет `gopls-check` как quality gate. | Consistent |
| pkg.go.dev command page | `v0.5.2` индексируется на pkg.go.dev с Apache-2.0 detection и command documentation. | README, changelog и implementation plan считают pkg.go.dev закрытым для текущего релиза. | Consistent |
| Graceful AdminDown | `SetAdminDown` выполняется через session control channel при running goroutine. | Docs claim graceful AdminDown drain. | Consistent |
| Production integration readiness | Core BFD supports BGP fast failover, EVPN/VXLAN checks и Kubernetes daemon deployment patterns. | Generic runbooks, BGP examples, observability assets и Linux applicability notes опубликованы; release evidence ещё нужно закрыть. | Partial |

## Findings

### F1: API/CLI advanced session gap

`internal/bfd`, `internal/netio` и `cmd/gobfd` поддерживают Echo, Micro-BFD,
VXLAN и Geneve через declarative config и daemon reconciliation. Public enum
values и formatting добавлены для:

- `SESSION_TYPE_ECHO`
- `SESSION_TYPE_MICRO_BFD`
- `SESSION_TYPE_VXLAN`
- `SESSION_TYPE_GENEVE`

Generic `AddSession` rejects these recognized types, потому что RFC 7130
требует UDP 6784 per LAG member, RFC 8971 требует Management VNI, а RFC 9521
зависит от Geneve VAP/VNI encapsulation. Создавать их через generic sender
было бы ложным API contract.

### F2: pkg.go.dev surface closed in v0.5.2

Public package pages отображают detected Apache-2.0 license, tagged module
version, README content и command package documentation. Дальнейшие изменения
pkg.go.dev являются editorial improvements, а не release blockers.

### F3: Independent production use needs operator assets

GoBFD -- независимый проект. Public roadmap описывает generic reusable
production scenarios вместо site-specific topology:

- Kubernetes DaemonSet manifests document `NET_RAW`, `NET_ADMIN`,
  hostNetwork, node selectors, probes, labels и namespace assumptions.
- FRR/GoBGP BGP fast-failover docs cover BGP neighbor BFD, `300/300/3`
  timers, packet checks, failure drills и rollback.
- Prometheus alerts map to exported GoBFD metrics.
- Optional public Arista EOS notes separated from runnable examples and tied to
  public vendor documentation.

### F4: Linux advanced BFD needs explicit dataplane ownership

Micro-BFD, VXLAN BFD и Geneve BFD применимы на Linux, но текущий GoBFD не
является универсальным dataplane controller.

Для Micro-BFD GoBFD создаёт одну RFC 7130 session на member link, tracks
aggregate state, wires dry-run actuator policy и может remove/add members для
explicit Linux kernel bonding через sysfs, existing OVS bonded port через
native OVSDB transactions, либо NetworkManager-owned bond ports через D-Bus.

Для VXLAN/Geneve реализован explicit `userspace-udp` backend. Reserved
`kernel`, `ovs`, `ovn`, `cilium`, `calico` и `nsx` backend names fail closed до
появления соответствующих dataplane integrations.

## Sprint Plan

| Sprint | Goal | Deliverable | Commit |
|---|---|---|---|
| S4.1 | Закрыть code/docs/tooling drift. | Harden `gopls-check`, обновить RFC status, записать audit. | `chore(lint): harden gopls diagnostics gate` |
| S5 | Синхронизировать public control plane с daemon capabilities. | Partial: API vocabulary, session snapshots и CLI output покрывают Echo, Micro-BFD, VXLAN, Geneve; dedicated create flows remain. | `feat(api): expose advanced session type vocabulary` |
| S5.1 | Согласовать state mutation paths. | AdminDown transition serialized through session goroutine and covered by wire test. | `fix(bfd): serialize admin-down transition` |
| S6 | Production security policy. | mTLS/localhost policy, vulnerability allowlist expiry, secret-handling docs. | `docs(security): define production hardening policy` |
| S6.1 | Linux advanced BFD applicability. | RFC docs, config examples and code comments aligned with actuator and overlay limits. | `docs(linux): document advanced bfd applicability` |
| S7 | Independent production integration readiness. | Generic runbooks, Kubernetes hardening, alert rules, FRR/GoBGP examples, Micro-BFD actuator, kernel-bond, OVSDB, NetworkManager and overlay backend model. | `feat(examples): add production integration assets` |
| S8 | `v0.5.0` release readiness without v1 bump. | Done: release dry-run, changelog, SemVer tag plan, docs layout and package artifacts. | `chore(release): prepare v0.5.0` |
| S8.1 | `v0.5.2` pkg.go.dev close-out. | Done: command package docs and canonical Apache-2.0 license text restored pkg.go.dev rendering. | `fix(docs): restore pkg.go.dev license detection` |
| S9 | Documentation and Scorecard hardening. | In progress: post-release doc synchronization, Scorecard plan for one maintainer, repository settings truth table, and removal of non-canonical Markdown. | `docs: sync scorecard and release documentation` |

## Current Readiness

| Dimension | Readiness | Notes |
|---|---:|---|
| Core RFC packet engine | 85% | Strong coverage for base, auth, echo, unsolicited and overlays; MPLS/PW remain stubs. |
| API/CLI operational completeness | 62% | Good for base sessions, auth and observability; advanced create/update flows missing. |
| Linux production daemon behavior | 82% | Raw sockets, buffers, rtnetlink, systemd, metrics, serialized drain behavior and Kubernetes examples exist. |
| Independent production applicability | 82% | Runbooks, BGP examples, Micro-BFD enforcement paths and explicit overlay ownership published; non-userspace overlay integrations remain future work. |
| Release/presentation quality | 86% | Release artifacts, changelogs, pkg.go.dev and docs layout are in place; Scorecard hardening and signed provenance remain. |
