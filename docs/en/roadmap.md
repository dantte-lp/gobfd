# GoBFD Roadmap

Control delivery slice `gobfd-qj0.8.2.1.3.1.1` adds typed queue outcomes,
owner terminal reasons, stop abandonment and a bounded process-lifetime
ListSessions/Prometheus ledger. Get/List/Control Watch share session drop/auth
projections. `gobfd-qj0.8.2.1.3.1.2` extends the ledger to Echo and all
raw/dispatch/recipient/legacy notification outcomes and preserves private
cleanup after raw loss with one parked worker per retained physical entry.
The parent `gobfd-qj0.8.2.1.3.1` is closed for bounded delivery accounting and
private cleanup at `2220cc0865752f2df31859a636d376fdc7fdddec`.
Echo Watch lifecycle is implemented by `gobfd-qj0.8.2.1.3.10`;
raw-loss Watch recovery remains `gobfd-qj0.8.2.1.3.11`. Kernel
`SO_RXQ_OVFL` remains `gobfd-qj0.8.2.1.6`. These development slices do not qualify
stable delivery.

The development causal-observation slice (`gobfd-qj0.8.2.1.2.1` and
`gobfd-qj0.8.2.4.8`) covers coherent scalar facts, per-row versions and
cause-aware GoBGP policy. It does not qualify a stable release or full C-bit
graceful restart. Control Watch registration/status is implemented by
`gobfd-qj0.8.2.1.3.8`; `gobfd-qj0.8.2.1.3.10` adds Echo frozen lifecycle and
mixed snapshots without changing typed Echo RPCs or Control List/Get/GoBGP scope.
See the [sampling contract](01-architecture.md#goroutine-model).
The RFC core parent `gobfd-qj0.8.2.1.2` and its four
children were accepted locally through
`e4f3761a0f1fc59fbf9b2f859923a81125d157bf`; this is not full RFC or release
qualification. H-12 (`gobfd-qj0.8.2.1.3.3`) now implements successful-write
confirmation, a negotiated notification window and owning UDP cancellation.
Local SIGTERM capture qualified eight AdminDown/7 writes to FRR and BIRD and
notification windows of at least 3.000147 seconds. Forty BGP/route samples over
20.280366 seconds retained Established state, unchanged uptime and both routes;
this is sampled evidence, not a continuity or peer-acknowledgement guarantee.
The exact source patch and scope are recorded in
[RFC compliance](08-rfc-compliance.md#section-6816-administrative-control).
The last-claim development slice (`gobfd-qj0.8.2.1.3.9`) reuses that drain for
final Control-session claims before sender release, with one bounded parallel
budget for reconciliation and explicit incomplete outcomes. Non-final claims
preserve wire state. RFC 9468 Section 2 failure cleanup stops unsolicited Down
sessions without administrative drain. These slices do not qualify stable
delivery; raw-loss convergence and RFC 9747 preview blockers remain separately owned.

![Current Release](https://img.shields.io/badge/Current-v0.6.4-1a73e8?style=for-the-badge)
![Next Release](https://img.shields.io/badge/Next-TBD-34a853?style=for-the-badge)
![Target](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Status projection from Beads, reconciled on 2026-10-15 18:10 UTC. Beads is the task
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

## Stack currency and GoBGP compatibility

Maintainer direction (2026-10-13): the primary goal is to move the whole `dev`
stack to current component versions and guarantee GoBGP compatibility. Beads
epic: `gobfd-qj0.8.6` (P0). Scope is `dev`; `release/v0.6` is limited to
`gobfd-qj0.8.1.16.4`. Other former P0 work is P1, except security work.
Components younger than seven days are adopted from their "eligible" date.

Maintainer decisions:

- GoBGP v4.10.0 is the `dev` baseline (`f85ca09`); GoBGP v3 daemons are not
  supported on `dev`.
- ConnectRPC moves to connect-go v2 now. This breaks Go consumers of
  `pkg/bfdpb`; the `bfd.v1` wire protocol is unchanged.

Status of the first stack-currency queue (2026-10-14):

| Step | Item | Status |
|---|---|---|
| 1 | `.8.6.2` runtime Go modules | Pass 1 done (`a0c3a55`); koanf v2.3.8 done (`5584967`); pass 2b updates client_golang v1.25.0 and moby client v0.6.2; dbus v5.2.4 waits for 2026-10-20 |
| 2 | `.8.6.3` tools | Pass 1 done (`a07791e`); govulncheck v1.9.0, osv-scanner v2.7.0, GoReleaser v2.18.3, buf v1.74.0, trivy v0.76.0 wait for 2026-10-16 to 2026-10-21 |
| 3 | `.8.2.4.4` capability preflight | Done (`2816f0c`) |
| 4 | Anycast, Kubernetes, Containerlab on GoBGP v4 | Anycast done (`7255aeb`); Containerlab GoBGP done (`a7c58ae`); Kubernetes code done (`2418fb2`), live run blocked (no k3s) |
| 5 | `.8.6.8` connect-go v2 | Done, in `dev` since `f75c31c` (`2514ae1`, `707ae7d`); wire compatibility pinned by `TestGRPCServerWireCompatibility`; the bead closes after `.8.6.2`; v2.0.1 from 2026-10-21 (`.8.6.10`) |
| 6 | `.8.6.5` base images, `.8.5.2.6` observability | Debian, HAProxy, nginx done (`04df88e`); Oracle Linux from 2026-10-19; observability open |
| 7 | `.8.6.4` Actions, `.8.6.6` peers | Open; several eligible 2026-10-19 |
| 8 | `.8.2.4.5`, `gobfd-0dc`, `.8.6.1` | Native BFD preflight done (`f5c6e42`); support statement and manual Compose open |
| 9 | `.8.4`, `.8.5.2.8` | `.8.4` dev scope done, now P1 for stable delivery; Oracle decision open |

Retrospective of 2026-10-14 against the 2026-10-13 queue:

- Delivered to `dev`: koanf v2.3.8 (`5584967`), H-03 outbound authentication
  validation (`1629b3c`), GoBGP action retry and dampening expiry
  (`eda8e91`).
- Gated on one stack, landing 2026-10-14 21:04 UTC: connect-go v2
  (`2514ae1`, `707ae7d`), pending-Down drop on AdminDown or session removal
  (`76415f7`), RFC 9468 single-hop, connected-subnet, limit and rate
  admission (`e8f9a38`, `cc9b40e`) and tuple-bound replies (`59bfee9`).
- Found by review, not by tests: the retry change could disable an
  unmanaged peer late (fixed in `76415f7`), and RFC 9468 Section 6.1
  single-hop was not enforced for port 4784 packets (fixed in `cc9b40e`).
- Environment: the host root filesystem filled up (1.8 GB free) and was
  cleaned with maintainer approval; `e2ectl rfc` needs the dev container, so
  the RFC gate runs through `interop_rfc_testcontainers`.
- New upstream input: connect-go v2.0.1 (2026-10-14, four bug fixes,
  including draining after a rejected message), eligible 2026-10-21.

Retrospective of the 2026-10-14 evening session against the queue of
2026-10-15 (previous version of this section):

- Step 1 done: the gated stack and a follow-up slice reached `dev` in one
  fast-forward `5584967..f75c31c` (20 commits) at 21:05 UTC, after the
  grpchealth v2.0.0 release-age date. Local gates on `f75c31c`: race 37
  packages, lint 18 profiles, core 64 s, haproxy-health 75 s,
  bgp-fast-failover 111 s, exabgp-anycast 108 s, RFC 159 s. GitHub CI does
  not run on pushes to `dev` (`ci.yml` triggers on `master`, `main` and
  `release/v*`), so local gates are the only evidence for `dev`.
- Step 3 went beyond the plan: `.8.2.1.10.2` closed; P1 `.8.2.1.10.3`
  (policy validation at load and rollback fault injection, `36d5434`,
  `f75c31c`), `.8.2.1.10.4` (shared-lease removal, `e90e9b1`) and
  `gobfd-b52` (`e0a22e0`, `06422d7`) closed; `.8.2.4.1.2` closed.
- Not done: `.8.6.8` stays open because Beads blocks it on `.8.6.2`;
  `.8.2.1.10.1` (IPv6 capture, lease-close fault injection); `.8.2.1.8`
  needs maintainer decisions (step 8).
- Root causes: `gobfd-b52` was tshark `-c` counting packets that fail the
  display filter (tshark(1)), not capture flush latency. Review found that
  weak decoding wraps `detect_mult: 4294967299` to 3; fixed for
  `unsolicited` (`f75c31c`), other fields are `gobfd-4j4`.
- Process: a mutation script from 2026-10-13 hung for a day on an
  interactive `cp` prompt (`cp` is aliased to `cp -i` on this host); scripts
  use `command cp -f`, and background tasks are checked on session resume.
- Repository hygiene: 15 Dependabot PRs targeting `master` closed with a
  pointer to `dev`; 5 merged remote branches, 29 merged worktrees and 87
  merged local branches removed; worktree reports kept under
  `reports/session-20261014/`.
- Environment: the root filesystem is at 96% (13 GB free) after the gates.

Retrospective of the 2026-10-14/15 night session (21:15 to 01:10 UTC)
against the queue of 2026-10-15 (previous version of the queue below):

- Delivered to `dev`: 31 commits `22abeea..f0e442a`, six of them breaking.
- GitHub #86 closed: GoBGP metrics and the `gobfd.gobgp` health check
  (`2a4afb1`, `.8.2.4.6`).
- GitHub #87 closed: loopback metrics and agent-check (`ff2d70f`), API on
  `127.0.0.1:50052` (`0314466`), the
  [transport decision](../superpowers/specs/2026-10-15-management-transport-decision.md)
  (`5879b15`) and `gobfd-330` in four phases: Unix socket (`02ffd16`),
  mTLS (`aae60cf`), socket-only defaults (`319d2bb`), principal-scoped
  claims and mTLS roles (`0d949dd`).
- `.8.2.3.2`: the flight recorder dump is served only behind the
  management boundary (`c7bfe14`).
- Step 2 in part: concurrent lease release under admission and reload
  (`bc768d6`); IPv6 capture waits for `.8.2.1.10.5`.
- Step 5: `.8.2.4.1` operator-disabled peers (`384b8a5`) and `.8.2.4.2`
  canonical session identity (`8536702`, `d9380e1`); both close after
  `.8.2.1.3.2`.
- Outside the queue: the
  [cilium/ebpf evaluation](../superpowers/specs/2026-10-15-ebpf-evaluation.md)
  (`b5a24b5`), `gobfd-376` moved to the backlog.
- Not done: step 1 (no update is eligible before 2026-10-15 13:02 UTC);
  step 3 `.8.2.1.8` (decisions); step 4 `.8.5.2.7` (containerlab v0.81.0
  from 2026-10-18); step 6 `.8.6.1` (`.8.6.9`); step 7 `gobfd-4j4`; step 8:
  no maintainer decision received.
- Found by review, not by tests: gating BFD Up on the canonical-session
  check left a peer GoBFD had disabled down after the ambiguity cleared;
  fixed before the merge (`d9380e1`). grpc-go clients need ALPN `h2` on the
  mTLS listener (measured, `aae60cf`).
- New beads: `gobfd-6de` (production Compose Prometheus cannot scrape
  metrics; predates `ff2d70f`), `gobfd-gyg` (per-session GoBGP opt-out, P3),
  `gobfd-5w0` (#88 design).
- Process: `bd update --notes` replaced existing notes on two beads; they
  were restored from `bd history`, dated entries use `--append-notes`. Edits
  once landed in the main checkout through `cd` in parallel commands and
  were moved to the worktree. A gate run on a superseded revision was left
  to finish, because Ryuk is disabled and an interrupted run leaves
  containers.
- Local gates on `f0e442a`: race 38 packages, lint 18 profiles,
  bgp-fast-failover 110 s, BGP interop 175 s. Root filesystem at 89%
  (34 GB free).

Priority changes of 2026-10-15, from a scan for open blockers with a lower
priority than the bead they block:

- `.8.2.1.3.2` P1 to P0: it blocks P0 `.8.2.4.1` and `.8.2.4.2`, whose code
  is done.
- `.8.2.1.10.5` P1 to P0: it blocks security P0 `.8.2.1.10.1`.
- `gobfd-6de` P2 to P1: the documented Compose deployment has no working
  metrics scrape.
- Q4 and Q5 slices `.8.2.8.1.4`, `.8.2.8.5.1`, `.8.2.8.6`, `.8.2.8.6.2` and
  their children (12 beads) P2 to P1: they block P1 sprint closures.
- Kept: epic `.8.2.3` (P0) depends on epic `.8.2.2` (P1, 13 open beads).

Retrospective of the 2026-10-15 day session (01:25 to 18:10 UTC) against
the queue of 2026-10-15 01:20 UTC (below, updated in place):

- Delivered to `dev`: 9 commits `393e0d8..044d776`, three of them breaking.
- Step 2 done: `.8.2.1.3.2` closed (Manager revisions, gap marker in the
  reserved last subscriber slot, additive `bfd.v1` revision fields, `GAP`
  and `SYNC_COMPLETE`, ambiguous `GetSession` fails with
  `FAILED_PRECONDITION`); `.8.2.4.1` closed (GoBGP resync after a stream gap,
  dampening that never holds a Down, RFC 5882 Section 10.2 citation);
  `.8.2.4.2` closed.
- Not done: step 1 (client_golang v1.25.0 eligible since 13:02 UTC, not
  started); steps 3 to 9 unchanged; no maintainer decision received.
- Found by review, not by tests: the H-31 type filter would have dropped the
  gap marker (the gap check now runs first); stale dampening comments and
  the RFC 5882 Section 4.3 citation that the 2026-09-08 matrix lists under
  H-27. Found by tests: duplicate-state filtering broke dampening tests,
  so it applies only to sessions touched by a resync. One boundary mutation
  (`<=` to `<`) survived until a covered event was added to the test.
- Measured: `buf breaking` against `dev` exit 0; a field-deletion probe
  exits 100.
- Process: the attribution grep matched a "regenerated ... buf" phrase in a commit
  message (reworded before the push); `buf breaking` from a worktree needs the repository
  mounted at its absolute path.
- New beads: `.8.2.1.3.6` (P1, deletion events), `.8.2.4.7` (P1, dampening
  observability). No new priority inversion; the only one left is
  `.8.2.3` on `.8.2.2`.
- Gates: on `d42553e` race 38 packages, lint 18 profiles, core 69 s,
  haproxy-health 81 s, bgp-fast-failover 109 s, exabgp-anycast 110 s, BGP
  interop 176 s; on `d65b994` race 38, lint 18, bgp-fast-failover 110 s, BGP
  interop 175 s; lint 18 on `35155e3`.
- Cleanup with maintainer approval: 15 merged task worktrees and their
  local branches removed (`task/preview-boundaries` was the duplicate of
  `b0d1620`); worktree reports kept under `reports/session-20261015/`;
  GoBFD build leftovers removed (47 untagged images, 8 stale buildah
  working containers, 14 stale gate images); root filesystem free space
  17 GB to 31 GB.
- Version recheck at 18:10 UTC (`proxy.golang.org`, go.dev, `gh api
  graphql`): no change except containerlab v0.81.1 (2026-10-15 08:21,
  eligible 2026-10-22; Docker management-subnet fix).
- Handoff: [agent handoff 2026-10-15](../superpowers/plans/2026-10-15-agent-handoff.md).

Continuation on 2026-10-15 after the 18:10 UTC snapshot:

- Closed in local `dev`: `gobfd-6de`, `gobfd-qj0.8.2.1.3.7`,
  `gobfd-qj0.8.2.1.14`, `gobfd-qj0.8.2.1.3.6`,
  `gobfd-qj0.8.2.5.1` through `gobfd-qj0.8.2.5.3`,
  `gobfd-qj0.8.2.4.7`, `gobfd-4j4`, and `gobfd-8f3.7` through
  `gobfd-8f3.9`. Accepted code reaches
  `9eaa4707877505a2439039957e606fd4fdfd3a82`. These twelve closures do not close
  parent milestones with remaining security dependencies.
- UDP buffer policy now reaches every existing daemon UDP construction
  path; real Linux probes cover control/Echo/Micro-BFD listeners, four sender
  paths and duplex VXLAN/Geneve. Invalid logging vocabulary fails before
  startup or reload mutation. The combined critical race gate passes all ten
  affected packages at the accepted source tree. Final bounded gates at
  `9eaa4707877505a2439039957e606fd4fdfd3a82`: four binaries built with
  `CGO_ENABLED=0` and `-trimpath`; 18 canonical lint profiles report zero
  issues; `go vet ./...`, `go mod tidy -diff` and `go mod verify` pass.
- Production Compose rendered all ten tracked files. In an isolated shared
  network namespace, the production monitoring configuration reported one
  healthy Prometheus target at `127.0.0.1:9100` and a healthy Grafana data
  source; listeners on ports 3000, 9090 and 9100 remained loopback-only.
- Companion live qualification at
  `55e3f94ee2af82bd6cb9377de1708b9e77834341`: HAProxy paused-sidecar
  exclusion and recovery passed in 97.29 s; ExaBGP announcement, withdrawal
  and restoration passed. Local evidence is under
  `reports/session-20261015-continuation/`. These gates do not qualify later
  changes or the full v1 release envelope.
- Strict `govulncheck` at
  `9eaa4707877505a2439039957e606fd4fdfd3a82` reports zero reachable
  vulnerabilities and zero vulnerable imported packages. Two module-only
  findings remain; they do not establish a clean dependency graph for stable.
- Remote integration follows the reviewed PR rule in `CONTRIBUTING.md`.
  Publication uses `task/v1-runtime-recovery-20261015`; `origin/dev` is not
  updated directly. Opening even a draft PR triggers the full test suite in
  `.github/workflows/build.yml`, so that step remains pending under the
  current critical-tests-only instruction. Local acceptance is not remote
  integration.
- Stable v1 remains blocked by the approved
  [GoBGP advisory gate and 24-hour qualification contract](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md).
  On 2026-10-15 the [Go vulnerability database](https://vuln.go.dev/ID/GO-2026-4736)
  still lists no fixed GoBGP v4 range. The development-only exception does
  not lift that stable gate. The 24-hour qualification has not run.

Ordered queue from 2026-10-15, with accepted continuation slices marked
below. Read the release notes across each range
before an update:

1. Second passes by eligibility (UTC):
   - 2026-10-15 pass 2b: pins updated to
     [client_golang v1.25.0](https://github.com/prometheus/client_golang/releases/tag/v1.25.0)
     (13:02; minimum Go 1.26; breaking `api/prometheus/v1` changes are unused)
     and [moby client v0.6.2](https://github.com/moby/moby/releases/tag/client/v0.6.2)
     (20:07; cancellation and deadlines during HTTP upgrade). Eight selected
     module versions change; runtime/tools graph counts stay 200/455.
     Inventory uses an explicit dev observation epoch and deferred decisions
     (`.8.2.8.7.6`); legacy external evidence is not reused. Generation collects
     exact-version license responses without claiming fresh upstream or security
     review. `.8.6.2` remains open
     for dbus; live Moby/Podman qualification is not part of this pass;
   - 2026-10-16: GoReleaser v2.18.3 (03:28);
   - 2026-10-18: govulncheck v1.9.0, containerlab v0.81.0 (v0.81.1 from
     2026-10-22);
   - 2026-10-19: osv-scanner v2.7.0, Actions, `oraclelinux:10-slim`, BIRD
     v3.4.0, ExaBGP 5.0.14;
   - 2026-10-20: dbus v5.2.4 (includes v5.2.3 allocation hardening);
   - 2026-10-21: connect-go v2.0.1 (`.8.6.10`), buf v1.74.0, trivy v0.76.0
     (no security fixes listed).
2. Done on 2026-10-15: P0 `.8.2.1.3.2` versioned snapshots, unambiguous
   Get and gap-aware Watch (`9385346`, `36fbf75`, `5cd96b6`, `e85a867`, `d42553e`); `.8.2.4.1` GoBGP resync after stream
   gaps (`5cd96b6`) and recovery-only dampening (`9fdd2c9`, `d65b994`,
   `35155e3`); `.8.2.4.2` closed. Deletion events `.8.2.1.3.6` and
   dampening observability `.8.2.4.7` are now closed. Epic `.8.2.4` still
   depends on `.8.2.3`.
3. Development qualification on 2026-10-16: `.8.2.1.10.5` interface-bound
   listeners and `.8.2.1.10.1` dual-stack capture passed on
   `task/unsolicited-listeners`, base `f907e8b6ef5c247d4cc9a4a175f87ca8a3e26981`,
   source patch `6f2f3c4587ba00bee32b599acbe886fa0f014774289b7887fb7232edbba0f786`.
   Evidence: `reports/unsolicited-listeners-wire-20261016/receipt.md`;
   four Up/Up sessions with no configured passive session, two IPv4 and two IPv6.
   Stable delivery remains unqualified. Natural Down and failed-setup TX-stop
   `.8.2.1.10.6` uses an independent owner budget, retained RFC 5880 state,
   ownership-aware suppression and attempt-qualified cleanup; acceptance
   remains pending before parent `.8.2.1.10`.
   `.8.2.1.8` follows the step 9 decisions.
4. Done: `gobfd-6de`, production Compose metrics, with the Prometheus
   targets API check described above.
5. Done: `.8.2.5.1` to `.8.2.5.3`, ExaBGP and HAProxy reconnect, snapshot
   recovery and unambiguous identity. Parent `.8.2.5` still waits for `.8.2.3`.
6. P1 `.8.2.3.3` systemd syscall, address-family and FD contract;
   `.8.2.3.1` component readiness.
7. `.8.5.2.7` Containerlab FRR from 2026-10-18; `.8.6.1` manual Compose on
   GoBGP v4 after `.8.6.9`.
8. Done: `gobfd-4j4`, raw range, list-shape and case-alias checks for
   `detect_mult` before weak decoding.
9. Maintainer decisions: Oracle Linux `.8.2.8.5.2.8`; closing PR `#62`;
   filing the GO-2026-4736 vulndb correction; host DNS `.8.6.9`; the
   `.8.2.1.10.5` listener design; for `.8.2.1.8`: reject `AddEchoSession`
   without a running Echo receiver or start one for API sessions, a
   `micro_bfd.enabled` key, capability status in the API or metrics; running
   CI on pushes to `dev` (`gobfd-20z`).

Done before 2026-10-14 and kept for reference: `gobfd-0dc` supported GoBGP
version statement (`1381879`), `.8.2.8.5.2.6` observability on trixie
(`5cf77e9`).

Blocked by the environment: Kubernetes live run `.8.5.2.5.3` (no k3s),
Containerlab vendor runs (no containerlab binary or vendor images).

Version matrix measured on 2026-10-14; GitHub releases, Go modules and go.dev rechecked on 2026-10-15 at 18:10 UTC, other channels not rechecked. Sources: `gh api graphql` releases,
`proxy.golang.org`, `go list -m -u`, Docker Hub and GHCR registry APIs,
`deb.frrouting.org` package index.

| Component | Channel | `dev` | Latest | Eligible | Owner |
|---|---|---|---|---|---|
| Go toolchain | go.dev | 1.27.2 | 1.27.2 | current | done |
| GoBGP | GitHub release | v4.10.0 | v4.10.0 | current | done |
| connect-go | Go module | v2.0.0 | v2.0.1 | 2026-10-21 | `.8.6.10` |
| grpchealth | Go module | v2.0.0 | v2.0.0 | current | done |
| grpc-go | Go module | v1.84.0 | v1.84.0 | current | done |
| koanf/v2 | Go module | v2.3.8 | v2.3.8 | current | done |
| client_golang | Go module | v1.25.0 | v1.25.0 | current | `.8.6.2` |
| client_model | Go module | v0.6.3 | v0.6.3 | current | done |
| godbus/dbus/v5 | Go module | v5.2.2 | v5.2.4 | 2026-10-20 | `.8.6.2` |
| moby api / client | Go module | v1.56.1 / v0.6.2 | v1.56.1 / v0.6.2 | current | `.8.6.2` |
| golangci-lint | tools module | v2.14.0 | v2.14.0 | current | done |
| govulncheck | Go module | v1.8.0 | v1.9.0 | 2026-10-18 | `.8.6.3` |
| osv-scanner | GitHub release | v2.6.0 | v2.7.0 | 2026-10-19 | `.8.6.3` |
| buf | GitHub release | v1.73.0 | v1.74.0 | 2026-10-21 | `.8.6.3` |
| Docker Compose provider | GitHub release | 5.6.0 | 5.6.0 | current | done |
| syft / trivy | GitHub release | v1.54.1 / v0.75.0 | v1.54.1 / v0.76.0 | trivy 2026-10-21 | `.8.6.3` |
| goreleaser | GitHub release | v2.18.2 | v2.18.3 | 2026-10-16 | `.8.6.3` |
| containerlab | GitHub release | 0.79.0 | v0.81.1 | v0.81.0 2026-10-18, v0.81.1 2026-10-22 | `.8.5.2.7` |
| debian:trixie-slim | Docker Hub digest | `a29215f6` (13.7) | `a29215f6` | current | done |
| oraclelinux:10-slim | Docker Hub digest | `965eb786` | `ea76251f` | 2026-10-19 | `.8.6.5` |
| HAProxy | Docker Hub trixie | 3.4.6 | 3.4.6 | current | done |
| nginx (mainline) | Docker Hub trixie | 1.31.6 | 1.31.6 | current | done |
| Prometheus | GitHub release | v3.15.0 | v3.15.0 | current | done |
| Grafana | GitHub release | 13.2.4 | 13.2.4 | current (security exception) | done |
| FRR | deb.frrouting.org frr-10.7 | 10.7.1 | 10.7.1 | current | done |
| BIRD | GitHub tag | v3.3.2 | v3.4.0 | 2026-10-19 | `.8.6.6` |
| ExaBGP | GitHub release | 5.0.13 | 5.0.14 | 2026-10-19 | `.8.6.6` |
| Holo | GHCR bundle | v0.9.0 | v0.9.0 | current | done |
| Thoro/bfd | Git commit | `5d09e29` | `5d09e29` (no commit since 2021-05-15) | decision | `.8.6.6` |
| Nokia SR Linux | GHCR | 25.10.2 | 26.7.2 | now | `.8.5.2.7` |
| Arista cEOS | vendor portal | 4.36.0.1F | not verified | — | `.8.5.2.7` |
| GitHub Actions | GitHub release | 7 of 11 updated | artifact actions, codeql v4.38.3, goreleaser-action v7.3.0, scorecard v2.5.0 | 2026-10-14 to 2026-10-19 | `.8.6.4` |

Arista cEOS currency is unverified: `arista-mcp` was unavailable on
2026-10-13. XRd, SONiC and VyOS images are vendor-gated or mutable
third-party tags. The k3s cluster for the Kubernetes example is external and
unpinned.

## GitHub issues reconciliation 2026-10-15

Source: `gh api graphql` on `dantte-lp/gobfd` issues (3 open, 0 closed),
checked against `dev@610ce2e`, the approved
[v1 design](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
and the RFC texts named below.

| Issue | Beads | State on `dev` | Remaining |
|---|---|---|---|
| [#86](https://github.com/dantte-lp/gobfd/issues/86) GoBGP v4 peer control | `gobfd-qj0.8.2.4.6` (P1), parent `.8.2.4` | Done; issue closed. v4.10.0 client, preflights, v4-only policy and docs, action retries, failover e2e on GoBGP v4.10.0, GoBGP metrics and the `gobfd.gobgp` health check (`2a4afb1`) | No release contains v4 support yet |
| [#87](https://github.com/dantte-lp/gobfd/issues/87) secure listen defaults | `gobfd-qj0.8.2.3.4` (P0), with `gobfd-330` and `.8.2.3.2` | API `:50051` and metrics `:9100` on all addresses; default `gobgp.addr` `127.0.0.1:50051` uses the same port; packages install `configs/gobfd.example.yml` as `/etc/gobfd/gobfd.yml` with the same wildcard binds; the HAProxy agent binds every address | `.8.2.3.4` done (`ff2d70f`, `0314466`): API `127.0.0.1:50052` and metrics `127.0.0.1:9100` by default, agent `listen_addr`, startup warning, side-by-side port test; Done; issue closed. `gobfd-330.1`–`.4`: Unix socket with peer-UID principals, mTLS, socket-only defaults with anonymous loopback reads only (`319d2bb`), principal-scoped claims and mTLS roles (`0d949dd`) |
| [#88](https://github.com/dantte-lp/gobfd/issues/88) anycast VIP and HAProxy health | `gobfd-5w0` (P2), blocked by `.8.2.3` and `.8.2.5.2` | not started | Design spec first |

Architecture notes for `gobfd-5w0`:

- RFC 4786 Section 4.4.1 couples advertisement to service availability and
  asks for a minimum delay before re-advertisement.
- BFD AdminDown is not a reliable withdrawal trigger: RFC 5882 Section 3.2
  says a system SHOULD NOT report a connectivity failure for AdminDown to a
  client with its own liveness detection, which BGP has. `bfd.v1` also has no
  AdminDown RPC.
- GoBGP v4.10.0 `AddPath`/`DeletePath` (`proto/api/gobgp.proto`) can own the
  VIP path; `DisablePeer` stays a fallback.
- HAProxy `show stat` and `show info` on the stats socket report frontend and
  server state (HAProxy `doc/management.txt`).
- Shape: a separate companion like `gobfd-exabgp-bridge`, not a daemon
  module: health source, hold-down state machine, GoBGP path owner, drain
  command, metrics.

`gobfd-5w0` waits for its blockers and a design review. Priority changes
are recorded with the 2026-10-15 retrospective above.

## Remaining work before v1.0.0

Beads on 2026-10-15 18:10 UTC: 171 open or in-progress beads in the v1
scope, epics and the `.8.2` milestone included. This is the historical
18:10 snapshot; continuation closures above supersede its task states.
The count excludes the root
`gobfd-qj0.8` and the post-v1 beads listed at the end. Counts are P0/P1/P2.

| Workstream | Beads | Open | State |
|---|---|---|---|
| Stack currency | `.8.6` | 11 (9/2/0) | Five in progress; dated second passes 2026-10-15 to 2026-10-21; `.8.6.1` waits for `.8.6.9` |
| RFC core and loss accounting | `.8.2.1` | 30 (4/23/3) | P0 `.1.8`, `.10`, `.10.1`, `.10.5`; `.1.3.2` done; deletion events `.1.3.6` and loss accounting `.1.3.1` open; Poll/Final and Demand in progress; live timer qualification `.1.1.1.4.3` open |
| Performance gates | `.8.2.1.4.1`, `.8.2.1.6`, `.8.2.1.7` | 3 (0/3/0) | Latency baseline, QoS socket policy, OS-thread pinning removal |
| Ownership and configuration | `.8.2.2`, with `gobfd-8f3` | 13 (0/11/2) | Four in progress; typed replace, Micro-BFD transactions, socket buffers, listener bindings |
| Secure management | `.8.2.3` | 3 (1/2/0) | `gobfd-330`, `.3.2`, `.3.4` done; `.3.1` and `.3.3` open |
| GoBGP v4 | `.8.2.4` | 2 (1/1/0) | `.4.1` and `.4.2` closed; `.4.7` dampening observability open; epic waits for `.8.2.3` |
| Companions | `.8.2.5` | 4 (0/4/0) | ExaBGP and HAProxy reconnect, canonical identity |
| Independent review | `.8.2.7` | 1 (0/1/0) | Waits for `.8.2.4` and `.8.2.5` |
| Quality sprints Q3 to Q6 | `.8.2.8` | 90 (6/81/2), one P3 | Q3 zero shell and Python 12; Q4 artifacts and images 25 (P0: trixie images `.8.5.2`, FRR `.8.5.2.4`, failover and ExaBGP `.8.5.2.5`, Containerlab `.8.5.2.7`, Oracle `.8.5.2.8`); Q5 semantics and Sonar 21; Q6 qualification, documentation, review and closure 27; four more |
| Release qualification | `.8.2.6` | 3 (0/2/1) | Depends on `.8.1`, `.8.2.4`, `.8.2.5`, `.8.2.7`, `.8.4` |
| v0.6 maintenance disposition | `.8.1` | 5 (0/5/0) | Required by `.8.2.6` |
| Vulnerability allowlist | `.8.4` | 1 (0/1/0) | Entries expire 2026-12-31 |
| Standalone | `gobfd-6de`, `gobfd-20z`, `gobfd-31o`, `gobfd-4j4` | 4 (0/3/1) | Compose metrics, CI on `dev` decision, attribution follow-up (stalled), `detect_mult` range checks |

Critical path: remaining ownership `.8.2.2` and management `.8.2.3`, then
parent closures `.8.2.4` and `.8.2.5`, independent review `.8.2.7`, the
Q6 closure `.8.2.8.7` and the release
qualification `.8.2.6`; security P0 `.8.2.1.10` and `.8.2.1.8` and the
image P0 beads run beside it. The release notes for v1.0.0 must cover the
breaking changes already on `dev`: connect-go v2 for Go consumers of
`pkg/bfdpb`, GoBGP v4 daemons only, the management API on the Unix socket
`/run/gobfd/gobfd.sock` (TCP `127.0.0.1:50052` only when configured, no
anonymous mutations), metrics on `127.0.0.1:9100`, the flight recorder
behind the management boundary, one BFD session per GoBGP neighbor,
dampening that never holds a Down (positive `reuse_threshold` and
`max_suppress_time` required), and `GetSession` by an ambiguous peer
address failing with `FAILED_PRECONDITION` (`.8.2.8.7.2.4`).

Outside v1: `gobfd-5w0` (#88 companion), `gobfd-gyg`, `gobfd-376`,
`gobfd-dor`, `.8.5` (Dependabot on `master`).

## Plan reconciliation 2026-10-13

Heads: `dev` and `origin/dev` at `524ba67`, `origin/master` at `5680385`,
`origin/release/v0.6` at `145d7a3`. No commits reached any of them after
2026-09-08. Two task worktrees hold no commits beyond `dev`.

Measured on `dev@524ba67` with local `go1.27.0`:

- `govulncheck@v1.7.0 ./...` exits 1 with 13 reachable advisories. Twelve are
  fixed in `go1.27.2` and `golang.org/x/net` v0.60.0: GO-2026-6599, 6600,
  6603, 6604, 6605, 6607, 6608, 6610, 6611, 6612, 6613, 6617. The `go1.27.2`
  release (2026-10-08) contains security fixes to the go command,
  `crypto/tls`, `html/template`, `net/http`, `net/textproto` and `os`
  ([Go release history](https://go.dev/doc/devel/release)). Owner:
  `gobfd-qj0.8.2.11`, P0.
- `make vulncheck` exits 1. Allowlist entries GO-2026-4736 (GoBGP v3) and
  GO-2026-5932 (`x/crypto` openpgp, module-only) expired on 2026-09-30 on
  `dev`, `master` and `release/v0.6`. GoBGP v4 can fix GO-2026-4736 on `dev`
  only, because `release/v0.6` keeps GoBGP v3.37.0. Per-branch decision owner:
  `gobfd-qj0.8.4`, P0. Dates are not extended without a recorded maintainer
  decision.
- Stable exposure of `v0.6.4` and the failed `master` nightly E2E run
  `38376260900` (Compose build of `test/interop/compose.yml`, cause not
  determined) belong to `gobfd-qj0.8.1.16.4`.
- Fifteen Dependabot PRs and external PR `#62` target `master`; nine of the
  sixteen are conflicting. Disposition owner: `gobfd-qj0.8.5`, P2.

Plan changes:

- Final qualification `gobfd-qj0.8.2.6` now depends on `gobfd-qj0.8.2.11` and
  `gobfd-qj0.8.4`.
- Live timer qualification `.1.1.1.4.3` depends on the accepted BASE FRR
  consumer `.8.5.2.4.2.1` instead of the whole image parent. Its harness
  (`test/interop`) contains no executable Alpine or `quay.io/frrouting`
  reference. Maintainer confirmation is pending.
- Q3 tasks `.8.2.2.5.1` and `.8.2.2.8` and task `gobfd-31o` are recorded as
  stalled, not done.

Ordered P0 queue on `dev` (superseded by the stack-currency queue above):

1. `gobfd-qj0.8.2.11` — `go1.27.2` and `x/net` v0.61.0; implemented on `dev`;
   the twelve advisories are no longer reported.
2. `gobfd-qj0.8.2.4.3` — first GoBGP v4 slice. It moves the client and every
   GoBGP daemon consumer together, because v3 `/apipb.GobgpApi` and v4
   `/api.GoBgpService` are wire-incompatible. Removes GO-2026-4736 from `dev`.
   Delivered to `dev` (`f85ca09`..`8365f1c`) with GoBGP v4.10.0
   for the client and the shared peer recipe consumers. Local amd64 gates on
   2026-10-13: `TestBGPBFDTopologyTestcontainers` passed in 359.25 s (four
   race-enabled subtests, no skips, GoBGP version 4.10.0); BGP fast-failover
   testcontainers passed in 105.39 s; the RFC testcontainers gate passed in
   162.12 s with four race scenarios. Manual Compose paths were not rerun. `govulncheck` reports no reachable GO-2026-4736 finding;
   the module-level finding remains allowlisted (step 3). ExaBGP anycast,
   Kubernetes and Containerlab remain on GoBGP v3.37.0 images under
   `gobfd-qj0.8.2.8.5.2.5.2`, `gobfd-qj0.8.2.8.5.2.5.3` and
   `gobfd-qj0.8.2.8.5.2.7`.
3. `gobfd-qj0.8.4` — expired allowlist decisions per ID and branch; the `dev`
   decision for GO-2026-4736 depends on step 2. Maintainer decisions of
   2026-10-13: `dev` uses GoBGP v4.10.0 with a `ModuleOnly` GO-2026-4736 entry
   for `github.com/osrg/gobgp/v4`, and the GO-2026-5932 `ModuleOnly` entry is
   renewed, both until 2026-12-31; `release/v0.6` accepts GO-2026-4736 on GoBGP
   v3.37.0 until 2026-12-31 with the GoBGP API on localhost or a trusted
   management network only, delivered separately in `gobfd-qj0.8.1.16.4`.
4. `gobfd-qj0.8.2.1.1.1.4.3` — live timer reload qualification against FRR
   and BIRD.
5. `.8.5.2.5.2`, `.8.5.2.5.3`, `.8.5.2.6`, `.8.5.2.7`, `.8.5.2.8` — remaining
   image consumers and the Oracle Linux product decision.
6. Remaining `gobfd-qj0.8.2.4` reconciliation scope, RFC core and ownership
   children in Beads `bd ready` order.

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
Manager locks (M-09/M-10). The gist reconciliation left the P0 sequence
unchanged; the 2026-10-13 order above supersedes it. Companion
recovery below is corrected to its existing P0 Beads priority.

### P0 sequence

Since 2026-10-13 these slices are P1 in Beads, except security work (secure
management defaults, outbound authentication, unsolicited admission and
preview boundaries); the stack-currency epic `gobfd-qj0.8.6` leads.

| Delivery slice | Status |
|---|---|
| RFC core correctness and loss accounting | In progress; Final retry and P/F exclusion implemented |
| Ownership and configuration reconciliation | In progress; C01.1 through C01.7 implemented |
| Repository-owned shell removal | In progress on `dev`; zero tracked `.sh`/`.bash` files does not mean zero inline shell |
| Secure management defaults | In progress; `gobfd-330`, `.8.2.3.2` and `.8.2.3.4` done; `.8.2.3.1` and `.8.2.3.3` open |
| Safe GoBGP v4 reconciliation | In progress; v4.10.0 client, preflights, retries, metrics, operator-disabled peers, canonical identity, gap resync and recovery-only dampening done; dampening observability `.8.2.4.7` open |
| Companion recovery and canonical identity | In progress; GoBGP canonical identity done; HAProxy and ExaBGP `.8.2.5` open |
| Independent implementation review | Open |
| Interop, scale, security, and release qualification | In progress; the strict local release-quality gate passed in a clean worktree on 2026-09-08; on 2026-10-13 the vulnerability gate is red (`gobfd-qj0.8.2.11`, `gobfd-qj0.8.4`); broader interop, scale, and security qualification remains open |

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
`.1.1.1.4.3` was blocked by P0 image replacement `gobfd-qj0.8.2.8.5.2.4`
until the 2026-10-13 narrowing to BASE child `.8.5.2.4.2.1`:
the pinned FRR vendor image was verified to inherit Alpine. Artifact child
`.8.5.2.4.1` adds the Debian FRR 10.7.1 recipe with amd64 startup/config/stop
smoke and two SBOM formats; it does not switch consumers. Child `.8.5.2.4.2`
owns all consumer migrations and local peer qualification. No live timer
qualification was run. The updated Beads plan reuses project-owned container
execution, packet capture and daemon JSON receipts, without a new public RPC.
H-05 remains partial until the compliant topology and wire evidence are accepted.

Consumer child `.8.5.2.4.2.1` passed the local amd64 BASE Compose and
testcontainers gates with Debian FRR 10.7.1, bounded builds/runtime and verified
owned-resource cleanup. Child `.8.5.2.4.2.1.1` adds sequential capped manual
builds and `up --no-build`. Evidence is in
`reports/e2e/frr-base-caps-20260908/`; capture-dependent RFC skips remain explicit.
P0 child `.8.5.2.4.2.2` passed the local amd64 manual and testcontainers
BGP gates, all four race-enabled functional tests per path without skips,
bounded build/runtime inspection and owned-resource cleanup. The complete
testcontainers gate took 200.07 seconds. Scoped race contracts, lint, vet,
gopls, inventory, Markdown, all ten Compose renders and independent SPEC/QUALITY
reviews also passed. Evidence is in `reports/e2e/bgp-trixie-20260908/`.
P0 child `.8.5.2.4.2.2.1` replaces the old GoBGP Alpine/scratch and ExaBGP
bookworm images with source-pinned trixie recipes. The approved ExaBGP-only
system-Python runtime exception is recorded in `AGENTS.md`; repository Python
tooling remains forbidden. Both recipes now have local BGP and SBOM evidence.
The complete image inventory `.8.5.2.1` also identified Grafana Alpine and
Prometheus BusyBox runtimes. Ordered remaining P0 slices are
integrations/Kubernetes `.8.5.2.5`, observability `.8.5.2.6`,
Containerlab/vendor boundaries `.8.5.2.7`, and the named Oracle release-product
decision `.8.5.2.8`. Do not relabel a Debian artifact as Oracle Linux or rebase
a vendor NOS into a different system under test. Holo already uses trixie;
download-only scratch stages do not contain an executable runtime.
RFC child `.8.5.2.4.2.3` passed the complete local amd64 testcontainers gate
in 117.50 seconds with all four race scenarios and no skips, verified resource
caps, no OOM kills and exact-owned cleanup. Evidence and peer SBOMs are in
`reports/e2e/rfc-trixie-20260908/`. BASE/BGP/RFC acceptance does not close the
parent image or timer tasks, full RFC coverage or the existing security gaps.
BGP fast-failover child `.8.5.2.5.1` passes the local amd64 race lifecycle
in 142.20 seconds: route withdrawal/restoration, packet evidence, all five
runtime limits, no OOM and exact cleanup. Compose uses the same trixie peers
and existing capped lifecycle controller; manual route withdrawal and recovery
are also verified locally. Evidence and peer SBOMs are in
`reports/e2e/bgp-failover-trixie-20260908/`. Integration parent `.8.5.2.5`
remains open for ExaBGP anycast `.8.5.2.5.2`, then Kubernetes immutable
build/import `.8.5.2.5.3`.
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

Task `gobfd-qj0.8.2.1.3.3` implements the H-09 prerequisite: owner-only
AdminDown/7 commit receipts, context-bounded queue admission and wait, one-shot
`Run`, and Manager fanout outside registry, ownership and lifecycle locks.
Shutdown retains commit errors and the fixed two-second notification window.
The task remains open for H-12: successful-send retries, negotiated Detection
Time drain and bounded I/O. A commit receipt does not qualify packet delivery.

RFC core work continues with the tracked gaps in Poll/Final and Demand procedures,
diagnostic and authentication reset behavior, atomic BFD/AdminDown delivery,
RFC 5881/5883 transport demultiplexing, RFC 9764 authenticated padding, and
fail-closed preview boundaries.

Task `gobfd-qj0.8.2.1.2.3` adds owner-timer RemoteDiscr expiry after one
Detection Time in every state and enforces the passive unknown-peer transmit
prohibition for Final retries and direct FSM sends. Last observed causal facts
remain intact; authentication sequence expiry remains independent at two
Detection Times. Parent `.1.2` retains its other diagnostic qualification gates.

### P1 sequence

| Delivery slice | Status |
|---|---|
| Configurable BFD QoS socket policy with packet evidence | Open |
| Committed-latency measurement and corrected performance gates | Open |
| Removal of permanent per-session OS-thread pinning with A/B evidence | Open |

Post-v1 scheduler, kernel, warm-restart, S-BFD, and authentication R&D remains
outside this release contract and is tracked separately in Beads.

The [cilium/ebpf evaluation](../superpowers/specs/2026-10-15-ebpf-evaluation.md)
(2026-10-15, ebpf-go v0.22.0) keeps eBPF out of v1: there is no measured
receive bottleneck yet, XDP adds BPF privileges and interface ownership
conflicts, and the pre-1.0 library broke APIs in v0.21.0 and v0.22.0. Socket
drop counts come from `SO_RXQ_OVFL` instead. An XDP/AF_XDP receive prototype
(`gobfd-376`, backlog P4) waits for the `.8.2.1.6` latency baseline and the
`recvmmsg` prototype.

## Release contracts

- [v0.6.2 maintenance design](../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md)
- [v1 production design](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
- [RFC compliance matrix](./08-rfc-compliance.md)
- [Development and quality gates](./09-development.md)
