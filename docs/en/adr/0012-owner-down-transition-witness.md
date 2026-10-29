# ADR-0012: Owner-monotonic Down transition evidence

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-26 |
| Echo proof amendment | 2026-10-29 |
| Selective Control proof amendment | 2026-10-29 |

## Context

Directional Detection Time and the actual owner transition are required to assess an intentional blackhole. Unix-time LastPacketReceived and a notification timestamp after logging/metrics do not bracket that commit. Serialization strips Go monotonic time; a wall-clock conversion cannot manufacture a missing witness.

## Decision

Capture an optional value witness only for an actual Up→Down transition. Under the existing observation mutex, retain last validated owner receive time, old negotiated D and the actual dispatched detection deadline before timer interruption can apply pending RX changes. Preserve interruption order, then take two monotonic timestamps around `commitObservation`. Bind the witness to its returned ObservationVersion.

Publication copies a witness only for that exact version. Missing, stripped, inconsistent or mismatched evidence stays unknown. The bracket covers local observation commit; logging, delivery and actuator delays occur afterward. ValidatedAt means owner processing after validation, not kernel arrival or packet-processing completion.

| Evidence | Retention rule | Boundary |
|:---|:---|:---|
| Down witness | Frozen cause, remote facts, endpoint, discriminator, version | No SessionRef in lifetime array |
| Relative phase | Compute while time.Time retains monotonic reading; serialize offsets/availability | Unknown remains unknown |
| Source journal | Preserve all delivered witnesses through setup/drain | Existing down_events/cutoff keep notification-time meaning |
| Remote discriminator | Copy old scalar before timeout clears it; expose ValidatedRemoteDiscriminator | Zero means unknown; protocol reset order unchanged |

Sources: [Control commit](../../../internal/bfd/session_observation.go), [timer handler](../../../internal/bfd/session_loop.go), [witness projection](../../../test/internal/bfdload/down_witness_linux.go), [RFC 5880 §6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4).

### Incarnation identity

Bind each row to fixed manifest slot, generation and full endpoint, with known/error fields. Seed inventory after subscription before churn. Match captured physical reference, version, tuple, type, interface and discriminator; a delayed notification uses its captured identity.

Only matching Deleted→Added advances generation. Repeated Added for the same active identity is idempotent. Missing seed, gaps, unmatched lifecycle, inconsistent history or overflow invalidates identity irreversibly; a later snapshot cannot repair it. Clear active references on deletion, invalidation and collector stop. Manifest/active table are session-count bounded; lifetime rows retain scalars without a new core ID or per-packet observer.

`incarnations_complete` requires seeded uninterrupted history and normal subscription closure; `incarnation_error` is independent of source-count `complete`. Classification requires both and known identity for every eligible row. Source: [incarnation tracking](../../../test/internal/bfdload/down_incarnation_linux.go).

### Conditional Control classification

Actual direction-specific blackhole windows retain wire-identity segments, omission offsets and decision sequences. TX valid means codec verification of a would-be packet; RX discard precedes acceptance and remains unqualified. Do not retain packets/auth slices/retired sessions. Bounds: 256 windows per peer, 16 segments and 128 combined directional omission times per window. Overflow counts omitted evidence and prevents completion, including cancellation races.

Bind reports to actual boot ID, private control-directory device/inode, native start and time namespace. Offset x with conversion bracket B denotes [x,x+B]; commit denotes [commit_start,commit_end+Bowner]. Use checked arithmetic and explicit bracket_known, including measured zero. Boundary intersections remain unresolved; delivery time never selects the measurement denominator.

Require settled source counts, scalar incarnation history, complete omission accounting and consistent TX schedule/write results/RX dispositions. Each direction's omission sequence increases across segments/windows and stays within actual Scheduled/Read totals; interwindow gaps are allowed. Invalid arithmetic/accounting leaves Down unresolved.

An intentional verdict requires timeout cause, prior remote Up, old actual deadline, commit wholly inside measurement/fault window and matching valid Up packets. Restore only captured would-be Up packets at owner delivery; keep every other recorded disposition, including RX omissions and withheld expired Down packets, unchanged. This selective intervention does not replay the peer generator with a different transmit decision or remove the entire physical fault.

Restored Up packets require codec validity, expected peer and owner discriminators, diagnostic None, clear Poll/Final flags and unchanged workload DesiredMinTX, RequiredMinRX and DetectMult. Old D must agree with these wire inputs and fixed workload RX. Validate every potentially in-range TX body and expected identity before accepting a proof. A withheld Down is supported only with expected peer discriminator, zero YourDiscriminator, ControlTimeExpired diagnostic, Final clear, DesiredMinTX equal to `max(workload_interval,1s)`, Poll set exactly when `workload_interval<1s`, and unchanged RequiredMinRX/DetectMult. It remains withheld; its time interval does not constrain restored-Up coverage, including when it straddles old due. Reject unsupported bodies, states, identities and Up after Down.

For a nonempty selected Up set, each actual offset p must satisfy `p>r+owner_bracket` and `p+peer_bracket<d`, where r is the last validated receive and d the old actual deadline. Require checked `max(p)+D>d+owner_bracket`. Under the explicit assumption that owner acceptance and detection Reset complete before old due, reset time s follows p and `s+D>=max(p)+D>d+owner_bracket`. The replacement deadline is strictly later than every bracket-consistent old due. Count and nominal packet cadence do not replace this proof; no gap tolerance or guessed schedule is introduced. Complete actual decision accounting remains mandatory.

Reused discriminator, unknown phase/identity, boundary overlap, absent progress and peer-originated Down remain unresolved. One omission/window overlap is insufficient. Additional known terminal loss, counter reset or absent measurement commit progress prevents positive attribution; aggregate loss counters cannot supply exact fault times.

Positive attribution assumes selected packets would reach owner acceptance and complete detection Reset before old due, with no independent hidden fault. Serialize the selective intervention and those assumptions. The conclusion concerns that old deadline, not absence of later Down or of Down by a delayed actual commit. Passing applies only to that conditional model; any false/unresolved eligible Down prevents it. Total uncertainty stays unknown and reference_qualified remains false. The fixture-only TX-only blackhole exercises the predicate separately; named profiles reject that selector.

The 2026-10-29 amendment replaces the earlier multiplier-count and nominal-cadence policy. Preserve assessments made under that policy. Reassessment uses a separate output bound to both original producer SHA and new assessor SHA; a changed reader does not establish native acceptance.

Source: [classifier](../../../test/internal/bfdload/down_classification_linux.go), [regressions](../../../test/internal/bfdload/down_classification_linux_test.go).

### Echo owner witness

Reuse TransitionTiming under Echo's observation mutex and version fence for actual Up→Down. ValidatedAt is the last return processed by the preview owner, without an RFC 9747 validation/authentication claim. D is locally provisioned. Copy the actual TimerEchoDetection deadline; direct handler calls without dispatch leave it unavailable. Remote discriminator stays zero and remote state unknown.

Capture adds no callback/wire field/observer and preserves Echo FSM, forwarding, receiver and scheduling policy. Control remote-Up/discriminator predicates cannot qualify Echo. The actual Echo UDP workload needs independent returns and typed incarnation identity.

The 2026-10-28 local consolidation instruction separately authorizes integration removing permanent Control/Echo owner thread pins. The witness does not qualify that change. Native 1000-session Control/Echo A/B, resource/timer criteria and final stable gates remain required. Preserve ADR-0007 PID headroom and pressure-worker thread pin for CLOCK_THREAD_CPUTIME_ID. Bind comparable secured inputs at actual runner admission; documentation-only commits do not require new pairs.

Sources: [Echo owner](../../../internal/bfd/echo.go), [Echo observations](../../../internal/bfd/echo_observation.go), [Echo workload](../../../test/internal/bfdload/owner_echo_linux.go).

### Echo old-deadline classification

For Echo require checked `D=interval*detect_multiplier`, actual old deadline d, last accepted return r, matching incarnation/version and diagnostic EchoFailed. Remote Control state/discriminator remain unknown.

Merge RX/return-TX omissions by actual received_sequences, not independent decision counters. Require unique increasing read indices across windows and nondecreasing input times. Every selected input must match owner wire generation; selected read indices must be consecutive and nonempty. Each input time p satisfies `p>r+owner_bracket` and `p+peer_bracket<d`. The whole commit bracket follows d and stays in the same blackhole window. Retain all journal bounds; sorting bounded references does not reorder reports.

Require checked `max(p)+D>d+owner_bracket` for every positive verdict. A counterfactual owner Reset at time s follows its selected input, so `s+D>=max(p)+D>d+owner_bracket`: the replacement deadline is strictly after every bracket-consistent old due. DetectMultiplier already enters D; the number of omitted inputs is not a substitute for this interval proof. ValidatedAt precedes Reset, so do not assume `d=r+D`.

The explicit assumption is that selected withheld inputs would return, be accepted and reset detection before that old deadline. Arrival at a socket alone does not establish this assumption. The result concerns that deadline, not absence of every later counterfactual timeout. Both modes use actual inputs rather than a guessed continuous TX schedule; their packet validation and omission identities remain distinct.

Missing/replayed read indices, boundary intersection, absent strict extension, arithmetic overflow, mismatched wire inputs or independent observed losses remain unresolved and make Echo assessment incomplete. Validate every selected input before accepting the proof. Peer kernel drops/invalid drop samples block qualification; unphased lifetime counters cannot be assigned to an unrelated window. The software returner qualifies neither forwarding plane nor RFC 9747 authentication.

Sources: [Echo omission model](../../../test/internal/bfdload/echo_omission_linux.go), [Echo classification regressions](../../../test/internal/bfdload/echo_classification_linux_test.go), [RFC 9747 §2](https://www.rfc-editor.org/rfc/rfc9747.html#section-2).

## Consequences

Witness storage is bounded per session/notification. Capture requires two clock reads and a mutex update per actual Down, without a per-packet callback or wire change. Allocation and notification-size measurements are required for source acceptance; no RSS/latency improvement follows from the decision.

Witness capture alone is not a causal verdict. Independent peer omissions, incarnation identity, phase brackets and conditional validation remain mandatory. Native environment/clock discipline, total kernel uncertainty, reference/engineering workloads, final wire and 24-hour release qualification remain separate.

## References

- [Workload phase/accounting contract](0007-workload-phase-accounting.md).
- [Witness tests](../../../test/internal/bfdload/down_witness_linux_test.go).
- [Incarnation tests](../../../test/internal/bfdload/down_incarnation_linux_test.go).
- [Echo assessment tests](../../../test/internal/bfdload/assessment_echo_linux_test.go).
