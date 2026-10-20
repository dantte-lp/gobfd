# GoBFD continuation handoff

Current execution state: 2026-10-20. Beads owns tasks, dependencies and release
age gates. The [EN roadmap](../../en/roadmap.md) and
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

- Accepted implementation baseline on local `dev`:
  `4e582b75dd5e6f0b8d994a66e8c9f3585bee82fa`.
- GitHub `dev`: `e6baa6fa416877a3caefb3e68421309578e749b0`;
  default `master`: `5680385679d8e03d74226b5fcde269210d63bde8`.
  This implementation baseline is 59 commits ahead of remote `dev`.
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
- The broad development goal remains paused. This retrospective updates Beads
  and documentation; it does not resume implementation or authorize publication.

## Resume

1. Read applicable `AGENTS.md`, default-branch README and current roadmap;
   run `bd prime`, `bd ready` and inspect actual worktree state.
2. Inspect any live gate handle before starting another job. A timeout in an
   observer is not proof that the job stopped. Never overlap heavy Go jobs.
3. Select `bd ready --priority 0`, then `bd ready --priority 1`. Finish claimed
   stable RFC/timer, transport, ownership/reload and management blockers;
   `gobfd-qj0.8.4.1` retains the stable-v1 advisory gate. Qualify security-patched
   BIRD `.8.6.6.2` before its dependent timer draft; broad tools `.8.6.3.3` no
   longer blocks that peer. Serialize overlapping source/inventory edits.
4. Qualify timer draft `.1.1.1.4.3.1` and its parent gaps against the accepted
   patched peer. Keep previews default-off/fail-closed before v1; graduation is
   separate. Execute an actual wrong-port RED before any API Echo `.1.17` fix.
   General godbus/Buf/Trivy/ConnectRPC/CodeQL/ExaBGP upgrades are P2 after-v1 work
   unless final compatibility/security audit `.8.6.11` identifies a concrete
   release blocker. Preserve their exact age holds and adoption checks.
5. Keep changes in `.worktrees/<task>`; integrate locally only after the
   relevant checks. Append Beads notes with `bd update --append-notes` and
   synchronize via `bd dolt push`. Publication follows `CONTRIBUTING.md` and
   existing authority; no new source-push authorization is implied.

## Qualified gate profile

Go and Go-owned tools run only in the exact pinned Go 1.27.2 Debian trixie
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
- Run only the task's critical race tests, then its required formatter, vet,
  tidy and documentation checks. Do not substitute a partial lint invocation.
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

- Stable `GO-2026-4736` disposition under `gobfd-qj0.8.4.1`; the development
  exception does not qualify stable. The Go database still lists no fixed v4
  range in the 2026-10-20 read.
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
