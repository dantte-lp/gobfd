# Architecture Decision Records

Living decision records for GoBFD. Each record states context, decision,
and consequences. The `Date` field is set when the record reaches `Accepted`
and never changes afterwards. Superseded records remain in place with a
forward link to the replacement.

## Format

```markdown
# ADR-NNNN: Title

| Field | Value |
|---|---|
| Status | One of: Proposed, Accepted, Superseded by ADR-NNNN, Deprecated |
| Date | YYYY-MM-DD |

## Context
## Decision
## Consequences
## References
```

## Index

| ID | Title | Status |
|---|---|---|
| [0001](./0001-link-state-rtnetlink.md) | Link-state monitoring via rtnetlink | Accepted |
| [0002](./0002-ovs-backend-ovsdb.md) | Native OVSDB client for the OVS LAG backend | Accepted |
| [0003](./0003-linux-advanced-bfd-applicability.md) | Linux applicability of Micro-BFD, VXLAN BFD, and Geneve BFD | Accepted |
| [0004](./0004-overlay-backend-vocabulary.md) | Shared overlay backend vocabulary | Accepted |
| [0005](./0005-smallstep-management-certificates.md) | External Smallstep CA renewal for management certificates | Accepted |
| [0006](./0006-session-preserving-reload-policy.md) | Session-preserving reload policy | Accepted policy; qualification separate |
| [0007](./0007-workload-phase-accounting.md) | Workload phases and terminal packet accounting | Accepted policy; qualification separate |
| [0008](./0008-experimental-prerelease-boundary.md) | Experimental prerelease boundary | Proposed; no publication authorization |
| [0009](./0009-echo-principal-claims.md) | Principal claims on a shared Echo wire | Accepted policy; local qualification separate |
| [0010](./0010-bfd-auth-value-ownership.md) | Value ownership of BFD authentication metadata | Superseded by ADR-0011 |
| [0011](./0011-bfd-auth-inline-payload.md) | Inline BFD authentication payload ownership | Accepted policy; source and runtime qualification separate |
