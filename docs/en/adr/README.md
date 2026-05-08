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
| Status | Proposed | Accepted | Superseded by ADR-NNNN | Deprecated |
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
