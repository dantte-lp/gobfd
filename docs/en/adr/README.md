# Architecture Decision Records

[Russian translation](../../ru/adr/README.md).

ADRs record architectural policy and its consequences. Acceptance of an ADR does not prove implementation, runtime qualification or stable delivery.

## Record contract

Keep the ID, filename and acceptance date immutable. Set Date when the record becomes Accepted; retain superseded records and link the replacement. Use Status, Date, Context, Decision, Consequences and References. Add a qualification field when policy acceptance and runtime acceptance differ.

Reference sections use present-tense facts with repository sources. Procedures use imperative steps. Record measured results as dated receipts bound to their input; do not promote an older receipt to a later candidate.

## Index

| ID | Decision | Status |
|:---|:---|:---|
| [0001](0001-link-state-rtnetlink.md) | rtnetlink link/address monitoring | Accepted |
| [0002](0002-ovs-backend-ovsdb.md) | Native OVSDB LAG backend | Accepted |
| [0003](0003-linux-advanced-bfd-applicability.md) | Linux advanced-mode ownership and applicability | Accepted |
| [0004](0004-overlay-backend-vocabulary.md) | Shared overlay backend vocabulary | Accepted |
| [0005](0005-smallstep-management-certificates.md) | External Smallstep certificate renewal | Accepted |
| [0006](0006-session-preserving-reload-policy.md) | Session-preserving reload | Accepted policy; qualification separate |
| [0007](0007-workload-phase-accounting.md) | Workload phases and terminal accounting | Accepted policy; qualification separate |
| [0008](0008-experimental-prerelease-boundary.md) | Experimental prerelease channel | Accepted policy; candidate qualification separate |
| [0009](0009-echo-principal-claims.md) | Principal claims on shared Echo wire | Accepted policy; local qualification separate |
| [0010](0010-bfd-auth-value-ownership.md) | Value auth metadata with borrowed payload | Superseded by ADR-0011 |
| [0011](0011-bfd-auth-inline-payload.md) | Inline authentication payload | Accepted policy; source/runtime qualification separate |
| [0012](0012-owner-down-transition-witness.md) | Owner-monotonic Down evidence | Accepted policy; classification/release qualification separate |

See [architecture](../01-architecture.md), [RFC coverage](../08-rfc-compliance.md) and [vendor references](../reference/vendor-bfd.md) for implementation and peer boundaries.
