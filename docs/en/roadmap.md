# GoBFD Roadmap

![Current Release](https://img.shields.io/badge/Current-v0.6.4-1a73e8?style=for-the-badge)
![Next Release](https://img.shields.io/badge/Next-TBD-34a853?style=for-the-badge)
![Target](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Status projection from Beads, reconciled on 2026-09-04. Beads is the task
> ledger; this document explains the public release sequence and must not be
> used as an independent checklist.

The latest published GitHub release is
[`v0.6.4`](https://github.com/dantte-lp/gobfd/releases/tag/v0.6.4). The immutable
`v0.6.2` and `v0.6.3` tags remain unpublished failed cuts. The v0.6.4 product
artifacts and cumulative notes are verified, and the accepted stable history
and bilingual changelogs have reached `master`. All independent v0.6 review
findings are resolved and accepted, and final local baseline qualification is
complete. The protected `release/v0.6` line keeps GoBGP v3.37.0; v1 and GoBGP
v4 development continues on `dev`.

## Status key

| Status | Meaning |
|---|---|
| Done | Accepted in the current `dev` history |
| In progress | Active work or independent review is not complete |
| Open | Planned in Beads and not yet accepted |

## v0.6 maintenance baseline

Beads milestone: `gobfd-qj0.8.1` — **Done**.

The protected `release/v0.6` branch keeps GoBGP v3.37.0 and the existing
`bfd.v1` and YAML runtime contracts. It updates dependencies, tools, CI,
reproducibility, documentation, and test infrastructure without adding BFD
protocol behavior. The v0.6.4 tag, assets, and OCI images are verified; final
qualification `gobfd-qj0.8.1.7` is accepted and the baseline is **Done**.

| Delivery slice | Status |
|---|---|
| Dependency and tool version inventory | Done |
| Go 1.27 toolchain and CI refresh | Done |
| Go-owned Podman testcontainers harness | Done |
| Interop, integration, and E2E orchestration migration | Done |
| Removal of repository Python tooling and Docker Compose v5 contract | Done |
| License, SBOM, OCI provenance, and vulnerability inventory | Done |
| Debian trixie / Oracle Linux 10 image boundary | Done |
| RFC and benchmark public-claim correction | Done |
| Roadmap, Quick Start, architecture, and EN/RU parity | Done |
| Independent review of all v0.6 slices and P0/P1 remediation | Done |
| Final local qualification and immutable release evidence | Done |
| Register the isolated `tools/go.mod` with Dependabot | Done |

Release task `gobfd-qj0.8.1.15` is complete after correction: immutable
`v0.6.4` still points to `b1c0bcd7d2e9abed00368b2082e34f521084c087`, all 12
assets and OCI indexes remain verified, and its body now covers v0.6.2-v0.6.4.
PRs `#67` and `#68` delivered the accepted correction to `release/v0.6` and
`master`; this `dev` history contains the separate forward-port. Qualification
`gobfd-qj0.8.1.7` and independent review `gobfd-qj0.8.1.8` are complete after
all 13 child findings were resolved and accepted.

## Legacy S12 reconciliation

The former S12-S20 waterfall document predated the approved Beads release
plan. Its S12 typed-CRUD scope was only partially delivered in `v0.6.0`:

| S12 contract | Current evidence | Status |
|---|---|---|
| `EchoService` CRUD and `gobfdctl echo` | Proto, server, CLI, and tests exist | Done |
| `MicroBFDService` CRUD and `gobfdctl micro` | Proto, server, CLI, and tests exist | Done |
| `OverlayService` for VXLAN/Geneve | No service or CLI command exists | Not delivered |

VXLAN and Geneve configuration/runtime paths do not imply the missing typed
Overlay API. Their public support boundary remains the one stated in
[RFC Compliance](./08-rfc-compliance.md).

The old S13-S20 sprint checklists are superseded. Capabilities such as secure
management, GoBGP v4, S-BFD, kernel backends, or AF_XDP must not be inferred
from those historical targets; only current Beads issues and accepted code are
authoritative.

## v1.0.0 production contract

Beads milestone: `gobfd-qj0.8.2` — **Open; the v0.6 prerequisite is accepted**.

Development of the v1 product line, including the GoBGP v4 migration, occurs
on `dev` and does not change the GoBGP v3.37.0 boundary of `release/v0.6`.

### P0 sequence

| Delivery slice | Status |
|---|---|
| RFC core correctness and loss accounting | Open |
| Ownership and configuration reconciliation | In progress; C01.1 through C01.4b implemented |
| Secure management defaults | Open |
| Safe GoBGP v4 reconciliation | Open |
| Independent implementation review | Open |
| Interop, scale, security, and release qualification | In progress; local release-quality gate remediation and independent review are active |

The first release-quality maintainability tranche is accepted: 82 of the 85
measured strict-lint findings are resolved, and the remaining 3 stay tracked
in Beads for ordered OCI, release, and bootstrap remediation.
Two pre-existing `GITHUB_PATH` publication defects found during review remain
separate release blockers in Beads; they were not folded into the lint refactor.

The accepted C01.1 core provides a canonical session key separate from packet
demultiplexing, serialized typed configuration, compatibility/API, and
unsolicited claims, and immutable static-auth identity. C01.2 adds complete
candidate validation before sender creation, empty desired-set forwarding,
and distinct typed owners for base BFD, Micro-BFD, VXLAN, and Geneve. C01.3a
adds lazy Manager-owned sender leases for accepted physical sessions, exact
last-claim and shutdown release, API source-port release, and explicit
non-owning leases for shared overlay and unsolicited transports. C01.3b adds
the Open/Closing/Closed Manager lifecycle, fail-closed mutation and
subscription gates, registered goroutine waits, exact notification-channel
closure, and sender callbacks after session exit and outside Manager locks. It
also gates echo reconciliation and Micro-BFD group CRUD/reconciliation as one
top-level lifecycle operation. Recursive blocking Close from a synchronous
release callback requires the explicit API design tracked by
`gobfd-qj0.8.2.2.5.1`; callbacks may otherwise reenter Manager APIs safely. It
does not complete C01 or SIGHUP reload. C01.4a adds lazy Manager-owned Echo
sender leases, API/config source isolation, complete Echo candidate preflight,
empty desired-set forwarding, and rollback of newly accepted Echo sessions on
sender acquisition failure. Listener/backend replacement, stable
per-group/per-tunnel owners, Poll/Final negotiation,
transport-aware demultiplexing, and authenticated API principals remain open.
C01.4b serializes startup and SIGHUP compilation/apply, publishes desired and
applied generations, retains bounded six-source receipts, and drives
empty-service gRPC readiness without changing systemd process readiness. It is
non-transactional across sources and does not add automatic retry.
C01.5 loads YAML through the already verified descriptor. C01.6 rejects
unsupported GoBGP strategies from one shared vocabulary. C01.7 rejects
startup-owned SIGHUP changes before any generation or runtime mutation and
allows desired-set membership changes only within startup-open transport
bindings; same-key parameter changes remain explicit reconciliation conflicts.
Socket buffer wiring, ambiguous listener-interface declarations, and strict log
vocabulary validation remain separately tracked follow-up work.

RFC core work begins with the tracked gaps in Poll/Final and Demand procedures,
diagnostic and authentication reset behavior, atomic BFD/AdminDown delivery,
RFC 5881/5883 transport demultiplexing, RFC 9764 authenticated padding, and
fail-closed preview boundaries.

### P1 sequence

| Delivery slice | Status |
|---|---|
| Configurable BFD QoS socket policy with packet evidence | Open |
| Committed-latency measurement and corrected performance gates | Open |
| Removal of permanent per-session OS-thread pinning with A/B evidence | Open |
| Companion binary hardening | Open |

Post-v1 scheduler, kernel, warm-restart, S-BFD, and authentication R&D remains
outside this release contract and is tracked separately in Beads.

## Release contracts

- [v0.6.2 maintenance design](../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md)
- [v1 production design](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
- [RFC compliance matrix](./08-rfc-compliance.md)
- [Development and quality gates](./09-development.md)
