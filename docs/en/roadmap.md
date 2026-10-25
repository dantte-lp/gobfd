# GoBFD Roadmap

Executable plans, priorities, dependencies and acceptance criteria live
exclusively in Beads. This page is a dated status projection and historical
record; use `bd show gobfd-qj0.8` and `bd ready` for current execution.

## Publication boundary — 2026-10-25

This projection describes local `dev` implementation, not published stable
delivery. The full 30-pair source and translation audit is accepted at
`f88872cc44c0422844cc52d61eb8e616d26335fe` under
`gobfd-qj0.8.2.8.7.2.9`; workload report corrections follow at
`308ce357f2c553fe71210f7888cf422c30f44f7b` under
`gobfd-qj0.8.2.8.7.2.10`. The complete 31-pair documentation review is accepted
at `dev@93b8d19867edd3731f1a964d37c2badefd6c6a68`, including Echo API transport,
principal claims and the sender recovery regression boundary. Echo ownership
is accepted locally under `gobfd-qj0.8.2.2.8` and
[ADR-0009](adr/0009-echo-principal-claims.md). The sender follow-up uses verified
socket device scope for matching named IPv6 zones: the native combined packet
loop retains 3 allocations before and after two recreations, replacing the
historical rise to 51. This is local fixture evidence, not whole-daemon or
reference-load acceptance. Unrelated unmerged work is outside this projection.
The documentation revalidation `gobfd-qj0.8.2.8.7.2.16` is accepted locally at
`bb244a6d42a35f6c968ecd9fedf4354b86a95754`. The next source change,
`abad22b2d9bd72042d337cdbee104f45946375c5`, replaces the Containerlab receipt
writer atomically; its documentation review is accepted at
`b944151bec08c44bdf0514e5945f257f0404c0dd` under
`gobfd-qj0.8.2.8.7.2.17`. The subsequent transport authorization fix is
accepted at `f43c9b5d87247cfdcee7aa5ac8414fb4515bf90f` under
`gobfd-qj0.8.2.3.8`; `gobfd-qj0.8.2.8.7.2.18` reviews the current
English/source contract before the Russian translation. The review corrects
MPLS/VCCV support claims, configuration and health semantics, HAProxy timing
and the disclosed Containerlab/compiler and Kubernetes listener boundaries.
These documentation gates are separate from release qualification; later
accepted source changes require review of the affected pages. Publication
remains held; alpha readiness
`gobfd-qj0.8.2.6.4` does not yet provide a prerelease path.

| Area | Current boundary | Beads authority |
|---|---|---|
| Documentation | The 31-pair source and Russian translation audit and receipt-writer follow-up are accepted locally; the transport-fix baseline has a separate current-source review | `gobfd-qj0.8.2.8.7.2.16`, `gobfd-qj0.8.2.8.7.2.17`, `gobfd-qj0.8.2.8.7.2.18` |
| Management/reload | Unsafe-loopback policy changes reject before candidate acceptance; core pending activation is accepted locally; the parent retains separate P2 interface-validation and P3 runtime-VNI work | `gobfd-qj0.8.2.3.6`, `gobfd-qj0.8.2.2.6`, `gobfd-qj0.8.2.2.6.5`, `gobfd-qj0.8.2.2.6.6` |
| Sender recovery | Same-socket IPv4/IPv6 rebind and denied-privilege failure pass locally; matching named scope no longer increases packet-loop allocations after recreation | `gobfd-qj0.8.2.2.6.7` |
| Pending qualification | Typed reason classes and active-address checks retain generation, claims and sockets; all-five periodic resource checks also cover missing monitor events; local process evidence remains separate from final release qualification | `gobfd-qj0.8.2.2.6.8`, `gobfd-qj0.8.2.2.6.8.1`, `gobfd-qj0.8.2.2.6.8.2` |
| Echo ownership | Idempotent principal claims share a canonical wire with config and retain RX through config withdrawal; this is management policy, not returned-packet authentication | `gobfd-qj0.8.2.2.8` |
| Smallstep | Existing-certificate renewal locally qualified against a real CA with an isolated dependency profile; enrollment, scheduling, reconnect and root rollover remain separate post-v1 lifecycle work | `gobfd-qj0.8.2.3.7`, `gobfd-qj0.8.2.3.7.4`, `gobfd-qj0.8.2.3.7.5` |
| Containerlab | The unmerged FRR lifecycle and vendor-policy candidate has local source checks; compiler/runtime isolation, native FRR qualification and integration into `dev` remain open; five NOS profiles remain unavailable in that candidate | `gobfd-qj0.8.2.8.5.2.7.3`, `gobfd-qj0.8.2.8.5.2.7.3.4`, `gobfd-qj0.8.2.8.5.2.7.4` |
| Echo preview | Declarative and API IPv4/IPv6 TX binding, UDP/3785 and lease cleanup pass local checks; API receive claims honor the requested interface; full RFC qualification remains open | `gobfd-qj0.8.2.1.19`, `gobfd-qj0.8.2.1.17` |
| Legacy demos | HAProxy/Kubernetes CLI and pod-selector drift require source fixes and actual execution | `gobfd-qj0.8.2.8.7.1.4.1` |
| Kubernetes API exposure | The host-network example exposes plaintext GoBGP API TCP/50051; listener restriction and compatible probes require a separate source fix and live qualification before acceptance | `gobfd-qj0.8.2.8.7.1.4.1.1` |
| Runtime | Bounded CPU, GC, scheduler and fixed-manifest churn fixtures exist; calibrated distributions, clock qualification and thread-pin A/B remain incomplete | `gobfd-qj0.8.2.1.6`, `gobfd-qj0.8.2.1.7` |
| Wire/integrations | Retained development slices do not replace final exact-candidate qualification | `gobfd-qj0.8.2.2.7.5.5`, `gobfd-qj0.8.2.8.5.2` |
| Security | The unsafe mutation opt-in is restricted to verified plaintext loopback TCP; TLS clients without a recognized principal cannot mutate. Local guard/CA regressions remain separate from final candidate security evidence | `gobfd-qj0.8.4.1`, `gobfd-qj0.8.2.3.8` |

Oracle Linux 10 is outside v1 OCI/lifecycle scope; generic RPM generation and
historical v0.6 evidence remain. These status rows neither close their owners
nor create an executable plan. Use Beads for current priorities and acceptance.

The publication closeout `gobfd-qj0.8.2.8.7.2.14` is accepted at
`d57e9ef277d477c0626d99279c83c29f668a5897`: 33 Markdown files, 31 EN/RU pairs,
1018 local links and zero link failures. English/source corrections precede
Russian translation. The pending-source follow-up updates configuration,
monitoring, ADR-0006 and both changelogs against its own source and process
evidence in `reports/pending-daemon-20261025/`. Earlier false-readiness and
counts-only reason receipts remain historical defect evidence. This local
work does not qualify calibrated/reference load, 24h, final wire or release.

The pending-source follow-up is accepted at
`7bd3bbaf8374f96301a93f2af2be9c4576608236`; its local process checks close
`gobfd-qj0.8.2.2.6.8`. The current documentation baseline is reconciled under
`gobfd-qj0.8.2.8.7.2.15`. Open parent counts do not represent the number of
remaining v1 defects; use each child's scope, priority and acceptance criteria.

## Down-journal source accounting — 2026-10-25

`gobfd-qj0.8.2.1.6.3.4.3.5` adds lifetime source/notification accounting
through joined owner shutdown. Final retained FSM `Up`→`Down` counts must
equal observed before/after-cutoff notifications; only `[start,end)` events
enter the measured journal. Missing notifications, gaps, omitted rows and
invalid source counters fail completeness. Normal post-window flaps remain
accounted without contaminating measurement rows. Existing production counters
are reused without changing the BFD hot path.

Evidence and acceptance belong to the Beads issue and
`reports/workload-down-cutoff-20261025/`. This boundary does not classify
intentional versus false Down, establish calibrated commit timestamps or
close reference/24h, engineering-scale, thread-pin, wire or security gates.

## Session-churn workload fixture — 2026-10-24

`gobfd-qj0.8.2.1.6.3.4.3.4` adds default-off real final-claim retirement and
recovery inside the existing owner workload. The independent-process mixed
IPv4/IPv6 fixture passes in 56.18 s with an 11-second measurement window:
two releases, two creations and two observed recoveries. Both peers record
two Up transitions. For each cycle, actual known-peer/active registrations
change from 2 to 1 and back to 2; two session and eight packet metric series
remain retained. One insufficient trailing cycle is explicitly skipped.

The lifetime ledger settles 190 successful peer writes as 174 commits and
16 unknown-session terminal rejections. Retired generations account for 73
commits and current generations for 101. All four owned containers are removed;
the runner peaks at 216944640/536870912 bytes with zero max/OOM events and swap.
Evidence: `reports/workload-churn-20261024/`.

This fixed-manifest fixture neither introduces arbitrary unique peer labels nor
qualifies reference distributions, scale, total clock uncertainty or stable
delivery. The worker requires actual recovery, bounds waiting for Up and joins
before measurement freeze. Down events outside the measurement interval are
excluded. Reference/engineering runs, calibrated loss/blackhole/stress
distributions, thread-pin A/B and final-candidate wire/security gates remain open.

## Scheduler-pressure workload fixture — 2026-10-24

`gobfd-qj0.8.2.1.6.3.4.3.3` adds bounded default-off compute/yield workers
inside the existing owner quota. The local mixed IPv4/IPv6 independent-process
fixture passes in 47.57 s: 79 lifetime peer writes settle as 79 owner commits.
Four helpers complete 7680 rounds/yields in 120 worker windows and all join
before measurement freeze. The process scheduler histogram retains 1094
window samples; production timer and committed-RX histogram counts increase
by 130 and 69 respectively. These window deltas exclude the initial snapshot;
the lifetime packet ledger includes setup/drain.

All four owned containers are removed. The runner peaks at
293404672/536870912 bytes with zero max/OOM events and swap. Evidence:
`reports/workload-scheduler-20261024/`. Yield calls do not establish OS context
switches or queue depth; these process observations are not calibrated
reference distributions. Clock uncertainty remains unknown and reference
qualification false. At this checkpoint the churn driver was still pending;
its later fixture is recorded above. Thread-pin A/B, calibrated distributions,
reference/engineering runs and final-candidate wire/security gates remain open.

## GC-pressure workload fixture — 2026-10-24

`gobfd-qj0.8.2.1.6.3.4.3.2` adds an explicit default-off allocation variant
inside the existing owner quota and common phase. The local mixed IPv4/IPv6
independent-process fixture passes in 47.93 s: 79 peer writes settle as 79
owner commits. In its three-second window, the helper allocates 125829120
bytes in 1920 objects/30 batches, retains at most 256 references and drops the
256 remaining references before measurement freeze. The owner observes nine
process GC cycles and 18 pause samples at `GOGC=100`. All four owned containers
are removed; the runner peaks at 220823552/536870912 bytes with zero max/OOM
events and swap. Evidence: `reports/workload-gc-20261024/`.

These are fixture observations, not calibrated reference distributions or
sole-helper GC causality. Clock uncertainty remains unknown and reference
qualification remains false. At this checkpoint scheduler/churn drivers were
still pending; their later fixtures are recorded above. Calibrated distributions,
thread-pin A/B, admitted reference/engineering runs and final-candidate wire
gates remain open.

## Historical checkpoints

The following dated records retain their original scope and measurements.
An old statement of remaining work or a closed development slice does not
override the current code, Beads state or stable delivery boundary.

<details>
<summary>Expand dated development and release checkpoints</summary>

## Alpha proposal and remote runner admission — 2026-10-24

The maintainer proposes `v1.0.0-alpha.1` and authorizes a new qualification VM
through `um-gate/netlab2`. Read-only admission confirms the intended hypervisor,
128 logical CPUs, approximately 184 GB available memory and 2.58 TB free storage.
No new VM or detached workload has been created. P0
`gobfd-qj0.8.2.1.6.3.4.4.1` owns isolated Pulumi provisioning and actual guest
admission. Moving the current candidate does not fix its per-session thread
pinning or establish final 24-hour acceptance.

[ADR-0008](./adr/0008-experimental-prerelease-boundary.md) proposes the separate
experimental channel. P0 `gobfd-qj0.8.2.6.4` owns alpha acceptance and delivery;
current release preflight/notes still reject prerelease tags. Typed pending
activation, unsafe loopback policy reload, remaining stress profiles,
integration/security acceptance and final qualification remain unclosed.
An alpha proposal is not a stable release or permission to publish remotely.

## CPU-pressure workload receipts — 2026-10-24 10:31 UTC

P0 `gobfd-qj0.8.2.1.6.3.4.2` connects requested owner CPU duty to bounded
workers and actual thread CPU-time receipts. The existing independent-process
IPv4/IPv6 fixture passes in 52.08 s: 79 writes equal 79 commits; one worker
records 751118863 ns CPU over 3000044323 ns wall time, with all 30 duty windows
completed and none missed. The scheduled epoch matches the owner measurement;
all four containers are absent after cleanup. Runner peak is 229322752 bytes
under 512 MiB, with zero max/OOM events and swap. Evidence:
`reports/workload-cpu-20261024/`. The updated critical race suites pass 82
tests with no skips; scoped lint reports zero issues.

The 25% fixture does not qualify a held 80% reference load, clock accuracy,
supported scale or stable delivery. Remaining GC/scheduler/loss/churn profiles
belong to P0 `gobfd-qj0.8.2.1.6.3.4.3`; admitted reference resources and all
parent distributions remain required before regression policy, thread-pin A/B
and final wire/24-hour qualification. Beads remains the executable authority.

## Baseline task admission — 2026-10-24

P0 `gobfd-qj0.8.2.1.6.3.4.1` adds the missing PID-limit preflight before
named owner profiles create sessions. Every visible ancestor must retain
`sessions + 64` additional tasks; missing data and insufficient headroom fail
closed. The original admission accepted all five deliberately invalid cases;
the corrected critical race suites pass 76 tests. Evidence:
`reports/workload-admission-20261024/`. [ADR-0007](./adr/0007-workload-phase-accounting.md)
defines this conservative baseline policy separately from capacity reservation.

Reference distributions remain P0 in `gobfd-qj0.8.2.1.6.3.4`. The current
shared runner has no admitted 8 GiB reference headroom; at this checkpoint the
80% CPU field had no pressure-generator receipt. Full CPU/GC,
scheduler, loss, churn and clock-validity evidence, regression policy,
thread-pin A/B and final four-profile wire qualification remain mandatory.
No reduced fixture or admission-only result closes those gates.

## UDP workload phase integration — 2026-10-24

P0 `gobfd-qj0.8.2.1.6.3.3` resumes the preserved workload implementation in
`task/udp-resume-20261024`. The common phase now reaches both CLI roles and the
existing owned topology: measurement ends before peer join and bounded owner
drain. Critical race suites pass 71 tests, including refusal of an unaccounted
write; scoped lint and the canonical dependency-inventory check pass. The
required-Podman missing-binary path has a retained RED/GREEN check and now fails
instead of silently skipping. [ADR-0007](./adr/0007-workload-phase-accounting.md)
states the accounting and clock boundaries.

The independent-process IPv4/IPv6 fixture passes in 47.98 s: 79 writes equal
79 commits; repeated setup changes assigned/existing counts from 4/0 to 0/4 in
the same private namespace; all four containers are removed. Runner peak is
293216256 bytes under 512 MiB, with zero max/OOM events and swap. Both process
reports retain `reference_qualified=false` and unknown total clock uncertainty.
Corrective snapshot-presence assertions have a retained mutation RED/GREEN;
malformed-peer JSON fails with retained diagnostics and four container removals.
The corrected normal fixture passes in 47.67 s with the same 79/79 ledger and
runner peak 203628544 bytes. Implementation is locally accepted at
`b2e68ad2a13824a05a9b92528fe55c9a8d4fd6d1`: 73 critical race tests, lint, vet,
tidy, 90 Markdown files, English spelling, inventory and fresh scanner pass;
independent whole-branch review has no open findings. Evidence:
`reports/udp-resume-20261024/`.
Reference profiles, regression policy and thread-pin A/B stay P0 before the final
four-profile wire gate. No supported-scale or stable-release claim follows.

## Unified qualifier corrections — 2026-10-24

The existing default FourPeer gate passes in 429.47 s on
`task/final-wire-20261024@e4e79a8eee8e7c38bfc78321bdc5e9030909c214`:
10 scenarios, initial Down and DetectionPrecision both executed, measured
detection gap 0.901 s. The sole RFC skip is the source-backed BIRD Linux
source-port range deviation. All six first-party image IDs and nine required
annotations per image survive cleanup as artifacts; the images themselves are
absent. All 867 source hashes and 310 artifact sidecars match; 14 readiness
probes return 28 SERVING results. Runner peak is 414269440 bytes under the
unchanged 512 MiB cap, with zero max/OOM events and swap.

Final unified qualification remains P0 after committed-load and Session/Echo
thread-pin work (`gobfd-qj0.8.2.1.6`, `gobfd-qj0.8.2.1.7`). Repeat all four
profiles on the resulting production candidate; this development receipt does
not replace final scale, security, OCI or stable-release qualification.

P0 `gobfd-qj0.8.2.2.7.5.5` remains in progress. Offline analysis of the retained
capture reproduces the TShark read-limit defect: `-c 1` yields no matching row,
whereas the same filter without it yields 13 rows, beginning at frame 511.
The qualifier limits decoded matching rows instead; missing initial Down and
detection-expiry evidence now fails the mandatory assertions. The known BIRD
source-port deviation remains separate. Evidence: `reports/final-wire-20261024/`.

P0 `.5.5.2` extends the existing image builder to retain immutable IDs and nine
allowlisted OCI annotations before cleanup. Critical race/shuffle tests verify
the provenance boundary and bounded Podman JSON parsing, including duplicate
members and invalid UTF-8 compatibility. Actual final-candidate topology runs,
independent review, scale/latency and security qualification remain required.
No stable-release or remote-publication acceptance is implied.

## Multihop TTL wire qualification — 2026-10-24

P0 `gobfd-qj0.8.2.2.7.5.4` passed local wire qualification on
`task/ttl-wire-20261024@491b2214994f07e017eb93ab67a29a95378c619e`.
The unchanged crafted baseline passes in 162.94 s; authenticated UDP/4784
multihop passes in 81.98 s. Seven probes cover floors 254→250→254 in Async
and remote Demand, then a rejected auth+hop-policy candidate. Each window
rejects a previously accepted signed payload and a fresh below-floor Poll;
its identical at-floor retry alone increments accepted RX and receives Final.
One expected auth failure accounts for the deliberate replay. Initial
identity/counter and authentication checkpoints span reloads and Demand entry;
local Poll remains absent. Rejection retains generation 5/5 and floor 254.

Both runners retain the 512 MiB cap, with peaks 380297216 and 388112384 bytes,
zero max/OOM events and swap. All 863 source hashes and 212 artifact sidecars
match; six exact image IDs are retained from engine events. Both health services
remain SERVING. Source gates pass 73 interop/peer and 12 auth/expiry/log tests
with race/shuffle, lint, vet, compilation, tidy and 88 Markdown checks.
The first multihop run exposed duplicate `peer` log attributes; a one-line
production fix preserves constructor-bound context and strict JSON ingestion.
Both actual profiles were repeated on the corrected source. Independent review
resolved two earlier counter/replay oracle gaps. Evidence:
`reports/ttl-wire-20261024/`.

P0 unified five-field `.5.5`, scale/latency and security gates remain mandatory
before v1. `.5.5.1` owns a newly sourced `tshark -c` read-limit issue that may
explain prior filtered-query SKIPs; actual reproduction and disposition remain
required. Dynamic DF and key rotation remain after-v1 P2. No routed/cloud,
general Demand, stable release or remote publication acceptance is asserted.
P0 `.5.5.2` owns automated immutable image-ID/source-provenance retention in
the existing shared qualifier before cleanup.

## Prepared-padding wire qualification — 2026-10-24

P0 `gobfd-qj0.8.2.2.7.5.3` passed local qualification on
`task/padding-wire-20261024@b97729458a696aee21023ba147af51417ed9ff7b`.
Default FourPeer passes in 451.84 s, meticulous SHA1 in 271.71 s and crafted
Demand in 134.28 s. Eight resize phases and seven rejected candidates retain
identity, source port, authentication continuity, counters and accepted
generation as applicable. Seventeen peer windows check actual DF, raw UDP
payload, declared BFD Length, zero padding and MAC boundary. Both aggregate
and named BFD readiness remain SERVING. The independent peer keeps its own
packet size; this is not bidirectional MTU or forwarding acceptance.

All three runners retain the 512 MiB cap; peaks are 402780160, 425705472 and
386764800 bytes, with zero max/OOM events and swap. All 853 source hashes and
675 artifact sidecars match. Critical source contracts pass 44 top-level tests
with race/shuffle; crafted-peer tests, lint, vet, compilation, module tidy and
88 Markdown checks pass. A reviewed host-bind mismatch was replaced by copying
the same compiled test executable through container files and probing the
existing Unix socket inside the owned daemon. A full disposable compiler cache
and scanner OOM were resolved using separate checks and a measured scanner
`GOMEMLIMIT=1000MiB`, without raising the 2 GiB job cap.

Evidence: `reports/padding-wire-20261024/`. Three baseline skips remain explicit:
BIRD fixed source port, missing initial Down capture and missing baseline
Down(diag=1) for DetectionPrecision. Final gate `gobfd-qj0.8.2.2.7.5.5` owns
their disposition. P0 TTL `.5.4`, unified final-candidate `.5.5`, scale/latency
and security gates remain mandatory for v1. The fresh scanner reports zero
reachable/imported-package findings and two existing module-only advisories;
`gobfd-qj0.8.4.1` retains their policy. Dynamic DF and auth rotation remain
after-v1 P2. Beads owns current priorities and acceptance; no stable release
readiness or remote publication is asserted.

## Reload wire qualifier preparation — 2026-10-24

`gobfd-qj0.8.2.2.7.5.2` is locally qualified on
`task/detect-wire-20261024@d9e67064e51012c0d25a1a22dc9bd469600d8469`:
authenticated FRR/BIRD3 Async DetectMult 1→5→3 and crafted remote-Demand 5→3
pass actual capture, receipt, UDP/authentication and identity checks.
The full four-peer gate passes in 346.02 s; crafted-peer passes in 84.94 s.
Both retain the 512 MiB runner cap with peaks 112635904 and 68599808 bytes,
zero max/OOM events and zero swap. Evidence: `reports/detect-wire-20261024/`.
The existing baseline DetectionPrecision skip has no Down(diag=1) packets;
it is not coverage and is tracked in final gate `gobfd-qj0.8.2.2.7.5.5`.
The second existing skip records BIRD3's fixed source-port deviation from
RFC 5881 §4; GoBFD's own source-port check passes independently.

Critical source contracts pass 33 top-level tests with race detection; tagged
lint/vet, compilation and 88 Markdown checks pass. Independent review's
next-Control gap is fixed with observed RED/GREEN. The first crafted run's
registry-buffer OOM is fixed by closed child `gobfd-qj0.8.2.2.7.5.2.1` using
scoped queries, without raising the cap. ADR 0006 separates local receive,
derived peer Async and local Demand Poll bounds; the crafted peer has no
detection timer. P0 padding `.5.3`, TTL `.5.4` and unified final-candidate `.5.5`
remain mandatory under `gobfd-qj0.8.2.2.7.5`; this is not stable acceptance.

P0 `gobfd-qj0.8.2.2.7.5.1` extends the existing receipt qualifier with explicit
immediate and Poll-phase expectations. Current timer and crafted-peer callers
retain Poll semantics; direct convergence and owner-pending are both valid for
immediate updates. Critical tagged contracts pass 22 top-level tests after an
observed direct-convergence RED. This is harness preparation, not new wire or
peer acceptance; parent `gobfd-qj0.8.2.2.7.5` remains mandatory P0.

## Prepared rekey and source retention — 2026-10-24

Reconciliation/rekey `gobfd-qj0.8.2.2.7.4`, Manager lifecycle
`gobfd-qj0.8.2.2.7.4.1`, overlay lifecycle `gobfd-qj0.8.2.2.7.4.2` and
lease-ref/preparation cleanup `gobfd-qj0.8.2.2.7.4.2.1` have completed local
implementation and bounded Go qualification. Beads records local integration
and task-closure receipts. The source prepares/claims resources
before stale release, gated by zero Failed/ResourcePending independently of
TimerPending. Fatal preparation rolls back new claims; resource-pending passes
may retain disjoint new claims. The prepared receive-key swap is limited to
unbound sole-config base SingleHop/MultiHop changes; other collisions fail
before factory, with base admission before generation publication.

Pre-publication cancellation keeps the old identity and cleans prepared
resources once. Peer-visible cutover retires the actual old session through
AdminDown/drain outside Manager/lifecycle locks; it does not promise hitless
rollback. Overlay source-local preflight rejects unequal same-wire scopes
against live/draining physical-session lease refs; an unused never-committed
preparation may be superseded. Successful preflight prunes obsolete unused
preparations before further claims, retaining existing current-desired scopes
union positive lease refs. Registered-session refs persist through drain until
idempotent close; unused/unpublished leases clean up once without drain.
Incomplete acquire retains every live/draining exact scope/backend
and skips that source's Manager apply/release. Disjoint supersets needed by the
current desired set may remain for retry; neither global overlay admission nor all-source atomicity is claimed.

Critical01 records 1049 top-level PASS, zero FAIL, five packages and two existing
environment-dependent skips. Evidence: `reports/rekey-lifecycle-20261024/critical01.log`.
Quality04 reports zero issues; vet01 and benchmark base/head exit 0. The
allocation diff is empty across 27 selected budgets: 26 stay at zero allocation;
authenticated unmarshal retains its existing 64 B/op and 1 alloc/op. The 577
source-inventory checks match frozen critical input. Evidence remains in
`reports/rekey-lifecycle-20261024/`; the Markdown gate checks 88 files.
This is bounded local Go qualification, not all-wire, supported
scale or stable-release acceptance.

Packet-policy `gobfd-qj0.8.2.2.7.2` is closed on dev at
`ef18750a2481d0126dff3fc399d2e8fe37bb6022`. Mandatory P0 actual-wire gate
`gobfd-qj0.8.2.2.7.5` remains open and explicitly depends on `gobfd-qj0.8.2.2.7.4`.
The existing wire harness must qualify all five reload fields before v1;
source unit tests do not establish interoperability. Beads owns executable plans,
commit receipts and priorities;
older dated projections retain their historical scope.

## Local receive-floor and prepared-padding reload — 2026-10-24

`gobfd-qj0.8.2.2.7.2` extends stable sole-config base sessions with unchanged
authentication to a five-field TX/RX/DetectMult/MinTTL/PaddedPduSize tuple and
generation/revision receipts. The local multihop `255 - N` receive floor changes
under `Session.mu` without Poll even under Demand; this is GoBFD policy, not an
RFC 5883 receive-TTL requirement. Padding permits only positive-to-positive
changes in Async on a sender proven DF-prepared at construction.

`Manager.AdmitSessionReload` receives only `candidate.base`, preserves
retained/live authentication checks and rejects unsupported base field/ownership
policy before desired-generation, log-level
or resource mutation. Owner revalidation rejects a queued padding tuple before
any field commit if Demand activates. Growth prepares one waiting buffer at
control admission; existing TX/RX Poll direction and queue semantics remain.
`PaddingPrepared` is immutable preparation evidence, not socket liveness.

The local frozen-source race run passed 1031 top-level tests in five packages;
two existing namespace/capability-dependent cases remain separate. All 27
selected baseline/head allocation budgets match. Evidence is retained in
`reports/session-policy-20261024/`; this does not establish live peer/MTU or
release acceptance. `gobfd-qj0.8.2.2.7.1` and `gobfd-qj0.8.2.2.7.3` are closed
on dev; integration `gobfd-qj0.8.2.2.7.4` remains P0, and P1
`gobfd-qj0.8.2.2.7.5` owns actual-wire qualification across all reload fields.
Padding enable/disable and dynamic DF are after-v1 P2 `gobfd-ebu`, discovered
from this slice and outside its v1 acceptance boundary; they do not block it.
Key rotation remains after-v1 P2 `gobfd-khr`. Beads owns execution; earlier dated
projections retain their historical scope.

## Authentication reload admission — 2026-10-24

`gobfd-qj0.8.2.2.7.3` implements admission for same-identity base authentication changes
are checked against retained accepted desired state, including absent runtime,
and the live Manager registry including API/shared owners. Unsupported
enable/disable, algorithm, key IDs/material, selected TX key and receive-key-set
changes are rejected before desired-generation publication, log-level/readiness
changes, configured-peer callbacks or listener/sender resource application.
Startup/new/removed identities and semantically identical fresh stores remain
admissible. Claims acquired after acceptance still face normal fail-closed
Manager apply; this is not whole-source atomicity or seamless rollback.

Detect Multiplier child `gobfd-qj0.8.2.2.7.1` is closed on dev. P0 children
`gobfd-qj0.8.2.2.7.2` and `gobfd-qj0.8.2.2.7.4` remain; P1
`gobfd-qj0.8.2.2.7.5` owns the actual-wire final gate. Key-ID rotation remains
after-v1 P2 `gobfd-khr`. [ADR-0006](adr/0006-session-preserving-reload-policy.md)
is accepted policy. The frozen-source critical race run passed 788 top-level
tests across BFD, daemon, server and dependency inventory; one existing nested
IPv6 wire subcase requires a separate namespace. Local evidence is retained in
`reports/auth-reload-20261024/`; this does not establish overall qualification.
Beads retains executable priorities and acceptance gates; older dated entries
below preserve their original slice scope.

## Session-preserving reload / Detect Multiplier — 2026-10-24

[ADR-0006](adr/0006-session-preserving-reload-policy.md) is accepted policy,
not implementation, interop or release qualification. Resumed parent
`gobfd-qj0.8.2.2.7` has four P0 children: `gobfd-qj0.8.2.2.7.1` (Detect
Multiplier), `gobfd-qj0.8.2.2.7.2` (padding/minimum TTL),
`gobfd-qj0.8.2.2.7.3` (authentication rejection) and `gobfd-qj0.8.2.2.7.4`
(reconciliation/rekey integration). Coordinated same-algorithm key-ID rotation
remains after-v1 P2 in `gobfd-khr`. Beads retains executable priorities and gates.

The active local Detect Multiplier slice preserves a solely config-owned base
session with stable identity through the existing TX/RX tuple and receipts.
Detect-only Async changes need no Poll; remote Demand with both ends Up
requires Poll/Final. Combined changes retain TX/RX direction rules, the active
advertised tuple and queued generation. Shared-owner/API, Micro-BFD, overlay,
authentication, padding and minimum-TTL changes remain conflicts outside this
slice; the accepted policy does not claim those gaps are implemented.

The frozen-source critical race run passed 781 top-level tests across BFD,
daemon, server and dependency inventory, including signed packet sequence
continuity, DetectMult 1 jitter and queued Poll updates. One existing nested
IPv6 wire subcase requires a separate namespace. Local evidence is retained
in `reports/session-inplace-20261024/`; scoped iteration checks do not establish
overall RFC or release qualification. Live FRR/BIRD qualification of the new
Detect Multiplier reload remains open. Older dated entries below retain their
historical scope.

## Bounded preview capability status — 2026-10-24

`gobfd-qj0.8.2.1.8.3` adds `CapabilityService.ListCapabilities` and
`gobfdctl capabilities` for the five RFC 9468/9747/7130/8971/9521 previews.
Maturity, validated enablement and local receive-path readiness are separate.
Absent runtime state remains unknown; an enabled profile without its actual
receive owner remains not ready. README and EN/RU configuration, CLI and RFC
compliance documentation preserve the same qualification boundary.

Local evidence is retained in `reports/capability-status-20261023/`.
The critical race run covers server, daemon, CLI and dependency inventory;
the existing namespace-dependent IPv6 wire subcase remains a separate skip.
This boundary does not qualify RFC wire behavior, Micro-BFD L2 actuation,
vendor backends, supported scale or the final v1 release. Executable priorities
and remaining acceptance gates stay in Beads; the local-only hold continues.

## Independent audit — 2026-10-23

The audit of dev `f9ed31bdd81425ba4efaf9688bb452ddea142266` found six
defects and a separate same-key drain qualification question. The new P0 is
TLS/Unix principal provenance (`gobfd-qj0.8.2.3.5`); reload policy, Echo owners,
NetworkManager ownership/cancellation and new protobuf-file detection have
specific P1 tasks. The release gate depends on those six defect tasks.
The [dated audit](../ru/audits/2026-10-23-independent-retrospective.md) records
history, ADR mapping, duplication measurements and diagnostic limitations.
Existing passing component tests do not establish whole-codebase idempotence
or v1 readiness. Beads remains the executable authority; general P2 deduplication
does not supersede release work.

## v1 product scope — 2026-10-23

Maintainer decision: retire Oracle Linux 10 from v1 OCI products and package
lifecycle qualification (`gobfd-qj0.8.2.8.5.2.8`). The release pipeline selects
only Debian trixie OCI images; generic RPM generation remains enabled.
Historical v0.6 images, tags and qualification records are preserved. Older
Oracle product decisions below are historical and do not define v1 scope.

## Release-first priority — 2026-10-20

The maintainer prioritizes delivery of v1.0.0 over all other work. This replaces
the 2026-10-13 stack-first ordering and the earlier retrospective queue below.

| Priority | Execute before v1 | After v1 |
|---|---|---|
| P0 | Stable RFC correctness, ownership/reload, secure readiness/systemd, preview boundaries, per-session OS-thread scale blocker, security-patched peer, stable-v1 advisory disposition and final release gate | No blanket component refresh prerequisite |
| P1 | Other required qualification tasks: compatibility, interop, latency/allocation/scale, packaging/OCI, zero-shell/Python, Sonar disposition, independent review and release evidence | No general wrapper/asset cleanup prerequisite |
| P2+ | Only a separately demonstrated release blocker may be promoted | General runtime/tool/Action upgrades, freshness automation, semantic/wrapper inventories, asset/local-residue cleanup and independent v0.6 maintenance |

Final qualification still requires source-backed current-compatible dependencies
or explicit reviewed exceptions with an owner and date. Priority changes grant
no exception and do not weaken security, RFC, performance, OCI or quality gates.
`gobfd-qj0.8.4.1` owns stable-v1 security separately from v0.6 maintenance;
`gobfd-qj0.8.6.11` owns the final reviewed-candidate compatibility/inventory
audit. These replace the v0.6/cross-line administrative prerequisites of
`gobfd-qj0.8.2.6`. Required gate failures block v1; broad program epics with
explicit after-v1 children remain open without implying release acceptance.

Inspect `bd ready --priority 0`, then `bd ready --priority 1`; continue claimed
P0 implementation where its actual prerequisites are met. Do not select P2
freshness/cleanup while executable release work remains. A passed release-age
hold makes an upgrade eligible, not the next task. Own temporary-resource cleanup
after each run remains required.

## Local packet-counter allocation fix — 2026-10-23

`gobfd-qj0.8.2.1.6.5` is implemented locally at
`3106b405606851fc0009502ef4625ee0e8535c4a`. Registered peer/local pairs
retain four packet/auth counter handles with registration references; the
last unregister removes the active entry. Historical CounterVec series and
unregistered updates retain their existing behavior. Reporter wrappers,
session ownership, wire contracts and protocol timers are unchanged.

The unchanged actual-owner real-collector benchmark reports 0 B/op and
0 allocs/op in three runs, compared with the 16 B/op and 2 allocs/op baseline.
Its synthetic timing excludes UDP, parsing and authentication. Local critical
race checks pass 183 named tests without skips, including concurrent counter
churn, shared registrations, bounded cache retirement and four real UDP routes.
All 19 lint profiles/92 linters, vet, formatter, both tidy-diff checks and
84 Markdown inputs pass; 520 source inputs match. Fresh whole-branch static
review has no new P0/P1. Govulncheck has zero reachable findings and the two
existing module-level records. Evidence: `reports/collector-owner-20261022/`.
The iteration lint reached 2 GiB; last sampled max19/OOM0 is not canonical
resource acceptance, and terminal counters were unavailable after auto-removal.

P0 `.1.6.3` is next for actual UDP workload, clock uncertainty, timer/drop/
environment distributions and retained-series churn accounting. Then `.1.6.4`
defines calibrated regression failure policy; `.1.7` follows the baseline
with thread-pin A/B. The current 2 CPU/2 GiB/GOMAXPROCS2/pids512 iteration
runner cannot qualify the approved 1,000-session/GOMAXPROCS8 reference.
Use separately admitted resources; preserve that envelope and final 24-hour
gate. Parent `.8.2.1.6` remains P0 in progress. No source/Dolt upload, Actions
dispatch or release preparation is accepted by these local results.

## Earlier receive-to-commit stage — 2026-10-22

`gobfd-qj0.8.2.1.6.2` is implemented locally at
`abb9ea138d07a07c95308f4e90192f2703a1aea7`. Paired realtime/monotonic
calibration follows each listener's lifetime. Timing reaches all four Control
admission callers and the actual normal-completion owner receipt. Observed
clock steps revoke the shared epoch, including queued samples; they never
change packet acceptance or protocol deadlines. Six fixed global outcomes
retain invalid/unavailable denominators; only valid samples enter the
latency histogram. A 1 ms consistency policy does not establish accuracy.

Local race checks pass 269 named tests and six fuzz roots across the selected
BFD cases, netio and metrics; one existing device capability test skips.
Actual IPv4/IPv6 UDP tests each report one input, one owner commit and one
valid sample on peer and discriminator routes. All 19 lint profiles/92 linters,
vet, formatter, both tidy-diff checks and 84 Markdown inputs pass. Source
readback covers 517 inputs. Reachable govulncheck findings are zero; the two
existing module-level records remain under the security gate. Evidence:
`reports/rx-commit-20261022/`. The iteration lint container reached 2 GiB
(last sampled max37, OOM0); canonical resource acceptance remains open.

Three non-race runs give zero allocations for the fixed timing metrics,
but real-collector owner processing costs 16 B/op and 2 allocs/op. P0
`gobfd-qj0.8.2.1.6.5` owns that measured address-formatting cost and now
precedes `.1.6.3` workload/timer/drop/environment distributions. Then `.1.6.4`
owns calibrated regression failure policy and `.1.7` owns thread-pin A/B
after the full baseline. Clock uncertainty must be measured before applying
latency thresholds. Preserve the approved 1,000-session/GOMAXPROCS8 envelope
and final 24-hour gate. Parent `.8.2.1.6` remains P0 in progress; this is not
supported-scale or release acceptance. Source/Dolt uploads and Actions remain
on hold. Optional kernel work and broad freshness stay after v1.

## Earlier raw receive telemetry stage — 2026-10-22

`gobfd-qj0.8.2.1.6.1` is closed at local implementation
`217febe9bf26680478c7ed1dce4b8487644f0263`. Linux listeners capture raw
time64 software receive timestamps and cumulative socket-drop messages.
The enlarged ancillary buffer retains IPv4/IPv6 GTSM and packet-info data;
truncation fails before demux. Unsupported telemetry remains unavailable on
`ENOPROTOOPT`; other socket errors stay fatal. This preserves the deployment
kernel contract without accepting missing measurements as zero latency/loss.

Local race validation passes 215 named netio tests and six fuzz roots; one
existing device-rebind capability test skips. All eight new RX cases pass,
including real IPv4/IPv6 timestamps and cumulative 126 drops on both final
markers. All 19 lint profiles report zero issues; vet, formatter, both
tidy-diff commands and 84 Markdown inputs pass. Six decoder benchmarks report
zero allocations; reachable govulncheck findings are zero, with the two
existing module records retained. Evidence: `reports/rx-timestamp-20261022/`.
The 2 GiB iteration lint container reached its cap (last sampled max33, OOM0);
this is not canonical resource or release qualification. Old-kernel and arm64
runtime behavior have not been measured. No GitHub or Dolt upload occurred.

At this earlier stage, parent `.8.2.1.6` remained P0 in progress. Child `.1.6.2`
owns paired clock conversion and actual owner-commit observations, followed
by `.1.6.3` workload/timer/drop/environment distributions and `.1.6.4`
calibrated blocking benchmark policy. Existing protocol deadlines remain
monotonic. Packet admission and `PacketsReceived` do not prove commit.
The approved 1,000-session/GOMAXPROCS8 envelope and final 24-hour gate remain
unchanged; thread-pin A/B `.1.7` follows the baseline. Optional kernel work
and general component freshness remain after v1.

## Local Micro-BFD preview boundary — 2026-10-22

`gobfd-qj0.8.2.1.8.1` implements the first runtime slice of the P0 preview
boundary on `task/micro-preview-optin-20261022`. `micro_bfd.enabled` defaults
to false. Groups and actuation without opt-in fail validation before resources
open; API creation returns `FailedPrecondition` while List/Delete retain
their existing authorization and ownership checks. Enable changes require
restart; group changes remain reloadable while enabled. Startup warns that
RFC 7130 L2 qualification is incomplete. README, examples and EN/RU
configuration/compliance docs describe the migration and remaining gates.

The candidate also contains reviewed batch 9 and the local audit documentation.
Critical local race runs pass 324 top-level config, API and daemon tests;
one existing namespace-dependent IPv6 subtest skips. Batch 9 selections pass
17 more top-level tests. Formatter, vet and both tidy-diff checks pass;
reachable govulncheck findings are zero, with the two existing module-level
records retained. All 19 current lint profiles report zero issues. This is
local iteration evidence: the 2 GiB lint container reached its memory cap
(last sampled `memory.events max=51`, no OOM); it does not qualify the final
canonical resource/release gate. Local `dev` now includes implementation
`08e4c9c249e8c51ace7be68f1276460a423053e7`; documentation checks passed before
the fast-forward integration. No remote delivery occurred.
The current linter contract
contains the base profile plus 18 build-tag profiles, all with 92 linters;
AGENTS.md and EN/RU development instructions now match the implementation.

The parent `.8.2.1.8` remains P0 for Echo admission and bounded capability
status. Committed-latency baseline `.8.2.1.6` precedes permanent thread-pin
removal `.8.2.1.7`; same-key replace `.8.2.2.7` retains its recorded decision
pause. Optional Micro-BFD L2 and kernel-bond graduation remain after v1.
The local-only delivery hold below remains in force.

## Local Echo API preview boundary — 2026-10-23

`gobfd-qj0.8.2.1.8.2` adds fail-closed Echo admission on
`task/echo-admission-20261023`. The API requires validated-generation
`echo.enabled` and the receive owner's Echo/runtime/binding readiness.
API-only configurations retain lazy listener acquisition before sender creation.
Readiness is an admission snapshot; previously admitted requests may complete.
List/Delete and request validation retain their existing behavior.

Local critical race tests pass 290 top-level tests across server, daemon and
dependency inventory; one existing unsolicited IPv6 wire subcase skips in the
container. Scoped lint reports zero issues. Evidence lives under
`reports/echo-admission-20261023`; this is not stable delivery or full RFC 9747
qualification. The P0 parent remains open for capability/status consistency
under `gobfd-qj0.8.2.1.8.3`. Executable priorities remain in Beads.

## Local verification and delivery hold — 2026-10-22 18:35 UTC

The maintainer temporarily suspended GitHub delivery to save Actions minutes.
Keep source commits, Beads updates and validation local: no source or Dolt
push, PR mutation or workflow dispatch until this direction is lifted.
The existing remote PR and hosted receipts below are historical context.

No new implementation commit or session transcript appeared after the earlier
retrospective. Review `gobfd-xy9` checked the pending branches against
`dev@93304161d77bd1e97a8eb36ecfd1d4b321dd8082`. Critical local runs used
the pinned Go 1.27.3 container with two CPUs, 2 GiB and the race detector;
they are iteration evidence, not the full canonical or release gate.

| Source | Local result | Remaining acceptance |
|---|---|---|
| Batch 9 `dc5a92c` | Auth, crafted-evidence and Go-peer selections passed in three packages | Review and integrate the candidate; retain exact-source live evidence |
| Typed re-key `46b8649` | Nine top-level reconciliation tests passed | This implements re-key failure protection, not same-key replace; `.8.2.2.7` stays P0 open |
| Uncommitted kernel-bond draft on `9330416` | Fourteen netio tests passed; two existing diagnostic-policy tests passed | One unrelated diagnostic test fails; two Go files need formatting; real bond and packaged-unit acceptance remain absent |

The failing draft assertion expects diagnostic zero after a silent Down
timeout. The approved design explicitly adopts Held Erratum 5205 and the FSM
publishes diagnostic one without a state edge. `gobfd-9ro` is closed through
its documented-RFC-reading acceptance alternative; no production fix is
justified. Separate that diagnostic experiment from the optional bond draft.
Source hashes, the uncommitted patch and logs are retained locally under
`reports/review-20261022-local/`.

| Priority | Beads owner | Next verifiable result |
|---|---|---|
| P0 | `.8.2.1.6` then `.8.2.1.7` | Establish committed-latency baseline, then A/B removal of permanent thread pinning against the existing scale limits |
| P0 | `.8.2.1.8` | Complete API/config/status/docs preview opt-in and fail-closed proof; stale stored blocked status was cleared |
| P0 | `.8.2.2.7`, `.8.4.1` | Finish the owned-session mutation contract and final-candidate security disposition |
| P1 | `gobfd-sow` | Integrate the cached-packet test ownership fix and resolve the independent rare cleanup-observation failure before final race qualification |
| P2 after-v1 | `.8.2.3.3.1`, `.8.2.2.1` | Qualify optional kernel-bond and Micro-BFD L2 without blocking the default-off v1 preview boundary |

## Earlier retrospective snapshot — 2026-10-22 18:09 UTC

At `dev@93304161d77bd1e97a8eb36ecfd1d4b321dd8082`, local and GitHub `dev`
match. Beads reports 375 closed and 164 unfinished issues (141 open, 18 in
progress, four blocked, one deferred); these counts include epics and post-v1
work and are not a release-completion percentage. The latest hosted nightly
E2E success, run [39763006963](https://github.com/dantte-lp/gobfd/actions/runs/39763006963),
is at `3a641a9585e058c1d628cc3231bdcaa5291f497b`, an ancestor of the
current `dev`; there is no hosted run for the exact head.

The latest local integration candidate, `task/integration-batch9-20261022`
at `dc5a92c163f9a5e9d302fc511759f93d5fd13b7a`, is four commits ahead
of `dev`. Its remote Go 1.27.3 canonical gate on `oel-lab-gui` reports
`gate_exit=0`, 39 full-race packages and 16 tagged passes at that exact commit.
This is not a live BASE/crafted-wire or hosted-release acceptance. The typed
replace branch remains outside `dev`; its same-key policy is unresolved under
`gobfd-qj0.8.2.2.7`. The RFC 7130 dedicated-MAC/member gate remains red on
the separate Micro-BFD branch; Micro-BFD is preview and default-off for v1.
The residual RFC 7130 gate in `gobfd-qj0.8.2.2.1` is P2 after-v1;
`gobfd-qj0.8.2.1.8` remains P0 for the v1 fail-closed preview boundary.

The 1,000-session contract requires at most `2*GOMAXPROCS+32 = 48` OS threads
at `GOMAXPROCS=8`. Control and Echo sessions still call
`runtime.LockOSThread` for their full lifetime, implying at least 1,000
threads for 1,000 active control sessions. This is a direct scale blocker,
so `gobfd-qj0.8.2.1.7` is P0. The existing bounded 505-thread `newosproc`
failure is historical; run the specified A/B timer-tail and thread/RSS gate
before changing the scheduler. [Go's runtime contract](https://pkg.go.dev/runtime#LockOSThread)
guarantees thread affinity, not timer precision.

## Historical execution snapshot — 2026-10-20

Beads owns task status and dependencies. This snapshot supersedes the active
queue and host instructions recorded on 2026-10-15; dated receipts below remain
evidence only for their named revision.

- Accepted implementation baseline on local `dev`:
  `f913050cfbd863f86ab0937972e2ec16d7e44ecf`.
  GitHub `dev` remains `e6baa6fa416877a3caefb3e68421309578e749b0`;
  this baseline is 62 commits ahead. Local acceptance is not remote integration
  or stable delivery.
- API/core tasks `gobfd-qj0.8.2.1.3.4`, `gobfd-qj0.8.2.1.3.5` and
  `gobfd-qj0.8.2.1.1.3` are closed after the final combined gate on branch
  `task/api-validation-padding`, integrated at
  `20e951ac14e0515eda6698df7c70e7bf9ee77a32`.
  Local receipt: `reports/worktree-archive-20261020/api-validation-padding/reports/api-validation-padding-20261020/acceptance.md`;
  `canonical-2560m02` records 25 expected top-level tests, 142 PASS rows across
  four race-tested packages, all 18 lint profiles/92 linters and command,
  container and host exit 0. Native/pinned formatter diffs are empty; vet and
  both module tidy checks exit 0, Markdown covers 83 files and spelling five
  changed English inputs. Readbacks of 730 source files and the harness, and exact
  cleanup are recorded there. Full-suite, new peer/path-MTU, performance,
  soak and stable qualification remain separate.
- Unsolicited TX stop `gobfd-qj0.8.2.1.10.6`, its parent `.1.10`,
  diagnostics/authentication `.1.2`, AdminDown drain `.1.3.3`, delivery
  accounting `.1.3.1`, Echo Watch `.1.3.10` and raw-loss recovery `.1.3.11`
  are accepted locally. Full RFC, preview graduation and release gates remain
  separate.
- Delivery parent `gobfd-qj0.8.2.1.3` is closed for the bounded local `dev`
  admission/commit/drain/status contract after an acceptance audit of its eleven
  closed children. Typed queue admission is not synchronous receive commit;
  legacy events remain best effort and telemetry outside the coherent causal
  tuple is sampled. Separate interface, Micro-BFD, transport, performance,
  combined-consumer, RFC and stable owners remain open.
- Actions leaf `gobfd-qj0.8.6.4.2` is accepted at the implementation baseline:
  four Actions, ten uses, the existing fixture, both changelogs and the
  Go-generated dependency inventory; eight files, +20/-12 lines. Its actual
  cold gate passed 33 critical top-level race tests, all 18 lint profiles/92
  linters and source/cleanup readbacks. Parent `.8.6.4` retains hosted
  execution and nested OCI qualification.
- Cleanup leaf `gobfd-qj0.8.2.8.5.3.4` is closed: 33 clean working copies and
  three exact old images removed, branches retained, 18 report archives/2557
  files hash-verified. Receipts now live under `reports/worktree-archive-20261020/`;
  absolute paths inside the original receipts remain historical. Two dirty
  drafts, two nonancestor variants and two container-bound trees remain.
  Details: `reports/space-merge-20261020/final-receipt.md`.

| Order | Owner | Required result |
|---|---|---|
| 1 | `.8.4.1`, `.1.1`, `.1.4`, `.2`, `.3`, `.1.8` | Resolve stable-v1 security, stable RFC/timer/transport and ownership/management blockers; enforce default-off/fail-closed preview boundaries |
| 2 | `.1.1.2.3`, `.1.1.1.4.3.2` | Prove remote zero RX and remote Demand with a crafted Go peer; harden the accepted live timer evidence helpers |
| 3 | `.1.6`, `.1.7`, `.8.2.6.2`, `.8.6.11` | Measure required performance/thread behavior and combined-consumer convergence; audit the final candidate's compatibility and dependency inventory |
| 4 | `.8.2.7`, `.8.2.8.7`, `.8.2.6` | Complete independent review, mandatory quality/security/interop/scale, packaging/provenance and upgrade/rollback evidence before release preparation |
| 5 | Issues labelled `after-v1` | Resume general currency, cosmetic cleanup and independent v0.6 maintenance after v1; retain their open states and release-age holds |

Interface-event coalescing (`gobfd-qj0.8.2.2.6.3.3`) is accepted locally at
`f913050cfbd863f86ab0937972e2ec16d7e44ecf`. Linux retries affected
pending sources in the retained generation; the non-Linux stub does not.
Startup listener and overlay capability lifecycle remains open under `.6.4`.
The patched BIRD peer under `gobfd-qj0.8.6.6.2` is accepted locally at
`1484581eb57d0bf210a4072ecb108a066ec26ad2`: image readback, FlowSpec RED/GREEN,
BASE and BGP+BFD testcontainers and the canonical gate passed. The dependent
timer draft is source-reviewed but uncompiled and unexecuted. The Echo draft contains only a prospective regression test.
See [handoff](../superpowers/plans/2026-10-15-agent-handoff.md) and Beads for
the exact resume order. None of these drafts is release acceptance.

Direct release correctness/management blockers and final release owner `.8.2.6`
are P0; mandatory qualification tasks are P1. Broad stack currency and `.8.4` are P2,
while stable-v1 security `.8.4.1` remains P0. The security-to-ownership dependency
remains, with both release owners P0. Execution order does not manufacture a
reachable security finding or remove a release gate. Closed interval task `.1.1.3` uses
the accepted API error mapping in `.1.3.5`; padding remains a separate scope.

GitHub API reports one open issue, [#88](https://github.com/dantte-lp/gobfd/issues/88),
and ten open PRs on 2026-10-20; their dispositions are separate from local
implementation. GitLab API verifies BIRD's upstream tag metadata; it is not
a GoBFD mirror or pipeline acceptance source. No remote write is implied.

### Deferred currency targets — checked 2026-10-20

These are planning targets, not adopted pins or compatibility results. UTC
thresholds are the source timestamp plus seven full days. Read all notes
between the installed pin and the target, including intermediate releases.
General upgrades are P2 after-v1 work unless the final compatibility/security
audit identifies a concrete release blocker. Security-patched BIRD remains P0.
The earlier audit covered 67 uses from 17 Action repositories. The four
eligible Action updates are now accepted locally under P0
`gobfd-qj0.8.6.4.2`; the rows below retain their installed-to-target history.
Parent `.8.6.4` retains hosted smoke and nested OCI provenance. CodeQL remains
on hold; this retrospective does not refresh every upstream version channel.

| Component | Installed → target | Earliest adoption UTC | Evidence and next check |
|---|---|---|---|
| godbus | 5.2.2 → 5.2.4 | 2026-10-20 16:01:38 | [5.2.3](https://github.com/godbus/dbus/releases/tag/v5.2.3) allocation/error hardening and [5.2.4](https://github.com/godbus/dbus/releases/tag/v5.2.4) system-bus lookup errors; NetworkManager missing-bus/cancellation tests; `gobfd-qj0.8.6.2.1` |
| ConnectRPC | 2.0.0 → 2.0.1 | 2026-10-21 13:57:00 | [Notes](https://github.com/connectrpc/connect-go/releases/tag/v2.0.1): rejected-message draining, early responses and truncated/error envelopes; RPC/Watch/generator parity; `gobfd-qj0.8.6.10` |
| ExaBGP | 5.0.13 → 5.0.15 | 2026-10-24 06:08:19 | [5.0.14](https://github.com/Exa-Networks/exabgp/releases/tag/5.0.14) changes UPDATE/withdraw/helper behavior; [5.0.15](https://github.com/Exa-Networks/exabgp/releases/tag/5.0.15) changes CI/tests only; BGP/helper regression gates; `gobfd-qj0.8.6.6.1` |
| OSV Scanner | 2.6.0 → 2.7.0 | 2026-10-19 01:32:07 | [Notes](https://github.com/google/osv-scanner/releases/tag/v2.7.0): parser/offline/call-analysis and SBOM changes; scan input counts and JSON/SBOM compatibility; `gobfd-qj0.8.6.3` |
| GoReleaser | 2.18.2 → 2.18.3 | 2026-10-16 03:27:35 | [Notes](https://github.com/goreleaser/goreleaser/releases/tag/v2.18.3): upload redirects, webhook headers and image metadata; configuration validation only until final release qualification; `gobfd-qj0.8.6.3` |
| Buf | 1.73.0 → 1.74.0 | 2026-10-21 17:36:02 | [Notes](https://github.com/bufbuild/buf/releases/tag/v1.74.0): generation/import strategy and YAML validation; generation drift, lint and breaking checks; `gobfd-qj0.8.6.3` |
| Trivy | 0.75.0 → 0.76.0 | 2026-10-21 11:45:44 | [Notes](https://github.com/aquasecurity/trivy/releases/tag/v0.76.0): parser/CSAF and crypto classification changes; scanner input and SARIF/result compatibility; `gobfd-qj0.8.6.3` |
| goreleaser-action | 7.2.3 → 7.3.0 | 2026-10-19 17:33:04 | [Notes](https://github.com/goreleaser/goreleaser-action/releases/tag/v7.3.0), published 2026-10-12 17:33:04 UTC; peeled `a4f02ab5355a589de60d917a659ce617ed19b3de`: runner cache keyed by distribution and checksum/cosign verification level; Node 24 unchanged. Action and GoReleaser CLI versions are separate; cache/verification smoke without publication; `gobfd-qj0.8.6.4.2` |
| download-artifact | 8.0.1 → 8.0.2 | 2026-10-14 10:09:51 | [Notes](https://github.com/actions/download-artifact/releases/tag/v8.0.2), published 2026-10-07 10:09:51 UTC; peeled `9000827ccba6bdab643e8b6fd33ac0654aef8333`: HTTP 429 retries honor valid `Retry-After`; preserve `digest-mismatch: error` and qualify artifact round-trip; `gobfd-qj0.8.6.4.2` |
| upload-artifact | 7.0.1 → 7.0.2 | 2026-10-14 10:08:14 | [Notes](https://github.com/actions/upload-artifact/releases/tag/v7.0.2), published 2026-10-07 10:08:14 UTC; peeled `cf430e030ddbb5b0abf93d22962f4752f3646cd9`: artifact dependency and HTTP 429 retry changes; update all seven uses and the workflow fixture, qualify artifact round-trip; `gobfd-qj0.8.6.4.2` |
| scorecard-action | 2.4.4 → 2.5.0 | 2026-10-19 02:54:09 | [Notes](https://github.com/ossf/scorecard-action/releases/tag/v2.5.0), published 2026-10-12 02:54:09 UTC; peeled `fa7117db83c52d787ae5a89f350999701505d78e`: Scorecard 5.6.0, token-output removal and result changes; qualify SARIF/results and the nested OCI image separately; `gobfd-qj0.8.6.4.2` |
| CodeQL Action | 4.38.2 → 4.39.0 | 2026-10-22 10:57:05 | [4.38.3](https://github.com/github/codeql-action/releases/tag/v4.38.3) and [4.39.0](https://github.com/github/codeql-action/releases/tag/v4.39.0), target published 2026-10-15 10:57:05 UTC; peeled `6f2ac81f5cd81fe351a197ba0b00d91d03cd2aee`: bundle selection/minimum and `checkout_path` validation; P2 `gobfd-qj0.8.6.4.1` deferred until this exact threshold |

CodeQL's six uses change atomically only after the hold and a fresh
source/advisory check. An authorized disposable hosted Go init/autobuild/analyze
and SARIF smoke is required and has **not run**. Local workflow checks do not
qualify hosted execution. [Buildx Action 4.4.1](https://github.com/docker/setup-buildx-action/releases/tag/v4.4.1) at
`f87e5991a6d7451dcb8d9637bfbc97413f497069` is already current in local `dev`;
PR [#97](https://github.com/dantte-lp/gobfd/pull/97) targets `master` and is
not a development gap.
Scorecard's Action SHA still references a mutable nested OCI tag; registry
digest/provenance and hosted results require separate qualification.

BIRD 3.4.0 has no GitHub Release `publishedAt`. Its official
[GitLab tag](https://gitlab.nic.cz/labs/bird/-/tags/v3.4.0) reports
`created_at=2026-10-12T19:10:15.000Z`, peeled commit
`c3669497e3949b942d86d07e343092cc439b4659`; the tag-channel age threshold is
2026-10-19 19:10:15 UTC. Read the 3.3.3 and 3.4.0 `NEWS` entries, including
the inherited 2.20.0 section, then qualify authenticated BFD, timers, BGP and
CLI parsing under `gobfd-qj0.8.6.6`. The 3.3.2 and 3.4.0 histories diverge:
commit-range repetitions do not prove a newly exposed vulnerability.

The subsequent source/security audit requires the patched basis owned by
`gobfd-qj0.8.6.6.2`: target commit above plus upstream fix
`7cf75587b6f38b87849fe192bf5dfd124b7702bc`, not unpatched 3.4.0. The recorded
fix age threshold, 2026-10-20 15:01:43 UTC, has elapsed. The recipe applies
this fix with zero fuzz and verifies the patched `nest/rt-table.c` digest. A
manual 2026-10-20 run marked the nonzero-offset IPv6 destination valid on
unpatched 3.4.0 and invalid on the patched image. BIRD is built from source,
so image SBOMs and package scanners do not list it; `gobfd-source.txt` records
its provenance. BASE/BGP acceptance evidence stays with the Bead.

govulncheck 1.9.0 is identified by the
[Go module metadata](https://proxy.golang.org/golang.org/x/vuln/@v/v1.9.0.info),
`Time=2026-10-11T03:39:53Z`; the module-channel threshold is 2026-10-18
03:39:53 UTC. GitHub's older `latestRelease` is not this channel's current
version. The [1.8.0→1.9.0 range](https://github.com/golang/vuln/compare/v1.8.0...v1.9.0)
updates module dependencies; qualify both tool pin sites and diagnostics under
`gobfd-qj0.8.6.3`. Tag/module times are labelled separately from Release
`publishedAt`. Other OCI and vendor channels are not refreshed by this table;
their owners and historical observations remain below. Actions source checks
do not establish adoption or hosted compatibility.

Control delivery slice `gobfd-qj0.8.2.1.3.1.1` adds typed queue outcomes,
owner terminal reasons, stop abandonment and a bounded process-lifetime
ListSessions/Prometheus ledger. Get/List/Control Watch share session drop/auth
projections. `gobfd-qj0.8.2.1.3.1.2` extends the ledger to Echo and all
raw/dispatch/recipient/legacy notification outcomes and preserves private
cleanup after raw loss with one parked worker per retained physical entry.
The parent `gobfd-qj0.8.2.1.3.1` is closed for bounded delivery accounting and
private cleanup at `2220cc0865752f2df31859a636d376fdc7fdddec`.
Echo Watch lifecycle is implemented by `gobfd-qj0.8.2.1.3.10`.
The `gobfd-qj0.8.2.1.3.11` implementation adds coalesced public Control/Echo
raw-loss invalidation for live revisioned Watch/GoBGP consumers and authoritative
resnapshots without another owner edge or reconnect. Private cleanup and legacy
best-effort delivery keep their existing boundaries. Local acceptance evidence
is tracked in Beads; this does not claim full RFC, stable or soak qualification. Kernel
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

The following 2026-10-13 stack-first priorities are historical; the release-first
policy above supersedes their execution order and numeric priority claims.

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
  still lists no fixed GoBGP v4 range. The maintainer decision of 2026-10-21
  (`.8.4.1`) permits stable v1 with the module-only entry while GHSA/OSV record
  the fix at or below the pinned v4.10.0, the final candidate's govulncheck
  reports `GO-2026-4736` only at module level (no imported package or reachable
  symbol) and the entry names owner `@dantte-lp` and the expiry (review) date
  2026-12-31. The entry is removed once the Go vulnerability database records a
  fixed v4 version. The final-candidate scan and the 24-hour qualification have
  not run.

The current execution order and exact release-age thresholds are in the
dated snapshot at the top. Earlier receipts remain revision-scoped; Beads
is the current task ledger.

Done before 2026-10-14 and kept for reference: `gobfd-0dc` supported GoBGP
version statement (`1381879`), `.8.2.8.5.2.6` observability on trixie
(`5cf77e9`).

Blocked by the environment: Kubernetes live run `.8.5.2.5.3` (no k3s),
Containerlab vendor runs (no containerlab binary or vendor images).

Version matrix measured on 2026-10-14; GitHub releases, Go modules and go.dev rechecked on 2026-10-15 at 18:10 UTC, other channels not rechecked. Sources: `gh api graphql` releases,
`proxy.golang.org`, `go list -m -u`, Docker Hub and GHCR registry APIs,
`deb.frrouting.org` package index.
The Go toolchain row was rechecked on 2026-10-21 against the go.dev download
index. `go1.27.3` was adopted before the seven-day age gate as a
maintainer-approved security exception for GO-2026-6809 (`gobfd-bjr`).

| Component | Channel | `dev` | Latest | Eligible | Owner |
|---|---|---|---|---|---|
| Go toolchain | go.dev | 1.27.3 | 1.27.3 | current | `gobfd-bjr` |
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
| BIRD | GitHub tag | v3.4.0 + `7cf75587` | v3.4.0 | current | done (`.8.6.6.2`) |
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

The counts below are retrospective snapshots before release-first reprioritization;
they include work now labelled `after-v1`. Use current Beads priorities and the
release-first queue above, not 154 as a count of current release blockers.

Retrospective snapshot at 2026-10-20 16:10 UTC: 353 closed issues out of
513 repository issues; 49 closed since 2026-10-15. These are tracker counts,
including parent closures, not a completion percentage or fresh runtime proof.
The bounded retrospective leaf itself is excluded from this snapshot.
There are 154 unfinished v1-scope issues, including epics and milestones:
119 open, 26 in progress, four blocked and five deferred. Scope includes
the v0.6 maintenance prerequisite, stack, quality and configuration owners;
it excludes the root, `.8.5` and the four post-v1 tasks listed below.

| Workstream | Unfinished |
|---|---:|
| Stack currency | 18 |
| RFC core and performance | 18 |
| Ownership and configuration, including `gobfd-8f3` | 10 |
| Secure management | 3 |
| GoBGP and companions | 2 |
| Quality sprints | 90 |
| Release qualification and independent review | 4 |
| Stable maintenance and advisory disposition | 6 |
| Standalone CI/attribution and v1 milestone | 3 |

The implemented companion children and delivery/accounting children are closed;
combined-consumer acceptance `.8.2.6.2` and their parent gates remain open.
Readiness, systemd, transactional replace/Micro-BFD, transport/latency, preview
boundaries, final interop/security, SBOM/provenance, upgrade/rollback and the
1,000-session 24-hour/5,000-session gates remain unqualified.
Local evidence indexes: `reports/retro-20261020/completed-all.tsv`,
`completed-since-20261015.tsv` and `remaining-all.tsv` in the same directory.

### Historical snapshot — 2026-10-15

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
Final qualification `gobfd-qj0.8.2.6` requires stable-v1 security
`gobfd-qj0.8.4.1` and final-candidate compatibility/inventory
`gobfd-qj0.8.6.11`; independent v0.6 maintenance follows v1. The historical
publication prerequisite `gobfd-qj0.8.1.15` remains accepted.

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
Manager locks (M-09/M-10). The release-first queue above supersedes the older
gist and stack-first execution orders; the table below retains dated context.

### Historical delivery snapshot — 2026-10-13

On 2026-10-13 these slices were P1 in Beads, except security work (secure
management defaults, outbound authentication, unsolicited admission and
preview boundaries); stack currency led. This is not the current priority map.

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
The `gobfd-qj0.8.2.1.3.5` implementation rejects malformed protobuf durations
in generic, Echo and Micro-BFD requests with `InvalidArgument` and applies the
same code to core-rejected client intervals and duplicate Micro-BFD members.
Omitted generic/Micro intervals retain `1s`, generic RX zero remains valid,
and Echo still requires an explicit positive interval. Internal Manager errors
retain their classification. Local acceptance at
`20e951ac14e0515eda6698df7c70e7bf9ee77a32` closes this bounded task;
well-formed Micro-BFD member timer validation and application remain owned by
`gobfd-qj0.8.2.2.1` (M09).

Task `gobfd-qj0.8.2.1.3.3` implements the H-09 prerequisite: owner-only
AdminDown/7 commit receipts, context-bounded queue admission and wait, one-shot
`Run`, and Manager fanout outside registry, ownership and lifecycle locks.
The accepted H-12 continuation adds successful-write retries, a negotiated
Detection Time notification window and bounded I/O. The fixed two-second
window is historical. A commit receipt alone does not qualify packet delivery.

Integration branch `task/integration-batch3-20261021` carries the RFC 5880
section 6.8.3 timer rules outside Up (`gobfd-qj0.8.2.1.1.2`): configured timer
changes take effect immediately in Down, Init and AdminDown, a receipt reports
Applied only when the effective tuple equals the confirmed one, and entry to
Up keeps the 1 s transmit floor until the Up Poll completes. It also carries
RFC 5881/5883 transport demultiplexing (`gobfd-qj0.8.2.1.4`): per-session
transport class, `multihop_max_hops` hop policy, single-hop receive-interface
checks and sender device binding, link-local zones and pending unresolved
interfaces; and the Go 1.27.3 toolchain (`gobfd-bjr`). Remote zero RX and
remote Demand still need the crafted Go peer `gobfd-qj0.8.2.1.1.2.3`.
That branch is on `dev` (`68b497f`). `dev` `963ed28` adds component-derived
gRPC readiness with fail-stop on receiver loss (`gobfd-qj0.8.2.3.1`, closed)
and transactional Micro-BFD group reconciliation with one LAG per member link
(`gobfd-qj0.8.2.2.1`; the RFC 7130 integration gate remains open). Hosted E2E
runs on `ubuntu-26.04`; the PR-safe and nightly profiles passed on `86700ba`.
`dev` `3a641a9` adds listener and overlay capability ownership with in-place
rebind (`gobfd-qj0.8.2.2.6.4`), the systemd sandbox contract with DEB/RPM
lifecycle tests (`gobfd-qj0.8.2.3.3`) and crafted-peer wire proof for remote
RX0, Demand and the section 6.8.18 reset (`gobfd-qj0.8.2.1.1.2`); all three
are closed, and hosted nightly passed on that revision.

RFC core work continues with Poll/Final, remote Demand handling, remaining
delivery acceptance and fail-closed preview boundaries. The public padding API/CLI slice is accepted locally;
core RFC/path-MTU qualification remains separate. Accepted diagnostic,
authentication and padded-PDU core slices are not reopened by this queue.

Task `gobfd-qj0.8.2.1.2.3` adds owner-timer RemoteDiscr expiry after one
Detection Time in every state and enforces the passive unknown-peer transmit
prohibition for Final retries and direct FSM sends. Last observed causal facts
remain intact; authentication sequence expiry remains independent at two
Detection Times. Parent `gobfd-qj0.8.2.1.2` is accepted locally through
`e4f3761a0f1fc59fbf9b2f859923a81125d157bf`; full release qualification is separate.

### Performance sequence

| Delivery slice | Status |
|---|---|
| Configurable BFD QoS socket policy with packet evidence | P1 open |
| Committed-latency measurement and corrected performance gates | P0 open |
| Removal of permanent per-session OS-thread pinning with A/B evidence | P0 open |

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

</details>
