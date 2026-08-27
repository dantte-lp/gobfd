# GoBFD Roadmap

![Current Release](https://img.shields.io/badge/Current-v0.6.1-1a73e8?style=for-the-badge)
![Next Release](https://img.shields.io/badge/Next-v0.6.4-34a853?style=for-the-badge)
![Target](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Status projection from Beads, reconciled on 2026-08-27. Beads is the task
> ledger; this document explains the public release sequence and must not be
> used as an independent checklist.

The latest published GitHub release is
[`v0.6.1`](https://github.com/dantte-lp/gobfd/releases/tag/v0.6.1). The immutable
`v0.6.2` and `v0.6.3` tags are unpublished failed cuts; the next maintenance
publication is `v0.6.4`, followed by the additive `v1.0.0` production
contract. The protected `release/v0.6` line keeps GoBGP v3.37.0; v1 and GoBGP
v4 development continues on `dev`.

## Status key

| Status | Meaning |
|---|---|
| Done | Accepted in the current `dev` history |
| In progress | Active work or independent review is not complete |
| Open | Planned in Beads and not yet accepted |

## v0.6 maintenance baseline

Beads milestone: `gobfd-qj0.8.1` — **In progress**.

After the protected branch is created, the release will be prepared on
`release/v0.6`, which keeps GoBGP v3.37.0 and the existing `bfd.v1` and YAML
runtime contracts. It updates dependencies, tools, CI, reproducibility,
documentation, and test infrastructure without adding BFD protocol behavior.
The baseline remains **In progress** until the v0.6.4 tag and GitHub Release
are verified.

| Delivery slice | Status |
|---|---|
| Dependency and tool version inventory | Done |
| Go 1.27 toolchain and CI refresh | Done |
| Go-owned Podman testcontainers harness | Done |
| Interop, integration, and E2E orchestration migration | Done |
| Python 3.14.7 and Docker Compose v5 tooling contract | Done |
| License, SBOM, OCI provenance, and vulnerability inventory | Done |
| Debian trixie / Oracle Linux 10 image boundary | Done |
| RFC and benchmark public-claim correction | Done |
| Roadmap, Quick Start, architecture, and EN/RU parity | Done |
| Independent review of all v0.6.2 slices and P0/P1 remediation | Done |
| Register the isolated `tools/go.mod` with Dependabot | Done |

The release remains untagged until the qualification milestone
`gobfd-qj0.8.1.7`, independent review `gobfd-qj0.8.1.8`, and all required local
and remote release gates are accepted.

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

Beads milestone: `gobfd-qj0.8.2` — **Open; blocked on the accepted and
published v0.6 maintenance baseline (`v0.6.4`)**.

Development of the v1 product line, including the GoBGP v4 migration, occurs
on `dev` and does not change the GoBGP v3.37.0 boundary of `release/v0.6`.

### P0 sequence

| Delivery slice | Status |
|---|---|
| RFC core correctness and loss accounting | Open |
| Ownership and configuration reconciliation | Open |
| Secure management defaults | Open |
| Safe GoBGP v4 reconciliation | Open |
| Independent implementation review | Open |
| Interop, scale, security, and release qualification | Open |

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
