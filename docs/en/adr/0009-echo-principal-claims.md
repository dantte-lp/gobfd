# ADR-0009: Principal claims on a shared Echo wire

| Field | Value |
|:---|:---|
| Status | Accepted policy; local qualification separate |
| Date | 2026-10-25 |

## Context

A shared Echo transport needs principal-scoped claims. Discriminator-only deletion cannot distinguish owners; withdrawing configuration must not remove reception for a surviving API owner.

## Decision

Reuse `SessionOwner`, `apiOwnerFromContext` and comparable `SessionKey`. Echo key includes unmapped peer/local addresses with their zones and the exact interface. TX interval and DetectMult must agree before another owner can share the wire. Maintain one physical discriminator, sender lease and owner-claim set under lifecycle→ownership→registry serialization.

| Operation | Required result |
|:---|:---|
| Same-owner identical Add | Idempotent; no new physical session, sender, discriminator or protocol reset |
| Compatible other-owner Add | Share physical wire |
| Parameter conflict | `FailedPrecondition` before sender acquisition or claim mutation |
| Delete existing wire without caller claim | `PermissionDenied` |
| Delete absent discriminator | `NotFound` |
| Final claim release/shutdown | Join Echo owner, then release lease outside Manager locks |

Principal comes from verified transport, never request fields. Unix UID, TLS URI, TLS DNS and unsafe-opt-in local namespaces remain disjoint. Observation remains daemon-wide. Preview enablement and RX readiness precede every Add, including retries; an unready retry may fail while its prior claim remains installed. List/Delete retain management authorization.

The physical Echo lease holds RX independently of the creating source. Configuration withdrawal preserves TX/RX for API claims; final physical release closes that RX ref, while source refs retain their own lifetimes.

Reconciliation validates the entire desired set against all owners, prepares new config claims before stale release and rolls back only claims acquired by that pass on preparation failure. An already canceled pass changes nothing. Cancellation before stale release preserves old claims; already published new claims remain and a live retry reuses them.

## Consequences

Claim count and physical-wire count differ. Claim-only changes emit no wire lifecycle event and preserve state, discriminator and source port. Compatibility methods hold their own owner and cannot release config or API principals. Cleanup errors remain visible.

Management authorization does not authenticate Echo returns or qualify RFC 9747, interhost forwarding, IPv6 link-local or scale. Echo remains preview. Claim-lifecycle acceptance, returned-packet security and final release qualification require separate evidence.

## References

- [Ownership and reconciliation](../../../internal/bfd/manager_echo.go).
- [Key and owner types](../../../internal/bfd/session_identity.go).
- [API admission](../../../internal/server/echo.go), [principal namespaces](../../../internal/server/mutation_guard.go).
- [Receive ownership](../../../cmd/gobfd/listeners.go), [reload](../../../cmd/gobfd/reload.go).
- [Security policy](../15-security.md), [RFC 9747 boundary](../08-rfc-compliance.md#rfc-9747-implementation-notes).
