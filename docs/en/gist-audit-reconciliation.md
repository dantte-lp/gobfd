# External Audit Reconciliation — Historical Snapshot

## Scope and provenance

This is the **2026-09-08** reconciliation, not the current release status.
Beads `gobfd-qj0.8.2.9` retains finding ownership and detailed evidence.
The source audit is
[gist revision 6a9a0b6c3b44c0138711df11e227de415f50ca1d](https://gist.github.com/dantte-lp/a74802b65605f482425487c19c4f04d9/6a9a0b6c3b44c0138711df11e227de415f50ca1d),
128300 bytes, SHA-256
`74312d56ab2978e1289604e4ce02128a35433f12bb9199a2d3cd91d18e6bc3a7`.
Its original target was `master@5680385679d8e03d74226b5fcde269210d63bde8`;
the reconciliation inspected `dev@49085d969237e4d283d7c712b50d54612b3fe466`.

The original snapshot contains 69 findings: 25 Fixed, 9 Partial,
34 Open and 1 Accepted constraint. Original severity was 8 Critical,
31 High, 22 Medium and 8 Low; severity is not the current Beads priority.
A source review, including inspected tests, is not a fresh runtime or interop run.
Later fixes do not silently change these historical counts.

The table retains the separately dated M-18 local update of **2026-10-20**
and the M-19 hardening note of **2026-10-21**. M-18 was Open in the original
snapshot; its later Fixed locally status is not included in the counts above.
These annotations do not re-audit or recount the other findings.

The previous full narrative and immutable source links remain in git at
`3e2a98045e11881ae86e1439aebe5f3496f9ab82:docs/en/gist-audit-reconciliation.md`.
Use `git show` for that local history; current behavior belongs in the
[RFC matrix](08-rfc-compliance.md), [architecture](01-architecture.md),
[security policy](15-security.md) and relevant ADRs. Those references supersede
old present-tense descriptions without rewriting the historical finding table.

## Historical dispositions

| Finding | Historical disposition, with explicitly dated local updates | Beads owner and historical priority |
|---|---|---|
| C-01 | Fixed | `gobfd-8f3.1` / P0 |
| C-02 | Fixed | `gobfd-qj0.8.2.1.9` / P0 |
| C-03 | Fixed | `gobfd-qj0.8.2.1.9` / P0 |
| C-04 | Open | `gobfd-qj0.8.2.3` / P0 |
| C-05 | Open | `gobfd-qj0.8.2.1.5` / P0 |
| C-06 | Open | `gobfd-qj0.8.2.6.2` / P0 |
| C-07 | Open | `gobfd-qj0.8.2.1.10.1` / P0 |
| C-08 | Open | `gobfd-qj0.8.2.5.1` / P0 |
| H-01 | Open | `gobfd-qj0.8.2.1.2.1` / P0 |
| H-02 | Partial | `gobfd-qj0.8.2.1.4` / P0 |
| H-03 | Open | `gobfd-qj0.8.2.1.11` / P0 |
| H-04 | Fixed | `gobfd-qj0.8.2.1.1.1.1` / P0 |
| H-05 | Open | `gobfd-qj0.8.2.1.1.1` / P0 |
| H-06 | Partial | `gobfd-qj0.8.2.1.1.2` / P0 |
| H-07 | Fixed | `gobfd-qj0.8.2.1.1.2.1` / P0 |
| H-08 | Open | `gobfd-qj0.8.2.1.1.2` / P0 |
| H-09 | Open | `gobfd-qj0.8.2.1.3.3` / P0 |
| H-10 | Open | `gobfd-qj0.8.2.1.3.1` / P0 |
| H-11 | Open | `gobfd-qj0.8.2.1.7` / P1 |
| H-12 | Open | `gobfd-qj0.8.2.1.3.3` / P0 |
| H-13 | Fixed | `gobfd-qj0.8.2.2.4` / P0 |
| H-14 | Fixed | `gobfd-qj0.8.2.2.4` / P0 |
| H-15 | Fixed | `gobfd-8f3.1` / P0 |
| H-16 | Partial | `gobfd-qj0.8.2.2.7` / P0 |
| H-17 | Open | `gobfd-qj0.8.2.1.10.2` / P0 |
| H-18 | Open | `gobfd-qj0.8.2.1.10.2` / P0 |
| H-19 | Partial | `gobfd-qj0.8.2.1.10.3` / P1 |
| H-20 | Open | `gobfd-qj0.8.2.1.12` / P1 |
| H-21 | Open | `gobfd-qj0.8.2.1.13` / P1 |
| H-22 | Fixed | `gobfd-qj0.8.2.1.9` / P0 |
| H-23 | Partial | `gobfd-qj0.8.2.2.6.4` / P0 |
| H-24 | Partial | `gobfd-qj0.8.2.2.1` / P0 |
| H-25 | Fixed | `gobfd-8f3.4` / P0 |
| H-26 | Open | `gobfd-qj0.8.2.1.3.1` / P0 |
| H-27 | Open | `gobfd-qj0.8.2.4.1` / P0 |
| H-28 | Open | `gobfd-qj0.8.2.5.2` / P0 |
| H-29 | Open | `gobfd-qj0.8.2.1.3.2` / P0 |
| H-30 | Partial | `gobfd-qj0.8.2.3.1` / P0 |
| H-31 | Open | `gobfd-qj0.8.2.6.2` / P0 |
| M-01 | Open | `gobfd-qj0.8.2.1.14` / P1 |
| M-02 | Fixed | `gobfd-qj0.8.2.2.2` / P0 |
| M-03 | Fixed | `gobfd-qj0.8.2.1.1.1.1` / P0 |
| M-04 | Open | `gobfd-qj0.8.2.1.2.2` / P1 |
| M-05 | Partial | `gobfd-qj0.8.2.1.15` / P2 |
| M-06 | Fixed | `gobfd-qj0.8.2.2.5` / P0 |
| M-07 | Open | `gobfd-qj0.8.2.1.3.2` / P0 |
| M-08 | Open | `gobfd-qj0.8.2.1.3.2` / P0 |
| M-09 | Partial | `gobfd-qj0.8.2.2.1` / P0 |
| M-10 | Open | `gobfd-qj0.8.2.2.1` / P0 |
| M-11 | Fixed | `gobfd-qj0.8.2.1.9.2` / P0 |
| M-12 | Fixed | `gobfd-qj0.8.2.1.9.1` / P0 |
| M-13 | Fixed | `gobfd-qj0.8.2.1.9.3` / P1 |
| M-14 | Fixed | `gobfd-qj0.8.2.1.9.3` / P1 |
| M-15 | Open | `gobfd-qj0.8.2.1.6` / P1 |
| M-16 | Fixed | `gobfd-8f3.4` / P0 |
| M-17 | Fixed | `gobfd-8f3.6` / P0 |
| M-18 | Fixed locally — 2026-10-20 | `gobfd-qj0.8.2.1.3.4` / P1, closed |
| M-19 | Open — unit hardened locally 2026-10-21 | `gobfd-qj0.8.2.3.3` / P0 |
| M-20 | Accepted constraint | `gobfd-qj0.8.2.7.1` / P1 |
| M-21 | Open | `gobfd-qj0.8.2.1.6` / P1 |
| M-22 | Open | `gobfd-qj0.8.2.6.1` / P2 |
| L-01 | Fixed | `gobfd-qj0.8.2.10` / P0 |
| L-02 | Fixed | `gobfd-qj0.8.2.10` / P0 |
| L-03 | Fixed | `gobfd-qj0.8.2.10` / P0 |
| L-04 | Fixed | `gobfd-qj0.8.2.10` / P0 |
| L-05 | Open | `gobfd-qj0.8.2.1.16` / P2 |
| L-06 | Open | `gobfd-qj0.8.2.1.16` / P2 |
| L-07 | Fixed | `gobfd-qj0.8.2.2.5` / P0 |
| L-08 | Fixed | `gobfd-qj0.8.2.10` / P0 |

## Interpretation

A fixed finding applies to its scoped audited source or explicitly observed
metadata. It does not close a broader task, establish complete RFC compliance
or deliver that source to a stable branch. The accepted constraint records
single-maintainer governance, not a technical correction or permission to
waive release checks.

RFC 5882 §4.3 concerns Graceful Restart; general routing reaction and EBGP
handling are in §§4.2 and 10.2. AdminDown handling maps to RFC 5880 §6.8.16
and RFC 5882 §8; hysteresis maps to RFC 5882 §3.1. Peer forwarding continuity
is not implied. Overlay review uses RFC 8971 §§5–6 and RFC 9521 §§4–5.
Security recommendations must retain their normative strength rather than
being restated as unconditional RFC MUST requirements.

Current implementation and qualification receipts belong in Beads and the
[dated roadmap](roadmap.md). Historical Python tooling observations do not
relax the current no-Python repository contract. Release tags, drafts and
assets wait for the applicable qualification and publication authorization.
