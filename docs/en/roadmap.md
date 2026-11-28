# Release qualification status

[Russian translation](../ru/roadmap.md).

Snapshot: **2026-11-27**. This page records the technical acceptance boundary;
it does not authorize publication. Use the documentation at the candidate's
commit and the applicable release-line contract.

## Source baseline

Release-line integration baseline:
[`816fc7e04d411c306fbe430a91a167cdb6481e81`](https://github.com/dantte-lp/gobfd/commit/816fc7e04d411c306fbe430a91a167cdb6481e81)
on branch `release/v1.0` (incorporating
[`eae8f0636055de8ac4714199e27ff0c9d18b9859`](https://github.com/dantte-lp/gobfd/commit/eae8f0636055de8ac4714199e27ff0c9d18b9859)
from `dev`, Go `1.27.4`, GoBGP `v4.11.0`, ADR-0013 Unix restart ownership,
ADR-0014 `v1.0` GoBGP v4 release-line graduation, RFC 9747 slow Echo pacing,
Micro-BFD loss resync and `enforce` health gating, bounded inactive per-peer
Prometheus series, run-owned Compose/stage image cleanup, direct containerd
gRPC lease import for Kubernetes, and Bash-free `Makefile`/OCI orchestration).
Stable branches, release tags and backup refs are preserved. No release tag or
publication follows from source integration.

## Stable v1 acceptance

| Area | Implemented | Remaining acceptance |
|:---|:---|:---|
| Control workload | Real owner/peer workload and terminal accounting | Calibrated native reference distributions and full reference run |
| Reference environment | Isolated Debian runner; identity, clock and admission checks | Canonical resource admission and qualified workload measurements |
| Pressure profiles | CPU, GC, scheduler, loss and session-churn drivers | Measurements in the admitted reference envelope |
| Echo workload | Real return traffic and owner Down witnesses | Native scale, timing and resource qualification |
| Session-preserving reload | Source implementation and scoped packet checks | Unified five-field qualification on the exact final candidate and supported peers |
| Security disposition | Scoped dependency exceptions and management controls | Stable v1 advisory, reachability and final-candidate security evidence |
| Documentation | Source-linked EN/RU manual and ADR contracts | Independent correspondence, links and language-parity checks |

Under [ADR-0014](adr/0014-v1-gobgp-v4-release-line-baseline.md), `release/v1.0`
graduates to the `1.0.0` product line coupled to GoBGP v4 while preserving
explicit preview/experimental boundaries in [RFC coverage](08-rfc-compliance.md);
users requiring GoBGP v3 remain on `release/v0.6`. Long-horizon 24-hour soak,
native Control/Echo scale campaigns and external hardware NOS runs remain
tracked as operational qualification evidence outside the initial `1.0.0`
branch cut. See [Performance](14-performance-analysis.md) and
[Interoperability](05-interop.md) for measurement conditions.

## Release channels and prerelease history

[ADR-0008](adr/0008-experimental-prerelease-boundary.md) defines the historical
`v1.0.0-alpha.N` prerelease boundary dated 2026-10-30, and
[ADR-0014](adr/0014-v1-gobgp-v4-release-line-baseline.md) graduates
`release/v1.0` to stable `1.0.0` proposals on 2026-11-27. Release Please
prepares the reviewed release PR; GoReleaser and the independent verifier
own draft creation and publication. Policy acceptance does not establish a
published release until the reviewed release PR merges and the annotated tag
passes `release.yml`.

## Additional scope

| Capability | Boundary |
|:---|:---|
| HAProxy health as anycast VIP policy | Not implemented; the existing agent exports BFD state to HAProxy |
| Broader ExaBGP process API | A native anycast cycle does not qualify ACK/reload, JSON/EOR or the full dual-stack matrix |
| Pending-interface validation | Separate from the implemented core pending activation |
| Runtime global VNI defaults | Separate from startup-default behavior |

For service-driven anycast, [RFC 4786 §4.4.1](https://datatracker.ietf.org/doc/html/rfc4786#section-4.4.1)
provides availability guidance. A configured hold-down and explicit route
owner are project requirements. Do not use BFD AdminDown as a portable route
withdrawal command; client reaction is governed separately.

## Branch-specific maintenance gaps

| Branch | Contract |
|:---|:---|
| `dev` | Next product line; source acceptance does not establish stable delivery |
| `master` | Latest accepted stable state |
| `release/v1.0` | Supported v1.0 public and GoBGP v4 dependency contract |
| `release/v0.6` | Supported v0.6 public and GoBGP v3 dependency contract |

[Release process](10-changelog.md) defines qualification, changelog,
ruleset, exact-tag and draft-asset checks. Oracle Linux 10 is excluded from
v1 OCI products; Debian trixie and generic RPM generation retain separate
contracts. Preserve existing v0.6 assets and tags.

## Historical records

The previous accumulated status and exact receipts remain available at
[the integration baseline](https://github.com/dantte-lp/gobfd/blob/eae8f0636055de8ac4714199e27ff0c9d18b9859/docs/en/roadmap.md).
Dated audits remain in [the 2026-11-27 retrospective](audits/2026-11-27-release-retrospective.md),
[the 2026-10-26 retrospective](audits/2026-10-26-release-retrospective.md)
and [external-audit reconciliation](gist-audit-reconciliation.md). Historical
results are not fresh measurements of the current candidate.
