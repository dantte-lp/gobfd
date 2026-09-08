# GoBFD Roadmap

![Current Release](https://img.shields.io/badge/Current-v0.6.4-1a73e8?style=for-the-badge)
![Next Release](https://img.shields.io/badge/Next-TBD-34a853?style=for-the-badge)
![Target](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Status projection from Beads, reconciled on 2026-09-08. Beads is the task
> ledger; this document explains the public release sequence and must not be
> used as an independent checklist.

The latest published GitHub release is
[`v0.6.4`](https://github.com/dantte-lp/gobfd/releases/tag/v0.6.4). The immutable
`v0.6.2` and `v0.6.3` tags remain unpublished failed cuts. The v0.6.4 product
artifacts and cumulative notes are verified, and the accepted stable history
and bilingual changelogs have reached `master`. The 13 independent review
findings and final local qualification are accepted on `dev`; this does not
prove their delivery to stable. Maintenance is reopened for the branch gaps
below. The protected `release/v0.6` line keeps GoBGP v3.37.0; v1 and GoBGP v4
development continues on `dev`.

New releases wait until development and final qualification are complete.
Intermediate tasks deliver code and local evidence to `dev`, not release
tags, drafts, or artifacts.

## Status key

| Status | Meaning |
|---|---|
| Done | Accepted in `dev` unless another branch and commit are named; not proof of stable delivery or publication |
| In progress | Active work or independent review is not complete |
| Open | Planned in Beads and not yet accepted |

## v0.6 maintenance baseline

Beads milestone: `gobfd-qj0.8.1` — **Open; reopened for stable delivery**
under `gobfd-qj0.8.1.16`.

The protected `release/v0.6` branch keeps GoBGP v3.37.0 and the existing
`bfd.v1` and YAML runtime contracts. It updates dependencies, tools, CI,
reproducibility, documentation, and test infrastructure without adding BFD
protocol behavior. The v0.6.4 tag, assets, and OCI images are verified.
Qualification `gobfd-qj0.8.1.7` records the accepted `dev` baseline, not
qualification of the current stable heads. The following rows describe
implementation acceptance on `dev`, not the contents of v0.6.4.

| Delivery slice | Status |
|---|---|
| Dependency and tool version inventory | Done |
| Go 1.27 toolchain and CI refresh | Done |
| Go-owned Podman testcontainers harness | Done |
| Interop, integration, and E2E orchestration migration | Done |
| Historical Python tooling island and Docker Compose v5 contract | Accepted then; Python retention superseded by the no-Python policy |
| License, SBOM, OCI provenance, and vulnerability inventory | Done |
| Debian trixie / Oracle Linux 10 image boundary | Done |
| RFC and benchmark public-claim correction | Done |
| Roadmap, Quick Start, architecture, and EN/RU parity | Done |
| Independent review of all v0.6 slices and P0/P1 remediation | Done |
| Final local `dev` qualification | Done |
| Register the isolated `tools/go.mod` with Dependabot | Done |

Release task `gobfd-qj0.8.1.15` is complete after correction: immutable
`v0.6.4` still points to `b1c0bcd7d2e9abed00368b2082e34f521084c087`, all 12
assets and OCI indexes remain verified, and its body now covers v0.6.2-v0.6.4.
PRs `#67` and `#68` delivered the accepted correction to `release/v0.6` and
`master`; this `dev` history contains the separate forward-port. That published
release receipt remains accepted and is not rewritten by the stable-gap review.
Independent review `gobfd-qj0.8.1.8` and all 13 child findings remain closed as
implementation evidence; stable delivery has separate ownership.

### Branch-specific maintenance gaps

The 2026-09-07 comparison uses `dev` at
`401f6a6a0adc20cd440f4de2d424327984c49111`, `master` at
`5680385679d8e03d74226b5fcde269210d63bde8`, and `release/v0.6` at
`145d7a33571b76f234fd06ccb00798586a40b8d5`. The fix for findings 1–7,
`e0e85b1`, is an ancestor of all three heads; current behavior is not
requalified here. The six later fix commits are present only on `dev`:

| Closed finding | Accepted `dev` fix | Current stable gap | Beads delivery owner |
|---|---|---|---|
| `gobfd-qj0.8.1.8.8` | `37f1b26` | Unqualified `ENV GOMEMLIMIT=256MiB` in `deployments/docker/Containerfile` | `gobfd-qj0.8.1.16.2` — Open |
| `gobfd-qj0.8.1.8.9` | `4965ef4` | RFC-count and scratch-image claims in EN/RU performance analysis | `gobfd-qj0.8.1.16.2` — Open |
| `gobfd-qj0.8.1.8.11` | `c5aa0c2` | SIGHUP no-session-drop claims in EN/RU performance analysis | `gobfd-qj0.8.1.16.2` — Open |
| `gobfd-qj0.8.1.8.10` | `b3677f8` — image ownership | Stable uses the older guarded shell runner; applicability unproven | `gobfd-qj0.8.1.16.3` — Open |
| `gobfd-qj0.8.1.8.12` | `0632c99` — host-global lock | Stable lacks this Go lifecycle; applicability unproven | `gobfd-qj0.8.1.16.3` — Open |
| `gobfd-qj0.8.1.8.13` | `d462b4c` — live identity validation | Stable lacks this Go lifecycle; applicability unproven | `gobfd-qj0.8.1.16.3` — Open |

The last three findings require source-backed applicability analysis, not
blind backports or new shell changes. Later v1 audit findings are not implied
to be fixed on stable either. Beads remains the delivery ledger.

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

Beads milestone: `gobfd-qj0.8.2` — **Open; the `dev` baseline is accepted,
stable delivery remains open**.

Development of the v1 product line, including the GoBGP v4 migration, occurs
on `dev` and does not change the GoBGP v3.37.0 boundary of `release/v0.6`.
Final qualification `gobfd-qj0.8.2.6` depends on `gobfd-qj0.8.1` for
maintenance disposition. This does not block the v1 development queue;
its historical publication prerequisite `gobfd-qj0.8.1.15` remains accepted.

### External audit reconciliation

The [complete gist matrix](./gist-audit-reconciliation.md) reviews all 69 findings
against `dev@49085d9`: **25 fixed, 9 partial, 34 open, 1 accepted constraint**.
Partial IDs are H-02, H-06, H-16, H-19, H-23, H-24, H-30, M-05 and M-09.
Every finding has a Beads owner; the matrix also maps the gist's unnumbered
regression and final-qualification proposals. These are source-review results,
not new runtime qualification or proof of stable delivery.

Cross-consumer acceptance `gobfd-qj0.8.2.6.2` joins seven existing implementation
owners for C-06/H-31 without duplicating their work. Micro-BFD ownership
`gobfd-qj0.8.2.2.1` explicitly retains shared validation and callbacks outside
Manager locks (M-09/M-10). The current P0 sequence remains unchanged; companion
recovery below is corrected to its existing P0 Beads priority.

### P0 sequence

| Delivery slice | Status |
|---|---|
| RFC core correctness and loss accounting | In progress; Final retry and P/F exclusion implemented |
| Ownership and configuration reconciliation | In progress; C01.1 through C01.7 implemented |
| Repository-owned shell removal | In progress on `dev`; zero tracked `.sh`/`.bash` files does not mean zero inline shell |
| Secure management defaults | Open |
| Safe GoBGP v4 reconciliation | Open |
| Companion recovery and canonical identity | Open |
| Independent implementation review | Open |
| Interop, scale, security, and release qualification | In progress; the strict local release-quality gate passed in a clean worktree, while broader interop, scale, and security qualification remains open |

The first release-quality maintainability tranche is accepted: all 85 measured
strict-lint findings are resolved, with the pinned cyclop, funlen, and gocognit
subset now reporting zero issues without configuration weakening.
The follow-up complete base profile exposed 151 additional diagnostics. All
151 are resolved, and the pinned 92-linter base plus all 17 build-tag profiles
now report zero issues without configuration weakening.
Two pre-existing `GITHUB_PATH` publication defects found during review were
resolved as separate Beads release blockers, outside the lint refactor.
The current `dev` tree contains no tracked Python source or Python manifests;
stable Python removal remains under `gobfd-qj0.8.2.8.3.5.2`. Mandatory
no-shell/no-Python scope is unchanged: Makefile recipes, workflow blocks,
container commands, test invocations, and Bash packages still require shell
removal work on `dev`.

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

Overlay update (2026-09-06): tasks `gobfd-qj0.8.2.1.9.1` through `.9.3` cover inner-packet
validation, exact tunnel identity, and listener port/cancellation ownership
(C-02/C-03, H-22, M-11 through M-14). Task `.9.4` adds real UDP qualification:
32 captured VXLAN/Geneve packets correlate with 12 accepted and 20 rejected
wire cases; six bounded race-enabled parser fuzz targets pass. Existing parser
and discriminator regression tests retain the remaining acceptance vectors.
This qualifies only the tested Linux userspace IPv4 profile, not production
deployment or vendor interoperability; owner-specific backends remain
unavailable. Beads retains the exact commands and evidence checksums.

Poll/Final task `gobfd-qj0.8.2.1.1.1.1` retains Final intent until successful
transmission and excludes simultaneous P/F flags (M-03/H-04). Child
`gobfd-qj0.8.2.1.1.1.2` starts local Poll on the slow-to-fast Up transition,
using existing transmissions. Child `.1.1.1.3` accepts its Final only after
a successful Poll transmission; failed sends and crossed Final-only replies
cannot confirm an unsent Poll. H-05 remains partial: next P0 child `.1.1.1.4`
owns queued parameter changes, their four-way timer effects, owner-safe config
update integration, truthful apply receipts and FRR/BIRD qualification.
Child `.1.1.1.4.1` records the approved one-active/one-latest-waiting policy
and cancellation without advertised-value rollback. Its detailed
[transaction contract](../superpowers/specs/2026-09-08-gobfd-timer-update-transactions-design.md)
defines approved Final separation, expiry and recovery. Design gate `.4.1`
and implementation `.1.1.1.4.2` are complete: solely config-owned base sessions
support bounded TX/RX transactions and automatic completion receipts, with
local race, lint and independent reviews passed. Live peer qualification
`.1.1.1.4.3` is blocked by P0 image replacement `gobfd-qj0.8.2.8.5.2.4`:
the pinned FRR vendor image was verified to inherit Alpine. No live timer
qualification was run. The updated Beads plan reuses project-owned container
execution, packet capture and daemon JSON receipts, without a new public RPC.
H-05 remains partial until the compliant topology and wire evidence are accepted.
Task `gobfd-qj0.8.2.1.1.2.1` adds peer-driven TX deadline
updates and remote Demand/zero-receive-interval suppression with Final retry.
Parent `.1.1.2` retains full Demand procedures, the remaining H-08 scope,
and FRR/BIRD interoperability qualification.

Task `gobfd-qj0.8.2.1.1.2.2` adds initial zero receive-interval support in
the shared core, ordinary base-session YAML and generic API. Omitted values
keep their defaults; preview per-peer override contracts are unchanged.
The historical 69-ID matrix above remains a snapshot at its named SHA.
H-08 has a bounded implementation slice, not full Poll/Demand qualification.
The existing API error-classification defect is tracked separately as P2
`gobfd-qj0.8.2.1.3.5`: invalid receive intervals are rejected but can return
`Internal` instead of `InvalidArgument`.

RFC core work continues with the tracked gaps in Poll/Final and Demand procedures,
diagnostic and authentication reset behavior, atomic BFD/AdminDown delivery,
RFC 5881/5883 transport demultiplexing, RFC 9764 authenticated padding, and
fail-closed preview boundaries.

### P1 sequence

| Delivery slice | Status |
|---|---|
| Configurable BFD QoS socket policy with packet evidence | Open |
| Committed-latency measurement and corrected performance gates | Open |
| Removal of permanent per-session OS-thread pinning with A/B evidence | Open |

Post-v1 scheduler, kernel, warm-restart, S-BFD, and authentication R&D remains
outside this release contract and is tracked separately in Beads.

## Release contracts

- [v0.6.2 maintenance design](../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md)
- [v1 production design](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
- [RFC compliance matrix](./08-rfc-compliance.md)
- [Development and quality gates](./09-development.md)
