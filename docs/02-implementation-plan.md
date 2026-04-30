# Implementation Plan -- `gobfd`

- **Method:** phased, gated lifecycle with short implementation sprints.
- **Scope:** production-grade BFD daemon, CLI, control-plane API, and interop
  test environment for Linux networking stacks.
- **Cadence:** 8 sprints x 2 weeks to `v0.5.0`, then release
  hardening and maintenance.
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
| ADR-8 | MCP-backed design validation. | Standards, Arista/EOS behavior, and library config claims must be checked against current primary docs. |

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
| Code/docs consistency | Improving | README, changelogs, and planning docs now reflect the implemented RFC set; `docs/03-codebase-consistency-audit.md` tracks remaining gaps. |
| Control-plane API | Partial | Proto/session snapshots and `gobfdctl` output now expose Echo, Micro-BFD, VXLAN, and Geneve vocabulary; generic `AddSession` and `gobfdctl session add` intentionally remain single-hop/multi-hop until dedicated transport-specific APIs exist. |
| Interface monitoring | Green on Linux | `internal/netio` subscribes to rtnetlink `RTMGRP_LINK` and `Manager.HandleInterfaceEvent` transitions sessions on link-down before detection timer expiry. eBPF is deferred; see `docs/s4-linux-netlink-ebpf-research.md`. |
| Auth wiring | Green for static per-session keys | YAML sessions, gRPC `AddSession`, and `gobfdctl session add` now wire RFC 5880 auth into session TX/RX, expose auth type in snapshots, reject missing raw wire bytes, and reset receive sequence knowledge after 2x Detection Time. Dynamic key rotation is deferred to production hardening. |
| pkg.go.dev command page | Weak | `cmd/gobfd` has a one-line package comment. |
| Documentation standards | Improving | Keep a Changelog and SemVer are present; commitlint and doc lint gates are now explicit. |

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
| **S5** | API/CLI coverage for Echo, Micro-BFD, VXLAN, and Geneve. | In progress: proto enum, server mappings, snapshots, and `gobfdctl` list/show/event formatting expose advanced session families; generic `AddSession` rejects recognized transport-specific families until dedicated configuration APIs exist. Commit: `feat(api): expose advanced session type vocabulary`. |
| **S5.1** | State mutation consistency. | Done: `SetAdminDown` routes through the session control channel when the session goroutine is running; startup syncs `cachedState` from atomic state for pre-run administrative changes; wire tests verify the next packet carries `AdminDown` / `DiagAdminDown`. Commit: `fix(bfd): serialize admin-down transition`. |
| **S6** | Production security posture. | ConnectRPC and GoBGP integrations document mTLS/default localhost policy; vulnerability allowlist has owner, expiry, and mitigation. Commit: `docs(security): define production hardening policy`. |
| **S7** | Kubernetes and routing integration hardening for um-docs. | DaemonSet/Helm manifests, Prometheus rules, Arista/FRR/GoBGP examples, and failure drills are documented. Commit: `feat(k8s): add production integration assets`. |

### Phase 4 -- Release Readiness

| # | Output | Exit |
|---|---|---|
| **S8** | `v0.5.0` readiness. | `make verify`, RFC/BGP interop, release dry-run, changelog, SemVer tag plan, and pkg.go.dev documentation all pass review. Commit: `chore(release): prepare v0.5.0`. |

## 5. Definition of Done

1. The change has a failing test or manual reproduction before the fix when it
   changes behavior.
2. All Go/proto/analyzer commands are run through Podman.
3. `make verify` passes unless the task explicitly documents a known failing
   gate and the reason.
4. Protocol behavior references the relevant RFC section and, where vendor
   behavior is discussed, Arista MCP / primary vendor docs.
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
