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

Bind each row's `incarnation` to a fixed workload `slot`, `generation` and
full manifest `endpoint`, with explicit `known` and `error` fields. Seed the
active table from the owner's existing inventory after subscription and before
churn starts. Match frozen notification/status references, versions, tuple,
type, interface and discriminator. A delayed notification uses its captured
physical identity, not a later mutable observation version.

Only matching `Deleted` followed by `Added` advances the slot generation.
Repeated `Added` for the same active physical identity is idempotent. Missing
seed, gaps, inconsistent or unmatched lifecycle notifications and generation
overflow invalidate identity history; later snapshots cannot repair it.
Clear active references on deletion, invalidation and collector termination.
The fixed manifest and active table are bounded by session count; lifetime
rows retain scalar identity only, without a new core ID or per-packet observer.

`down_journal.incarnations_complete` requires a seeded, uninterrupted identity
history and normal subscription closure. `incarnation_error` records failure
independently of the existing `complete` source-count settlement. Classification
must require both and known identity for each eligible row. Neither flag alone
proves causality or acceptance.

### Conditional controlled-model classification (2026-10-27)

Detection expiry clears the wire remote discriminator before the FSM commit.
Copy its previous scalar value through the existing timer observation into
`ValidatedRemoteDiscriminator`; project it as `validated_remote_discriminator`
with the same version-bound witness. Zero remains unknown. Keep discriminator
clearing and protocol actions in their existing order.

Actual blackhole windows retain bounded, direction-specific wire-identity
segments, every omission offset and decision sequence. TX `valid` means only
codec verification of the would-be packet. RX discard precedes acceptance and
remains unqualified. No packet/Auth slice or retired session reference is retained.
Keep 256 windows per peer, at most 16 identity segments and 128 omission times
combined across both directions per window. Overflow increments omitted
accounting and prevents completion even when cancellation loses the worker error.

Bind both reports to the actual boot ID, private control directory device/inode,
native start and time namespace. A stored offset `x` with conversion bracket `B`
denotes the conditional interval `[x,x+B]`; an owner commit denotes
`[commit_start_ns,commit_end_ns+Bowner]`. Use checked arithmetic. Commit intervals
intersecting measurement or fault boundaries remain unresolved; notification
delivery time does not select the measurement denominator.
The explicit `bracket_known` flag distinguishes a measured zero conversion
bracket from missing input.

`ClassifyDowns` independently checks settled source counts, scalar incarnation
history and complete omission accounting. It rejects inconsistent peer TX
scheduling, write-result or RX disposition totals;
all sums use checked arithmetic. Each direction's omission sequence is bounded
by its actual Scheduled/Read total and increases across segments and windows,
allowing gaps between windows. Invalid accounting leaves Down unresolved.
The conditional `intentional` case
requires timeout cause, prior remote Up, the actual old deadline, a commit wholly
inside the measurement/fault window, and matching valid Up packets. Consecutive
actual TX decisions must cover the old receive-to-deadline model: at least the
negotiated multiplier, first attempt within one negotiated interval after the
validated receive, no longer actual attempt gap, and the last interval covering
the actual due time. The old directional Detection Time must match their wire
inputs and fixed workload RX interval. Reused discriminators, unknown phase or
identity, boundary overlap, missing progress and peer-originated Down remain
unresolved. A lone omission or scheduled window overlap cannot establish intent.
Known additional terminal loss, counter reset or missing commit progress anywhere
in the measurement also prevents positive attribution; their exact fault times
are not established by aggregate snapshots.

The positive verdict assumes unchanged transport would deliver those unsent
packets to the owner before due and no independent hidden fault coincides.
These assumptions are serialized explicitly. `passing` applies only to this
conditional model; any false/unresolved eligible Down prevents it. Total timing
uncertainty remains unknown and `reference_qualified` remains false. The
fixture-only `--tx-only-blackhole` case exercises this predicate independently
of the retained bidirectional stress case; named qualification profiles reject it.

## Consequences

The witness adds bounded value storage to each session and notification.
Capture takes two clock readings and one mutex update per actual Down
transition, without a per-packet callback, heap allocation or wire change.
Allocation and notification-size measurements are required before source
acceptance; this decision makes no RSS or latency improvement claim.

The witness alone is not a classification verdict. The independent consumer
requires actual peer omission evidence, incarnation identity, phase brackets
and conditional causal validation. It does not establish
total kernel timestamp uncertainty or qualify the reference, engineering,
native wire or 24-hour release gates.

## References

- [RFC 5880 Section 6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4).
- [Go time semantics at the pinned toolchain](https://github.com/golang/go/blob/go1.27.3/src/time/time.go).
- Beads `gobfd-qj0.8.2.1.6.3.4.3.6.2`, `gobfd-qj0.8.2.1.6.3.4.3.6.2.1`
  and `gobfd-qj0.8.2.1.6.3.4.3.6.2.2`.
