# RFC Compliance

[![RFC 5880](https://img.shields.io/badge/RFC_5880-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5880)
[![RFC 5881](https://img.shields.io/badge/RFC_5881-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5881)
[![RFC 5882](https://img.shields.io/badge/RFC_5882-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5882)
[![RFC 5883](https://img.shields.io/badge/RFC_5883-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5883)
[![RFC 7419](https://img.shields.io/badge/RFC_7419-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7419)
[![RFC 9384](https://img.shields.io/badge/RFC_9384-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9384)
[![RFC 9468](https://img.shields.io/badge/RFC_9468-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9468)
[![RFC 9747](https://img.shields.io/badge/RFC_9747-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9747)
[![RFC 7130](https://img.shields.io/badge/RFC_7130-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7130)
[![RFC 8971](https://img.shields.io/badge/RFC_8971-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc8971)
[![RFC 9521](https://img.shields.io/badge/RFC_9521-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9521)
[![RFC 5884](https://img.shields.io/badge/RFC_5884-Stub-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5884)

> RFC compliance matrix, per-section implementation notes, design rationale, and links to RFC source texts.

---

### Table of Contents

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
| [RFC 7419](https://datatracker.ietf.org/doc/html/rfc7419) | Common Interval Support | **Implemented** | 6 common intervals, optional alignment |
| [RFC 9384](https://datatracker.ietf.org/doc/html/rfc9384) | BGP Cease NOTIFICATION for BFD | **Implemented** | Cease/10 subcode in shutdown communication |
| [RFC 9468](https://datatracker.ietf.org/doc/html/rfc9468) | Unsolicited BFD | **Implemented** | Passive session auto-creation, per-interface policy |
| [RFC 9747](https://datatracker.ietf.org/doc/html/rfc9747) | Unaffiliated BFD Echo | **Implemented** | Echo session, DiagEchoFailed, UDP 3785 |
| [RFC 5884](https://datatracker.ietf.org/doc/html/rfc5884) | BFD for MPLS LSPs | **Stub** | Interfaces defined, pending LSP Ping (RFC 4379) |
| [RFC 5885](https://datatracker.ietf.org/doc/html/rfc5885) | BFD for PW VCCV | **Stub** | Interfaces defined, pending VCCV/LDP |
| [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130) | Micro-BFD for LAG | **Implemented** | Per-member-link sessions, aggregate state, UDP 6784 |
| [RFC 8971](https://datatracker.ietf.org/doc/html/rfc8971) | BFD for VXLAN Tunnels | **Implemented** | VXLAN encap/decap, Management VNI, inner port 3784 |
| [RFC 9521](https://datatracker.ietf.org/doc/html/rfc9521) | BFD for Geneve Tunnels | **Implemented** | Geneve encap/decap, O bit control, Ethernet/IP payloads |

> Traditional Echo Mode (RFC 5880 Section 6.4, affiliated with a control session) and Demand Mode (RFC 5880 Section 6.6) are intentionally not implemented. Asynchronous mode covers the primary use case of BFD-assisted failover in ISP/DC environments. Unaffiliated echo (RFC 9747) is implemented as a standalone forwarding-path test without requiring a control session.

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
| 6.4 | Affiliated Echo Mode | Requires control session; RFC 9747 unaffiliated echo implemented instead |
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

### RFC 7419 Implementation Notes

**Implementation**: [`internal/bfd/intervals.go`](../../internal/bfd/intervals.go)

RFC 7419 defines a set of common BFD timer interval values to ensure interoperability between software-based and hardware-based implementations.

| Common Interval | Use Case |
|---|---|
| 3.3 ms | MPLS-TP (GR-253-CORE) |
| 10 ms | General consensus minimum |
| 20 ms | Software-based minimum |
| 50 ms | Widely deployed |
| 100 ms | G.8013/Y.1731 reuse |
| 1 s | RFC 5880 slow rate |

Additionally, 10s is recommended for graceful restart (multiplier 255 = 42.5 min timeout).

| Feature | Implementation |
|---|---|
| Common interval set | `CommonIntervals` array (6 values) |
| Align to common interval | `AlignToCommonInterval()` — rounds UP |
| Check if common | `IsCommonInterval()` |
| Nearest common interval | `NearestCommonInterval()` |
| Config option | `bfd.align_intervals: true` in YAML config |
| Graceful restart interval | `GracefulRestartInterval = 10s` |

When `bfd.align_intervals` is enabled, `DesiredMinTxInterval` and `RequiredMinRxInterval` are aligned to the nearest common interval (rounded up) during session creation. This prevents negotiation mismatches with hardware BFD implementations from Arista, Nokia, Juniper, and Cisco.

### RFC 9384 Implementation Notes

**Implementation**: [`internal/gobgp/rfc9384.go`](../../internal/gobgp/rfc9384.go)

RFC 9384 defines Cease NOTIFICATION subcode 10 ("BFD Down") for BGP sessions torn down due to BFD failure.

| Requirement | Implementation |
|---|---|
| Cease subcode 10 (BFD Down) | `CeaseSubcodeBFDDown = 10` constant |
| NOTIFICATION on BFD failure | `FormatBFDDownCommunication()` enriches the DisablePeer communication |
| Diagnostic context | BFD `Diag` code included in the communication string |

**Limitation**: GoBGP v3 does not expose per-subcode control in its `DisablePeer` API — it sends Cease subcode 2 (Administrative Shutdown) with the communication string per RFC 8203. The communication string is enriched with `"BFD Down (RFC 9384 Cease/10): diag=..."` so that operators can identify BFD-triggered shutdowns in logs and monitoring systems. Full subcode 10 support requires upstream GoBGP changes.

### RFC 9468 Implementation Notes

**Implementation**: [`internal/bfd/unsolicited.go`](../../internal/bfd/unsolicited.go), [`internal/bfd/manager.go`](../../internal/bfd/manager.go)

RFC 9468 enables one BFD endpoint to dynamically create passive sessions in response to incoming BFD Control packets, without per-session configuration. Useful for static route next-hop tracking and IXP route-server deployments.

| Requirement | Implementation |
|---|---|
| Disabled by default (MUST) | `unsolicited.enabled: false` default |
| Per-interface policy (MUST) | `UnsolicitedInterfaceConfig` per interface |
| Source address validation (MUST) | `AllowedPrefixes` ACL check |
| Single-hop only (MUST) | `SessionTypeSingleHop` enforced |
| Local discriminator allocation (MUST) | `DiscriminatorAllocator` for passive sessions |
| Configurable timers (SHOULD) | `UnsolicitedSessionDefaults` |
| Max session limit | `MaxSessions` prevents resource exhaustion |
| Session cleanup on Down (SHOULD) | `CleanupTimeout` configuration |

Auto-creation happens in `Manager.demuxByPeer()` when an incoming packet matches no existing session and unsolicited BFD is enabled for the receiving interface. The passive session is created with `RolePassive` and immediately receives the triggering packet.

### RFC 9747 Implementation Notes

**Implementation**: [`internal/bfd/echo.go`](../../internal/bfd/echo.go)

RFC 9747 defines the unaffiliated BFD echo function for forwarding-path liveness detection without requiring the remote system to run BFD. The local system sends BFD Control packets (echo packets) to the remote, which forwards them back via normal IP routing.

| Requirement | Implementation |
|---|---|
| UDP port 3785 | `netio.PortEcho = 3785` |
| Standard BFD Control packet format | Reuses `MarshalControlPacket` codec |
| DiagEchoFailed on timeout | `DiagEchoFailed` (value 2) |
| Locally provisioned timers | `EchoSessionConfig.TxInterval`, no negotiation |
| Two-state FSM (Up/Down) | Simplified FSM in `EchoSession` |
| DetectionTime = DetectMult * TxInterval | `EchoSession.DetectionTime()` |
| Demux by MyDiscriminator on return | Echo listener matches returned packets |
| Session type | `SessionTypeEcho` constant |
| TTL 255 send, TTL >= 254 receive | GTSM validation via `netio.ValidateTTL` |

Key differences from BFD control sessions:
- No three-way handshake (no Init state)
- No timer negotiation with remote (locally provisioned)
- No authentication (echo packets are self-originated)
- Separate `EchoSession` type with simplified FSM

### RFC 7130 Implementation Notes

**Implementation**: [`internal/bfd/micro.go`](../../internal/bfd/micro.go)

RFC 7130 defines Micro-BFD — independent BFD sessions on every LAG member link for per-link forwarding verification with faster detection than LACP alone.

| Requirement | Implementation |
|---|---|
| UDP port 6784 | `netio.PortMicroBFD = 6784` |
| One BFD session per member link | `MicroBFDGroup.members` map |
| Aggregate state tracking | `upCount >= minActive` threshold |
| Member removed on BFD Down | `UpdateMemberState()` triggers aggregate change |
| Dedicated multicast MAC | `01-00-5E-90-00-01` for initial packets |
| Asynchronous mode only | Standard RFC 5880 procedures per member |
| Session type | `SessionTypeMicroBFD` constant |
| Per-group configuration | `MicroBFDConfig` with LAG interface + member links |

Aggregate state logic:
- Group starts with all members Down, aggregate Down
- When `upCount >= MinActiveLinks`, aggregate transitions to Up
- When `upCount < MinActiveLinks`, aggregate transitions to Down
- State changes are reported only on aggregate transitions (threshold crossing)
- Init state on a member is not counted as Up (only `StateUp` increments `upCount`)

`MicroBFDGroupSnapshot` provides a read-only view of the group state including per-member link details, useful for gRPC API responses and monitoring.

### RFC 8971 Implementation Notes

**Implementation**: [`internal/netio/vxlan.go`](../../internal/netio/vxlan.go)

RFC 8971 defines BFD encapsulated in VXLAN for forwarding-path liveness detection between VTEPs (Virtual Tunnel Endpoints). BFD Control packets are carried inside VXLAN-encapsulated inner Ethernet frames.

| Requirement | Implementation |
|---|---|
| Outer UDP port 4789 | `netio.VXLANPort = 4789` |
| Inner UDP port 3784 | Standard BFD single-hop port |
| VXLAN header codec | `MarshalVXLANHeader` / `UnmarshalVXLANHeader` |
| Management VNI | `VXLANConfig.ManagementVNI` |
| VNI validation (24-bit) | `ErrVXLANVNIOverflow` sentinel |
| I flag validation | `ErrVXLANInvalidFlags` sentinel |
| Inner destination MAC | `VXLANBFDInnerMAC = 00:52:02:00:00:00` (IANA) |
| Session type | `SessionTypeVXLAN` constant |

Packet encapsulation stack:
```
Outer IP → Outer UDP (4789) → VXLAN Header (8 bytes) →
Inner Ethernet → Inner IP → Inner UDP (3784) → BFD Control
```

The VXLAN header codec handles the 8-byte fixed format with I flag (VNI valid) and 24-bit VNI encoding. Management VNI packets are processed locally and not forwarded to tenant networks.

### RFC 9521 Implementation Notes

**Implementation**: [`internal/netio/geneve.go`](../../internal/netio/geneve.go)

RFC 9521 defines BFD encapsulated in Geneve for forwarding-path liveness detection between NVEs (Network Virtualization Edges) at the VAP (Virtual Access Point) level. Geneve is the evolution of VXLAN for cloud-native environments.

| Requirement | Implementation |
|---|---|
| Outer UDP port 6081 | `netio.GenevePort = 6081` |
| Geneve header codec | `MarshalGeneveHeader` / `UnmarshalGeneveHeader` |
| O bit (control) = 1 | `GeneveHeader.OBit` for BFD control messages |
| C bit (critical) = 0 | `GeneveHeader.CBit` validation |
| VNI validation (24-bit) | `ErrGeneveVNIOverflow` sentinel |
| Version validation | `ErrGeneveInvalidVersion` (only version 0 supported) |
| Ethernet payload (Format A) | `GeneveProtocolEthernet = 0x6558` |
| IPv4 payload (Format B) | `GeneveProtocolIPv4 = 0x0800` |
| IPv6 payload (Format B) | `GeneveProtocolIPv6 = 0x86DD` |
| Variable-length options | `GeneveHeader.OptLen` + `TotalHeaderSize()` |
| Session type | `SessionTypeGeneve` constant |

Key differences from VXLAN BFD (RFC 8971):
- Geneve supports variable-length TLV options (VXLAN has fixed 8-byte header)
- Two payload formats: Ethernet (Format A) and IP (Format B)
- O bit control flag indicates management/control traffic
- Sessions originate/terminate at VAPs, not directly at NVEs

### Stub Interfaces

The following RFCs have stub interfaces defined for future implementation:

| RFC | Dependency | Status |
|---|---|---|
| RFC 5884 (BFD for MPLS) | LSP Ping (RFC 4379) | Interfaces defined in `internal/bfd` |
| RFC 5885 (BFD for VCCV) | VCCV (RFC 5085), LDP (RFC 4447) | Interfaces defined |

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

### Related Documents

- [02-protocol.md](./02-protocol.md) -- BFD protocol details (FSM, timers, packet format)
- [01-architecture.md](./01-architecture.md) -- System architecture
- [05-interop.md](./05-interop.md) -- Interoperability testing

---

*Last updated: 2026-02-23*
