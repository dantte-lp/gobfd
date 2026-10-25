# ADR-0009: Principal claims on a shared Echo wire

| Field | Value |
|---|---|
| Status | Accepted policy; local qualification separate |
| Date | 2026-10-25 |

## Context

Before principal claims, Echo's exclusive API/config source flag could not
represent shared ownership. Repeated API creation allocated another discriminator
and sender, while deletion by discriminator alone could remove another
principal's session. Sharing the wire also requires preserving RX after its
original declarative source is withdrawn.

## Decision

Reuse `SessionOwner`, transport-derived `apiOwnerFromContext` and the comparable
`SessionKey`. Echo identity contains the unmapped peer/local addresses and the
exact interface; zones remain part of addresses. A structured key avoids
delimiter collisions. TX interval and Detect Multiplier must match an existing
wire before another claim is accepted.

Under the existing lifecycle/ownership/registry lock order, keep one Echo wire
and sender lease per identity and a set of owner claims. An admitted same-owner
retry is a no-op; a compatible config or API owner shares the wire. A conflicting
parameter tuple returns `FailedPrecondition` before allocation or mutation.
API owners come from the verified transport, never from request fields. Unix
UID, TLS URI, TLS DNS and unsafe-opt-in anonymous local owners retain the existing
principal namespaces. Read-only observation remains daemon-wide.

Deletion releases only the caller's claim. A caller without a claim receives
`PermissionDenied`; an absent discriminator returns `NotFound`. Only the final
claim retires the wire. Release and shutdown join the Echo owner before closing
its sender lease, outside Manager locks. Cleanup errors remain observable.

The physical Echo lease retains its receive listener regardless of which source
created it first. Withdrawing configuration must preserve RX/TX for a surviving
API claim. The final wire release closes its lease-owned RX reference; source
configuration references retain their independent lifetime.

Reconciliation validates the complete desired set against all existing owners,
then prepares new config claims before releasing stale ones. Preparation failure
rolls back only claims acquired by that pass, preserves previous config claims
and never removes surviving API claims. Claim counts and physical-wire counts
remain distinct.

## Consequences

- Repeated identical Echo Add is idempotent; List/Watch describe one wire even
  when several owners share it. Claim changes alone do not publish wire lifecycle
  events or reset protocol state, discriminator or source port.
- Preview/readiness admission still precedes every Add, including retries.
  Unready admission can fail while an existing claim remains intact; List/Delete
  remain available under the management policy.
- The compatibility Go creation/deletion methods use their own compatibility
  owner and cannot release configuration or another API principal's claim.
- Principal authorization is control-plane policy. It does not authenticate
  returned Echo packets or qualify interhost forwarding, IPv6 link-local, RFC
  9747 security or supported scale. Echo remains preview.

## References

- [Echo ownership and reconciliation](../../../internal/bfd/manager_echo.go)
- [Shared identity and owner types](../../../internal/bfd/session_identity.go)
- [Echo API adapter](../../../internal/server/echo.go)
- [Transport principal namespaces](../../../internal/server/mutation_guard.go)
- [Declarative Echo receive lease](../../../cmd/gobfd/reload.go)
- [Receive listener owner](../../../cmd/gobfd/listeners.go)
- [Security policy](../15-security.md)

Executable acceptance belongs to Beads `gobfd-qj0.8.2.2.8`; RFC Echo security
remains separate under `gobfd-qj0.8.2.1.13`. Local checks do not close final
release qualification.
