# Existing-session configuration changes: in-place, re-key and replace

Status: design for Beads `gobfd-qj0.8.2.2.7` (audit finding H-16). The re-key
part is implemented. The same-key replace part stops at design because it
changes the operator-visible contract: an applied reload would flap an Up
session. It needs a maintainer decision.

## Field classification

The canonical `SessionKey` (type, address family, peer, local, interface,
network scope, transport scope) selects the wire session. `effectiveSessionConfig`
holds every other effective field. A change to a key field is a re-key: a
different wire session. A change to an effective field of the same key is
same-key drift.

| Field | Class | Applied today | Source |
|---|---|---|---|
| Desired Min TX, Required Min RX | same-key | In place through the Poll/Final transaction, config-owned base sessions only | RFC 5880 6.8.3; `canUpdateTimers` |
| Detect Mult | same-key | Conflict | RFC 5880 6.8.1, 6.8.3: no Poll; "timing parameter changes MUST be effected immediately" |
| Padded PDU size | same-key | Conflict | RFC 9764: no protocol change; affects packet length only |
| Multihop max hops (min TTL) | same-key | Conflict | Local receive policy (RFC 5082 Appendix A reading) |
| Authentication type and keys | same-key | Conflict | RFC 5880 6.7.1: both ends must switch within a Detection Time |
| Role | same-key | Not configurable for declarative base sessions | RFC 5880 6.1 |
| Type, local address, interface, transport scope | key | Re-key: claim the new key, release the old | `SessionKey` |

## Re-key (implemented)

Claims precede releases for the base, VXLAN and Geneve sources, as they
already did for Micro-BFD groups. A failed claim rolls back the claims of the
pass and releases nothing, so the previous session stays and the receipt
reports `failed`. This replaces the earlier rule, pinned by
`TestManagerDetailedReconcilePreservesStaleReleaseAndRollsBackCreations`, that
released stale claims even when a creation failed: a local-address or
interface re-key cannot be paired with its old session, so only source-level
atomicity keeps the old session. One permanently failing entry therefore
blocks the removals of its source until it is fixed. A pending claim does not
block releases of unrelated entries removed from the desired set.

The packet demultiplexing key (peer, local, interface, transport scope) has no
session type. A single-hop session without an interface and a multihop session
with the same addresses therefore share it, and both cannot be registered. When
the demultiplexing key of a new config-owned base claim belongs to a base
session that the config owner alone holds and no longer desires, the pass
replaces that session. Other sources keep their own rules; VXLAN and Geneve
demultiplexing keys carry their full transport scope. The replacement runs in
this order:

1. Claims that do not collide run first.
2. If none failed, each replacement resolves its interface index, acquires its
   sender lease and builds its session outside the registries.
3. If no preparation failed, one Manager lock section removes the old entry
   of every prepared replacement and registers its new session. No packet
   lookup sees neither session.
4. A replacement whose sender waits for an address or interface is `pending`;
   its old session stays until a retry swaps it.
5. Any failure discards the prepared sessions and rolls back the pass.

The old session leaves through the release path: AdminDown drain for at least a
Detection Time (RFC 5880 6.8.16), then teardown. RFC 5882 sections 3.2 and 4.2
say clients SHOULD NOT take control-protocol action when the remote session
goes to AdminDown, while a silent teardown ends in a detection timeout that
signals a path failure. A demultiplexing key held by another owner is not a
replacement; the claim fails with `ErrDuplicateSession`.

A re-key always produced a new discriminator and a new handshake; the peer sees
the same AdminDown and Down sequence as before. The change is that a failed
new session no longer removes the old one.

## Same-key replace (decision required)

A same-key replace creates a new session with a new local discriminator. The
new session starts Down; the peer follows it through Down, Init and Up. An Up
session therefore flaps on reload where it is rejected as a conflict today.
Authentication changes flap even in place unless both ends switch within a
Detection Time (RFC 5880 6.7.1).

| Option | Behavior | Cost |
|---|---|---|
| A. Keep the conflict (current) | Stale generation, `failed` receipt, session untouched; operator removes then adds the entry, or restarts | No flap without an explicit operator step |
| B. Opt-in replace | Per-entry or per-reload flag allows an AdminDown-drained replace with the swap above | Explicit flap; new config/API field; same-key swap needs a key-identical registry exchange |
| C. In-place non-timer fields | Detect Mult, padded PDU size and min TTL change on the running session | No flap; these fields become mutable session state read by the receive and transmit paths |
| D. RFC 5880 6.7.1 authentication rollover | Configured auth is not "in use" until a matching packet arrives; at most one change without intervention | No flap when the peer follows; security-sensitive sequence-number handling |

Recommendation: C for Detect Mult, padded PDU size and min TTL, because RFC
5880 6.8.3 requires immediate effect and no Poll and none of them changes the
discriminator; A for authentication until D is designed as its own task. B only
if operators ask for a one-step disruptive change.
