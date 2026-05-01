# Implementation Plan -- `gobfd`

- **Method:** phased, gated lifecycle with short implementation sprints.
- **Scope:** production-oriented BFD daemon, CLI, control-plane API, and interop
  test environment for Linux networking stacks.
- **Cadence:** 8 sprints x 2 weeks to `v0.5.0`, then release hardening,
  Scorecard hardening, extended E2E evidence, and maintenance.
- **Standards:** [Keep a Changelog 1.1.0], [Conventional Commits 1.0.0],
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

## 1. Decisions

| ID | Decision | Rationale |
|---|---|---|
| ADR-1 | Podman-only Go toolchain. | Raw-socket tests, interop stacks, and analyzer versions must be reproducible without host Go. |
| ADR-2 | `golangci-lint` v2 allowlist. | `default: none` keeps analyzer changes explicit and reviewable. |
| ADR-3 | Keep `CHANGELOG.md` and `CHANGELOG.ru.md`. | User-facing changes must be curated for humans, not dumped from git logs. |
| ADR-4 | SemVer tags only for released module versions. | Released versions are immutable; any fix ships as a new version. |
| ADR-5 | Conventional Commits for commits and PR titles. | Commit type maps cleanly to release notes and SemVer impact. |
| ADR-6 | Compose files follow the Compose Specification. | Interop stacks need portable service, network, volume, and profile semantics. |
| ADR-7 | Containerfiles are native Podman/Buildah inputs. | Use `Containerfile`, `.containerignore`, and minimal capabilities instead of Docker-only assumptions. |
| ADR-8 | Source-backed design validation. | Standards, vendor behavior, and library config claims must be checked against current primary or official docs. |

## 2. Quality Gates

| Gate | Tool | Trigger |
|---|---|---|
| Go build | `make build` | Every code change. |
| Unit and package tests | `make test` | Every code change; always inside Podman. |
| Go language diagnostics | `make gopls-check` | Every code change; official `gopls` diagnostics inside Podman. |
| Go static analysis | `make lint` | Every code change; `golangci-lint` v2 allowlist. |
| Proto lint | `make proto-lint` | API changes and `make verify`. |
| Vulnerability audit | `make vulncheck` | Dependency changes and `make verify`. |
| Markdown | `make lint-md` | Documentation changes. |
| YAML | `make lint-yaml` | Compose, CI, config, and docs tooling changes. |
| Spelling | `make lint-spell` | Documentation and public API text changes. |
| Commit message | `make lint-commit MSG='type(scope): subject'` | Before committing or PR title review. |
| RFC interop | `make interop-rfc` | Protocol behavior changes; currently expected to expose the Echo gap. |
| BGP interop | `make interop-bgp` | GoBGP, route withdrawal, and integration changes. |

No local `go test`, `go build`, `go vet`, `golangci-lint`, or `buf` commands
are valid project evidence. Use Makefile targets backed by Podman.

## 3. Current Reality Snapshot

| Area | Status | Evidence / Gap |
|---|---|---|
| Core build/test/lint | Green | `make verify` passes in Podman and includes `gopls-check`, `golangci-lint`, doc lint, proto lint, and vulnerability audit. |
| RFC 7419 / 9384 / 9468 interop | Green | `make interop-rfc-test` passes these scenarios when the RFC stack is running. |
| RFC 9747 Echo interop | Green | `make interop-rfc-test` verifies Echo `Up`, UDP 3785 packet capture both ways, Echo failure on reflector pause, and recovery. |
| Code/docs consistency | Partial | Canonical EN/RU docs need post-`v0.5.2` release synchronization; `docs/en/codebase-consistency-audit.md` tracks remaining gaps. |
| Control-plane API | Partial | Proto/session snapshots and `gobfdctl` output now expose Echo, Micro-BFD, VXLAN, and Geneve vocabulary; generic `AddSession` and `gobfdctl session add` intentionally remain single-hop/multi-hop until dedicated transport-specific APIs exist. |
| Interface monitoring | Green on Linux | `internal/netio` subscribes to rtnetlink `RTMGRP_LINK` and `Manager.HandleInterfaceEvent` transitions sessions on link-down before detection timer expiry. eBPF is deferred; see `docs/en/linux-netlink-ebpf-research.md`. |
| Linux Micro-BFD enforcement | Partial | GoBFD creates per-member RFC 7130 sessions, tracks aggregate state, has dry-run actuator wiring, and supports explicit Linux kernel-bond sysfs, native OVSDB bonded-port enforcement, and NetworkManager D-Bus bond port activation with operator-selected owner policy. |
| Linux VXLAN/Geneve dataplane coexistence | Partial | GoBFD has an explicit `userspace-udp` backend for dedicated endpoints and fails closed for reserved kernel/OVS/OVN/Cilium/Calico/NSX backend names; actual non-userspace integrations remain future work. |
| Auth wiring | Green for static per-session keys | YAML sessions, gRPC `AddSession`, and `gobfdctl session add` now wire RFC 5880 auth into session TX/RX, expose auth type in snapshots, reject missing raw wire bytes, and reset receive sequence knowledge after 2x Detection Time. Dynamic key rotation is deferred to production hardening. |
| pkg.go.dev command page | Green | `v0.5.2` is indexed on pkg.go.dev, Apache-2.0 is detected, and `cmd/gobfd` has command documentation. |
| Documentation standards | Partial | Keep a Changelog, SemVer, commitlint, and doc lint gates are present; non-canonical temporary research files must not remain in the published Markdown corpus. |
| Extended E2E / interop evidence | Planned | S10 adds a Podman-only evidence matrix for core daemon, routing interop, RFC behavior, Linux dataplane ownership, overlay boundaries, optional vendor profiles, and CI artifacts. |

## 4. Sprints

### Sprint Close Protocol

Every sprint closes with a small, reviewable commit after fresh evidence:

1. Update this plan and both changelogs with the sprint result.
2. Run `make verify` inside Podman.
3. Run `make lint-commit MSG='type(scope): subject'` for the planned commit.
4. Commit with a Conventional Commit subject:
   `type(scope): short imperative summary`.
5. If the sprint changes protocol behavior, also run the matching interop gate
   before the commit and record pass/fail evidence in the sprint notes.

### Phase 1 -- Tooling and Documentation Baseline

| # | Output | Exit |
|---|---|---|
| **S1** | Podman-only analyzer stack, doc lint configs, commitlint config, `.containerignore`, canonical plan. | `make verify` exists; `make lint-docs` runs from the dev container; no host Go commands remain in Makefile test targets. Commit: `chore(lint): establish podman quality gates`. |
| **S1.1** | Full-corpus spelling dictionary. | `make lint-spell` can expand from process docs to all published EN docs without mass false positives. Commit: `chore(docs): expand spelling coverage`. |
| **S1.2** | Optional function-order cleanup. | `funcorder` can be enabled only after large legacy files are split or reordered without unrelated behavior changes. Commit: `refactor: normalize function order`. |

### Phase 2 -- Protocol Correctness

| # | Output | Exit |
|---|---|---|
| **S2** | RFC 9747 Echo fix with packet-level evidence. | Done: `make interop-rfc-test` passes all RFC scenarios, including Echo. Commit: `fix(bfd): complete rfc9747 echo interop`. |
| **S3** | Auth wire integration audit and fixes. | Done for static keys: YAML sessions, gRPC `AddSession`, and `gobfdctl session add` wire auth into TX/RX; static key-store validation prevents nil-key panics; missing raw wire bytes are rejected without panic; failed auth packets do not refresh receive counters; receive sequence knowledge resets after 2x Detection Time; snapshots expose auth type; incomplete API auth key material is rejected. Commits: `fix(bfd): wire declarative authentication`, `fix(bfd): harden auth wire verification`, `fix(bfd): reset auth sequence window`, `feat(api): accept auth key material`, next `feat(cli): add auth session flags`. |
| **S4** | Interface monitor implementation. | Done: Linux rtnetlink link-down events drive immediate BFD `Down` / `Path Down` transition before detection timer expiry; cilium/ebpf was researched and deferred as packet-fast-path tooling, not link-notification tooling. Commit: `feat(netio): react to link state events`. |
| **S4.1** | Code/docs/tooling consistency close-out. | Fix `gopls-check` to use the Linux build context and fail on diagnostics; align README RFC status with implementation; record a repo-wide consistency audit. Commit: `chore(lint): harden gopls diagnostics gate`. |

### Phase 3 -- Control Plane and Operations

| # | Output | Exit |
|---|---|---|
| **S5** | API/CLI coverage for Echo, Micro-BFD, VXLAN, and Geneve. | Partial: proto enum, server mappings, snapshots, and `gobfdctl` list/show/event formatting expose advanced session families; generic `AddSession` rejects recognized transport-specific families until dedicated configuration APIs exist. Commit: `feat(api): expose advanced session type vocabulary`. |
| **S5.1** | State mutation consistency. | Done: `SetAdminDown` routes through the session control channel when the session goroutine is running; startup syncs `cachedState` from atomic state for pre-run administrative changes; wire tests verify the next packet carries `AdminDown` / `DiagAdminDown`. Commit: `fix(bfd): serialize admin-down transition`. |
| **S6** | Production security posture. | Done: security policy documents ConnectRPC, GoBGP, BFD auth, container privileges, and vulnerability gate boundaries; allowlist entries require owner, expiry, reason, and mitigation. Commit: `docs(security): define production hardening policy`. |
| **S6.1** | Linux advanced BFD applicability close-out. | Align code comments, config examples, RFC docs, and audit notes with Micro-BFD actuator and VXLAN/Geneve dataplane ownership limits. Commit: `docs(linux): document advanced bfd applicability`. |
| **S7** | Independent production integration assets. | Done for generic runbooks, Kubernetes manifest hardening, Prometheus rule correction, FRR/GoBGP example documentation, public EOS verification notes, Micro-BFD actuator policy/config wiring, kernel-bond backend, OVS CLI fallback, OVSDB research, native OVSDB backend, NetworkManager D-Bus backend, and overlay backend model. Commit: `feat(examples): add production integration assets`. |
| **S7a** | Production runbooks and manifest hardening. | Generic EN/RU production runbooks, Kubernetes probes/labels, and Prometheus alerts aligned with exported GoBFD metrics. Commit: `docs(examples): add production integration runbooks`. |
| **S7b** | BGP failover interop documentation. | FRR/GoBGP example README, RFC packet checks, troubleshooting matrix, and optional public Arista EOS verification note. Commit: `docs(examples): document bgp failover interop`. |
| **S7.1** | Linux Micro-BFD actuator. | Partial production integration: `MicroBFDActuator` hook, guarded `netio.LAGActuator` policy, config validation, daemon dry-run wiring, explicit kernel-bond backend, OVS CLI fallback, native OVSDB backend, and NetworkManager D-Bus backend are done. Dedicated API/CLI create flows remain S5b. Commit: `feat(netio): add linux lag actuator`. |
| **S7.1b** | Linux LAG actuator config wiring. | Add config validation and daemon wiring for disabled/dry-run/enforce policy modes without destructive Linux changes. Commit: `feat(config): wire micro-bfd actuator config`. |
| **S7.1c** | Kernel bond LAG backend. | Add Linux bonding sysfs backend and daemon enforce wiring for explicit `backend: kernel-bond` plus `owner_policy: allow-external`. Commit: `feat(netio): add kernel bond lag backend`. |
| **S7.1d** | OVS CLI fallback backend. | Add transitional OVS bonded-port backend using `ovs-vsctl add-bond-iface` / `del-bond-iface` for explicit `backend: ovs` plus `owner_policy: allow-external`. Commit: `feat(netio): add ovs lag backend`. |
| **S7.1d2** | OVSDB API research. | Document OVSDB JSON-RPC as the native OVS management API and `libovsdb` as the preferred Go integration route. Commit: `docs(netio): document ovsdb backend path`. |
| **S7.1e** | Native OVSDB backend. | Done: `backend: ovs` selects native OVSDB-backed LAG enforcement for OVS bonded ports with configurable `ovsdb_endpoint`; `OVSLAGBackend` remains a fallback/diagnostics type. Commit: `feat(netio): add ovsdb lag backend`. |
| **S7.1f** | Optional NetworkManager backend. | Done: NetworkManager D-Bus backend deactivates active bond port profiles on member Down and activates remembered or available bond port profiles on member recovery. Commit: `feat(netio): add networkmanager lag backend`. |
| **S7.2** | Overlay dataplane backend model. | Done: `userspace-udp` is an explicit VXLAN/Geneve backend, reserved kernel/OVS/OVN/Cilium/NSX backend names fail closed, and sender reconciliation reuses the runtime receiver backend instead of binding duplicate sockets. Commit: `feat(netio): add overlay backend model`. |

### Phase 4 -- Release Readiness

| # | Output | Exit |
|---|---|---|
| **S8** | `v0.5.0` release readiness. | Done: release dry-run, changelog, SemVer tag plan, docs layout, and package artifacts were prepared without a v1 bump. Commit: `chore(release): prepare v0.5.0`. |
| **S8.1** | `v0.5.2` pkg.go.dev close-out. | Done: command package docs and canonical Apache-2.0 license text restored pkg.go.dev command and license rendering. Commits: `docs(docs): document pkg.go.dev command pages`, `fix(docs): restore pkg.go.dev license detection`. |
| **S9** | Documentation and Scorecard hardening. | Done: post-release doc drift closed, non-canonical Markdown removed, one-maintainer Scorecard constraints documented, and repository hardening work split into follow-up actions without a new release. Commit: `docs: sync scorecard and release documentation`. |

### Phase 5 -- Extended Evidence

| # | Output | Exit |
|---|---|---|
| **S10** | Extended E2E and interoperability evidence. | Planned: `docs/en/18-s10-extended-e2e-interop.md` defines the Podman-only evidence matrix, source validation, sprint breakdown, benchmark policy, and close criteria. Commit: `docs(interop): plan s10 extended interop evidence`. |

## 5. Definition of Done

1. The change has a failing test or manual reproduction before the fix when it
   changes behavior.
2. All Go/proto/analyzer commands are run through Podman.
3. `make verify` passes unless the task explicitly documents a known failing
   gate and the reason.
4. Protocol behavior references the relevant RFC section and, where vendor
   behavior is discussed, primary vendor docs.
5. User-facing changes update `CHANGELOG.md` and, when appropriate,
   `CHANGELOG.ru.md`.
6. Commit message and PR title follow Conventional Commits.
7. Released artifacts follow SemVer; released tags are never rewritten.
8. Container changes respect Compose Specification and Containerfile /
   `.containerignore` behavior.

## 6. Risk Register

| ID | Risk | L | I | Mitigation |
|---|---|---|---|---|
| R1 | Echo interop remains red. | M | H | Make RFC 9747 the first protocol sprint and require packet capture evidence. |
| R2 | Analyzer expansion creates noisy gates. | M | M | Keep `golangci-lint` allowlist explicit; add exclusions only with comments. |
| R3 | Interop requires Podman socket access from a container. | M | H | Use `security_opt: label=disable` for the dev container only; avoid `privileged`. |
| R4 | GoBGP vulnerability allowlist becomes stale. | M | H | Track allowlist expiry and keep GoBGP gRPC on localhost/trusted networks until upstream fix. |
| R5 | README/pkg.go.dev overclaim feature readiness. | M | M | Treat interop and API surface as source of truth before release notes. |
| R6 | Micro-BFD is mistaken for universal Linux LAG enforcement. | M | H | Keep docs explicit: current code enforces explicit kernel-bond, native OVSDB, and NetworkManager D-Bus paths only when the operator selects the matching owner policy/backend. |
| R7 | VXLAN/Geneve userspace sockets conflict with kernel/OVS/Cilium/Calico dataplane. | M | H | `userspace-udp` is explicit and reserved backend names fail closed until kernel/OVS/OVN/Cilium/NSX/Calico integrations exist. |
