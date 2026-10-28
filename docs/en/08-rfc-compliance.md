# RFC Compliance

[![RFC 5880](https://img.shields.io/badge/RFC_5880-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5880)
[![RFC 5881](https://img.shields.io/badge/RFC_5881-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5881)
[![RFC 5882](https://img.shields.io/badge/RFC_5882-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5882)
[![RFC 5883](https://img.shields.io/badge/RFC_5883-Constrained-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5883)
[![RFC 7419](https://img.shields.io/badge/RFC_7419-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7419)
[![RFC 9384](https://img.shields.io/badge/RFC_9384-Not_Implemented-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9384)
[![RFC 9468](https://img.shields.io/badge/RFC_9468-Unsafe_Preview-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9468)
[![RFC 9747](https://img.shields.io/badge/RFC_9747-Preview-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9747)
[![RFC 7130](https://img.shields.io/badge/RFC_7130-Preview-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7130)
[![RFC 8971](https://img.shields.io/badge/RFC_8971-Incomplete_Preview-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc8971)
[![RFC 9521](https://img.shields.io/badge/RFC_9521-Incomplete_Preview-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9521)
[![RFC 9764](https://img.shields.io/badge/RFC_9764-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9764)
[![RFC 7880](https://img.shields.io/badge/RFC_7880-Planned-2196f3?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7880)
[![RFC 7881](https://img.shields.io/badge/RFC_7881-Planned-2196f3?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7881)
[![RFC 5884](https://img.shields.io/badge/RFC_5884-Not_implemented-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5884)

> RFC compliance matrix, per-section implementation notes, design rationale, and links to RFC source texts.

---

## Table of Contents

- [Compliance Matrix](#compliance-matrix)
- [RFC 5880 Implementation Notes](#rfc-5880-implementation-notes)
- [RFC 5881 Implementation Notes](#rfc-5881-implementation-notes)
- [RFC 5882 Implementation Notes](#rfc-5882-implementation-notes)
- [RFC 7419 Implementation Notes](#rfc-7419-implementation-notes)
- [RFC 5883 Implementation Notes](#rfc-5883-implementation-notes)
- [RFC 9384 Implementation Notes](#rfc-9384-implementation-notes)
- [RFC 9468 Implementation Notes](#rfc-9468-implementation-notes)
- [RFC 9747 Implementation Notes](#rfc-9747-implementation-notes)
- [RFC 7130 Implementation Notes](#rfc-7130-implementation-notes)
- [RFC 8971 Implementation Notes](#rfc-8971-implementation-notes)
- [RFC 9521 Implementation Notes](#rfc-9521-implementation-notes)
- [RFC 9764 Implementation Notes](#rfc-9764-implementation-notes)
- [RFC 7880/7881 (Planned)](#rfc-78807881-planned)
- [Unimplemented MPLS and VCCV](#unimplemented-mpls-and-vccv)
- [Reference RFCs](#reference-rfcs)
- [RFC Source Files](#rfc-source-files)

### Compliance Matrix

| RFC | Title | Status | Notes |
|---|---|---|---|
| [RFC 5880](https://datatracker.ietf.org/doc/html/rfc5880) | BFD Base Protocol | **Asynchronous core; partial** | Local timer Poll/Final and remote Demand handling implemented; scoped wire receipts retained; final unified qualification open; local Demand and affiliated Echo deferred |
| [RFC 5881](https://datatracker.ietf.org/doc/html/rfc5881) | BFD for IPv4/IPv6 Single-Hop | **Partial; numbered-multiaccess profile** | UDP 3784, TTL=255, per-session transport-class and receive-interface checks, single-hop TX device binding; point-to-point initial demux remains deferred |
| [RFC 5882](https://datatracker.ietf.org/doc/html/rfc5882) | Generic Application of BFD | **Application integration partial** | State delivery and actuator convergence remain incomplete; penalty dampening is implementation policy |
| [RFC 5883](https://datatracker.ietf.org/doc/html/rfc5883) | BFD for Multihop Paths | **Constrained GTSM profile** | UDP 4784; per-session hop policy `multihop_max_hops` (default 1: TTL>=254) is GoBFD policy, not an RFC 5883 rule; real 2/5-hop path qualification is pending |
| [RFC 7419](https://datatracker.ietf.org/doc/html/rfc7419) | Common Interval Support | **Implemented** | 6 common intervals, optional alignment |
| [RFC 9384](https://datatracker.ietf.org/doc/html/rfc9384) | BGP Cease NOTIFICATION for BFD | **Not implemented** | GoBGP v4 (like v3) emits Cease/2; the Cease/10 text is only an operator annotation |
| [RFC 9468](https://datatracker.ietf.org/doc/html/rfc9468) | Unsolicited BFD | **Unsafe preview** | Connected-subnet, session-limit and rate admission enforced; replies from the receiving local tuple; optional Meticulous Keyed SHA1 admission |
| [RFC 9747](https://datatracker.ietf.org/doc/html/rfc9747) | Unaffiliated BFD Echo | **Preview** | Echo session and port 3785 wiring exist; full RFC qualification is pending |
| [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130) | Micro-BFD for LAG | **Preview; owner integration partial** | Protocol and selected actuators exist; production ownership remains constrained |
| [RFC 8971](https://datatracker.ietf.org/doc/html/rfc8971) | BFD for VXLAN Tunnels | **Incomplete preview** | Exact supported-profile identity is bound; owner-specific dataplane integration remains incomplete |
| [RFC 9521](https://datatracker.ietf.org/doc/html/rfc9521) | BFD for Geneve Tunnels | **Incomplete preview** | Explicit IPv4 Format A VAP identity is bound; unsupported formats and missing identity fail closed |
| [RFC 9764](https://datatracker.ietf.org/doc/html/rfc9764) | BFD Large Packets | **Partial** | PDU-length authentication covered locally; padded TX qualified with FRR/BIRD |
| [RFC 7880](https://datatracker.ietf.org/doc/html/rfc7880) | Seamless BFD Base | **Planned** | Stateless reflector + initiator for infrastructure liveness |
| [RFC 7881](https://datatracker.ietf.org/doc/html/rfc7881) | S-BFD for IPv4/IPv6 | **Planned** | Port 7784 encapsulations for S-BFD |
| [RFC 5884](https://datatracker.ietf.org/doc/html/rfc5884) | BFD for MPLS LSPs | **Not implemented** | No MPLS session implementation or stub API |
| [RFC 5885](https://datatracker.ietf.org/doc/html/rfc5885) | BFD for PW VCCV | **Not implemented** | No VCCV session implementation or stub API |

`gobfdctl capabilities` and `CapabilityService.ListCapabilities` report the five
preview profiles separately from this normative matrix. Maturity, validated
enablement and local receive-path readiness are distinct. `ready` requires an
owned profile receive path plus source/runtime/binding readiness; it proves
neither peer health, external forwarding ownership nor RFC qualification.
Missing runtime observations remain `unknown`. Unsafe/incomplete profiles above
remain unsafe/incomplete even when enabled and locally ready.

> Traditional Echo Mode (RFC 5880 Section 6.4, affiliated with a control
> session) is not implemented. Remote Demand suppresses periodic TX when both
> ends are Up and no local Poll is active; local Demand and complete RFC 5880
> Section 6.6 procedures remain unimplemented. Unaffiliated echo (RFC 9747) is
> a separate preview implementation.

### RFC 5880 Implementation Notes

The selected Held errata 5205/7240 policy updates a Down timeout to
`Down/ControlTimeExpired` without a state edge and preserves the diagnostic
on recovery to Up. Owner publication captures the final causal tuple before
observers run. The selected graceful administrative policy guards an existing
AdminDown before local PathDown, preserving diagnostic 7. RFC 5880 Section
6.8.16 also permits AdminDown with PathDown; diagnostic 7 is this daemon's
policy, not a universal RFC requirement.

Under the selected [Held Erratum 5205](https://www.rfc-editor.org/errata/rfc5880)
policy, an initially Down session can publish diagnostic 1 after a silent
Detection Time even if it never received a packet. This changes the causal
diagnostic, not the state-transition count. The erratum remains Held for
Document Update; adopting it is an explicit project choice, not a claim that
the original RFC text requires this behavior. See `gobfd-9ro`.

#### Section 4.1: BFD Control Packet Format

**Implementation**: [`internal/bfd/packet.go`](../../internal/bfd/packet.go)

The mandatory 24-byte header uses `encoding/binary.BigEndian` on caller-owned
storage. Valid parse paths and selected marshal paths allocate zero.
Authentication metadata and the bounded payload are held by value; logical
payload length is checked independently of wire length. Hash verification
leaves received wire unchanged and receivers retain the complete authenticated
datagram. The internal ownership is recorded in [ADR-0011](adr/0011-bfd-auth-inline-payload.md).
See [codec lifetime](02-protocol.md) before reusing a receive buffer.

See [02-protocol.md](./02-protocol.md) for the complete packet format table.

#### Section 6.1: State Variables

**Implementation**: [`internal/bfd/session.go`](../../internal/bfd/session.go)

Local state and diagnostic use one packed atomic pair. `Observation` returns
coherent committed local/remote facts under `Session.mu`; remote getters do
not read independent atomics. Local Demand remains unimplemented.

See [02-protocol.md](./02-protocol.md#state-variables) for the full variable mapping table.

#### Section 6.2: Overview (FSM)

**Implementation**: [`internal/bfd/fsm.go`](../../internal/bfd/fsm.go)

Pure indexed FSM lookup uses an array built from the declarative
`map[stateEvent]transition` at initialization. Session executes the actions.
The Down timeout diagnostic follows the explicit Held-erratum policy above.

#### Section 6.3: Demultiplexing

**Implementation**: [`internal/bfd/manager_session.go`](../../internal/bfd/manager_session.go)

Two-tier demultiplexing:
- Tier 1: O(1) lookup by Your Discriminator (fast path)
- Tier 2: Composite key (SrcIP, DstIP, Interface, transport scope) for session establishment or re-establishment

#### Section 6.5: Poll Sequences

Incoming packets with Poll set schedule an immediate Final reply. The pending
reply survives signing, encoding, and send failures until a successful send;
retry attempts bypass periodic transmission suppression. Final clears the Poll
bit in that packet without terminating the local Poll sequence. Entering Up
starts a local Poll when the advertised Desired Min TX falls from the one-second
slow floor to the configured value. It uses existing scheduled/FSM-driven sends;
config-owned timer updates serialize one active and one latest waiting proposal.
Final confirms only a successfully transmitted Poll; protected TX increases and
RX decreases take effect on confirmation. Consecutive transactions use measured
exchange-time separation. Leaving Up fails update receipts and serializes the
mandatory slow-floor Poll with recovery. Outside Up, including AdminDown, a
promoted update takes effect immediately once no Poll or separation is pending,
and its Poll confirms the receipt (Section 6.8.3 defers changes only "when
bfd.SessionState is Up"). This releases a local zero Required Min RX hold-down
while a compliant peer stays silent. A receipt is Applied only when the
effective tuple equals the confirmed one. If that Poll gets no Final before the
deadline, the receipt fails, the tuple stays effective and later generations
queue behind the Poll. Entering Up through a Final keeps the one-second TX
floor until a recovery Poll carries a faster Desired Min TX. Full Section 6.5
qualification remains pending. Scoped FRR/BIRD reload receipts are recorded
in [Interop](05-interop.md); final unified candidate and deployment RTT
qualification remain separate.

Peer receive-interval changes reschedule TX from the last successful send;
unchanged incoming packets do not postpone it. A zero peer receive interval
stops periodic TX, as does remote Demand while both ends are Up unless a
local Poll is active. Immediate Final replies and bounded retries bypass
these limits. Initial base sessions accept a local zero receive interval through
YAML and generic `AddSession`, preserving omitted-field defaults. Preview
per-peer override rules are unchanged. Timer reloads use the same explicit-zero
semantics; interop scope for these procedures is stated below.

A transition to Down on Detection Time expiry or on a received Down or
AdminDown sends the new state at once (Section 6.8.7: changed contents "SHOULD
be transmitted during the interval between periodic Control packet
transmissions"). The same zero remote RX, Passive unknown-peer and unsolicited
limits apply, a pending Final carries the change instead, and the next
periodic packet follows the Section 6.8.3 slow-rate floor.

One Detection Time without valid packets restores `bfd.RemoteMinRxInterval`
to 1 microsecond and `bfd.RemoteDemandMode` to 0 in every state (Sections
6.8.1 and 6.8.18): a peer's Required Min RX binds only while the peer keeps
transmitting. When the reset changes the TX interval or periodic permission,
the next packet is scheduled from the previous successful send, which also
releases a large nonzero hold-down value. In AdminDown an Active session then
sends the Section 6.8.16 notification and the drain can complete within its
budget (10 s for a final-claim drain, otherwise the caller's context); a Passive
session stays silent because `bfd.RemoteDiscr` is zero (Section 6.8.7). Remote
Desired Min TX and Detect Mult changes update the Detection Time on the packet
that carries them (Sections 6.8.3 and 6.8.4). Virtual-time tests also cover a
timer Poll under remote Demand, Poll expiry under zero remote RX, the Final
reply to a remote RX increase and the Demand-clear resume from the previous
send.

FRR 10.7.1 cannot advertise zero RX (`receive-interval` minimum 10) and has no
`demand-mode`. BIRD 3.4.0 has no Demand option; its zero RX support is
unverified. Remote Demand interop with both pinned peers and remote zero-RX
interop with FRR are source-supported inapplicable (maintainer decision
2026-10-21, `gobfd-qj0.8.2.1.1.2`). `TestCraftedPeerTestcontainers` proves the
remote behaviour on the wire with a repository-owned Go peer
([Interop Testing](05-interop.md#crafted-go-peer), `gobfd-qj0.8.2.1.1.2.3`):
under remote zero RX GoBFD sends only Finals, one per peer Poll (Sections 6.5
and 6.8.7); after one silent Detection Time it goes Down with diag 1, resets
the remote receive interval and sends Down with Your Discriminator 0 at once,
then continues at the slow rate (Sections 6.8.4, 6.8.7 and 6.8.18); under
remote Demand it sends only Poll or Final frames, carries a reloaded timer in a
Poll Sequence completed by the peer Final, and resumes periodic TX after the
Demand clear (Sections 6.5 and 6.6). Measured run on 2026-10-22 at `c954ec1`:
an 8.74 s RX0 window with 2 Polls, each answered by a Final within 201 us;
Down 900.4 ms after the last peer frame (DT 900 ms) and the first Down packet
after 900.5 ms, followed by 6 slow-rate intervals; a 7.24 s Demand window with
one GoBFD Poll at 500 ms; periodic TX 425 ms after the Demand clear with 17
intervals, mean 434.1 ms. Before `b72b3c7` the first Down packet waited for
the next slow-rate interval (1.862 s after silence on 2026-10-21).

The same topology proves crossed and queued Poll sequences (Sections 6.5 and
6.8.3): with the peer Final lost, GoBFD repeats P=1 with the active 400 ms tuple,
answers the peer crossing Poll with a Final-only packet carrying that tuple,
resumes its own Poll, and advertises a queued 800 ms tuple only in a separate
Poll after the peer Final and at least the measured exchange time. No captured
frame sets both P and F. Run on 2026-10-22 at `1e19026`: 8 Polls before the
Final, 5 of them after the crossing Final, exchange 2.620 s, separation
2.720 s, and the 800 ms receipt published after its own Final. FRR/BIRD interop
for nonzero remote TX/RX changes remains open.

The session receive path rejects the reserved zero Desired Min TX before
updating peer state or timers, including packets supplied through `RecvPacket`.
This prevents a zero detection-timer loop when the local receive interval is zero.

#### Section 6.7: Authentication

**Implementation**: [`internal/bfd/auth.go`](../../internal/bfd/auth.go)

All five RFC-defined auth types implemented:

| Type | Status | Implementation |
|---|---|---|
| Simple Password (1) | Complete | `SimplePasswordAuth` |
| Keyed MD5 (2) | Complete | `KeyedMD5Auth` |
| Meticulous Keyed MD5 (3) | Complete | `MeticulousKeyedMD5Auth` |
| Keyed SHA1 (4) | Complete | `KeyedSHA1Auth` |
| Meticulous Keyed SHA1 (5) | Complete | `MeticulousKeyedSHA1Auth` |

Key features:
- All keyed variants increment TX sequence on each signing call
- Inclusive receive windows use received Detect Multiplier: `[last, last + 3 * DetectMult]` for non-meticulous, `[last + 1, last + 3 * DetectMult]` for meticulous
- `AuthKeyStore` represents multiple keys; this does not qualify live rotation

RFC 5880 §6.7 leaves authentication coordination outside BFD. Same-identity
base reload rejects unqualified enable/disable, algorithm, key IDs/material,
selected TX key and receive-key-set changes before generation acceptance.
Immutable fingerprints are checked against both retained accepted desired
state, including absent runtime entries, and live sessions regardless of
API/shared ownership. Startup/new/removed identities and semantically identical
fresh stores remain admissible. A later API claim still encounters normal
fail-closed reconciliation; the boundary does not make whole-source apply atomic.
[ADR-0006](adr/0006-session-preserving-reload-policy.md) defines the policy;
`gobfd-qj0.8.2.2.7.3` tracks admission qualification. Coordinated rotation remains
after-v1 P2 `gobfd-khr`, not a qualified wire capability or seamless rollback.

RFC 5880 Section 6.8.1 receive-sequence expiry uses the owner loop's monotonic
last-valid-receive timestamp. At `2 × DetectionTime` without a valid packet,
`AuthSeqKnown` is cleared in every session state; `RcvAuthSeq` is retained
until the next successful authenticated reception replaces it. Authentication
also checks expiry before verifying a queued packet, so a restarted peer can
resynchronize even when RX is processed before a ready detection timer.
Invalid authentication never refreshes the receive timestamp. Exported
wall-clock receive timestamps do not participate in this decision.

The owner detection timer clears `RemoteDiscr` after one Detection Time in
Down, Init, Up and AdminDown. The last observed remote state, diagnostic and
C-bit remain available to causal policy; clearing the wire discriminator alone
does not publish a new observation. A passive session with `RemoteDiscr = 0`
does not transmit, including direct FSM sends and pending Final retries
(Section 6.8.7). The Final obligation remains pending until a valid peer
discriminator permits a successful send.

Valid Down/AdminDown reception and local PathDown retain the learned
discriminator until detection expiry. A received Poll therefore produces Final
with the peer's `MyDiscriminator`, including passive sessions and retries after
a failed send (Sections 6.8.6–6.8.7). Local PathDown commands do not postpone the
receive detection deadline.
If Down interrupts a pending RX decrease, the new Detection Time remains
anchored to the last valid receive.

#### Section 6.8.6: Reception of BFD Control Packets

Receive processing spans three layers. The numbered groups in code comments
are implementation shorthand; the RFC also requires session selection between
header and authentication validation.

| Layer | Responsibility | File |
|---|---|---|
| Codec | Header and authentication-section shape, discriminator validity | [`packet.go`](../../internal/bfd/packet.go) |
| Manager | Session selection, transport scope, interface and TTL admission | [`manager_session.go`](../../internal/bfd/manager_session.go) |
| Session | Zero Desired Min TX, auth consistency/verification, variables, FSM and timers | [`session_packet.go`](../../internal/bfd/session_packet.go) |

#### Section 6.8.7: Jitter

**Implementation**: [`applyJitterWithRand` and `Session.applyJitter`](../../internal/bfd/session_packet.go); `ApplyJitter` is also used by the Echo preview.

- Normal (DetectMult > 1): 75-100% of interval
- DetectMult == 1: 75-90% of interval
- Session-local PRNG prefers cryptographic seed and falls back to time-based
  seed on read failure; jitter is not a security boundary

#### Section 6.8.12: Detect Multiplier Reload

Solely config-owned base sessions with stable wire identity accept
`detect_mult` through the existing TX/RX timer tuple and generation/revision
receipts (`gobfd-qj0.8.2.2.7.1`). A detect-only Async update applies locally
without Poll and is advertised in the next Control packet. Remote Demand
with both ends Up requires Poll/Final under Section 6.6; its receipt remains
pending until the relevant Final. Combined TX/RX changes retain Section 6.8.3
direction rules, one active advertised tuple and the latest queued proposal.
Changing to or from DetectMult 1 reschedules TX for Section 6.8.7 jitter.

`internal/bfd/session_detect_update_test.go` contains regression cases for
serialized Control fields, Async versus remote-Demand confirmation, repeated
intent, shared-owner/unsupported rejection, the DetectMult 1 deadline,
failure/retirement and concurrent getters/snapshots. Final validation results
belong to the frozen-source evidence; this section asserts no final PASS.
Bounded Detect Multiplier wire slices are recorded in [Interop](05-interop.md);
final unified candidate qualification remains open. Shared-owner/API,
Micro-BFD, overlay and authentication changes remain outside this slice;
bounded minimum-TTL/padding reload is documented below. [ADR-0006](adr/0006-session-preserving-reload-policy.md)
accepts the policy without graduating overall Poll/Demand or RFC compliance.

#### Section 6.8.16: Administrative Control

Graceful shutdown uses one ten-second deadline for owner commit, the first
successful AdminDown/7 write, and the notification window recommended here and
in RFC 5882 Section 8. Failed writes retry through the normal TX timer. The
window starts at the first successful write and covers both local receive DT
and `localDetectMult * max(advertisedAdminDownTX, remoteMinRX)`; only larger
negotiations extend it. A shorter deadline returns typed `DrainIncompleteError`.
Passive unknown-peer and zero remote receive-interval suppression remain
mandatory; zero remote RX binds until the Section 6.8.18 reset after one silent
Detection Time. A permitted Final reply can provide the first successful write.
Owning UDP leases interrupt pending writes and join callbacks before releasing
resources. Success confirms local writes and time, not peer receipt.

Final Control-session claim release uses this drain before cancel/join and
lease release, including reconciliation rollback.
The private ten-second cap honors earlier caller cancellation; incomplete drain
and release errors are preserved. Non-final claims do not change wire state.
Reconciliation shares one concurrent drain budget.
Rekey keeps the captured old sender lease and discriminator until its actual
AdminDown/drain completes or reports typed incomplete failure, outside Manager
and lifecycle mutation locks, then cleans up once. Prepared replacement
cancellation before publication keeps the old identity; after peer-visible
cutover this is no hitless rollback. Client effects remain governed by
[RFC 5880 §6.8.16](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.16)
and [RFC 5882 §3.2](https://www.rfc-editor.org/rfc/rfc5882.html#section-3.2).
RFC 9468 Section 2 failure
cleanup of unsolicited Down sessions cancels without this administrative
exchange, because the passive side must stop Control transmission.
Standalone Echo and
Micro-BFD group-only removal do not own this Control exchange. This development
scope does not qualify full Watch registration/status or a stable release.

Local SIGTERM qualification on 2026-10-16 used source patch SHA256
`d1394d09fb3b902d3e34a5e34467c5aa4c506bd1d71c49416f6176773dc86947`
over `e4f3761a0f1fc59fbf9b2f859923a81125d157bf`. Capture recorded eight
AdminDown/7 packets, four each to FRR and BIRD, with TTL 255, multiplier 3,
advertised TX 1,000,000 microseconds and RX 300,000 microseconds. Drain completion
followed the first wire packet by 3.000171 seconds for FRR and 3.000147 for BIRD.
Forty BGP/route samples over 20.280366 seconds retained Established state,
unchanged uptime and routes `10.20.0.0/24` and `10.30.0.0/24`. These samples do
not prove uninterrupted state between observations or peer acknowledgement.
The daemon exited with code 0; all five topology containers were stopped and
retained without OOM. Evidence: `reports/h12-wire-20261016/`; PCAP SHA256
`f10937cc6a7969bc336e4f1b5217d2f458e5f046236f68530225ef26ec281c68`.

#### Not Implemented (RFC 5880)

| Section | Feature | Rationale |
|---|---|---|
| 6.4 | Affiliated Echo Mode | Requires control session; RFC 9747 unaffiliated echo is provided instead, as a preview |
| 6.5 | Complete Poll Sequence procedures | Config-owned timer transactions, confirmed Poll transmission, Final retry, P/F exclusion, crossed Poll and queued-tuple separation exist; live FRR/BIRD reload and crafted-peer crossed/queued Poll wire evidence pass. Qualification of the deployed RTT envelope remains pending |
| 6.6 | Full Demand Mode | Remote periodic-TX suppression, a local timer Poll under remote Demand and the Section 6.8.18 reset are proven on the wire with the crafted Go peer (`TestCraftedPeerTestcontainers`); pinned FRR/BIRD cannot send Demand. Local Demand and complete Poll procedures remain open |
| 4.1 | Multipoint bit | Reserved for future P2MP extensions |

### RFC 5881 Implementation Notes

**Implementation**: [`internal/netio/`](../../internal/netio/)

| Requirement | Implementation |
|---|---|
| Destination port 3784 | `netio.PortSingleHop = 3784` |
| Source port 49152-65535 | `SourcePortAllocator`; `NewUDPSender` rejects out-of-range values, including zero, before options or socket creation |
| TTL=255 outgoing | `IP_TTL` / `IPV6_UNICAST_HOPS` set with `unix.SetsockoptInt` |
| TTL=255 incoming check | `IP_RECVTTL` / `IPV6_RECVHOPLIMIT` + listener pre-filter; the demultiplexed session also requires a single-hop transport class (Section 5) |
| Transport class | A packet from the 4784 multihop listener never reaches a single-hop session, by Your Discriminator or by peer key (`scope_mismatch`) |
| Interface binding RX (Section 3) | Configured `interface`: packets from another listener device are rejected. Every single-hop session also compares the PKTINFO interface index with the index resolved at creation from `interface` or from the interface that owns `local` |
| Interface binding TX (Section 6) | `SO_BINDTODEVICE` on the session sender when `interface` is set (YAML and API); IPv6 link-local sender addresses are zoned to it |
| IPv6 link-local demux | Zones are not part of the demux key; without `interface`, the link-local zone selects the session interface, listener device and sender device; a zone that contradicts `interface` is rejected (`ErrInvalidSessionZone`) |
| TTL in the session (Section 5) | The demultiplexed single-hop session also requires TTL/Hop Limit 255 (`demux`/`ttl_rejected`) |
| Separate IPv4/IPv6 listeners | Separate `udp4` / `udp6` sockets |

The shared UDP sender retains the same source-port range for Echo as a product
policy; RFC 5881 Section 4 leaves the Echo source port unspecified. Destination
overrides do not relax this constructor contract. Production factories already
allocate in range, so session source-port selection is unchanged.

Linux TTL/Hop Limit ancillary values are decoded as four-byte native-endian
integers and accepted only in 0-255. Packet-info interface indices use native
byte order; destination addresses retain network byte order. The intended
platforms remain `linux/amd64` and `linux/arm64`. Native kernel qualification
for both platforms is tracked in `gobfd-qj0.8.2.1.16.2`; cross-compilation alone
does not close that gate. Source corrections are tracked in
`gobfd-qj0.8.2.1.16.1`.

The implemented boundary is the numbered-multiaccess single-hop profile: each
session is bound to one receive interface, explicit or derived from its local
address at creation. A YAML session whose local address is on no interface or
on several interfaces stays pending and is retried on interface events, resync
or SIGHUP, with readiness `NOT_SERVING` while pending; an address owned by
several interfaces stays pending until `interface` is configured and applied
with SIGHUP. The API returns `FailedPrecondition`. A local address on `lo` or a
dummy interface binds the session to that interface, so packets arriving on a
physical interface are refused as `scope_mismatch`; such deployments must set
`interface`. Point-to-point initial demultiplexing (Section 6: accept
the initial packet from any source address) remains deferred and is not
production-qualified.

### RFC 5882 Implementation Notes

**Implementation**: [`internal/gobgp/`](../../internal/gobgp/)

- Section 3.1 permits implementation-defined session-state hysteresis. GoBFD's configurable penalty-based dampening is implementation policy, not an RFC-defined algorithm.
- Section 10.2 (Interactions with BGP; Section 4.2 for the reaction to state changes): `handler.go` watches BFD state changes and calls GoBGP gRPC API
  - Ordinary remote Down/detection failure or explicit local PathDown --> `DisablePeer()`
  - Local or known remote AdminDown --> no disable (Sections 3.2 and 4.2);
    the last observed remote AdminDown survives subsequent timeout diagnostics
  - BFD Up --> `EnablePeer()` only for a peer disabled by this process
  - Generic route withdrawal/restoration is not implemented; when GoBGP
    integration is enabled, the reserved `withdraw-routes` strategy fails
    configuration validation
  - Each GoBGP API action is bounded by `gobgp.action_timeout` so a slow external API cannot block state-change processing indefinitely

### RFC 5883 Implementation Notes

| Requirement | Implementation |
|---|---|
| Destination port 4784 | `netio.PortMultiHop = 4784` |
| TTL=255 outgoing | Same as single-hop |
| Receive TTL policy | RFC 5883 has none. GoBFD applies `multihop_max_hops` N per demultiplexed session: accept TTL/Hop Limit >= 255 - N (1/2/5 -> 254/253/250, default 1), reading A of the non-normative RFC 5082 Appendix A, matching FRR `minimum-ttl`. Refusals count as `demux`/`ttl_rejected` |
| Transport class | A packet from the 3784 listener never reaches a multihop session (`scope_mismatch`) |
| Interface | `multi_hop` with `interface` is rejected in YAML and API validation (Section 4.1 demultiplexes by address pair) |
| Echo (Section 3: MUST NOT be used over multiple hops) | Multihop Control packets advertise Required Min Echo RX Interval 0; RFC 9747 Echo has no multihop type |
| Demux (Section 4.1) | Your Discriminator, or (source, destination) when it is zero; `Manager.DemuxWithWire` |

For a stable sole-config multihop session with unchanged authentication,
`multihop_max_hops` reload updates the local `255 - N` floor under `Session.mu`
at the receive gate. A TTL-only change needs no Poll even under remote Demand.
The tuple/receipt tracks MinTTL alongside TX/RX, DetectMult and PaddedPduSize;
this local policy does not add a normative RFC 5883 TTL rule.

**Authentication guidance (Sections 3 and 6).** TTL/Hop Limit 255 on receive
cannot protect a multihop path; RFC 5883 Section 3 assigns spoofing protection
to BFD authentication, and Section 6 says implementations SHOULD use
cryptographic authentication over multihop paths. The hop-policy floor is
defence in depth only. Configure `auth` on every `multi_hop` session, preferably
`meticulous_keyed_sha1`; an unauthenticated multihop session with
`multihop_max_hops` above 1 accepts any spoofed packet that reaches it with a
sufficient TTL and matching discriminators.

### RFC 7419 Implementation Notes

**Implementation**: [`internal/bfd/intervals.go`](../../internal/bfd/intervals.go)

RFC 7419 defines a set of common BFD timer interval values to ensure interoperability between software-based and hardware-based implementations.

| Common Interval | Wire value (microseconds) |
|---|---|
| 3.3 ms | 3300 |
| 10 ms | 10000 |
| 20 ms | 20000 |
| 50 ms | 50000 |
| 100 ms | 100000 |
| 1 s | 1000000 |

These are the common values from [RFC 7419 Section 3](../rfc/rfc7419.txt),
not universal minimum timers for a deployment class.

Additionally, 10s is recommended for graceful restart (multiplier 255 = 42.5 min timeout).

| Feature | Implementation |
|---|---|
| Common interval set | `CommonIntervals` array (6 values) |
| Align to common interval | `AlignToCommonInterval()` — rounds UP |
| Check if common | `IsCommonInterval()` |
| Nearest common interval | `NearestCommonInterval()` |
| Config option | `bfd.align_intervals: true` in YAML config |
| Graceful restart interval | `GracefulRestartInterval = 10s` |

When `bfd.align_intervals` is enabled, session creation rounds intervals up
within the common set; values above its 1 s maximum remain unchanged.
Alignment alone establishes neither hardware/cloud interoperability nor timer
accuracy. Verify the selected peer procedure and intervals on the target path.

### RFC 9384 Implementation Notes

**Implementation**: [`internal/gobgp/rfc9384.go`](../../internal/gobgp/rfc9384.go)

RFC 9384 defines Cease NOTIFICATION subcode 10 ("BFD Down") for BGP sessions torn down due to BFD failure.

| Requirement | Implementation |
|---|---|
| Cease subcode 10 (BFD Down) | **Not emitted**; the constant is used only for formatting text |
| NOTIFICATION on BFD failure | `DisablePeer` emits Administrative Shutdown (Cease/2) |
| Diagnostic context | BFD `Diag` code included in the communication string |

GoBGP v4, like v3, does not expose per-subcode control in its `DisablePeer` API. The
current integration therefore does not implement RFC 9384: it sends Cease
subcode 2 and places `"BFD Down (RFC 9384 Cease/10): diag=..."` in the RFC 8203
administrative communication field for operator correlation. Full compliance
requires an API path that emits wire subcode 10.

### RFC 9468 Implementation Notes

**Implementation**: [`internal/bfd/unsolicited.go`](../../internal/bfd/unsolicited.go), [`internal/bfd/manager.go`](../../internal/bfd/manager.go)

RFC 9468 enables one BFD endpoint to dynamically create passive sessions in response to incoming BFD Control packets, without per-session configuration. Useful for static route next-hop tracking and IXP route-server deployments.

| Requirement | Implementation |
|---|---|
| Disabled by default (MUST) | `unsolicited.enabled: false` default |
| Per-interface policy (MUST) | Explicit nonempty interface names; IPv4/IPv6 wildcard listeners bound with `SO_BINDTODEVICE`, independent of configured sessions |
| Source address validation (MUST) | Source must belong to the subnet of the receiving local address (Section 2); `AllowedPrefixes` narrows it further; unnumbered paths fail closed |
| Single-hop only (MUST, Section 6.1) | Only TTL 255 packets from a single-hop listener create sessions; multi-hop (port 4784) packets are refused |
| Local discriminator allocation (MUST) | `DiscriminatorAllocator` for passive sessions |
| Configurable timers (SHOULD) | `UnsolicitedSessionDefaults` |
| Bounded admission (Section 6.1) | `max_sessions` must be >= 1 when enabled; new sessions are admitted at up to 10 per second, burst 20 |
| Session cleanup on Down (SHOULD) | `CleanupTimeout` configuration |
| Stop after failure (MUST, Section 2) | The session owner stops non-Final Control TX on natural failure or expiry of its independent setup budget; configured/API claims retain ordinary RFC 5880 behavior |
| Authentication (RECOMMENDED, Section 6.2) | Optional `unsolicited.auth`, Meticulous Keyed SHA1 only; the triggering packet is verified before a session exists; without it, A-bit packets are refused |

Auto-creation happens in `Manager.demuxByPeer()` when an incoming packet matches no existing session and unsolicited BFD is enabled for the receiving interface. The passive session is created with `RolePassive` and immediately receives the triggering packet. The `MaxSessions` quota is reserved atomically before creation and released on create failure, explicit destroy, or cleanup of a passive Down session after `CleanupTimeout`.

Each passive session gets its own sender on the local address and interface
that received the triggering packet, with its own RFC 5881 source port; an IPv6
link-local address is scoped to the receiving interface. The enabled policy
is validated at load: session defaults representable on the wire with a
nonzero `required_min_rx`, at least one enabled interface and valid
`allowed_prefixes`. Development qualification covers dual-stack capture and
78 concurrent sender leases closed once (`gobfd-qj0.8.2.1.10.1`). Unsolicited
mode remains an unsafe preview. Do not enable it on an untrusted interface.

Unsolicited setup has an independent budget from
the first accepted Control packet, extended only to cover a larger observed
local RX Detection Time. Repeated packets with unchanged timers do not restart
that budget. Claim-mode changes preserve the original budget and failure latch;
configured/API claims retain generic TX even if the private budget expires.
Expiry suppresses exclusively unsolicited TX while retaining the RFC 5880 FSM and
diagnostic; valid receive silence alone expires the learned discriminator
after one RX Detection Time and the authentication sequence window after two.
The next valid authenticated Control packet starts a new local attempt using
the retained FSM, an implementation policy rather than a distinct wire epoch:
retained Init plus remote Up becomes Up; retained Down plus remote Up stays
Down. An initial remote AdminDown also receives the setup budget; AdminDown
maintenance after a failed attempt preserves suspension. Required Final
responses and retries, and local AdminDown notification, retain the RFC 5880
Sections 6.5, 6.8.7 and 6.8.16 procedures. Cleanup qualifies the failed attempt
under the owner lock before detaching its claim; a previous timer cannot
retire a later attempt or a locally disabled session. Setup failure signals
only request cleanup and do not publish a fabricated state transition.

### RFC 9747 Implementation Notes

**Status**: Preview; default-off

`echo.enabled: true` is required for declarative peers and API creation.
Each Add, including an idempotent retry, also requires local receive-path
readiness; otherwise it returns `FailedPrecondition`. List/Delete retain their
management authorization rules. A valid SIGHUP candidate updates the enablement
opt-in; a rejected candidate preserves it. See
[`EchoServer.AddEchoSession`](../../internal/server/echo.go) and
[`TestEchoPreviewReloadUpdatesAdmission`](../../cmd/gobfd/echo_admission_test.go).

**Implementation**: [`internal/bfd/echo.go`](../../internal/bfd/echo.go), [`internal/netio/echo_receiver.go`](../../internal/netio/echo_receiver.go)

RFC 9747 defines unaffiliated BFD Echo for single-hop forwarding-path liveness
without a BFD implementation on the adjacent device. Packets use a local IP
destination and traverse that device's forwarding path back to the originator
([Section 1](../rfc/rfc9747.txt)). The current preview's peer-address send path
does not establish that forwarding behavior; interhost qualification remains open.

The table describes the current preview, not a list of satisfied RFC
requirements. [RFC 9747 Section 2](../rfc/rfc9747.txt) requires the RFC 5880
state machine without AdminDown, initial address/source-port demultiplexing
when Your Discriminator is zero, and subsequent Your Discriminator-based
demultiplexing. Section 5 recommends packet authentication. The current
two-state loop and My Discriminator-only receive path are implementation gaps.

| Surface | Current implementation |
|---|---|
| UDP port 3785 | `netio.PortEcho = 3785`; `echoListenerConfigs()` and API listener claims in `cmd/gobfd/listeners.go` |
| Standard BFD Control packet format | Reuses `MarshalControlPacket` codec |
| DiagEchoFailed on timeout | `DiagEchoFailed` (value 2) |
| Locally provisioned timers | `EchoSessionConfig.TxInterval`, no negotiation |
| Two-state FSM (Up/Down) | Simplified FSM in `EchoSession` |
| DetectionTime = DetectMult * TxInterval | `EchoSession.DetectionTime()` |
| Demux by MyDiscriminator on return | `EchoReceiver` matches returned packets |
| Session type | `SessionTypeEcho` constant |
| TTL 255 send, exact TTL 254 receive | `netio.ValidateExpectedTTL` on the Echo listener |
| Declarative echo peers | `echo.peers[]` in config, reconciled on SIGHUP |
| Sender with port 3785 destination | Declarative and API-created Echo use `WithDstPort(PortEcho)`; base and multihop API sessions retain destinations 3784 and 4784 |
| TX interface | `singleHopSenderBinding` applies the optional `SO_BINDTODEVICE` and scopes an unzoned IPv6 link-local source for declarative and API senders; source-port lease and write-buffer policy remain shared |

Management ownership is separate from RFC packet validation. Compatible config
and API principal claims share one Echo wire; retry and final-claim cleanup
follow [ADR-0009](adr/0009-echo-principal-claims.md). These rights do not
authenticate returned packets or close the preview qualification gates.

Current preview behavior and gaps:
- No three-way handshake (no Init state)
- No timer negotiation with remote (locally provisioned)
- No returned-packet authentication; self-originated packets remain spoofable
- Separate `EchoSession` type with simplified FSM

Beads `gobfd-qj0.8.2.1.20` owns the FSM gap,
`gobfd-qj0.8.2.1.13` owns returned-packet identity/authentication, and
`gobfd-qj0.8.2.1.12` owns slow pacing before Up and after Down. Management
transport or ownership checks do not close these protocol tasks.

### RFC 7130 Implementation Notes

**Status**: Preview; default-off, member L2 qualification incomplete

`micro_bfd.enabled: true` is required for configured groups, actuation and
API group creation. The default rejects creation with `FailedPrecondition`;
List/Delete remain available. Enable changes require restart. This boundary
does not close the dedicated multicast MAC or enslaved-member wire gates.

**Implementation**: [`internal/bfd/micro.go`](../../internal/bfd/micro.go)

RFC 7130 defines Micro-BFD — independent BFD sessions on every LAG member link for per-link forwarding verification with faster detection than LACP alone.

| Requirement | Implementation |
|---|---|
| UDP port 6784 | `netio.PortMicroBFD = 6784`; per-member `microListenerConfigs()` in `cmd/gobfd/micro_bfd.go` |
| One BFD session per member link (Sections 2, 2.2) | `microBFDMemberSessionConfig()` derives one `SessionTypeMicroBFD` session per member link from its group; one group owns each member link (`ErrMicroBFDMemberOwned`) |
| Member session contract (Section 2.2) | `ValidateMicroBFDConfig()` applies RFC 5880 session validation (peer address, interval wire bounds, detect multiplier) to every member session on the daemon and direct Manager/API paths; groups store and report the shared timers |
| `SO_BINDTODEVICE` per member | `WithBindDevice()` functional option on sender |
| Aggregate state tracking | `upCount >= minActive` threshold |
| Member Down handling | `UpdateMemberState()` records member state and triggers aggregate threshold changes |
| Dedicated multicast MAC (Section 2.3) | `01-00-5E-90-00-01` is not sent: the UDP sender resolves the peer MAC. The separate wire gate is red; see `gobfd-rle` and `gobfd-j8r`. |
| Asynchronous mode only | Standard RFC 5880 procedures per member |
| Session type | `SessionTypeMicroBFD` constant |
| Per-group configuration | `MicroBFDGroupConfig` with LAG interface + member links |
| Group reconciliation | `Manager.ReconcileMicroBFDGroupsDetailed()` commits each group with its member sessions on SIGHUP reload; a failed member claim rolls back the new groups and sessions of the pass |
| Group ownership | Groups carry typed owner claims like sessions: reload removes only its own groups, identical API and reload groups are shared, `DeleteMicroBFDGroup` of a group the caller does not own returns `ErrMicroBFDGroupOwnerClaimNotFound` |
| Member start state (Section 3) | A new group seeds each member from its existing member session; an Up member counts toward `MinActiveLinks` and is reported to the actuator in dispatch order |
| State dispatch | `RunDispatch` routes a member state change through the `{member link, address family}` index to its single owning group |
| Actuator hook (Section 5) | `MicroBFDActuator` receives member state events after the group update, outside Manager locks, in dispatch order, with a 5 s context deadline per call |
| Policy gate | `netio.LAGActuator` supports `disabled`, `dry-run`, and `enforce` modes |
| Daemon wiring | `micro_bfd.actuator` configures mode, backend, OVSDB endpoint, owner policy, and member actions |
| Kernel bond backend | `KernelBondLAGBackend` writes `-member` / `+member` to Linux bonding sysfs |
| OVS backend | `OVSDBLAGBackend` mutates `Port.interfaces` through OVSDB; `OVSLAGBackend` remains a CLI fallback type |
| NetworkManager backend | `NetworkManagerLAGBackend` deactivates and activates NM-owned bond port profiles through D-Bus |

Aggregate state logic:
- New member sessions start Down; an existing shared session can seed its current state
- When `upCount >= MinActiveLinks`, aggregate transitions to Up
- When `upCount < MinActiveLinks`, aggregate transitions to Down
- State changes are reported only on aggregate transitions (threshold crossing)
- Init state on a member is not counted as Up (only `StateUp` increments `upCount`)

`MicroBFDGroupSnapshot` provides a read-only view of the group state including per-member link details, useful for gRPC API responses and monitoring.

**Linux production limitation**: RFC 7130 also requires a member link whose
micro-BFD session is Down to be removed from the LAG load-balancing table.
GoBFD now has a `MicroBFDActuator` hook and a tested `netio.LAGActuator` policy
gate for disabled, dry-run, and enforce modes. YAML wiring is present, including
NetworkManager-aware owner policy selection. `backend: kernel-bond` can enforce
member remove/add through Linux bonding sysfs when `owner_policy:
allow-external` is explicit. `backend: ovs` can enforce member remove/add on an
existing OVS bonded port with native OVSDB transactions against
`Port.interfaces`. `OVSLAGBackend` remains a direct CLI fallback type, while
`backend: networkmanager` can enforce member remove/add by deactivating the
active NetworkManager bond port profile and reactivating the remembered or
available bond port profile when `owner_policy: networkmanager-dbus` is
explicit.

### RFC 8971 Implementation Notes

**Status**: Incomplete preview; owner-specific backends planned

**Implementation**: [`internal/netio/vxlan.go`](../../internal/netio/vxlan.go), [`internal/netio/vxlan_conn.go`](../../internal/netio/vxlan_conn.go), [`internal/netio/overlay.go`](../../internal/netio/overlay.go), [`internal/netio/overlay_backend.go`](../../internal/netio/overlay_backend.go), [`internal/netio/overlay_inner.go`](../../internal/netio/overlay_inner.go)

RFC 8971 defines BFD encapsulated in VXLAN for forwarding-path liveness detection between VTEPs (Virtual Tunnel Endpoints). BFD Control packets are carried inside VXLAN-encapsulated inner Ethernet frames.

| Requirement | Implementation |
|---|---|
| Outer UDP port 4789 | `netio.VXLANPort = 4789`, `VXLANConn` through explicit `userspace-udp` backend |
| Inner UDP port 3784 | `BuildInnerPacketInto()` with dst port 3784 |
| VXLAN header codec | `MarshalVXLANHeader` / `UnmarshalVXLANHeader` |
| Management VNI | `VXLANConfig.ManagementVNI`, VNI mismatch rejection |
| VNI validation (24-bit) | `ErrInvalidVXLANVNI` config validation |
| I flag validation | `ErrVXLANInvalidFlags` sentinel |
| Inner destination MAC | `VXLANBFDInnerMAC = 00:52:02:00:00:00` (IANA) |
| Inner source MAC | Per-peer remote/local originating VTEP MACs are required and matched exactly |
| Inner TTL=255 | `BuildInnerPacketInto()` sets TTL=255 (RFC 5881 GTSM) |
| Inner IPv4 checksum | `ipv4HeaderChecksum()` per RFC 1071 |
| Session type | `SessionTypeVXLAN` constant |
| OverlaySender adapter | `OverlaySender` implements `bfd.PacketSender` |
| OverlayReceiver loop | Strips VXLAN + inner headers, delivers to `Manager.DemuxWithWire` |
| Backend model | `NewVXLANOverlayBackend` supports `userspace-udp`; reserved kernel/OVS/OVN/Cilium/Calico/NSX backends fail closed |
| Receive validation | Overlay headers and the full outer/VNI/inner IPv4/MAC tuple are matched exactly before discriminator demux; malformed inner framing is rejected |
| Declarative peers | `vxlan.peers[]` in config, reconciled on SIGHUP |
| Config validation | VNI range, concrete IPv4 endpoints, remote/local unicast source MACs, detect_mult, and duplicate wire identity detection |

Packet encapsulation, outer to inner:

| Layer | Supported profile |
|---|---|
| Outer | IP, UDP destination 4789, VXLAN fixed header 8 B |
| Inner | Ethernet 14 B, IPv4 20 B, UDP destination 3784, BFD Control |

The VXLAN header codec handles the 8-byte fixed format with I flag (VNI valid) and 24-bit VNI encoding. Management VNI packets are processed locally and not forwarded to tenant networks.

**Linux production limitation**: `vxlan.backend: userspace-udp` owns a UDP
socket on `localAddr:4789`. This is suitable for a lab endpoint, a dedicated
management VNI endpoint, or a Linux VTEP where GoBFD owns the socket. If kernel
VXLAN, OVS/OVN, Cilium, Calico, NSX, or another dataplane already owns UDP 4789 for the
same local address/namespace, GoBFD fails closed for reserved backend names
until an owner-specific integration exists. Sender reconciliation reuses the
runtime backend already serving the receiver and does not bind a second socket.
Listeners are grouped by local VTEP and each session sender retains its exact
tunnel scope. A deployment that shares the UDP endpoint with another dataplane is outside
this backend's qualified ownership profile.

VXLAN and Geneve listeners each own one allocated inner source-port lease;
closing the listener returns it exactly once. Session senders borrow the shared
listener and do not release its port. Send and receive operations honor context
cancellation, including queued writes; cancellation does not close a healthy
listener or leave a deadline on subsequent operations. `Close` unblocks pending
socket I/O. Receive payloads borrow the listener buffer until the next receive,
so each listener has one receiver loop.

Reload preflight checks one complete overlay source before the first backend
open or `SetScopes`. Unequal scopes sharing a live/draining wire identity,
including VXLAN local-MAC changes, require separate explicit removal/recreation.
An unused never-committed preparation may be superseded; duplicate desired wire
identities still fail preflight. Successful preflight removes obsolete unused
preparations before further claims. Incomplete
acquisition returns Pending/Failed before Manager apply/release for that source,
preserving live/draining exact scopes/backends. Pruning keeps existing scopes
in current desired union positive physical-session lease refs. Registered
sessions retain refs through drain until idempotent lease close; unused or
unpublished leases use ordinary once-only cleanup without drain. Current desired
preparations may remain for retry. This does not add global pre-generation admission or all-source
atomicity. Mandatory P0 wire gate `gobfd-qj0.8.2.2.7.5` depends on
`gobfd-qj0.8.2.2.7.4` and must cover all five reload fields before v1 using the
existing harness. Source unit tests are not interoperability qualification.

The [overlay wire qualification](./09-development.md#overlay-wire-qualification)
checks this userspace IPv4 profile with real UDP and capture-correlatable
results. It does not qualify owner-specific backends or vendor forwarding paths.

### RFC 9521 Implementation Notes

**Status**: Incomplete preview; explicit Format A VAP identity required

**Implementation**: [`internal/netio/geneve.go`](../../internal/netio/geneve.go), [`internal/netio/geneve_conn.go`](../../internal/netio/geneve_conn.go), [`internal/netio/overlay.go`](../../internal/netio/overlay.go), [`internal/netio/overlay_backend.go`](../../internal/netio/overlay_backend.go), [`internal/netio/overlay_inner.go`](../../internal/netio/overlay_inner.go)

RFC 9521 defines BFD encapsulated in Geneve for forwarding-path liveness detection between NVEs (Network Virtualization Edges) at the VAP (Virtual Access Point) level. Geneve is the evolution of VXLAN for cloud-native environments.

| Requirement | Implementation |
|---|---|
| Outer UDP port 6081 | `netio.GenevePort = 6081`, `GeneveConn` through explicit `userspace-udp` backend |
| Geneve header codec | `MarshalGeneveHeader` / `UnmarshalGeneveHeader` |
| O bit (control) = 1 | RFC 9521 Section 4: set on send and validated on receive |
| C bit (critical) = 0 | RFC 9521 Section 4: cleared on send and validated on receive |
| Protocol Type 0x6558 | Format A: Ethernet payload (`GeneveProtocolEthernet`); receive codec validation exists |
| VNI validation (24-bit) | `ErrInvalidGeneveVNI` config validation; receive codec validation exists |
| Version validation | `ErrGeneveInvalidVersion` (only version 0 supported) |
| Ethernet payload (Format A) | `GeneveProtocolEthernet = 0x6558` |
| Variable-length options | `GeneveHeader.OptLen` + `TotalHeaderSize()` |
| Inner TTL=255 | `BuildInnerPacketInto()` sets TTL=255 (RFC 5881 GTSM) |
| Session type | `SessionTypeGeneve` constant |
| OverlaySender adapter | `OverlaySender` implements `bfd.PacketSender` |
| OverlayReceiver loop | Reaches `Manager.DemuxWithWire` only after exact configured VAP identity matches |
| Backend model | `NewGeneveOverlayBackend` supports `userspace-udp`; reserved kernel/OVS/OVN/Cilium/Calico/NSX backends fail closed |
| Receive validation | Matches outer endpoints, VNI, inner VAP IPs, address family, and source/destination MACs before BFD demux |
| Declarative peers | `geneve.peers[]` in config, per-peer VNI override, reconciled on SIGHUP |
| Config validation | VNI range, concrete outer IPv4 endpoints, detect_mult, duplicate keys, and complete unicast VAP MAC/IPv4 identity are checked |

Packet encapsulation, outer to inner (Format A):

| Layer | Supported profile |
|---|---|
| Outer | IP, UDP destination 6081, Geneve base header 8 B; O=1, C=0, Protocol=0x6558 |
| Inner | Ethernet 14 B, IPv4 20 B, UDP destination 3784, BFD Control |

Key differences from VXLAN BFD (RFC 8971):
- Geneve supports variable-length TLV options (VXLAN has fixed 8-byte header)
- Two payload formats: Ethernet (Format A) and IP (Format B)
- O bit control flag indicates management/control traffic
- Sessions originate/terminate at VAPs, not directly at NVEs

**Linux production limitation**: `geneve.backend: userspace-udp` requires
explicit local and remote VAP MAC/IPv4 identity. Missing or special-use identity
(unspecified, loopback, multicast), IPv6 inner payloads, Format B, and other protocol types fail closed. Exact scopes can share
one local listener without aliasing VNI/VAP tuples. The backend does not
integrate with kernel Geneve, OVS/OVN, or NSX dataplane socket ownership.

### RFC 9764 Implementation Notes

**Status**: Partial

**Implementation**: [`internal/bfd/session_packet.go`](../../internal/bfd/session_packet.go) (PDU construction), [`internal/bfd/session_loop.go`](../../internal/bfd/session_loop.go) (padded send), [`internal/netio/sender.go`](../../internal/netio/sender.go) (DF bit), [`internal/bfd/session_update.go`](../../internal/bfd/session_update.go) (reload policy)

RFC 9764 defines BFD Large Packets for MTU path verification. A BFD implementation pads Control packets to a configured size and sets the IP Don't Fragment (DF) bit. If the padded packet is larger than the path MTU, it will be dropped, causing BFD to detect the MTU issue.

| Requirement | Implementation |
|---|---|
| Pad BFD packet to configured size | `SessionConfig.PaddedPduSize`, zero-padding in TX path |
| Set IPv4 DF bit (IP_PMTUDISC_DO) | `WithDFBit()` on `UDPSender` for nonzero YAML/API targets |
| Zero-fill padding | `cachedPacket` extended with zero bytes after BFD payload |
| Per-session configuration | YAML `padded_pdu_size`, API `AddSessionRequest.padded_pdu_size`, CLI `--padded-pdu-size` |
| Public session status | `BfdSession.padded_pdu_size` carries the configured target in bytes; an authenticated PDU may be larger |
| Global default | `bfd.default_padded_pdu_size` in YAML config |
| Configured target range | 0 disables padding; 24-9000 bytes without authentication, 26-9000 with authentication (v1 policy) |

In-place reload is limited to positive-to-positive targets on a sole-config
base session with stable identity, unchanged authentication and a sender whose
immutable `PaddingPrepared` proves successful constructor-time DF preparation.
It does not establish socket liveness. Remote Demand, unknown/unprepared
senders, zero transitions and shared-owner base padding updates reject the
complete candidate before desired-generation/log/resource mutation. The
coordinator admission guard receives only `candidate.base`. Micro-BFD and
overlay remain outside this qualified profile; their configuration gains no
padding/minTTL fields, and other-source conflict/stale behavior is unchanged.
Queued policy is rechecked before any field commit; newly active Demand fails
the whole pending tuple, including a reversal to confirmed values. TX/RX Poll
directions and active/queued generation rules remain unchanged.

Capacity growth prepares one waiting buffer at control admission and swaps it
on the owner before field commit; growth allocation does not occur in the
packet loop. Padding enable/disable and dynamic DF remain after-v1 P2
`gobfd-ebu`; zero-target DF defaults are unchanged. Final live peer/MTU
qualification belongs to mandatory P0 `gobfd-qj0.8.2.2.7.5`, after
`gobfd-qj0.8.2.2.7.4` and before v1; component checks do not close it.

Keyed MD5 and SHA1 verification hashes exactly the declared BFD `Length`,
including any PDU extension bytes, after checking that the digest fits within
`Length` and `Length` fits within the received payload. Transport padding is
outside this boundary and its contents are not validated, following
[RFC 9764 Section 3](../rfc/rfc9764.txt) and
[RFC 5880 Sections 6.7.3-6.7.4](../rfc/rfc5880.txt). Simple Password validates
the password, not a packet digest. Independent wire vectors cover all five
authentication types at unpadded, 64-, 128-, and 9000-byte payload sizes.

The approved v1 configuration policy rejects authenticated targets of 24 or
25 bytes. RFC 9764's YANG typedef also permits raising a smaller configured
target to the session minimum; rejection is the selected GoBFD policy.
Valid targets such as 26 never truncate a larger authenticated PDU: transmission
uses `max(actual PDU length, configured target)` in transport payload bytes,
excluding IP/UDP headers. YAML global defaults and
per-session overrides apply the same policy before runtime changes; raw
numeric inputs are checked before weak decoding can wrap or truncate them.

The direct UDP receive pool holds the complete BFD transport payload through
9000 bytes; the codec's `MaxPacketSize` remains 64 bytes. Linux rejects
`MSG_TRUNC` before metadata or packet parsing, and listeners reject invalid
transport read lengths before slicing while returning the borrowed buffer.
Authenticated reception retains an owned copy of the complete wire payload.

Userspace overlay UDP payload buffers include framing around the same
9000-byte inner BFD limit: VXLAN uses 9090 bytes with a 60-byte inner IPv4
header; Geneve uses 9342 bytes with the same IPv4 header and 252 option bytes.
The extra framing space does not permit inner BFD payloads above 9000 bytes.
These are transport buffer limits, not a qualification of the path MTU.

Local hash-boundary and configuration coverage is tracked by
`gobfd-qj0.8.2.1.5.2`. The [critical BASE gate](05-interop.md) qualifies
128-byte GoBFD Control payloads toward FRR Simple Password and BIRD Keyed SHA1,
including unchanged BFD Length, zero padding and IPv4 DF. Both peers transmit
ordinary authenticated PDUs back to GoBFD; padded peer-originated traffic is
covered by local vectors, not this interop run. Path MTU qualification remains
separate; stable RFC 9764 compliance remains partial.

### RFC 7880/7881 (Planned)

**Status**: Planned

RFC 7880 defines Seamless BFD (S-BFD) — a simplified BFD mechanism for infrastructure liveness testing. Unlike standard BFD which requires a three-way handshake, S-BFD uses a stateless reflector that immediately responds to initiator probes.

RFC 7881 defines S-BFD encapsulation for IPv4 and IPv6 using destination port 7784.

| Requirement | Planned Implementation |
|---|---|
| Stateless reflector (RFC 7880) | Future reflector on port 7784 |
| Discriminator pool matching | Reflector matches `YourDiscriminator` against local pool |
| Reflects with State=Up | No session state maintained |
| S-BFD initiator (RFC 7880) | Future initiator with detection timer |
| Port 7784 (RFC 7881) | Future dedicated S-BFD listener |
| No three-way handshake | Initiator sends, reflector responds immediately |

No `SBFDReflector`, `SBFDInitiator`, or port-7784 listener exists in the
current codebase; the names above describe planned roles, not APIs.

### Unimplemented MPLS and VCCV

These transport families have no session implementation or stub interfaces in
the current codebase. Retained RFC texts are references, not implementation.

| RFC | Dependency | Status |
|---|---|---|
| RFC 5884 (BFD for MPLS) | LSP Ping (RFC 4379) | Not implemented |
| RFC 5885 (BFD for VCCV) | VCCV (RFC 5085), LDP (RFC 4447) | Not implemented |

### Reference RFCs

These RFCs are referenced but not directly implemented:

| RFC | Title | Relevance |
|---|---|---|
| RFC 8203 | BGP Administrative Shutdown | Communication string for DisablePeer |
| RFC 5082 | GTSM | Basis for TTL=255 requirement |
| RFC 4379 | LSP Ping | Dependency of RFC 5884 |
| RFC 5085 | VCCV | Dependency of RFC 5885 |
| RFC 4447 | LDP | Dependency of RFC 5885 |
| RFC 7726 | Clarifying BFD for MPLS | MPLS session procedures |
| RFC 9127 | YANG Data Model for BFD | Configuration model reference |
| RFC 9355 | OSPF BFD Strict-Mode | Requires OSPF daemon integration (deferred) |

### RFC Source Files

Full RFC text files are available in the `docs/rfc/` directory:

| File | Size |
|---|---|
| [rfc5880.txt](../rfc/rfc5880.txt) | 110 KB |
| [rfc5881.txt](../rfc/rfc5881.txt) | 14 KB |
| [rfc5882.txt](../rfc/rfc5882.txt) | 40 KB |
| [rfc5883.txt](../rfc/rfc5883.txt) | 12 KB |
| [rfc5884.txt](../rfc/rfc5884.txt) | 28 KB |
| [rfc5885.txt](../rfc/rfc5885.txt) | 31 KB |
| [rfc7130.txt](../rfc/rfc7130.txt) | 21 KB |
| [rfc7419.txt](../rfc/rfc7419.txt) | 12 KB |
| [rfc9985.txt](../rfc/rfc9985.txt) | 45 KB |
| [rfc9986.txt](../rfc/rfc9986.txt) | 71 KB |

### Related Documents

- [02-protocol.md](./02-protocol.md) -- BFD protocol details (FSM, timers, packet format)
- [01-architecture.md](./01-architecture.md) -- System architecture
- [05-interop.md](./05-interop.md) -- Interoperability testing

---

*Reviewed against `dev` source: 2026-10-25.*
