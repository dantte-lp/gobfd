# RFC Compliance

![RFC 5880](https://img.shields.io/badge/RFC_5880-Implemented-34a853?style=for-the-badge)
![RFC 5881](https://img.shields.io/badge/RFC_5881-Implemented-34a853?style=for-the-badge)
![RFC 5882](https://img.shields.io/badge/RFC_5882-Implemented-34a853?style=for-the-badge)
![RFC 5883](https://img.shields.io/badge/RFC_5883-Implemented-34a853?style=for-the-badge)
![RFC 9384](https://img.shields.io/badge/RFC_9384-Implemented-34a853?style=for-the-badge)
![RFC 5884](https://img.shields.io/badge/RFC_5884-Stub-ffc107?style=for-the-badge)
![RFC 7130](https://img.shields.io/badge/RFC_7130-Stub-ffc107?style=for-the-badge)

> RFC compliance matrix, per-section implementation notes, design rationale, and links to RFC source texts.

---

### Table of Contents

- [Compliance Matrix](#compliance-matrix)
- [RFC 5880 Implementation Notes](#rfc-5880-implementation-notes)
- [RFC 5881 Implementation Notes](#rfc-5881-implementation-notes)
- [RFC 5882 Implementation Notes](#rfc-5882-implementation-notes)
- [RFC 5883 Implementation Notes](#rfc-5883-implementation-notes)
- [Stub Interfaces](#stub-interfaces)
- [Reference RFCs](#reference-rfcs)
- [RFC Source Files](#rfc-source-files)

### Compliance Matrix

| RFC | Title | Status | Notes |
|---|---|---|---|
| [RFC 5880](https://datatracker.ietf.org/doc/html/rfc5880) | BFD Base Protocol | **Implemented** | FSM, packet codec, auth, timers, jitter, Poll/Final |
| [RFC 5881](https://datatracker.ietf.org/doc/html/rfc5881) | BFD for IPv4/IPv6 Single-Hop | **Implemented** | UDP 3784, TTL=255, `SO_BINDTODEVICE` |
| [RFC 5882](https://datatracker.ietf.org/doc/html/rfc5882) | Generic Application of BFD | **Implemented** | GoBGP integration, flap dampening |
| [RFC 5883](https://datatracker.ietf.org/doc/html/rfc5883) | BFD for Multihop Paths | **Implemented** | UDP 4784, TTL>=254 check |
| [RFC 9384](https://datatracker.ietf.org/doc/html/rfc9384) | BGP Cease NOTIFICATION for BFD | **Implemented** | Cease/10 subcode in shutdown communication |
| [RFC 5884](https://datatracker.ietf.org/doc/html/rfc5884) | BFD for MPLS LSPs | **Stub** | Interfaces defined, pending LSP Ping (RFC 4379) |
| [RFC 5885](https://datatracker.ietf.org/doc/html/rfc5885) | BFD for PW VCCV | **Stub** | Interfaces defined, pending VCCV/LDP |
| [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130) | Micro-BFD for LAG | **Stub** | Per-member-link sessions planned |

> Echo Mode (RFC 5880 Section 6.4) and Demand Mode (RFC 5880 Section 6.6) are intentionally not implemented. Asynchronous mode covers the primary use case of BFD-assisted failover in ISP/DC environments.

### RFC 5880 Implementation Notes

#### Section 4.1: BFD Control Packet Format

**Implementation**: [`internal/bfd/packet.go`](../../internal/bfd/packet.go)

The 24-byte mandatory header is encoded/decoded using `encoding/binary.BigEndian` directly on a caller-owned byte buffer. No reflection, no `unsafe`, no gopacket. Zero-allocation codec using `sync.Pool` for buffers.

See [02-protocol.md](./02-protocol.md) for the complete packet format table.

#### Section 6.1: State Variables

**Implementation**: [`internal/bfd/session.go`](../../internal/bfd/session.go)

All mandatory state variables are implemented. Thread safety via `atomic.Uint32` for state fields that are read by the gRPC server goroutine.

See [02-protocol.md](./02-protocol.md#state-variables) for the full variable mapping table.

#### Section 6.2: Overview (FSM)

**Implementation**: [`internal/bfd/fsm.go`](../../internal/bfd/fsm.go)

Table-driven FSM with `map[stateEvent]transition`. Pure function -- no side effects. All 16 transitions from Section 6.8.6 are implemented.

#### Section 6.3: Demultiplexing

**Implementation**: [`internal/bfd/manager.go`](../../internal/bfd/manager.go)

Two-tier demultiplexing:
- Tier 1: O(1) lookup by Your Discriminator (fast path)
- Tier 2: Composite key (SrcIP, DstIP, Interface) for session establishment

#### Section 6.5: Poll Sequences

**Implementation**: `session.go` (`pollActive`, `pendingFinal`, `terminatePollSequence`)

Only one Poll Sequence active at a time. Pending parameter changes are applied only after receiving the Final bit.

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
- Meticulous variants increment sequence on every packet; non-meticulous on state change only
- Sequence window: `3 * DetectMult` for non-meticulous
- `AuthKeyStore` supports multiple keys for hitless rotation

#### Section 6.8.6: Reception of BFD Control Packets

Validation split across two layers:

| Layer | Steps | File |
|---|---|---|
| Codec | 1-7 (stateless) | `packet.go` |
| Session | 8-18 (stateful) | `session.go` |

Steps 1-7 (codec): version, length, detect mult, multipoint, discriminators.
Steps 8-18 (session): auth consistency, auth verification, state variable update, FSM event, timer reset.

#### Section 6.8.7: Jitter

**Implementation**: `bfd.ApplyJitter`

- Normal (DetectMult > 1): 75-100% of interval
- DetectMult == 1: 75-90% of interval
- Uses `math/rand/v2` (not security-sensitive, hot path)

#### Section 6.8.16: Administrative Control

Graceful shutdown sends AdminDown with Diag=7, waits 2x TX interval, then cancels goroutines. This prevents false positive detection timeouts on remote peers.

#### Not Implemented (RFC 5880)

| Section | Feature | Rationale |
|---|---|---|
| 6.4 | Echo Mode | Requires kernel cooperation; low benefit for BFD-BGP use case |
| 6.6 | Demand Mode | Rarely used; interval tuning achieves same goal |
| 4.1 | Multipoint bit | Reserved for future P2MP extensions |

### RFC 5881 Implementation Notes

**Implementation**: [`internal/netio/`](../../internal/netio/)

| Requirement | Implementation |
|---|---|
| Destination port 3784 | `netio.PortSingleHop = 3784` |
| Source port 49152-65535 | `SourcePortAllocator` |
| TTL=255 outgoing | `ipv4.SetTTL(255)` via `x/net/ipv4` |
| TTL=255 incoming check | `IP_RECVTTL` + check in listener |
| `SO_BINDTODEVICE` | Applied when interface is specified |
| Separate IPv4/IPv6 listeners | Separate `ipv4.PacketConn` / `ipv6.PacketConn` |

### RFC 5882 Implementation Notes

**Implementation**: [`internal/gobgp/`](../../internal/gobgp/)

- Section 3.2 (Flap Dampening): `dampening.go` implements penalty-based dampening with configurable thresholds
- Section 4.3 (BFD for BGP): `handler.go` watches BFD state changes and calls GoBGP gRPC API
  - BFD Down --> `DisablePeer()` (or `DeletePath()` per strategy)
  - BFD Up --> `EnablePeer()` (or `AddPath()`)

### RFC 5883 Implementation Notes

| Requirement | Implementation |
|---|---|
| Destination port 4784 | `netio.PortMultiHop = 4784` |
| TTL=255 outgoing | Same as single-hop |
| TTL>=254 incoming check | Separate TTL validation for multihop |
| Demux by (MyDiscr, SrcIP, DstIP) | Manager.DemuxWithWire composite key |

### RFC 9384 Implementation Notes

**Implementation**: [`internal/gobgp/rfc9384.go`](../../internal/gobgp/rfc9384.go)

RFC 9384 defines Cease NOTIFICATION subcode 10 ("BFD Down") for BGP sessions torn down due to BFD failure.

| Requirement | Implementation |
|---|---|
| Cease subcode 10 (BFD Down) | `CeaseSubcodeBFDDown = 10` constant |
| NOTIFICATION on BFD failure | `FormatBFDDownCommunication()` enriches the DisablePeer communication |
| Diagnostic context | BFD `Diag` code included in the communication string |

**Limitation**: GoBGP v3 does not expose per-subcode control in its `DisablePeer` API — it sends Cease subcode 2 (Administrative Shutdown) with the communication string per RFC 8203. The communication string is enriched with `"BFD Down (RFC 9384 Cease/10): diag=..."` so that operators can identify BFD-triggered shutdowns in logs and monitoring systems. Full subcode 10 support requires upstream GoBGP changes.

### Stub Interfaces

The following RFCs have stub interfaces defined for future implementation:

| RFC | Dependency | Status |
|---|---|---|
| RFC 5884 (BFD for MPLS) | LSP Ping (RFC 4379) | Interfaces defined in `internal/bfd` |
| RFC 5885 (BFD for VCCV) | VCCV (RFC 5085), LDP (RFC 4447) | Interfaces defined |
| RFC 7130 (Micro-BFD for LAG) | Per-member-link sessions | `SO_BINDTODEVICE` per member ready |

### Reference RFCs

These RFCs are referenced but not directly implemented:

| RFC | Title | Relevance |
|---|---|---|
| RFC 8203 | BGP Administrative Shutdown | Communication string for DisablePeer |
| RFC 5082 | GTSM | Basis for TTL=255 requirement |
| RFC 4379 | LSP Ping | Dependency of RFC 5884 |
| RFC 5085 | VCCV | Dependency of RFC 5885 |
| RFC 4447 | LDP | Dependency of RFC 5885 |
| RFC 7419 | Common Interval Support | Interval negotiation guidance |
| RFC 7726 | Clarifying BFD for MPLS | MPLS session procedures |
| RFC 9127 | YANG Data Model for BFD | Configuration model reference |

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

### Related Documents

- [02-protocol.md](./02-protocol.md) -- BFD protocol details (FSM, timers, packet format)
- [01-architecture.md](./01-architecture.md) -- System architecture
- [05-interop.md](./05-interop.md) -- Interoperability testing

---

*Last updated: 2026-02-21*
