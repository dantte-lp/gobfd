# Release qualification status

[Russian translation](../ru/roadmap.md).

Snapshot: **2026-10-29**. This page records the technical acceptance boundary;
it does not authorize publication. Use the documentation at the candidate's
commit and the applicable release-line contract.

## Source baseline

Runtime integration baseline:
[`eae8f0636055de8ac4714199e27ff0c9d18b9859`](https://github.com/dantte-lp/gobfd/commit/eae8f0636055de8ac4714199e27ff0c9d18b9859),
branch `dev`. All 11 reviewed PRs are merged and their topic branches are
retired. Stable branches, release tags and backup refs are preserved.
No release tag or publication follows from source integration.

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

The reference 24-hour run, native Control/Echo scale acceptance and final
wire evidence are release conditions. Local unit/race tests, zero-allocation
micro-benchmarks and short native lifecycle runs do not substitute for them.
See [Performance](14-performance-analysis.md) and [Interoperability](05-interop.md)
for measurement conditions.

## Experimental prerelease

[ADR-0008](adr/0008-experimental-prerelease-boundary.md) defines a proposed
experimental boundary. The existing release path accepts stable tags; an
alpha requires an explicitly implemented and accepted prerelease path.
A documentation change does not authorize an alpha tag, draft or publication.

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
| `release/v0.6` | Supported v0.6 public and GoBGP v3 dependency contract |

[Release process](10-changelog.md) defines qualification, changelog,
ruleset, exact-tag and draft-asset checks. Oracle Linux 10 is excluded from
v1 OCI products; Debian trixie and generic RPM generation retain separate
contracts. Preserve existing v0.6 assets and tags.

## Historical records

The previous accumulated status and exact receipts remain available at
[the integration baseline](https://github.com/dantte-lp/gobfd/blob/eae8f0636055de8ac4714199e27ff0c9d18b9859/docs/en/roadmap.md).
Dated audits remain in [the retrospective](audits/2026-10-26-release-retrospective.md)
and [external-audit reconciliation](gist-audit-reconciliation.md). Historical
results are not fresh measurements of the current candidate.
