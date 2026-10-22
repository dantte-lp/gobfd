# GoBFD continuation handoff

Current local audit state: 2026-10-22 18:35 UTC at
`dev@93304161d77bd1e97a8eb36ecfd1d4b321dd8082`. Beads owns tasks,
dependencies and release age gates. The [EN roadmap](../../en/roadmap.md) and
[RU roadmap](../../ru/roadmap.md) contain the current queue and release-note
sources. Earlier 2026-10-15 receipts remain historical evidence at their named
commits; they are not the current host runbook.
The maintainer's 2026-10-20 release-first direction supersedes earlier stack-first
ordering: P0 direct v1 blockers and the final release gate, P1 qualification tasks,
P2+ unrelated currency,
cosmetic cleanup and separate v0.6 maintenance after v1. Required compatibility,
security, RFC, OCI, zero-shell/Python, performance/scale and review gates remain.
The original handoff and its dated measurements remain in local Git:
`git show 20e951ac14e0515eda6698df7c70e7bf9ee77a32:docs/superpowers/plans/2026-10-15-agent-handoff.md`.

## Source and acceptance boundaries

- Historical 2026-10-20 accepted implementation baseline on local `dev`:
  `1484581eb57d0bf210a4072ecb108a066ec26ad2`.
- GitHub `dev` received the local `dev` history up to
  `ea9d9ad0e89bc90b23424b53efbf823c2a8a3e93` on 2026-10-20; default `master`:
  `5680385679d8e03d74226b5fcde269210d63bde8`.
- Accepted API/core source `task/api-validation-padding` at
  `20e951ac14e0515eda6698df7c70e7bf9ee77a32` covers
  `gobfd-qj0.8.2.1.3.4`, `gobfd-qj0.8.2.1.3.5` and
  `gobfd-qj0.8.2.1.1.3`; all three are closed in Beads. The local acceptance
  record is `reports/worktree-archive-20261020/api-validation-padding/reports/api-validation-padding-20261020/acceptance.md`.
  Final `canonical-2560m02` completed at 2026-10-20 06:06 UTC: 25 expected
  top-level critical tests, 142 PASS rows across four race-tested packages,
  zero FAIL/SKIP/DATA RACE; all 18 canonical profiles/92 linters, command,
  container and host exit 0. Native/pinned formatter diffs are empty; vet and
  both module tidy checks exit 0; Markdown covers 83 files and spelling five
  changed English inputs. The 730-file source manifest and harness were read
  back, and exact own cleanup was verified. Attempt 01's original lint failure
  remains preserved. This does not qualify the full suite, new peer/path-MTU,
  benchmarks, security scans, scale/soak or stable delivery.
- Local acceptance already covers unsolicited interface listeners and TX stop,
  owner-acknowledged AdminDown and final-claim drain, diagnostic/authentication
  expiry, delivery accounting, Control/Echo Watch and raw-loss convergence.
  Preview graduation, full RFC compliance and stable delivery remain separate.
- Delivery parent `gobfd-qj0.8.2.1.3` is closed after the source/receipt audit
  of all eleven closed children at the accepted SHA. Its bounded contract covers
  typed admission, owner commit/drain and causal status. Queue admission does
  not promise synchronous RX commit; legacy events remain best effort, other
  telemetry is sampled and arbitrary external callbacks have no universal
  deadline. Interface, Micro-BFD, transport, performance, combined-consumer,
  RFC and stable acceptance remain with their separate owners. This audit
  adds no test run.
- GitHub release `v0.6.4` is the published stable baseline. Development does
  not create release branches, tags, drafts or release artifacts.
- Actions leaf `gobfd-qj0.8.6.4.2` is closed at the implementation baseline:
  eight files, +20/-12 lines, four Actions/ten uses plus fixture, changelogs and
  generated inventory; actual 33 critical race tests and 18 profiles/92 linters
  passed. Source08/730-file readbacks and exact own cleanup passed. Receipt:
  `reports/space-merge-20261020/actions-acceptance.md`;
  detailed gates: `reports/worktree-archive-20261020/actions-currency-20261020/reports/actions-currency-20261020/`.
- Cleanup removed 33 clean working copies and three exact old images; branches
  and 2557 files in 18 report archives are retained and hash-verified. Historical
  absolute paths in the original receipts are unchanged. Timer/Echo drafts,
  two nonancestor variants and two container-bound trees remain. Neither draft
  is compiled or executed. Cleanup does not close broader hygiene owners.
- Interface-event coalescing (`gobfd-qj0.8.2.2.6.3.3`) and its parent
  `gobfd-qj0.8.2.2.6.3` are accepted locally at the implementation baseline.
  Security-patched BIRD (`gobfd-qj0.8.6.6.2`) is accepted; see below.

## BIRD acceptance — 2026-10-20

- `gobfd-qj0.8.6.6.2` is accepted locally: `dev` fast-forwarded to
  `1484581eb57d0bf210a4072ecb108a066ec26ad2` (backup
  `backup/dev-before-bird-security-20261021`). The recipe builds BIRD 3.4.0
  commit `c3669497e3949b942d86d07e343092cc439b4659` with the checksum-pinned
  upstream fix `7cf75587b6f38b87849fe192bf5dfd124b7702bc`.
- Manual image evidence in ignored `reports/bird-security-20261020/`: patch
  applies with zero fuzz, patched `nest/rt-table.c` digest
  `0fdd10a135787dd14226cadfaee542d2a4e13444a32444782ed5deb26907c5e5`; the
  nonzero-offset IPv6 FlowSpec destination is valid on unpatched 3.4.0 and
  invalid on the patched image. Syft 1.54.1 emitted CycloneDX and SPDX; BIRD
  is absent from both because it is built from source. Trivy 0.75.0 reported
  zero CRITICAL; the two fixable `liblzma5` findings belong to `gobfd-cs8`.
- Gate `gate04` under the worktree report directory: 6 critical race tests,
  18 canonical profiles with zero issues, formatter, vet, both tidies and
  docs exit 0; regenerated inventory is byte-identical. Peak memory
  2,148,237,312 bytes, no max/OOM event. Host runs of the gate-compiled
  binaries passed `TestBIRDFlowSpecValidation`,
  `TestFourPeerTopologyTestcontainers` and
  `TestBGPBFDTopologyTestcontainers`. QUALITY approved; SPEC requested a
  roadmap-only correction, applied in the follow-up docs commit.
- Fixed-name residue from 2026-10-15/16 runs (14 containers, two networks) was
  removed with maintainer approval. Builder-stage images left by
  testcontainers builds are tracked in `gobfd-aob`.
- The live timer qualification `gobfd-qj0.8.2.1.1.1.4.3.1` that used this peer
  is recorded below. `gobfd-qj0.8.4.1` remains a separate P0 stable-v1
  security gate. `gobfd-bjr` moves every first-party Go pin to `go1.27.3`
  as a 2026-10-21 security exception to the age gate (GO-2026-6809).

## Timer reload and GoBGP advisory — 2026-10-21

- `gobfd-qj0.8.2.1.1.1.4.3.1`: the testcontainers BASE topology runs
  `TestTimerReload` against FRR 10.7.1 and BIRD 3.4.0 with fix `7cf75587`
  (`300→100→500→300 ms` and RX0 per peer). Each GoBFD periodic interval must lie
  in the RFC 5880 section 6.8.7 range widened by max(2 ms, 2% of TX), and the
  sample mean within three standard errors of 87.5%; the upper side carries the
  same latency allowance. Gate and live evidence:
  `.worktrees/timer-reload-20261020/reports/timer-reload-20261021/gate03/` and
  the batch gate of the integration branch. Follow-ups:
  `gobfd-qj0.8.2.1.1.1.4.3.2`, CI coverage of the contract tests in `ci.yml`.
- `gobfd-qj0.8.4.1`: maintainer decision (option C). Stable v1 may ship GoBGP
  v4.10.0 with the module-only `GO-2026-4736` entry while (1) GHSA/OSV record
  the fix at or below the pin, (2) the final candidate's govulncheck reports
  `GO-2026-4736` only at module level (no imported package or reachable symbol)
  and (3) the allowlist entry and the inventory decision name owner `@dantte-lp`
  and the expiry (review) date 2026-12-31; (4) remove the entry once the Go
  vulnerability database records a fixed v4 version (golang/vulndb#5035,
  #6562). The audit fails on an entry that matches no scanner finding. The
  2026-10-21 scan of revision `c5b414d`
  (`.worktrees/gobgp-advisory-20261021/reports/gobgp-advisory-20261021/scan-20261021/`,
  govulncheck v1.8.0, `db_last_modified` 2026-10-20T00:20:56Z) reported the
  advisory at module level only; it is not the final candidate.
- Recorded decisions for the next slices: readiness `.8.2.3.1` (fail-stop on
  receiver loss; GoBGP outside `""`; sender health from creation outcome;
  unsolicited loss affects `""` and `BfdService`; LAG actuator health in
  `gobfd-68x`), transport demux `.8.2.1.4` (`minTTL = 255 − N`, YAML
  `multihop_max_hops`, IfIndex check for single-hop without interface), remote
  timers `.8.2.1.1.2` (pinned peers inapplicable for remote RX0 and Demand;
  crafted Go peer `gobfd-qj0.8.2.1.1.2.3`). Flaky test: `gobfd-0ac`.

## Integration batch 3 — 2026-10-21

- Branch `task/integration-batch3-20261021` stacks on batch 2 (`7a78f51`): the
  review round for timer updates outside Up (`2d92dac`, `0099ead`), the Go
  1.27.3 toolchain with regenerated dependency inventory (`2ac9a38`,
  `5af4067`) and RFC 5881/5883 transport demultiplexing (`2d78a8f` through
  `1614a7c`, cherry-picked). Its gate runs the full untagged race suite in the
  Go 1.27.3 image; `TestBenchmarkResultMountContract` and
  `TestInteropCaptureStorageContract` render Compose through Podman and run
  from a gate-compiled binary on the host. Receipts:
  `reports/batch3-20261021/gate01/` in that worktree.
- Known limits: admission of a new generation exactly at the active deadline,
  before the session loop expires it, still returns a terminal Failed (one loop
  pass); a sender on a link-local address already inherits the zone device from
  `bind()`; a parallel test package binding `[::1]:3784/4784` can steal the
  netio transport test datagrams. Flaky `TestManagerOwnedInterruptErrorPreserved`
  (`gobfd-bka`) lost the interrupt when the owner reached `stopInterrupt`
  before the cancelling goroutine; the test now holds the owner in a blocked
  write before destroy.

## Integration batch 4 and hosted E2E — 2026-10-21

- `dev` `963ed28` carries component-derived readiness
  (`gobfd-qj0.8.2.3.1`, closed): the empty gRPC health service and
  `bfd.v1.*` are the AND of reconciliation sources, runtime workers and
  device-bound listener interfaces; receiver or dispatch loss is fail-stop.
  It also carries transactional Micro-BFD reconciliation
  (`gobfd-qj0.8.2.2.1`, open): groups publish only after member claims
  succeed, a member link belongs to one LAG, owner claims split API and reload
  groups, and the actuator runs outside Manager locks. Micro-BFD still lacks an
  RFC 7130 integration gate; dual-stack groups per LAG are `gobfd-9sr`.
- Gate and live evidence: `reports/batch4-20261021/gate01/` in that worktree
  (full untagged race, BASE, BGP and RFC testcontainers suites).
- Hosted E2E runs on `ubuntu-26.04` (`gobfd-22v`, closed). The tshark image
  keeps its binaries outside `/usr/bin` to avoid the host AppArmor profile, and
  the jobs let pasta receive Podman signals (Ubuntu LP #2154379). `make
  e2e-rfc` runs under Compose project `gobfd-interop-rfc`, and `make e2e-linux`
  runs in the image ID of the running dev container (`gobfd-dah`, closed).
  PR-safe run 39633642656 and nightly run 39656156566 (`86700ba`) passed.

## Integration batches 5–8 — 2026-10-22

- `dev` `3a641a9` carries listener and overlay capability ownership
  (`gobfd-qj0.8.2.2.6.4`, closed): listeners and VXLAN/Geneve backends are
  claimed per generation, device-bound listeners rebind in place on interface
  recreation, API sessions claim their listeners, and `EADDRNOTAVAIL` is a
  typed pending source retried on address events. Backend kind and global VNI
  changes stay restart-only (`gobfd-qj0.8.2.2.6.6`).
- The systemd sandbox contract (`gobfd-qj0.8.2.3.3`, M-19, closed) is proven by
  `cictl package-build` and `cictl package-lifecycle` on Debian 13 and Oracle
  Linux 10; a peer-attributed AdminDown drain on stop exits 0, a local send
  failure stays an error. The kernel-bond enforce drop-in is unqualified
  (`gobfd-qj0.8.2.3.3.1`).
- A crafted Go peer (`test/interop/scapy` `peer` mode) proves remote RX0,
  the RFC 5880 section 6.8.18 reset and Demand on the wire
  (`gobfd-qj0.8.2.1.1.2`, closed). A Down state change now transmits
  immediately (section 6.8.7).
- Each batch passed the full-race gate and live BASE/BGP/RFC (and crafted)
  suites; reports live under `reports/batch{5,6,7,8}-*/gate01/` in the
  integration worktrees. Hosted nightly 39763006963 on `3a641a9` passed
  routing, RFC, Linux dataplane (listener rebind and address-event tests) and
  the DEB/RPM lifecycle steps.
- In progress on 2026-10-22: crossed Poll wire proof for the Poll/Final chain
  (`gobfd-qj0.8.2.1.1`), typed session replace (`gobfd-qj0.8.2.2.7`) and the
  RFC 7130 integration gate (`gobfd-qj0.8.2.2.1`).

## Retrospective readback — 2026-10-22 18:09 UTC

- Local and GitHub `dev` match at `93304161d77bd1e97a8eb36ecfd1d4b321dd8082`.
  The latest hosted nightly E2E success, run 39763006963, checked ancestor
  `3a641a9585e058c1d628cc3231bdcaa5291f497b`; it is not a check of
  the current head.
- `task/integration-batch9-20261022` at
  `dc5a92c163f9a5e9d302fc511759f93d5fd13b7a` has four commits beyond
  `dev`. Read-only remote gate inspection on `oel-lab-gui` found `gate_exit=0`,
  `full_race_exit=0`, 39 packages and 16 tagged passes for that exact source
  commit. Live BASE/crafted-wire validation and integration are still open.
- `task/typed-replace-20261022` remains outside `dev`, with same-key policy
  unresolved in `gobfd-qj0.8.2.2.7`. The RFC 7130 L2 gate is red on
  `task/microbfd-gate-20261022`; preview/default-off remains the v1 boundary.
  The residual gate in `.8.2.2.1` is P2 after-v1, while P0 `.8.2.1.8` owns
  fail-closed preview qualification for v1.
  The kernel-bond worktree contains uncommitted work and is not accepted.
- `gobfd-qj0.8.2.1.7` is now P0: the current permanent Control/Echo
  `runtime.LockOSThread` calls contradict the approved 48-thread ceiling at
  1,000 sessions and `GOMAXPROCS=8`. The old bounded 505-thread failure is
  historical; no fresh scale run was made during this readback.

## Resume

The maintainer's local-only delivery hold overrides earlier synchronization
instructions: no GitHub source/Dolt push, PR mutation or workflow dispatch
until it is lifted. Keep tests and new receipts local.

1. Read applicable `AGENTS.md`, default-branch README and current roadmap;
   run `bd prime`, `bd ready` and inspect actual worktree state.
2. Inspect any live gate handle before starting another job. A timeout in an
   observer is not proof that the job stopped. Never overlap heavy Go jobs.
3. Select `bd ready --priority 0`, then `bd ready --priority 1`. Finish claimed
   stable RFC/timer, transport, ownership/reload and management blockers;
   `gobfd-qj0.8.4.1` retains the stable-v1 advisory gate. Patched BIRD
   `.8.6.6.2` is accepted; serialize overlapping source/inventory edits.
4. The timer draft `.1.1.1.4.3.1` is already qualified in the dated receipt
   above. Complete batch 9 live BASE/crafted-wire checks at its exact commit
   after its remote canonical gate, then review integration into `dev`.
   Resolve typed replace `.8.2.2.7` separately. Keep Micro-BFD preview
   default-off: its RFC 7130 L2 gate is red on the separate branch. Treat
   committed-latency baseline `.8.2.1.6` and then permanent per-session
   OS-thread pinning `.8.2.1.7` as P0 v1 scale work. Preview owner `.8.2.1.8`
   is open with no active blocking edge; the stale stored blocked status was
   cleared. The A/B and 1,000-session gates remain unqualified. Execute an
   actual wrong-port RED before any API Echo `.1.17` fix.
   General godbus/Buf/Trivy/ConnectRPC/CodeQL/ExaBGP upgrades are P2 after-v1 work
   unless final compatibility/security audit `.8.6.11` identifies a concrete
   release blocker. Preserve their exact age holds and adoption checks.
5. Keep changes in `.worktrees/<task>`; integrate locally only after the
   relevant checks. Append local Beads notes with
   `bd --sandbox update --append-notes`; do not synchronize remotely during
   the delivery hold.

Local review `gobfd-xy9` retained critical logs and the exact dirty-bond source
manifest in `reports/review-20261022-local/`. Batch 9 selections passed in
three packages, typed re-key passed nine top-level tests, and netio on the
uncommitted bond draft passed fourteen. These are bounded iteration checks,
not the full canonical gate. The bond draft fails its unrelated
`TestDownTimeoutWithoutReceivedPacketStaysSilent`: it contradicts the approved
Held Erratum 5205 policy; two existing policy tests pass. `gobfd-9ro` is closed
through its documented-reading criterion, not by changing production.
The draft also has two files reported by gofmt. Optional bond qualification
`.8.2.3.3.1` is P2 after-v1; P0 preview boundary proof remains mandatory.
`gobfd-sow` is P1: the copied-send-buffer auth test fix is outside `dev`, and
the separate rare cleanup-observation failure remains unresolved. An isolated
local dev auth test passed twenty runs; it did not reproduce or clear the
historical whole-package race.

## Qualified gate profile

Go and Go-owned tools run only in the exact pinned Go 1.27.3 Debian trixie
image from the repository contract. Do not use host Go, a compiler container
with the Podman socket, a cached keeper PID, or another project's cache.

The accepted API/core source's ignored receipt directory is
`reports/worktree-archive-20261020/api-validation-padding/reports/api-validation-padding-20261020/canonical-2560m02/`.
Its checked-in-source snapshot and ignored `cold-gate-host.sh`,
`cold-gate-container.sh`, `quality-commands.sh` and `harness.sha256` are
reviewed together. Inspect the Beads memory
`gate-ephemeral-canonical-2560m-profile-host` before adapting the profile.
The script is not a portable bypass for another machine's resource guards.

- Full profile: own hard limit 2560 MiB, high 2048 MiB, swap zero; preserve
  parent admission, control/reserve budgets and at least 12 GiB disk admission.
- Canonical lint remains `go run ./test/cmd/cictl lint`: all 18 profiles and
  92 linters. Its existing child environment uses `GOMEMLIMIT=1500MiB` and
  `GOMAXPROCS=2`; the other commands use their declared bounded environment.
- Run the task's critical race tests, or the full untagged race suite for a
  wide integration batch, then its required formatter, vet, tidy and
  documentation checks. Do not substitute a partial lint invocation.
- Inspect errors, resource-stop, timeout, OOM, missing expected test names and
  nonempty formatter diffs prevent acceptance. Preserve every original status.
- Cleanup only newly created exact container IDs, snapshots and runtime state.
  Probe absence independently; engine error or timeout is not absence.
  Preserve source, refs, reports, shared images and foreign resources.

Host memory admission failed before runtime creation during earlier attempts.
A fresh precheck passed on 2026-10-20 after shared load dropped. Historical
resource readings do not admit the next run. Do not restart shared services,
change parent limits or stop foreign processes to make a guard pass.

## Next protocol qualification

The following timer contract was qualified at its dated commit; it is
historical evidence, not the next runnable task.
`gobfd-qj0.8.2.1.1.1.4.3` owns live timer reloads: `300→100 ms`,
`100→500 ms`, RX0, generation-qualified pending/applied receipts and unchanged
session identity for successful ordinary changes. Lower the peer RX floor
within the scenario so it does not mask the 100 ms transmit change; restore
its baseline afterward. Use existing Go-owned topology, copy/reload, packet
capture and exact-ID cleanup helpers. Run before destructive shutdown cases.

No authenticated selective-Final-drop controller, deterministic crossed-Poll
scheduler or remote-Demand transmitter is qualified by the existing fixtures.
Do not replace packet evidence with counters or synthetic state. New peer
versions require fresh command/parser/authentication qualification.

## Remaining final gates

- Stable `GO-2026-4736`: maintainer decision of 2026-10-21 under
  `gobfd-qj0.8.4.1`; the final candidate's govulncheck must report
  `GO-2026-4736` only at module level (no imported package or reachable
  symbol), and GHSA/OSV must still list a fixed version at or below the pin.
- Final reviewed-v1-candidate inventory `.8.6.11` covers runtime/tools, OCI,
  Actions/nested identities, generators, peers and toolchains: current-compatible
  target or explicit reviewed owner/date exception. Scheduling changes grant no
  waiver. Separate v0.6 maintenance and broad Q5 wrapper inventories no longer
  block v1 qualification; their issues remain open. Q6 waits for canonical
  implementation/review, OCI, zero-shell/Python and Sonar owners instead of Q5.
- Ownership/reload, transport, readiness, systemd, preview boundaries and
  committed-latency/allocation evidence remain under their Beads owners.
- The 1,000-session 24-hour qualification, 5,000-session engineering gate,
  final interop/security, packaging, SBOM, provenance and upgrade/rollback
  evidence remain unqualified.
- Local Demand initiation, affiliated Echo, S-BFD, MPLS/PW and the other
  explicit design deferrals do not become v1 requirements through this note.
