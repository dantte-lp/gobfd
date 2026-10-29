# V1 release retrospective and effort assessment — 2026-10-26

Publication derivative; the original is preserved in Git snapshot `c5bcbfcb7e058a5a7f22b8a6811c8e4250963d50`, blob `de6cd2e4dfb48fd73dc58d66b8d831ea83b0e551`. Internal identifiers and owner metadata are omitted; results, states, estimates and limitations retain their original date. [Russian translation](../../ru/audits/2026-10-26-release-retrospective.md).

This assessment compares the [23 October audit](2026-10-23-independent-retrospective.md)
with local `dev@1c535f9ac1de7a12f7d85054a9d0fe81216f02c0`.
The earlier audit examined `f9ed31bdd81425ba4efaf9688bb452ddea142266`.
The [production contract](../reference/production-contract.md)
and its accepted amendments define release scope. This document is a dated assessment of implementation and acceptance.
No source, Dolt, Actions or release publication is authorized by it.

Correction: the request for a separate YC profile is withdrawn. The runner
program creates only Proxmox resources on existing VLAN 101; YC inventory was
introduced by the broader greenfield network audit. Its applicability belongs
to the infrastructure task and does not establish missing user credentials.
Existing Teleport/PVE access and image delivery remain the provisioning path;
this correction does not claim that the VM or its preflight is complete.

## Change since the previous audit

The six confirmed defects from 23 October are fixed in accepted local source:

| Previous finding | Current source boundary | Historical record |
|:---|:---|:---|
| TLS identity could obtain Unix UID privileges | Typed transport principal separates Unix, TLS URI and TLS DNS identities | closed |
| Unsafe loopback mutation policy omitted from startup-only reload contract | Startup contract includes `grpc.unsafe_loopback_mutations` | closed |
| Echo Add duplication and cross-owner Delete | Canonical admission and transport-principal claims | closed |
| NetworkManager could select another bond's profile | Active/cached profile and controller ownership checks | closed |
| NetworkManager property reads ignored context | Property calls use `CallWithContext` and bounded setup | closed |
| Proto guard missed untracked generated files | Git status includes untracked files under `pkg/bfdpb` | closed |

The previous same-key replacement/draining qualification remains open as a separate same-key replacement qualification. The timer addendum now explicitly supersedes its old
Up-only promotion restriction. Neither change implies final wire acceptance.

There are 79 commits after the earlier audited revision, including two merge
commits. The final-tree difference covers 386 files, +41,665/-8,562 lines:

| Scope | Files changed | Added | Deleted |
|:---|---:|---:|---:|
| Runtime/API, excluding tests and generated code | 59 | 3,341 | 581 |
| Tests, tools, scripts and CI | 240 | 25,608 | 887 |
| Documentation and data below docs | 81 | 12,095 | 7,080 |
| Generated code | 2 | 603 | 0 |
| Other files | 4 | 18 | 14 |

These are tree differences, not personal productivity or a readiness measure.
The repository has 698 tracked Go files. This assessment checks contract
coverage and selected implementation/consumer boundaries; it is not a new
semantic review of every body or a rerun of the complete test matrix.

Actual UDP owner/peer control, terminal packet settlement, CPU/GC/scheduler
pressure and churn producers now exist. Added evidence includes actual peer
omissions, owner-monotonic Down timing and scalar incarnation binding. The
engineering blackhole period is now 10 minutes, producing two scheduled
in-window blackholes during its 30-minute profile. Previously, equal 30-minute
duration and period produced none. These are source/fixture improvements,
not completed native load qualification.

The initial Smallstep renewal adapter and actual provider qualification are
accepted locally. Initial enrollment and unattended renewal remain separate;
they are not added to the stable release critical path by this assessment.

## Plan compared with code

| Area | Verified current state | Remaining release work |
|:---|:---|:---|
| Protocol, ownership and management | RFC/timer, claims, reload, principal and preview boundaries implemented with individual receipts | Final exact-candidate packet, reload, cross-consumer and failure qualification |
| Down classification | Timing, actual omission windows, scalar slot/generation producers implemented | Historical valid omitted-packet identity, run/clock provenance, independent classification and actual controlled execution |
| Receive/timer qualification | Real UDP and conditional clock evidence exist; total uncertainty remains unknown | Validity-aware distributions, numerical pass/fail and admitted reference evidence |
| Thread scaling | Control and Echo still call `runtime.LockOSThread` | Remove permanent pinning and retain native 1,000-session A/B evidence; 1,000 Control threads cannot meet the 48-thread bound |
| Socket QoS | Ordinary BFD DSCP/Traffic Class/SO_PRIORITY/SO_MARK configuration and implementation absent | Implement the approved policy and IPv4/IPv6 packet/failure checks |
| Micro-BFD dispatch | Nil/JSON logging samples are 0 allocations; noop actuator remains 4 allocations/272 B | Meet the recorded strict target while preserving the per-call timeout, cancellation and context lifetime |
| Benchmark verdict | `BenchmarkReport` returns `writeBenchmarkWarnings` | Calibrated critical regressions must return nonzero and retain reports |
| Small delivery defects | Fresh-empty alert uses `sum(gobfd_bfd_sessions) == 0`; five aggregate E2E wrappers use host Go; dev/debug images install Bash | Required alert, launcher and zero-shell corrections with existing consumers preserved |
| OCI and integration | Shared trixie recipes and several consumers accepted; Containerlab candidate `4852538df409e0b0309b89505b6dc88861a49540` remains isolated | Native FRR lifecycle/wire assertions, Kubernetes immutable build/import, architecture evidence and explicit vendor OS-policy dispositions |
| Runner | Pulumi source/operator datastore fix accepted in um-iac; VM absent | Existing Teleport/PVE access, applicable preflight, managed Debian image delivery, preview/apply and guest admission |
| Final release | Per-slice local evidence exists | Final security/inventory, package upgrade/rollback, full required quality/review, engineering and 24-hour reference gates |
| Cross-tree security/failure owners | Recipes retain the base from a historical fixable liblzma5 scan; an intermittent race-run failure remains unattributed | Fresh candidate disposition/remediation through base-image security disposition and intermittent race-failure diagnosis, without claiming historical findings are current measurements |

Source entry points: `internal/bfd/session_loop.go`, `internal/bfd/echo.go`,
`internal/bfd/manager_micro.go`, `internal/server/principal.go`,
`internal/server/echo.go`, `internal/netio/lag_networkmanager.go`,
`cmd/gobfd/startup_contract.go`, `test/internal/cirunner/proto.go`,
`test/internal/cirunner/benchmark_report.go`, `test/internal/bfdload/`,
`Makefile`, `deployments/docker/Containerfile.dev`,
`deployments/docker/Containerfile.debug` and
`deployments/integrations/observability/configs/prometheus/alert-rules.yml`.

The historical execution snapshot uses actual `parent` links under the v1 work hierarchy, before closing
this assessment's own task. Counts include deferred work and acceptance parents.

| Measure | 23 October | 26 October |
|:---|---:|---:|
| Descendants | 423 | 523 |
| Closed descendants | 293 | 384 |
| Unclosed descendants | 130 | 139 |
| Leaf tasks | 322 | 392 |
| Unclosed leaves | 84 | 75 |
| Unclosed leaves by priority P0/P1/P2/P3 | 8/51/24/1 | 7/41/26/1 |

Since the earlier audit, 100 task records were added and 91 more records are
closed; the open leaf count fell by nine. Counts do not provide a completion
percentage: parents, implementation, tests, housekeeping and post-v1 work
have different costs. Open parents are not necessarily unimplemented features;
closed children do not establish their parent's composite acceptance.

The release owner previously had no direct blocking edges to eight mandatory
owners: QoS, thread pinning, latency qualification, benchmark regression exit,
Micro-BFD allocations, OCI migration, fresh-empty alert and containerized E2E
launchers. Those edges are now recorded in final release qualification. Alpha preparation is open/P2 and is not a stable-release prerequisite.
Two further required cross-tree owners, intermittent race-failure diagnosis and base-image security disposition, are now
direct release blockers. They are included in H; being outside the counted
v1 subtree does not make them post-v1 work.

## Engineering estimate

The following ranges are engineering estimates, not measured execution times.
They include implementation, focused tests, documentation and review within
each row. Parent/child estimates must not be added together.

| Work package | Active hours |
|:---|---:|
| A: complete UDP/clock/causal verdict as one vertical delivery | 10–16 |
| B: remove permanent thread pinning and analyze native A/B | 3–5 |
| C: implement socket QoS and packet checks | 4–6 |
| D: finish bounded actuator allocation work | 3–5 |
| E: critical regression, alert, launchers and zero-shell corrections | 4–7 |
| F: OCI consumer closure and final interop/wire qualification | 8–12 |
| G: Pulumi runner and guest admission after G0 | 2–4 |
| H: final security/base and intermittent-failure disposition, packages, upgrade/rollback, review and docs | 6–10 |
| Total active engineering | **40–65** |
| Required reference run | **24 elapsed hours** |

Without rework, the sequential envelope is 64–89 hours. Some integration work
can overlap, but qualification must use post-fix code. The planning window is
**4–6 continuous calendar days from 26 October, approximately 21:00 MSK:
30 October–1 November 2026**. It includes integration contingency and provision
for one repeated 24-hour run; it is a forecast of local release readiness,
not a publication commitment. Confidence is moderate to low until the first
admitted 1,000-session baseline.

Critical path: complete A and product gaps; admit G; establish short native
baseline/A-B and engineering evidence; freeze the candidate; run 24-hour
reference and final supported wire/security/package acceptance; reconcile
release evidence. Code changes that invalidate qualification restart the
relevant gate. The first checkpoint targets implementation completion within
48 hours; threshold failures update the affected row and remaining estimate.

External waiting is excluded from active hours and adds directly to calendar
time: Teleport/PVE access, image/network availability, required
native architecture resources, and vendor licensing/OS-policy decisions.
The current publication hold must be lifted separately after a concrete
candidate is ready; no publication is requested now.

Deterministic random loss can produce RFC-correct expiry outside a blackhole.
The classifier must expose such a result instead of relabeling it intentional.
A failed resource/latency/false-Down gate may require remediation and a complete
repeat. Clock uncertainty and the remaining actuator allocations are technical
risks in UDP/clock/causal qualification and bounded actuator allocation; they are not accepted exceptions.

## Process correction and evidence

The earlier “only 24-hour and wire checks remain” claim was incorrect. The
23 October audit already retained implementation and qualification blockers.
Repeated source acceptance was reported without a consolidated weighted
release estimate. Small producer slices and late lint findings added repeated
validation work; historical receipts never qualified the final candidate.

Execution remains assigned to the recorded technical owners. Complete A as one end-to-end
result, with only the missing evidence it consumes. Run static checks before
expensive critical tests; repeat only for behavior changes or unresolved
findings. General dependency freshness, broad Q5 duplicate/wrapper inventories,
preview graduation, R&D and unattended certificate enrollment remain outside
the stable critical path under the accepted contract amendments. Mandatory
security, quality, OCI, wire and scale gates are preserved.

The incarnation leaf finished during this assessment:
`1c535f9ac1de7a12f7d85054a9d0fe81216f02c0`, 14 files +538/-50;
330 named race passes, 0 failures/skips, 92 scoped linters with 0 issues,
root/scoped vet, tidy, gopls, CLI build and documentation checks passed.
The final scan reports 0 reachable/imported and 2 module-only findings;
final security disposition remains open for final-candidate security disposition. This is leaf evidence, not release
acceptance. Its owned SDK/cache and integrated clean worktree were removed;
source branch, backup refs and reports were retained.

Local evidence: `reports/clab-frr-owned-20261025/retro-20261026-issues.json`,
`retro-20261026-counts.json`, `retro-20261026-diff-stat.tsv`,
`down-incarnation-review.md` and `down-incarnation-final01-*`.
No new runtime or long-duration tests were launched for this assessment.
