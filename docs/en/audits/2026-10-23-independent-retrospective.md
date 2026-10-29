# Independent retrospective audit — 2026-10-23

Publication derivative; the original is preserved in Git snapshot `c5bcbfcb7e058a5a7f22b8a6811c8e4250963d50`, blob `c3af026c84cc5277e99a6749dfd3590219d14cb5`. Internal identifiers and owner metadata are omitted; results, states, estimates and limitations retain their original date. [Russian translation](../../ru/audits/2026-10-23-independent-retrospective.md).

This is the English counterpart of the [historical Russian audit](../../ru/audits/2026-10-23-independent-retrospective.md). All results, policies, task states and limitations below describe the 2026-10-23 snapshot at `f9ed31bdd81425ba4efaf9688bb452ddea142266`, not fresh qualification.

**Result:** completed; the independent review identified six defects; full compliance with the v1.0.0 contract was not confirmed.
**Required from you:** none

## Completed work

Period: 2026-02-20 — 2026-10-23. Reviewed implementation: `dev`,
`f9ed31bdd81425ba4efaf9688bb452ddea142266`.
Specification: [production contract](../reference/production-contract.md)
with the October amendments. Four ADRs and their Russian translations were read.
Requirement sources were separated from historical plans and dated roadmaps.
This document records a historical result, not an executable plan.

The inventory covers 543 tracked Go files: 212 files excluding tests and
generated code, 329 test files and two generated files. All 541 handwritten
files were supplied to the duplication analyzer. Independent checks were split
into protocol, management/ownership and ADR/delivery. Semantic review focused
on authorization, reconciliation, sender ownership, timers and external
actuator boundaries. Not every test body or delivery-parser branch was reviewed
individually. Coverage and exclusion maps were retained in local records
`reports/independent-retro-20261023/{protocol,reconciliation,adr-delivery}.txt`.

Development retrospective:

| Period | Plan and actual result | Verification boundary |
|:---|:---|:---|
| 20–24 February | Foundation: codec/FSM, sessions/timers, auth/netio, API/CLI and integrations | The initial plan and history do not prove production acceptance |
| 19 August | Separate v1 production contract introduced | Stable/preview, ownership, security and measurable load became independent requirements |
| August–September | Canonical keys, claims/leases, source reconciliation, exact overlay identities, Poll/Final | Implemented components were separated from wire qualification |
| 15–19 October | Unix/mTLS, principal claims, protected debug, revisioned Watch, companion and GoBGP recovery | Normal scenarios were covered; the new audit identified unmet trust-boundary cases |
| 21–22 October | Micro-BFD transactions, API listener ownership, systemd/lifecycle, timer wire fixes, preview opt-in | These are implementations and local receipts; they do not complete all of v1 |
| 22–23 October | Commit-based RX timing, socket drops, workload/environment tooling; Oracle Linux excluded from v1 | The unfinished UDP branch and reference workload remain separate from `dev` |

Initial commit: `5e8ffdc85adf63bf0d41c243bff4a8c53c522e2a`.
Production contract commit: `88b07ee0e1e272fe6017a9ce0125c2bf8b0c8713`.
Latest published stable checked through the GitHub API: `v0.6.4`,
2026-08-27. Of 96 PR records checked, 39 were merged and 11 open. This is
delivery history, not fresh Actions results; no remote changes were made.

| Tree comparison | Commits after base | Files | Lines added | Lines removed |
|:---|---:|---:|---:|---:|
| Initial commit → reviewed `dev` | 757, including 726 non-merge | 806 | 235207 | 2987 |
| Parent of first production contract → reviewed `dev` | 623 | 705 | 132206 | 16743 |

These are differences between final trees, not cumulative effort or personal
statistics. In the first comparison, runtime/API account for +32650/−17 lines;
tests, tooling and CI for +126906/−0; documentation for +60645/−2968; generated
code for +3522/−0. The remainder belongs to configuration and delivery files.
Growth primarily reflects verifiable contracts, test infrastructure and
documentation; line count alone does not confirm quality.

Requirements-to-implementation mapping:

| Area | Confirmed by source and selected checks | Remaining boundary |
|:---|:---|:---|
| RFC core | One FSM; Poll/Final mutual exclusion; bounded timer queue; monotonic commit-based detection | Full RFC/wire/load gate; source-port/QoS and allocation boundaries already have tasks |
| Base ownership | Typed owners, canonical keys, lazy sender leases, source-scoped release, detailed receipts | Same-key mutation policy and interaction between the old drain and new wire epoch |
| Idempotency | Repeated Micro reconcile, shared base claims and concurrent Close pass selected tests | Echo Add creates a duplicate; Echo Delete does not isolate the principal |
| Management | Unix UID, verified mTLS, roles, loopback metrics and protected debug exist | TLS principal namespace collision and omitted startup-only unsafe flag |
| GoBGP/companions | Desired/applied reconciler, retry/gap recovery, operator-disable protection and Watch recovery exist | Final compatibility/security/interop; dynamic native-BFD conflict after startup remains a known limitation |
| ADR-0001 | rtnetlink, link flags, ENOBUFS/MSG_TRUNC resync, PathDown consumer | Fresh host/netns/wire qualification was not performed by this audit |
| ADR-0002 | Native OVSDB default, explicit CLI fallback, shared interface-name validation | Separate NM backend violates owner validation and context bounds |
| ADR-0003 | Advanced modes marked preview/experimental; reserved backends fail closed | This is not full RFC7130/8971/9521 qualification; Echo admission already has an open P0 |
| ADR-0004 | `internal/overlay` is a shared leaf package for config/netio vocabulary/normalization | No semantic deviation from the ADR was found in this path |
| Delivery | Go-owned tools, separate tools module, trixie OCI, retained generic RPM | Proto drift guard misses new generated files; final OCI/security/Kubernetes gates remain open |

Go 1.27 review:

- Both modules specify `go 1.27` and `toolchain go1.27.3`; the reviewed compiler
  pins agree. Diagnostics used exactly Go 1.27.3 in the pinned container.
- `synctest`, `slices`/`maps`, `os.Root`, flight recorder and a shared
  `MaxHeaderValueCount` with parser tests are used. Adoption of generic methods
  or JSON v2 is not a quality requirement in itself.
- No removed experiment/debug switches or `-vet=off` were found in the reviewed
  operational surfaces. Default `stdversion` vet was not disabled.
- Handwritten sources do not import `unsafe`; generated protobuf imports it
  for descriptor metadata. A literal claim that unsafe is absent from the entire
  codebase is incorrect; the prohibition's scope needs clarification without
  manually editing generated code.
- The lint owner contains 18 tagged profiles plus base, totaling 19 runs.
  The full matrix was not executed in this audit.

Primary Go sources:
[release notes](https://go.dev/doc/go1.27),
[exact compiler tag](https://github.com/golang/go/tree/go1.27.3).
Context7 was used for Go, golangci-lint and godbus; disputed godbus behavior
was additionally checked against the selected version's source.

Duplication and reuse:

- Standalone `dupl`, selected through `tools/go.mod`, threshold 100, without
  repository exclusions: 12 groups across 212 non-test files; 42 groups across
  all 541 non-generated files. No parser errors. These are syntactic matches,
  not 42 proven architectural violations or a canonical lint result.
- Some matches are justified: IPv4/IPv6 socket options, different typed overlay
  constructors already sharing `openScopedOverlayUDP`, and CLI delegates.
  Matching sentinel-error blocks and regexp declarations are not shared
  business logic.
- Specific reuse candidates: `repositoryRoot` in `test/cmd/e2ectl/main.go`
  and `test/cmd/integrationctl/main.go`; the write/sync/stat/close sequence in
  `benchreport.finishTemporary` and `routingartifacts.writePreparedOutput`.
  They were assigned to the existing P2 reuse assessment, without
  prerelease priority.
- Base and Micro already share typed owner/lease infrastructure. Echo retains
  a separate compatibility lifecycle and does not receive the same guarantees.
  This is a functional reuse gap confirmed by diagnostic RED.

Before the audit: 416 v1 descendants, 293 closed; 78 unfinished leaf tasks.
After findings were registered: 423 descendants, 293 closed, 130 not closed;
322 leaf tasks, 84 not closed: 8 P0, 51 P1, 24 P2 and 1 P3.
Counts use actual `parent` relationships, not ID prefixes. These are task
counts, not a readiness percentage or schedule estimate. P2/P3 and general
inventories do not automatically become obligations before v1.

Final release qualification explicitly depends on six new defect tasks.
The first priority is eliminating transport-principal impersonation. Other
blockers remain: preview fail-closed, typed mutation policy, completion of
UDP workload/clock accounting, A/B thread pinning, reference/engineering load,
final RFC/interop/security/OCI and independent release-candidate acceptance.
The supported envelope still requires 1000 mixed IPv4/IPv6 sessions at
100 ms/multiplier 3 for 24 hours on reference 8 vCPU/8 GiB;
5000 sessions/300 ms/30 min remain a separate engineering gate.

## Successful checks

Local evidence was retained under `reports/independent-retro-20261023/`.

| Check | Result | Evidence |
|:---|:---|:---|
| Independent ADR review | 4 EN/RU pairs, code/test mapping | `adr-delivery.txt` |
| Positive critical regressions with race | 9 top-level, 19 including subtests; 0 FAIL | `critical-baseline01.log` |
| Runtime/CLI vet | exit 0, no diagnostic errors | `vet01.log`, `critical-run01.log` |
| Targeted benchmarks | 11 rows, all 0 B/op and 0 allocs/op | `benchmarks01.log` |
| Duplication scan | 541 inputs; 42 raw groups, stderr without parser errors | `dupl-all-inputs.txt`, `dupl-all.txt` |
| Proto guard check | exit 0 with an untracked generated fixture present | `proto-guard-result.txt` |
| Resource bounds | 2 CPU/2 GiB/swap 0; peaks 1270317056 and 941002752 B; OOM/max events 0 | `critical-run01.log`, `critical-run02.log` |

Benchmarks measure individual functions on linux/amd64 with GOMAXPROCS=2 and
a short window; they are not end-to-end UDP latency, an authentication
allocation gate or proof of supported scale.

## Unsuccessful checks

| ID | Requirement and violation | Evidence | Status | Next |
|:---|:---|:---|:---|:---|
| Н1 | P0: TLS identity is accepted as a privileged Unix UID | `internal/server/principal.go:72`, `critical-red02.log` | task | transport-principal isolation |
| Н2 | P1: startup contract omits revocation of anonymous mutation policy | `cmd/gobfd/startup_contract.go:185`, diagnostic RED | task | startup-only loopback mutation policy |
| Н3 | P1: Echo permits cross-owner Delete and repeated wire identity on identical Add | `internal/server/echo.go:115`, two diagnostic RED cases | task | Echo principal claims |
| Н4 | P1: NetworkManager uses another bond's active/cached profile | `internal/netio/lag_networkmanager.go:142`, three diagnostic RED cases | task | NetworkManager bond ownership |
| Н5 | P1: NM property reads bypass actuator context/deadline | `internal/netio/lag_networkmanager.go:310`, upstream v5.2.2 source | task | bounded NetworkManager property reads |
| Н6 | P1: proto guard misses new untracked generated files | `test/internal/cirunner/proto.go:68`, isolated Git fixture | task | generated-file drift detection |
| Н7 | Same-key replacement safety with an unfinished old drain is unproven | `internal/bfd/manager_claim_drain_test.go:215`, source-only observation | task | same-key replacement qualification |
| Н8 | Timer addendum contradicts accepted immediate non-Up behavior | Timer addendum:53–55, `internal/bfd/session_update.go:192` | task | documentation contract correction |
| Н9 | Reference envelope and final release qualification are incomplete | Specification:443–480, open qualification | task | final release qualification |

Н1 requires a certificate issued by a trusted CA; an arbitrary untrusted TLS
certificate is not accepted. The diagnostic uses a verified-chain fixture,
not a new handshake/PKI check. For Н2, the omitted startup field was reproduced
by an executable diagnostic; reload consequences were confirmed through the captured-policy
path. Н3–Н4 were reproduced through existing Manager/fake sender/DBus seams,
without changing operational network devices. For Н5, the source call through
`context.Background` was confirmed; a live D-Bus hang was not reproduced.
For Н6, exact Git guard semantics were checked, rather than an actual Buf run.
Н7 is a review of the contract and known permitted overlap, not a measured flap.

No runtime fixes were applied within this audit. Diagnostic tests reproduce
defects rather than permanently accepting incorrect behavior. Full inputs
and criteria were retained in the original audit materials.

## Errors and warnings

| ID | Source | Issue | Status | Next |
|:---|:---|:---|:---|:---|
| О1 | `pkg/bfdpb/bfd/v1/bfd.pb.go:17` | Generated protobuf uses unsafe; the absolute prohibition does not describe the actual boundary | task | Clarify the documentation contract |
| О2 | Full qualification | Full race suite, all 19 lint profiles, live interop, vulnerability/Buf breaking and 24h load were not run | accepted | The audit does not replace final release qualification or independent candidate review |
| О3 | Unfinished worktrees | UDP, Echo port and bond worktrees are not included in the accepted `dev` implementation | accepted | Their sources and receipts were retained separately; qualification follows their own tasks |

Runtime kernels, all target architectures and external dataplane integrations
were not fully checked. Readiness for v1.0.0 and the absence of every possible
defect were not confirmed.
