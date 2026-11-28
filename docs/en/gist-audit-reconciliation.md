# External audit reconciliation

[Russian translation](../ru/gist-audit-reconciliation.md).

## Historical baseline

This record describes the **2026-09-08** reconciliation. It does not describe
current release readiness. The original target was
`master@5680385679d8e03d74226b5fcde269210d63bde8`; the inspected development
source was `49085d969237e4d283d7c712b50d54612b3fe466`.

Original input: [immutable audit revision](https://gist.github.com/dantte-lp/a74802b65605f482425487c19c4f04d9/6a9a0b6c3b44c0138711df11e227de415f50ca1d),
128300 bytes, recorded SHA-256
`74312d56ab2978e1289604e4ce02128a35433f12bb9199a2d3cd91d18e6bc3a7`.

| Original classification | Count |
|:---|---:|
| Fixed | 25 |
| Partial | 9 |
| Open | 34 |
| Accepted constraint | 1 |
| Total | 69 |

Original severity: 8 Critical, 31 High, 22 Medium and 8 Low. The later
M-18 local update of 2026-10-20 and M-19 hardening note of 2026-10-21 do not
recount the original snapshot. Source inspection and existing tests are
not fresh runtime, peer or wire qualification.

## Historical dispositions

| Finding | Recorded disposition | Historical priority |
|:---|:---|:---|
| C-01 | Fixed | P0 |
| C-02 | Fixed | P0 |
| C-03 | Fixed | P0 |
| C-04 | Open | P0 |
| C-05 | Open | P0 |
| C-06 | Open | P0 |
| C-07 | Open | P0 |
| C-08 | Open | P0 |
| H-01 | Open | P0 |
| H-02 | Partial | P0 |
| H-03 | Open | P0 |
| H-04 | Fixed | P0 |
| H-05 | Open | P0 |
| H-06 | Partial | P0 |
| H-07 | Fixed | P0 |
| H-08 | Open | P0 |
| H-09 | Open | P0 |
| H-10 | Open | P0 |
| H-11 | Open | P1 |
| H-12 | Open | P0 |
| H-13 | Fixed | P0 |
| H-14 | Fixed | P0 |
| H-15 | Fixed | P0 |
| H-16 | Partial | P0 |
| H-17 | Open | P0 |
| H-18 | Open | P0 |
| H-19 | Partial | P1 |
| H-20 | Open | P1 |
| H-21 | Open | P1 |
| H-22 | Fixed | P0 |
| H-23 | Partial | P0 |
| H-24 | Partial | P0 |
| H-25 | Fixed | P0 |
| H-26 | Open | P0 |
| H-27 | Open | P0 |
| H-28 | Open | P0 |
| H-29 | Open | P0 |
| H-30 | Partial | P0 |
| H-31 | Open | P0 |
| M-01 | Open | P1 |
| M-02 | Fixed | P0 |
| M-03 | Fixed | P0 |
| M-04 | Open | P1 |
| M-05 | Partial | P2 |
| M-06 | Fixed | P0 |
| M-07 | Open | P0 |
| M-08 | Open | P0 |
| M-09 | Partial | P0 |
| M-10 | Open | P0 |
| M-11 | Fixed | P0 |
| M-12 | Fixed | P0 |
| M-13 | Fixed | P1 |
| M-14 | Fixed | P1 |
| M-15 | Open | P1 |
| M-16 | Fixed | P0 |
| M-17 | Fixed | P0 |
| M-18 | Fixed locally — 2026-10-20 | P1 |
| M-19 | Open — unit hardened locally 2026-10-21 | P0 |
| M-20 | Accepted constraint | P1 |
| M-21 | Open | P1 |
| M-22 | Open | P2 |
| L-01 | Fixed | P0 |
| L-02 | Fixed | P0 |
| L-03 | Fixed | P0 |
| L-04 | Fixed | P0 |
| L-05 | Open | P2 |
| L-06 | Open | P2 |
| L-07 | Fixed | P0 |
| L-08 | Fixed | P0 |

## Interpretation

Use [Architecture](01-architecture.md), [Protocol](02-protocol.md),
[Security](15-security.md) and the [RFC matrix](08-rfc-compliance.md) for the
current implementation. A scoped fix does not establish full RFC support,
stable delivery or forwarding continuity.

[RFC 5882 §3.2](https://datatracker.ietf.org/doc/html/rfc5882#section-3.2)
defines AdminDown client handling; §3.1 defines hysteresis, §4.2 routing-client
reaction and §4.3 graceful restart. Preserve normative strength when applying
these requirements. The single-maintainer governance constraint does not
waive release checks.

The [complete historical record](https://github.com/dantte-lp/gobfd/blob/eae8f0636055de8ac4714199e27ff0c9d18b9859/docs/en/gist-audit-reconciliation.md)
retains original correlation metadata. The [qualification status](roadmap.md)
records the separately dated release boundary.
