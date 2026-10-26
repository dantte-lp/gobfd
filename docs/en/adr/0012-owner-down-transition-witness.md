# ADR-0012: Owner-monotonic Down transition evidence

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-26 |

## Context

RFC 5880 Section 6.8.4 derives Detection Time independently in each direction.
The reference workload permits Down only for intentional blackholes. Its
classification therefore needs the actual owner transition and valid receive
history, independently of the peer omission schedule.

`LastPacketReceived` is Unix-time telemetry. A notification's `Timestamp` is
constructed after logging and metric updates. Neither proves a monotonic
commit boundary. Go's `time.Time` preserves monotonic readings in value copies;
UTC conversion and serialization remove them. A kernel-to-wall conversion
cannot supply a missing owner witness.

## Decision

Capture an optional value witness only for a real `Up` to `Down` transition.
The owner copies its last validated receive time and negotiated Detection Time,
and the actual timer deadline when a detection timer is being dispatched,
before timer interruption can apply a pending RX change. Interruption keeps
its existing position before commit; two monotonic timestamps then bracket
`commitObservation`. Bind the
witness to the returned `ObservationVersion` and retain it under the existing
session mutex. Notification publication copies it only when that version
matches the explicitly published observation. Publications without a matching
captured version have no witness; missing evidence remains unknown.

The bracket contains the local observation commit. Logging, notification
delivery and actuator delays occur after it and cannot move its endpoints.
The last validated receive denotes owner processing after packet validation,
not kernel arrival or the end of packet processing. Keep these meanings
separate from the UDP-to-owner latency measurements.

Carry the value through the existing bounded Down journal. Compute relative
phase intervals while its `time.Time` values still retain monotonic readings;
serialize numeric offsets and explicit availability. Missing, stripped,
inconsistent or version-mismatched timestamps must remain unknown. Preserve
the existing source/subscriber completeness requirements independently.

`down_witnesses` retains every delivered source transition, including setup
and drain. Existing `down_events` and cutoff counters keep their notification
timestamp semantics. A delayed notification must not discard an otherwise
eligible commit witness before causal window classification.

Keep the frozen cause, remote observation, endpoint, discriminator and
observation version. Do not retain `SessionRef` pointers in the lifetime
event array: doing so would keep retired sessions alive during churn.
Lifetime-safe incarnation correlation remains required separately.

## Consequences

The witness adds bounded value storage to each session and notification.
Capture takes two clock readings and one mutex update per actual Down
transition, without a per-packet callback, heap allocation or wire change.
Allocation and notification-size measurements are required before source
acceptance; this decision makes no RSS or latency improvement claim.

This witness is a prerequisite for independent Down classification, not a
classification verdict. Actual peer omission evidence, incarnation identity,
phase brackets and causal validation remain mandatory. It does not establish
total kernel timestamp uncertainty or qualify the reference, engineering,
native wire or 24-hour release gates.

## References

- [RFC 5880 Section 6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4).
- [Go time semantics at the pinned toolchain](https://github.com/golang/go/blob/go1.27.3/src/time/time.go).
- Beads `gobfd-qj0.8.2.1.6.3.4.3.6.2` and `gobfd-qj0.8.2.1.6.3.4.3.6.2.1`.
