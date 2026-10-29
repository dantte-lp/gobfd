# RFC Compliance

[Russian translation](../ru/08-rfc-compliance.md).

This matrix describes source behavior on `dev`, not stable-release acceptance. An implemented function, a local fixture, an actual UDP/capture result and acceptance of the final candidate are separate evidence levels. [Interop](05-interop.md) and [performance](14-performance-analysis.md) retain qualification procedures and measurements; final-candidate qualification remains separate.

BFD means Bidirectional Forwarding Detection; FSM means finite state machine. TX and RX denote transmission and reception. Generalized TTL Security Mechanism (GTSM) constrains received hop counts. PDU means protocol data unit, and MTU means maximum transmission unit. Seamless BFD (S-BFD), MPLS label-switched paths (LSPs), pseudowires (PWs) and Virtual Circuit Connectivity Verification (VCCV) have separate profiles.

## Compliance Matrix

| RFC | Profile | Source status | Qualification boundary |
|:---|:---|:---|:---|
| [5880](../rfc/rfc5880.txt) | Base protocol | Asynchronous core; partial | Complete Poll/Demand, affiliated Echo and final qualification open |
| [5881](../rfc/rfc5881.txt) | IPv4/IPv6 single-hop | Numbered multiaccess; partial | Point-to-point initial demux and native architecture gates open |
| [5882](../rfc/rfc5882.txt) | Client application | Partial integration | State delivery and external convergence measured separately |
| [5883](../rfc/rfc5883.txt) | Multihop | Constrained GTSM product profile | Real routed 2/5-hop qualification open |
| [7419](../rfc/rfc7419.txt) | Common intervals | Helpers and optional alignment implemented | No universal hardware/cloud minimum inferred |
| [9384](../rfc/rfc9384.txt) | BGP Cease/10 | Not implemented | GoBGP action emits Cease/2 |
| [9468](../rfc/rfc9468.txt) | Unsolicited BFD | Unsafe default-off preview | Admission limits do not make untrusted exposure safe |
| [9747](../rfc/rfc9747.txt) | Unaffiliated Echo | Default-off preview | FSM, demux, authentication, pacing and forwarding gaps |
| [7130](../rfc/rfc7130.txt) | Per-member Micro-BFD | Preview; selected actuators | Dedicated member wire/MAC and owner interoperability open |
| [8971](../rfc/rfc8971.txt) | VXLAN BFD | Incomplete userspace preview | Exact IPv4 profile; owner-specific dataplane integration open |
| [9521](../rfc/rfc9521.txt) | Geneve BFD | Incomplete userspace preview | IPv4 Format A only; owner-specific integration open |
| [9764](../rfc/rfc9764.txt) | Large packets | Partial | Auth vectors and scoped padded TX; path MTU/final gates separate |
| [7880](https://www.rfc-editor.org/rfc/rfc7880.html), [7881](https://www.rfc-editor.org/rfc/rfc7881.html) | S-BFD | Planned | No reflector, initiator or UDP/7784 listener |
| [5884](../rfc/rfc5884.txt), [5885](../rfc/rfc5885.txt) | MPLS LSP/PW VCCV | Not implemented | No session implementation or stub API |

The five preview profiles are explicitly reported by [CapabilityService](../../internal/server/capabilities.go). Maturity, enablement and local readiness are independent. `ready` requires an owned receive path and source/runtime/binding readiness; it does not prove peer health, dataplane ownership or RFC compliance. Missing runtime data stays unknown.

## RFC 5880 Implementation Notes

### Packet, state and receive boundaries

| Section | Behavior | Repository source |
|:---|:---|:---|
| 4.1 | 24-byte fixed header, validated auth shape, M=1 rejection | [packet.go](../../internal/bfd/packet.go) |
| 6.1–6.2 | Packed local pair and coherent committed observation; pure FSM | [session.go](../../internal/bfd/session.go), [fsm.go](../../internal/bfd/fsm.go) |
| 6.3 | Discriminator/initial-key lookup followed by exact transport checks | [manager_session.go](../../internal/bfd/manager_session.go) |
| 6.7 | Five auth types, immutable wire verification, bounded inline payload | [auth.go](../../internal/bfd/auth.go), [ADR-0011](adr/0011-bfd-auth-inline-payload.md) |
| 6.8.3–6.8.4 | Directional timing, one-second non-Up floor, receive expiry | [session_packet.go](../../internal/bfd/session_packet.go), [session_loop.go](../../internal/bfd/session_loop.go) |
| 6.8.6 | Codec validation, Manager admission, owner auth and FSM | [receive flow](01-architecture.md#packet-rx-flow) |
| 6.8.7 | 75–100% jitter, or 75–90% with local multiplier 1 | [jitter](02-protocol.md#jitter) |
| 6.8.12 | DetectMult joins the five-field reload tuple | [session_update.go](../../internal/bfd/session_update.go) |

The selected Held-errata 5205/7240 policy sets diagnostic 1 on a Down timeout without a state edge and preserves diagnostic across recovery to Up. AdminDown/7 survives local PathDown. These are explicit project policies; Held for Document Update is not an amended normative RFC requirement. Sources: [FSM](../../internal/bfd/fsm.go), [owner](../../internal/bfd/session_loop.go), [RFC errata record](https://www.rfc-editor.org/errata/rfc5880).

### Poll Sequences and reload

One active advertised tuple and the latest queued tuple serialize TX, RX, DetectMult, MinTTL and PaddedPduSize. In Up, TX increases and RX decreases wait for the relevant Final; reverse directions apply on activation. Final cannot confirm a Poll that was not successfully sent. Crossed Poll, Final retry, P/F exclusion and transaction separation retain independent tests and wire checks.

Detect-only asynchronous changes need no Poll; remote Demand can require it. TTL is local product policy and never needs Poll. Positive-to-positive padding updates require unchanged auth, sole config ownership, asynchronous mode and immutable constructor proof of DF preparation. Auth transitions are rejected before generation acceptance against retained intent and live owners. Sources: [ADR-0006](adr/0006-session-preserving-reload-policy.md), [owner transactions](../../internal/bfd/session_update.go), [admission](../../internal/bfd/manager_auth_reload.go).

Scoped DetectMult, TTL and padding receipts remain historical slice evidence. The unified final-candidate gate follows remaining production Session/Echo thread and scale work. General Demand, deployed RTT-envelope qualification, forwarding continuity and stable acceptance do not follow from those slices.

### Administrative control

AdminDown drain confirms owner commit, the first successful permitted write and elapsed negotiated window within one ten-second budget. The window covers both directional bounds; passive unknown-peer and remote RX=0 suppression remain in force. Final replies can supply the first write. Last-claim release drains outside Manager locks and retains the exact key through cleanup. Unsolicited failure cleanup is the separate stop-TX path. Sources: [drain](../../internal/bfd/session_drain.go), [replacement](../../internal/bfd/manager_replace.go), [RFC 5880 §6.8.16](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.16).

The dated SIGTERM receipt in `reports/h12-wire-20261016/` records eight AdminDown/7 packets, four per FRR/BIRD peer, with TTL 255, multiplier 3, advertised TX 1000000 µs and RX 300000 µs. First-wire-to-drain times are 3.000171 s and 3.000147 s. Forty samples over 20.280366 s retain BGP Established and the two test routes; gaps between samples remain unobserved. Input is source patch SHA256 `d1394d09fb3b902d3e34a5e34467c5aa4c506bd1d71c49416f6176773dc86947` over commit `e4f3761a0f1fc59fbf9b2f859923a81125d157bf`; PCAP SHA256 `f10937cc6a7969bc336e4f1b5217d2f458e5f046236f68530225ef26ec281c68`. This is historical local evidence, not a current candidate result.

### Not implemented

Local D remains zero. Affiliated Echo is absent and Control Required Min Echo RX remains zero. Remote-Demand periodic suppression and crafted-peer timer Poll are partial support. P2MP is absent. See [protocol limits](02-protocol.md#not-implemented).

## RFC 5881 Implementation Notes

[UDP sender](../../internal/netio/sender.go), [listener](../../internal/netio/listener.go) and [Manager admission](../../internal/bfd/manager_session.go) implement destination 3784, a fixed source port in 49152–65535, TX TTL/Hop Limit 255 and per-session RX=255 enforcement. Separate IPv4/IPv6 sockets retain packet-info destination and ingress interface.

Single-hop RX checks the configured interface or the interface owning the local address at creation. Missing/ambiguous local ownership leaves configuration pending; API creation returns `FailedPrecondition`. Loopback/dummy local addresses need explicit physical `interface` when packets arrive there. Point-to-point initial demux accepting any remote source is deferred.

IPv6 link-local zones select and must agree with the interface; zones are excluded from the initial key. Linux TTL/Hop Limit and interface indices use native-endian ancillary values; IP addresses retain network order. The shared sender's Echo source-port restriction is product policy: RFC 5881 does not specify Echo source port. Native `linux/amd64` and `linux/arm64` kernel qualification remains required; cross-compilation cannot close it.

## RFC 5882 Implementation Notes

[GoBGP handler](../../internal/gobgp/handler.go) disables on ordinary remote Down, detection failure or explicit local PathDown. Local or known remote AdminDown does not disable; retained remote AdminDown survives later timeout diagnostics. Up enables only peers disabled by this process. Each action uses `gobgp.action_timeout`.

Penalty dampening is implementation policy under §3.1, not an RFC-prescribed algorithm. Generic route withdrawal/restoration is absent; configured `withdraw-routes` fails validation. Watch delivery, external action success and routing convergence need independent receipts. Sources: [integration](../../internal/gobgp), [configuration validation](../../internal/config/config.go), [RFC 5882 §§3.2, 4.2, 10.2](https://www.rfc-editor.org/rfc/rfc5882.html#section-3.2).

## RFC 5883 Implementation Notes

Multihop uses UDP/4784 and address-pair initial demux; an explicit interface is rejected. TX TTL remains 255. Per-session `multihop_max_hops=N` accepts received TTL/Hop Limit ≥255−N: default N=1 gives 254; N=2 gives 253; N=5 gives 250. This is GoBFD's constrained GTSM policy, not an RFC 5883 receive-TTL requirement. Wrong transport class is rejected before delivery.

[RFC 5883 §§3, 6](https://www.rfc-editor.org/rfc/rfc5883.html#section-3) prohibits multihop Echo and recommends cryptographic authentication. Configure authentication for multihop; the TTL floor only adds defence in depth. Prefer the supported meticulous SHA1 mode where peer policy permits. Actual routed/cloud paths need separate qualification. Sources: [Manager](../../internal/bfd/manager_session.go), [config](../../internal/config/config.go), [ADR-0006](adr/0006-session-preserving-reload-policy.md).

## RFC 7419 Implementation Notes

[Interval helpers](../../internal/bfd/intervals.go) provide six common intervals: 3300, 10000, 20000, 50000, 100000 and 1000000 microseconds. Alignment rounds upward in the set and preserves values above its maximum. The separate graceful-restart interval is 10 s. Common values establish neither timer precision nor hardware/provider acceptance. Verify the [target peer profile](reference/vendor-bfd.md).

## RFC 9384 Implementation Notes

[GoBGP adapter](../../internal/gobgp/rfc9384.go) labels communication text with Cease/10 but the current DisablePeer path emits Administrative Shutdown, Cease/2. A string containing the RFC identifier does not implement wire subcode 10. Full support requires an action API and capture proving that subcode.

## RFC 9468 Implementation Notes

| Admission condition | Product behavior | Source |
|:---|:---|:---|
| Opt-in/interface | Disabled by default; explicit nonempty interfaces with device-bound dual-stack listeners | [config](../../internal/config/config.go), [listeners](../../cmd/gobfd/listeners.go) |
| Peer/path | TTL 255, single-hop listener, source in receiving local subnet; optional narrower prefixes | [unsolicited](../../internal/bfd/unsolicited.go) |
| Capacity/rate | Enabled `max_sessions≥1`; 10 admissions/s, burst 20 | [Manager](../../internal/bfd/manager.go) |
| Auth | Optional Meticulous Keyed SHA1 trigger verification; authenticated packets refused without policy | [admission tests](../../internal/bfd/unsolicited_auth_test.go) |
| Sender/cleanup | Own receiving local tuple/interface and source-port lease; stop non-Final TX after natural failure/setup expiry | [owner](../../internal/bfd/session_loop.go), [cleanup](../../internal/bfd/unsolicited.go) |

Unnumbered admission fails closed. Quota reserves atomically and releases on failure/destroy/cleanup. Repeated unchanged packets do not restart the independent setup budget. Shared API/config claims retain ordinary Control TX. Cleanup fences the actual failed attempt; it cannot retire a resumed attempt or local AdminDown. Final responses and administrative notification retain base-protocol rules.

The dual-stack/78-lease receipt does not graduate unsafe preview. Do not expose unsolicited mode on an untrusted interface.

## RFC 9747 Implementation Notes

The [Echo owner](../../internal/bfd/echo.go) and [receiver](../../internal/netio/echo_receiver.go) implement port 3785, standard Control-shaped packets, local timers, Down↔Up, My Discriminator return demux, TX TTL 255/RX exactly 254 and timeout diagnostic 2.

The RFC requires a different bootstrap and subsequent Your Discriminator demux, the base FSM without AdminDown, slow pacing and suitable return validation/authentication. The current send path also does not establish adjacent forwarding-plane traversal. Qualification must close FSM, returned-packet identity/authentication and pacing gaps.

Every Add, including a retry, requires preview enablement and local RX readiness. Compatible principals share a wire under [ADR-0009](adr/0009-echo-principal-claims.md); management ownership does not fix protocol gaps. Owner witnesses and conditional load assessment in [ADR-0012](adr/0012-owner-down-transition-witness.md) do not qualify RFC 9747.

## RFC 7130 Implementation Notes

One member/family session uses UDP/6784 and member-device binding. `MicroBFDGroup` counts only Up members; aggregate becomes Up at `upCount≥MinActiveLinks`. A shared existing session seeds initial group state. Group/source claims are reconciled with their member sessions; crossing the configured threshold as the number of Up members changes publishes aggregate transitions.

The actuator receives ordered member changes outside Manager locks with a five-second deadline. Modes are `disabled`, `dry-run`, `enforce`; enforcement requires explicit backend and owner policy. Kernel-bond sysfs, native OVSDB and NetworkManager D-Bus paths exist; the OVS CLI fallback is not daemon-selected. Dedicated multicast MAC/member-wire and real-owner qualification remain separate. Sources: [group](../../internal/bfd/micro.go), [daemon wiring](../../cmd/gobfd/micro_bfd.go), [actuator](../../internal/netio/lag_backend.go), [ADR-0003](adr/0003-linux-advanced-bfd-applicability.md).

## RFC 8971 Implementation Notes

VXLAN preview uses outer UDP/4789, an eight-byte VXLAN header with I flag, Management VXLAN network identifier (VNI) and inner Ethernet/IPv4/UDP/3784/BFD. Inner destination MAC is `00:52:02:00:00:00`; originating local/remote source MACs and full endpoint/VNI/IP tuple must match the configured scope.

Only `userspace-udp` is implemented. One listener per local VXLAN tunnel endpoint (VTEP) shares exact scopes and physical source-port ownership. Reserved kernel/OVS/OVN/Cilium/Calico/NSX backends fail closed. A socket owned by another dataplane is outside this profile. Sources: [VXLAN connection](../../internal/netio/vxlan_conn.go), [inner validation](../../internal/netio/overlay_inner.go), [backend admission](../../internal/netio/overlay_backend.go), [overlay ownership](../../cmd/gobfd/overlay_owner.go).

## RFC 9521 Implementation Notes

Geneve preview uses outer UDP/6081, O=1, C=0, version 0, Protocol Type `0x6558`, VNI and explicit IPv4 Format A Virtual Access Point (VAP) IP/MAC identity. Inner Ethernet/IPv4/UDP/3784/BFD is validated before discriminator lookup. Missing/special-use VAP identity, IPv6 inner payloads, Format B and other protocol types fail closed.

Only `userspace-udp` is implemented; socket-sharing dataplane integration is absent. Send/receive cancellation must not close a healthy shared listener; Close unblocks I/O, payload borrows the receive buffer until the next read. Rates and congestion exposure require a target profile. Sources: [Geneve connection](../../internal/netio/geneve_conn.go), [inner validation](../../internal/netio/overlay_inner.go), [ADR-0003](adr/0003-linux-advanced-bfd-applicability.md).

## RFC 9764 Implementation Notes

The target counts BFD transport-payload bytes, excluding IP/UDP headers. Zero disables padding; valid nonzero ranges are 24–9000 bytes without auth and 26–9000 bytes with auth. Rejecting authenticated targets of 24/25 bytes is approved product policy; a target below the actual authenticated PDU never truncates it. TX uses `max(actual PDU, target)`, zero fills padding and prepares IPv4 DF at sender construction.

Hash verification covers exactly the declared BFD Length, including in-PDU extension bytes, and excludes transport padding. Padding contents are not validated on receive. Local independent vectors cover five authentication types with unpadded, 64-, 128- and 9000-byte datagrams. The direct receive pool accepts through 9000 bytes and rejects Linux `MSG_TRUNC`; codec `MaxPacketSize=64` is not the datagram bound. Overlay buffers allow framing, not a larger inner BFD payload: VXLAN 9090 bytes, Geneve 9342 bytes.

[Critical BASE interop](05-interop.md) covers 128-byte padded GoBFD TX toward FRR Simple Password and BIRD Keyed SHA1, BFD Length, zero padding and DF. Reverse peer TX is ordinary authenticated PDU; it does not qualify padded peer-originated traffic or path MTU. Positive-to-positive reload and the dated padding slices follow [ADR-0006](adr/0006-session-preserving-reload-policy.md). Enable/disable/dynamic DF remains outside the accepted reload profile; unified exact-candidate gate remains required.

Sources: [padding policy](../../internal/config/padding.go), [auth vectors](../../internal/bfd/auth_padding_test.go), [receive bounds](../../internal/netio/receive_size_test.go), [sender](../../internal/netio/sender.go), [RFC 9764 §3](https://www.rfc-editor.org/rfc/rfc9764.html#section-3).

## RFC 7880/7881 (Planned)

No S-BFD reflector, initiator or port-7784 listener exists. Retained plans and RFC links are not APIs. A future implementation needs independent discriminator, packet and ownership contracts.

## Unimplemented MPLS and VCCV

RFC 5884/5885 remain reference scope. No MPLS/VCCV session or stub interface is implemented. MPLS LSP Ping and PW signaling integration require separate work.

## Reference RFCs

RFC 5082 supplies GTSM context; RFC 8203 supplies administrative shutdown communication. RFC 9127 is a management-model reference. OSPF strict mode from RFC 9355 requires an OSPF integration not implemented here. Sources and applicability are linked from [protocol](02-protocol.md), [GoBGP adapter](../../internal/gobgp/rfc9384.go) and [vendor references](reference/vendor-bfd.md).

## RFC Source Files

The [local RFC archive](../rfc/) retains standards texts. Presence of RFC 9985/9986 or other texts does not establish implementation. Prefer section-specific references over an unqualified compliance label.

## Related Documents

- [Architecture](01-architecture.md) — physical ownership, delivery and retirement.
- [Protocol](02-protocol.md) — packet and timer reference.
- [ADR index](adr/README.md) — accepted policy and separate qualification.
