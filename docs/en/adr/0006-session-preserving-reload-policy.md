# ADR-0006: Session-preserving reload policy

[Russian translation](../../ru/adr/0006-session-preserving-reload-policy.md).

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-24 |
| Qualification | Accepted policy; local implementation qualification; actual-wire qualification remains open |

## Context

Replacing an established session changes its discriminator, protocol state and client observations. Preserving the local object alone does not prove peer continuity or forwarding. RFC 5880 timer/auth/admin procedures and RFC 5882 client policies do not provide a general hitless-reload guarantee.

## Decision

Preserve instance, discriminator, sender lease, auth replay state, counters and claims for a stable canonical base identity with one config owner and a supported complete tuple. Reject incompatible shared-owner changes. Never silently convert an unsupported update into delete/recreate.

### Five-field transaction

| Field/change | Required effect | Restriction |
|:---|:---|:---|
| TX increase / RX decrease in Up | Retain old protected local effect until relevant Final | Existing Poll contract |
| TX decrease / RX increase in Up | Apply when proposal activates | Existing Poll contract |
| Non-Up TX/RX proposal | Apply local effects immediately on promotion | Poll/separation still serialize confirmation |
| DetectMult in Async | Apply and advertise next Control without Poll | Nonzero; reschedule jitter when crossing 1 |
| DetectMult with both peers Up and remote Demand | Poll/Final confirmation | Does not qualify general Demand |
| MinTTL | Update local multihop `255−N` floor under mutex without Poll, including remote Demand | GoBFD product policy |
| PaddedPduSize | Positive→positive, unchanged auth, Async, constructor-proven `PaddingPrepared` | No zero transition, unknown/unprepared sender or shared base owner |

One tuple contains TX, RX, DetectMult, MinTTL and PaddedPduSize. One active proposal keeps its advertised tuple; retain only the latest waiting proposal, including RX=0. Final confirms only after successful Poll transmission. Crossed Final-only replies cannot confirm the local Poll. After Final, wait the measured exchange duration before a consecutive transaction. Expiry fails the receipt without undoing already advertised values.

Keep desired, advertised, effective and confirmed values/revisions distinct. Local MinTTL has no wire advertisement. Pending admission/advertisement is not completion. Same-generation/revision reapply creates no replacement, Poll or duplicate side effect; stale completion cannot overwrite a newer intent.

Validate and prepare the complete tuple before any owner field commit. The coordinator guards only `candidate.base` through `Manager.AdmitSessionReload`. Micro-BFD/overlay gain no padding/MinTTL fields and remain outside this qualified profile. Other-source conflict/stale behavior does not become five-source atomicity.

Padding policy is checked before equality shortcuts, including reversal to a confirmed target under Demand. Recheck a queued tuple before promotion; newly active Demand rejects the whole tuple before any field commit. Capacity growth prepares one replaceable waiting buffer during control admission; owner swaps it before field commit. Neither owner promotion nor packet send performs growth allocation.

The padding target counts transport payload, excluding IP/UDP. Send length is `max(actual PDU, target)`, preserving auth. Padding is zero and outside the authentication domain. Authenticated minimum 26 and zero-target DF defaults remain unchanged. Enable/disable and dynamic DF stay outside the accepted reload profile.

Sources: [owner transaction](../../../internal/bfd/session_update.go), [tests](../../../internal/bfd/session_update_test.go), [packet construction](../../../internal/bfd/session_packet.go), [timer transaction specification](../reference/timer-transactions.md).

### Authentication admission

Reject same-identity enable/disable, algorithm and unqualified key transitions before accepting a desired generation, even in a combined otherwise-supported update. Never reset sequence/replay state or fall back to replacement.

Compare immutable semantic fingerprints against retained accepted base intent, including absent runtime entries, and the live registry regardless of API/shared ownership. Fingerprints include enablement, algorithm, key IDs/material, selected TX key and receive-key set. Startup/new/removed identities and fresh stores with identical semantics remain admissible.

Acquire lifecycle→ownership→registry. Release registry before short local acceptance; retain ownership serialization through generation publication, then release before configured-peer callbacks and resource application. Rejection precedes generation, log/readiness and resource mutation. Later claims still pass normal reconciliation. This is admission protection, not whole-source atomicity or seamless rollback.

Coordinated same-algorithm key-ID rotation remains unqualified. It needs receive overlap, TX cutover, old-key retirement, replay continuity and peer interop. A key store alone does not enable rotation. Sources: [admission](../../../internal/bfd/manager_auth_reload.go), [admission tests](../../../internal/bfd/manager_auth_reload_test.go), [RFC 5880 §6.7](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.7).

### Source reconciliation and replacement

Prepare/claim desired resources before stale release. Release old claims only at zero Failed and ResourcePending; TimerPending is separate. Fatal preparation rolls back new claims; valid disjoint claims may remain for resource-pending retry.

A receive-key collision permits a prepared SingleHop/MultiHop swap only for interface-empty base identity, one config owner, old key absent from desired and unchanged remaining identity. Other collisions fail before sender factory and, for base admission, before generation acceptance. Before replacement publication, cancellation preserves the old identity and releases prepared lease/discriminator once.

After publication, the captured old session commits AdminDown and drains outside mutation locks. Retain its lease and discriminator until drain completion or typed incomplete failure, then clean up once. The retiring exact key remains privately reserved through owner join, lease/discriminator release and metrics cleanup, including errors. Add/reverse rekey to it fails before factory with `ErrDuplicateSession`/`AlreadyExists`; explicit retry after Delete can create the next incarnation. No automatic retry is introduced.

Disruptive replacement requires separate explicit identity/transition and client-impact intent. Before peer visibility, preparation must roll back on failure. After peer-visible cutover, seamless rollback is not guaranteed. AdminDown is not a promise that BGP or forwarding remains unchanged. The [same-key FRR fixture](../../../test/interop/same_key_topology_test.go) checks conflict while the old UDP inode is owned, closure before retry and old/new discriminator epochs. Peer Down is allowed. A passing same-key wire receipt remains required on the exact candidate.

Sources: [replacement](../../../internal/bfd/manager_replace.go), [reconciliation](../../../cmd/gobfd/reconciliation.go), [RFC 5880 §6.8.16](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.16), [RFC 5882 §§3.2, 4.3](https://www.rfc-editor.org/rfc/rfc5882.html#section-3.2).

### Overlay resource ownership

Pure preflight covers a complete VXLAN or Geneve source before backend open/`SetScopes`. Duplicate desired wire identities are invalid. Unequal scopes sharing a live/draining wire identity, including VXLAN local-MAC changes, require separate removal/recreation. An unused never-committed preparation may be superseded.

After successful preflight, prune obsolete unused preparations before claims. Retain existing scopes in current desired union positive physical SenderLease refs. Actual factory acquisition increments the ref; registered sessions retain it through drain until idempotent lease close. Unused/unpublished leases use once-only cleanup without drain.

Incomplete acquisition returns Pending/Failed before that source's Manager apply/release, preserving live/draining exact scopes/backends. Desired preparations may remain for retry. This is source-local protection, not global pre-generation overlay admission or all-source atomicity. Source: [overlay owner](../../../cmd/gobfd/overlay_owner.go).

### Availability and receipts

Receipts retain bounded interface/address-class presence flags, never resource identity/raw errors. A class change updates status at the same pending count; identical receipts deduplicate and convergence clears flags. Timer pending and permanent failures keep separate retry/accounting.

Availability covers listeners and outer overlay endpoints as well as bound interfaces. Address loss preserves desired generation/claims, marks source pending and health not ready; recovery retries the same generation. A two-second check covers five sources without monitor events. Inventory errors fail the source and require explicit SIGHUP. Source: [coordinator](../../../cmd/gobfd/reconciliation.go).

## Alternatives

Delete/recreate changes the discriminator and protocol state, so it is not a fallback for an unsupported retained-identity update. Whole-source atomicity and seamless rollback are not provided by source-local admission; disruptive replacement requires its separate explicit intent.

## Consequences

Supported changes preserve local identity. Peer timing, padding acceptance and forwarding require measurement. RFC 9764 §4.1 permits an Up session to go Down if its peer cannot receive padded Control. Cloud/hardware limits, Async/Echo and graceful restart require their own target profiles; RFC 7419 intervals do not establish acceptance.

### Qualification contract

Use the existing actual-wire harness; do not infer wire Poll/Final from generation JSON alone. Scenario-selected receipt expectations are independent of capture validation: immediate updates may converge directly or after owner queueing; existing Poll phases require pending-before-applied.

- DetectMult: authenticated Async 1→5→3 and remote-Demand 5→3 at negotiated 300 ms. Expected jitter mean is 247.5 ms for multiplier 1 and 262.5 ms otherwise. Local multiplier does not change local RX D, which uses the received multiplier. Crafted remote-Demand 5×500 ms gives a derived Poll bound; the peer has no detection timer. Responsive Finals/quiet window do not qualify missing-Final expiry or general Demand. Permit one old in-flight Control only as the first post-receipt packet within slack; new Control must arrive within one TX plus slack.
- TTL: authenticated UDP/4784 startup; pause peer before reload. First replay the last accepted payload at admissible TTL: exactly one auth failure and zero accepted input. Then send one signed Poll below floor and identical payload/sequence at floor; only the latter may be accepted or return Final. One identity/counter/auth/capture baseline spans seven boundaries and remote-Demand entry; probes finish inside existing D so replay expiry cannot substitute for continuity.
- Padding: one capture cursor/auth RX state per direction across resize/rejections. Failed/incomplete evaluation advances neither. Local 128→192→128 target does not change peer target or prove bidirectional MTU. Check DF, UDP payload, declared Length, zero padding and MAC domain separately. Auth5 is startup-only and separate from auth4. Probe aggregate/named BFD health through the owned container's management Unix socket using the same compiled interop executable; TCP stays loopback-only.
- Final gate: limit TShark results after display filtering; `-c` is not absence evidence. Missing mandatory initial-state/expiry evidence fails. Before test-image cleanup retain immutable image IDs and required OCI source/base annotations. No runner path becomes an engine-host bind path.

### Dated local receipts

These are retained results for their named inputs, not fresh validation or final acceptance.

| Slice | Exact source | Retained result | Evidence |
|:---|:---|:---|:---|
| Lifecycle | Frozen input named in receipt | 1049 PASS, 0 FAIL; 5 packages, 2 environment skips; 27 budgets match; 577 inventory hashes match | `reports/rekey-lifecycle-20261024/` |
| DetectMult | `d9e67064e51012c0d25a1a22dc9bd469600d8469` | 6 Async + 2 crafted remote-Demand phases | `reports/detect-wire-20261024/` |
| TTL | `491b2214994f07e017eb93ab67a29a95378c619e` | Default 162.94 s, multihop 81.98 s; 7 boundary/replay windows | `reports/ttl-wire-20261024/` |
| Padding | `b97729458a696aee21023ba147af51417ed9ff7b` | 8 resize + 7 rejection phases, 17 peer windows; health SERVING | `reports/padding-wire-20261024/` |
| Default qualifier correction | `e4e79a8eee8e7c38bfc78321bdc5e9030909c214` | Local acceptance of corrected qualifier | Unified five-field qualification |

Lifecycle receipt also retains zero lint issues, vet/two benchmark exits zero and overlay lease cleanup qualification. Padding's three runs retain the 512 MiB cap, zero max/OOM/swap, 853 source matches and 675 sidecar matches.

DetectMult, packet-policy, authentication-admission and lifecycle receipts are source slices, not stable acceptance. Mandatory actual-wire qualification depends on completed lifecycle work. The final four-profile/five-field gate follows production Session/Echo thread and scale work and must qualify the exact candidate before v1. Later production changes cannot inherit older receipts.

## References

- [Configuration and receipt reference](../03-configuration.md).
- [Interop](../05-interop.md), [RFC coverage](../08-rfc-compliance.md).
- [RFC 5880 §§6.6, 6.8.3, 6.8.12](../../rfc/rfc5880.txt), [RFC 9764 §§3–4.1](../../rfc/rfc9764.txt).
- [Target peer references](../reference/vendor-bfd.md).
