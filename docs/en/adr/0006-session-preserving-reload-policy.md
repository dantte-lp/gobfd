# ADR-0006: Session-preserving reload policy

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-24 |
| Qualification | Accepted policy; implementation and peer qualification remain separate |

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
| Wire identity or other unsupported transition | No implicit replacement; require separate explicit operator intent for disruptive replacement |

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
`gobfd-qj0.8.2.2.7.2` remains subject to final gates;
`gobfd-qj0.8.2.2.7.4` (reconciliation/rekey integration) remains P0.
`gobfd-qj0.8.2.2.7.5` owns the P1 actual-wire final gate for all reload fields,
including RFC 9764 peer/MTU qualification. Beads owns executable plans, priorities, dependencies and
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
