# ADR-0006: Session-preserving reload policy

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-24 |
| Qualification | Accepted policy; implqual-local; actual-wire qualification remains open |

## Context

A configuration reload must not replace an established BFD session merely
because a supported effective parameter changes. Replacement changes the
discriminator and protocol state and can affect routing clients. Preserving a
local session instance alone does not establish uninterrupted peer state or
traffic forwarding.

RFC 5880 separates timer negotiation, Detect Multiplier changes,
authentication and administrative control. RFC 5882 further makes client
handling of AdminDown and graceful restart depend on independent liveness
detection and control-plane fate sharing. These mechanisms do not provide a
general hitless-reload guarantee.

## Decision

Preserve an existing base session when its canonical wire identity is stable,
its sole owner is declarative configuration, and the complete proposed
parameter tuple is supported. Preserve its instance, discriminator, sender
lease, authentication replay state, counters and ownership. Shared sessions
must not be mutated for one owner; an incompatible proposal is rejected.

| Change | Required behavior |
|---|---|
| Desired TX / required RX intervals | Retain the existing Poll/Final transaction rules of RFC 5880 §6.8.3 |
| Detect Multiplier | Advertise the new nonzero value in the next Control packet without Poll in Async; apply §6.6 Poll requirements when remote Demand is active |
| Multihop minimum TTL | Apply the local 255 − N receive floor under Session.mu without replacement or Poll, including under remote Demand; this is GoBFD policy, not an RFC 5883 receive-TTL requirement |
| Padded PDU target | Permit only positive-to-positive changes in Async on an already DF-prepared sender, with unchanged authentication and a sole config owner; reject unsupported base candidates before generation acceptance |
| Unsupported authentication transition | Reject the complete candidate before accepting a new generation; retain the old configuration |
| Unqualified wire-identity change or other unsupported transition | No implicit replacement; require separate explicit operator intent for disruptive replacement |

Multihop receive-floor qualification uses an authenticated UDP/4784 startup
profile of the existing crafted-peer topology. The peer pauses before reload;
an admissible-TTL replay of its last accepted signed payload must be rejected
before the fresh boundary probe. That deliberate replay accounts for exactly
one auth failure and zero accepted inputs; reload itself adds neither auth
failures nor state transitions. An initial identity/counter baseline spans
all reloads and remote-Demand entry. For each boundary, a signed
Poll below `255 - N` is followed by the identical payload and sequence at
the floor. Only the latter may count as accepted input or elicit Final;
rejected input must not advance receive authentication state. One persistent
capture/authentication checkpoint spans reloads and remote-Demand entry.
Probes complete within the existing Detection Time so replay-state expiry
cannot substitute for continuity. Source tests alone do not close the actual
kernel/capture gate `gobfd-qj0.8.2.2.7.5.4.3`. This profile does not qualify
routed/cloud paths or general Demand operation.

Local TTL wire qualification passed on
`task/ttl-wire-20261024@491b2214994f07e017eb93ab67a29a95378c619e`:
the default crafted profile takes 162.94 s and authenticated multihop 81.98 s.
Seven boundary/replay windows retain identity, source lease and generation
semantics. Evidence: `reports/ttl-wire-20261024/`. This qualifies the TTL slice;
unified five-field final-candidate gate `gobfd-qj0.8.2.2.7.5.5` remains mandatory.

TX increases and RX decreases retain their protected old local effect until
the relevant Final; the reverse directions retain their existing immediate
effect. An active Poll keeps its advertised tuple, and a later proposal is
queued according to the existing transaction contract. Adding Detect Multiplier,
TTL or padding must not weaken those timer rules. The remote-Demand exception
does not qualify general Demand-mode operation.

The v1 packet-policy slice extends the tuple, references and confirmed/effective
receipts to five fields: TX, RX, DetectMult, MinTTL and PaddedPduSize.
The coordinator passes only `candidate.base` to `Manager.AdmitSessionReload`,
which checks its stable-identity field and ownership policy before local
generation acceptance, preserving the retained
and live authentication checks below. Padding requires immutable constructor
proof `PaddingPrepared`, not a requested option or a socket-liveness claim.
Zero-to-positive and positive-to-zero transitions, unknown/unprepared senders,
remote Demand and shared-owner base padding updates fail closed. Micro-BFD and
overlay remain outside this qualified profile; their configuration gains no
padding/minTTL fields. Other-source effective changes retain their existing
per-source conflict/stale behavior; no new five-source preflight is implied.
Policy is checked before equality shortcuts, including a return to a confirmed
target while Demand is active. An owner recheck rejects the whole queued tuple
before any field commit if Demand activates while it waits.

Capacity growth prepares one replaceable waiting buffer at control admission;
the owner swaps it before committing fields. No growth allocation is deferred
to owner promotion or packet transmission. The target counts transport payload
bytes excluding IP/UDP headers; send length is the larger of the declared PDU
and target, so authentication is never truncated. Padding is zero and lies
outside the existing authentication MAC domain. Existing authenticated minimum
26 and zero-target DF defaults remain unchanged. Padding enable/disable and
dynamic DF preparation are separate after-v1 P2 work in `gobfd-ebu`.

Validate the complete candidate, including same-session transitions and
ownership, before accepting its desired generation. Apply a same-session
update through its protocol owner as one coherent transaction: no field may
be committed and then left behind when another field fails validation or
preparation. Deliberate staged desired, advertised and effective values are
recorded as pending rather than reported as a completed update.

Reapplying the same generation/revision is idempotent: it creates no new
session, replacement, Poll or duplicate side effect. Stale completion must
not overwrite a newer desired revision. Receipts distinguish desired,
advertised and effective values where a field has a wire representation;
minimum TTL remains a local admission value. Receipt state distinguishes
pending, committed and failed outcomes. Admission or advertisement alone
does not establish effective completion or readiness. This is a same-session
transaction policy, not a claim that all reload sources commit atomically.

Authentication algorithm changes, authentication enable/disable and
unqualified key changes are rejected before the new generation is accepted,
even when combined with otherwise supported changes. They must not silently
fall back to delete/recreate or reset sequence/replay protection. RFC 5880
§6.7 leaves coordination outside BFD; the existence of a key store does not
qualify a rotation protocol.

Admission compares immutable authentication fingerprints against both the
retained accepted base desired set, including entries absent from runtime,
and the live Manager registry without filtering API or shared owners. The
comparison includes enablement, algorithm, key IDs/material, selected TX key
and the receive-key set. Startup authentication, new or removed identities and
fresh stores with identical semantics remain admissible.

The lock order is Manager lifecycle, ownership, then registry. Release the
registry lock before short local acceptance; retain ownership serialization
through desired-generation publication, then release it before configured-peer
callbacks or listener/sender resource application. Rejection precedes generation,
log-level and readiness changes and preserves the old accepted configuration
and sessions. Claims acquired after acceptance still pass normal fail-closed
Manager reconciliation. This admission boundary is not a whole-source atomic
transaction or a seamless-rollback guarantee.

Optional coordinated key-ID rotation within one authentication algorithm is
separate after-v1 work in `gobfd-khr` at P2. It does not graduate until receive
overlap, transmit cutover, old-key retirement, replay/sequence continuity and
peer interoperability are qualified. This ADR does not enable that feature.

Disruptive replacement must identify the intended identity/transition and its
client impact separately from ordinary reload. Preparations before a
peer-visible cutover must roll back on failure. Once changed state or packets
are visible to the peer, a seamless rollback is not guaranteed. Administrative
signaling follows RFC 5880 §6.8.16 and the applicable RFC 5882 client policy;
AdminDown is not a universal promise that BGP or forwarding remains unchanged.

The source claims/prepares desired resources before stale-claim release. Only
zero `Failed` and `ResourcePending` permit old release; timer negotiation is
tracked separately. Fatal preparation rolls back newly created claims, while
valid disjoint claims may remain on resource-pending retry. An occupied receive
key permits a prepared SingleHop/MultiHop swap only for interface-empty base
sessions with one config owner, no old key in desired and all other identity
fields unchanged. Other collisions fail before the sender factory and, for
base reload, before desired-generation acceptance. Cancellation before swap
publication retains the old identity; prepared lease/discriminator cleanup is
exactly once. After publication the old captured session commits AdminDown and
drains outside Manager/lifecycle mutation locks, retaining its lease and
discriminator until completion or typed incomplete failure before cleanup.

Overlay scope preflight is pure and covers one complete VXLAN or Geneve source
before any backend open or `SetScopes`. Unequal scopes sharing a wire identity
with a live/draining lease, including a VXLAN local-MAC change, require explicit
separate removal/recreation. Duplicate desired wire identities remain invalid;
a never-committed unused preparation may be superseded by a new same-wire scope.
After successful preflight, obsolete unused preparations are pruned before
further claims. The retained set is existing scopes intersected with current
desired, union scopes with positive physical `SenderLease` refs. Each actual
sender factory increments a ref. A registered physical session retains its ref
through retirement drain; idempotent lease close then releases it. An unused or
unpublished lease uses ordinary once-only cleanup without drain, including failed
construction or cancellation. Active/draining exact scopes survive failed generations.
An incomplete acquire returns Pending/Failed before that source's Manager apply
or release, retaining its live/draining scopes/backends; currently desired
preparations may remain for retry. This is neither global
pre-generation overlay admission nor all-source atomicity.

## Consequences

Supported changes preserve local session identity and avoid an unnecessary
restart. Peer behavior, timing, padding acceptance and forwarding continuity
still require measured qualification. RFC 9764 §4.1 explicitly permits the
practical outcome that an existing Up session goes Down when its peer cannot
receive padded Control packets.

Cloud and hardware requirements remain separate target profiles. Timer
limits, Async/Echo support and graceful-restart policy must be checked for the
specific provider and connection; common intervals in RFC 7419 are not cloud
acceptance evidence. The earlier source comparison is recorded at
`reports/reload-policy-20261024/sources.txt`. Its historical discussion status
does not establish this decision's approval or implementation qualification.
No provider-specific runtime interoperability is asserted by this ADR.

Implementation and qualification remain in Beads parent `gobfd-qj0.8.2.2.7`.
Detect Multiplier `gobfd-qj0.8.2.2.7.1` and authentication admission
`gobfd-qj0.8.2.2.7.3` are closed on dev. Packet-policy child
`gobfd-qj0.8.2.2.7.2` is closed on dev. Reconciliation/rekey integration
`gobfd-qj0.8.2.2.7.4`, `gobfd-qj0.8.2.2.7.4.1` and
`gobfd-qj0.8.2.2.7.4.2` have completed lifecycle implementation and bounded
local Go qualification; integration and closure receipts are maintained in Beads.
Evidence is in `reports/rekey-lifecycle-20261024/`.
Critical01 records 1049 top-level PASS, zero FAIL, five packages and two existing
environment skips; quality04 has zero issues, vet01 and both benchmark runs
exit 0. All 27 selected allocation budgets match baseline; 577 source-inventory
checks match the frozen critical input. This is not all-wire, scale or stable acceptance.
Overlay lease-ref/preparation cleanup `gobfd-qj0.8.2.2.7.4.2.1` is included in that qualification.
`gobfd-qj0.8.2.2.7.5` owns the mandatory P0 actual-wire final gate, explicitly
depending on `gobfd-qj0.8.2.2.7.4`. The existing wire gate must qualify all five
reload fields, including RFC 9764 peer/MTU behavior, before v1. Source unit tests
do not establish interoperability; no new harness is introduced by this policy.
Wire qualification selects receipt expectations from the scenario, independently
of capture validation. Immediate updates may converge directly or after owner
queueing; existing Poll phases retain pending-before-applied evidence. That
ordering is the current owner/coordinator contract, not an RFC JSON requirement.
Neither mode alone proves the presence or absence of wire Poll/Final. Shared
qualifier preparation is tracked in `gobfd-qj0.8.2.2.7.5.1`.

Detect Multiplier wire qualification (`gobfd-qj0.8.2.2.7.5.2`) extends the
existing authenticated Async phases with 1→5→3 and remote-Demand phases with
5→3. At a 300 ms negotiated interval, multiplier 1 has 75–90% jitter and
expected mean 247.5 ms; the other tested multipliers have 75–100% jitter and
expected mean 262.5 ms (RFC 5880 §6.8.7). Changing the local multiplier leaves
GoBFD's receive Detection Time based on the received peer multiplier.
The peer's Async Detection Time is derived, not measured by these phases.
For the crafted remote-Demand peer, 5×500 ms derives a local Poll bound;
that peer has no detection timer. Responsive Finals and a quiet window do not
qualify missing-Final expiry or graduate general Demand support. Capture checks
retain discriminator, UDP source port/length, authentication type/key/sequence,
timer tuple and state continuity independently of generation receipts. Actual
candidate captures remain mandatory before closing the wire task.
Async capture permits one old in-flight Control only as the first local
post-receipt packet within the existing timer slack; the first new Control must
arrive within one negotiated TX plus slack. Later old multipliers fail the gate.
Runtime field discovery uses scoped registry queries and exact first-column
matching; it does not buffer the full dissector registry in the race runner.

Local DetectMult wire qualification passed on
`task/detect-wire-20261024@d9e67064e51012c0d25a1a22dc9bd469600d8469`:
six authenticated Async phases and two crafted remote-Demand phases, with
captures and receipts in `reports/detect-wire-20261024/`. Children `.5.2` and
`.5.2.1` are closed locally. Prepared padding, actual TTL admission and the
unified exact-candidate gate remain P0; no stable acceptance is implied.

Prepared-padding qualification (`gobfd-qj0.8.2.2.7.5.3`) retains one capture
cursor and authentication receive state per direction across the whole resize
and rejection sequence. Retry evaluation is transactional: failed or incomplete
evidence cannot advance either state. The independent peer keeps its own PDU
size; a local 128→192→128 target is not a bidirectional MTU claim. Verify DF,
declared BFD Length, UDP payload, zero padding and the existing MAC boundary
separately. Meticulous SHA1 is a startup-only profile of the same FourPeer
lifecycle; default keyed SHA1 remains a separate qualification. Runtime auth
rotation remains outside v1. Probe aggregate and named BFD readiness through
the existing management Unix socket inside the owned daemon container, using
the same compiled interop executable. Copy it through the existing container
file mechanism; runner paths must not become host-engine bind paths.
The TCP listener remains loopback-only.
Source contracts alone do not close this actual-wire task.

Local prepared-padding qualification passed on
`task/padding-wire-20261024@b97729458a696aee21023ba147af51417ed9ff7b`:
default auth4 and meticulous auth5 FourPeer profiles plus crafted remote-Demand
rejection, with captures and receipts in `reports/padding-wire-20261024/`.
Eight resize phases and seven rejected candidates cover seventeen peer windows;
actual aggregate and named BFD health remain SERVING. The 512 MiB runner cap
is unchanged; all three runs have zero max/OOM events and swap. Source and
artifact integrity checks match 853 files and 675 sidecars. This closes the
local padding slice, not actual TTL, unified final-candidate, forwarding,
general Demand or stable-release qualification.

Beads owns executable plans, priorities, dependencies and
acceptance evidence. Acceptance of this policy is not implementation,
interop or stable-release acceptance.

## References

- [RFC 5880 §6.6: Demand mode](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.6)
- [RFC 5880 §6.7: Authentication](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.7)
- [RFC 5880 §6.8.3: Timer manipulation](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.3)
- [RFC 5880 §6.8.12: Detect Multiplier change](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.12)
- [RFC 5880 §6.8.16: Administrative control](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.16)
- [RFC 5882 §3.2: AdminDown state](https://www.rfc-editor.org/rfc/rfc5882.html#section-3.2)
- [RFC 5882 §4.3: Graceful restart interactions](https://www.rfc-editor.org/rfc/rfc5882.html#section-4.3)
- [RFC 7419: Common interval support](https://www.rfc-editor.org/rfc/rfc7419.html)
- [RFC 9764 §§3–4.1: Large packets and peer compatibility](https://www.rfc-editor.org/rfc/rfc9764.html#section-3)
- [AWS Direct Connect BGP/BFD settings](https://docs.aws.amazon.com/directconnect/latest/UserGuide/bgp-specific-settings.html)
- [Google Cloud Router BFD profile](https://docs.cloud.google.com/network-connectivity/docs/router/concepts/bfd)
- [Azure ExpressRoute BFD profile](https://learn.microsoft.com/en-us/azure/expressroute/expressroute-bfd)
- [Existing timer transaction contract](../../superpowers/specs/2026-09-08-gobfd-timer-update-transactions-design.md)
