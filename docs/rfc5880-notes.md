# RFC 5880 Implementation Notes

This document records implementation decisions for each relevant section of
[RFC 5880](https://datatracker.ietf.org/doc/html/rfc5880) (Bidirectional
Forwarding Detection). Where GoBFD deviates from or simplifies the RFC, the
rationale is stated explicitly.

## Section 4.1: BFD Control Packet Format

**Implementation**: [`internal/bfd/packet.go`](../internal/bfd/packet.go)

The 24-byte mandatory header is encoded and decoded using `encoding/binary.BigEndian`
directly on a caller-owned byte buffer. No reflection, no `unsafe`, no gopacket.

Wire layout:

| Offset | Size  | Field                         |
|--------|-------|-------------------------------|
| 0      | 1     | Version (3b) + Diag (5b)      |
| 1      | 1     | State (2b) + P F C A D M (6b) |
| 2      | 1     | Detect Mult                   |
| 3      | 1     | Length                         |
| 4-7    | 4     | My Discriminator              |
| 8-11   | 4     | Your Discriminator            |
| 12-15  | 4     | Desired Min TX (microseconds) |
| 16-19  | 4     | Required Min RX (microseconds)|
| 20-23  | 4     | Required Min Echo RX (us)     |
| 24+    | var   | Auth Section (optional)       |

> [!IMPORTANT]
> All interval fields are in **microseconds** on the wire. Conversion to `time.Duration` happens at the boundary:
> ```go
> interval := time.Duration(pkt.DesiredMinTxInterval) * time.Microsecond
> ```

**Zero-allocation codec**: `MarshalControlPacket` writes into a pre-allocated
buffer (typically from `sync.Pool`). `UnmarshalControlPacket` fills a
caller-provided `ControlPacket` struct in-place. Auth section digest/password
fields reference the original buffer (zero-copy); callers must copy before
returning the buffer to the pool.

## Section 6.1: State Variables

**Implementation**: [`internal/bfd/session.go`](../internal/bfd/session.go)

All mandatory state variables from RFC 5880 Section 6.1 are represented:

| RFC Variable              | Go Field                       | Notes                    |
|---------------------------|--------------------------------|--------------------------|
| bfd.SessionState          | `session.state` (atomic)       | External reads via atomic|
| bfd.RemoteSessionState    | `session.remoteState` (atomic) | From received packets    |
| bfd.LocalDiscr            | `session.localDiscr`           | Immutable after creation |
| bfd.RemoteDiscr           | `session.remoteDiscr`          | Set from received pkts   |
| bfd.LocalDiag             | `session.localDiag` (atomic)   | Set by FSM actions       |
| bfd.DesiredMinTxInterval  | `session.desiredMinTxInterval` | Configurable             |
| bfd.RequiredMinRxInterval | `session.requiredMinRxInterval`| Configurable             |
| bfd.RemoteMinRxInterval   | `session.remoteMinRxInterval`  | From received packets    |
| bfd.DemandMode            | Not implemented                | See "Not Implemented"    |
| bfd.RemoteDemandMode      | `session.remoteDemandMode`     | Parsed but ignored       |
| bfd.DetectMult            | `session.detectMult`           | Configurable             |
| bfd.AuthType              | `session.auth` (interface)     | Via Authenticator        |
| bfd.RcvAuthSeq            | `session.authState`            | AuthState tracks this    |
| bfd.XmitAuthSeq           | `session.authState`            | AuthState tracks this    |
| bfd.AuthSeqKnown          | `session.authState`            | AuthState tracks this    |

**Thread safety**: `state`, `remoteState`, and `localDiag` use `atomic.Uint32`
for lock-free reads from the gRPC server goroutine. All other state is
owned exclusively by the session goroutine.

## Section 6.5: Poll Sequence

**Implementation**: [`internal/bfd/session.go`](../internal/bfd/session.go)
(`pollActive`, `pendingFinal`, `pendingDesiredMinTx`, `pendingRequiredMinRx`,
`terminatePollSequence`)

When parameters change (TX or RX interval), GoBFD:

1. Stores pending values in `pendingDesiredMinTx` / `pendingRequiredMinRx`
2. Sets `pollActive = true`, causing the Poll (P) bit to be set in outgoing packets
3. When a packet with Final (F) bit is received, `terminatePollSequence()` applies
   the pending values and clears `pollActive`

The RFC states (Section 6.5): "Only one Poll Sequence may be active at a time."
This is enforced by the single `pollActive` flag.

> [!NOTE]
> Parameter changes are deferred until poll completion rather than applied
> immediately. This matches the RFC intent: "A Poll Sequence MUST be used in
> order to verify that the change has been received."

## Section 6.7: Authentication

**Implementation**: [`internal/bfd/auth.go`](../internal/bfd/auth.go)

The `Authenticator` interface supports all five RFC-defined auth types:

| Type | RFC Section | Implementation          |
|------|-------------|-------------------------|
| 1    | 4.2         | SimplePasswordAuth      |
| 2    | 4.3         | KeyedMD5Auth            |
| 3    | 4.3         | MeticulousKeyedMD5Auth  |
| 4    | 4.4         | KeyedSHA1Auth           |
| 5    | 4.4         | MeticulousKeyedSHA1Auth |

**Meticulous vs Non-Meticulous**: Meticulous variants increment
`bfd.XmitAuthSeq` on every transmitted packet. Non-meticulous variants
increment only on session state changes. This distinction is critical for
replay protection.

**Sequence number window**: For non-meticulous auth, received sequence
numbers are accepted if they fall within `3 * DetectMult` of
`bfd.RcvAuthSeq`. Meticulous auth requires strict monotonic increment.

**Key rotation**: `AuthKeyStore` supports multiple simultaneous keys indexed
by Key ID. This allows hitless key rotation per RFC 5880 Section 6.7.1.

> [!WARNING]
> MD5 and SHA1 are retained despite cryptographic weakness because the RFC
> mandates them as the only defined hash-based auth types. GoBFD logs a
> warning at startup when MD5 auth is configured.

## Section 6.8.1: State Variables Initialization

All state variables are initialized per RFC:

- `bfd.SessionState` = Down
- `bfd.RemoteSessionState` = Down
- `bfd.LocalDiag` = 0 (None)
- `bfd.RemoteDiscr` = 0
- `bfd.RemoteMinRxInterval` = 1 (microsecond)
- `bfd.XmitAuthSeq` = random 32-bit value (via `crypto/rand`)

## Section 6.8.2-6.8.3: Timer Negotiation and Manipulation

**Implementation**: `session.calcTxInterval()`, `session.calcDetectionTime()`

TX interval calculation:

```go
desired := s.desiredMinTxInterval
if s.State() != StateUp && desired < slowTxInterval {
    desired = slowTxInterval  // 1 second minimum when not Up
}
return max(desired, s.remoteMinRxInterval)
```

The 1-second slow rate (Section 6.8.3) applies to both the actual
transmission interval AND the wire-format `DesiredMinTxInterval` field,
so the remote peer calculates a correct detection time.

## Section 6.8.4: Detection Time Calculation

```go
agreedInterval := max(s.requiredMinRxInterval, s.remoteDesiredMinTxInterval)
detectionTime := time.Duration(s.remoteDetectMult) * agreedInterval
```

Before any packet is received (`remoteDetectMult == 0`), the detection time
is calculated using local parameters as a fallback.

## Section 6.8.6: Reception of BFD Control Packets

**Implementation**: `UnmarshalControlPacket` (steps 1-7),
`session.handleRecvPacket` (steps 8-18)

The validation is split across two layers:

- **Codec layer** ([`packet.go`](../internal/bfd/packet.go)): Steps 1-7
  (version, length, detect mult, multipoint, discriminators). These are
  stateless and can reject packets before any session lookup.

- **Session layer** ([`session.go`](../internal/bfd/session.go)): Steps 8-18
  (auth consistency, auth verification, update state variables, FSM event,
  timer reset).

This split allows the listener to discard obviously invalid packets without
acquiring session locks.

## Section 6.8.7: Transmitting BFD Control Packets

**Implementation**: `session.handleTxTimer`, `session.maybeSendControl`,
`session.sendControl`, `session.rebuildCachedPacket`

Transmission preconditions (all enforced):

1. Passive role: do not transmit if `bfd.RemoteDiscr` is zero
2. Do not transmit if `bfd.RemoteMinRxInterval` is zero (peer requests no packets)
3. Apply jitter per Section 6.8.7 (see below)

**Cached packet pattern**: Inspired by FRR bfdd. The session pre-serializes
its BFD Control packet into `cachedPacket []byte`. This is rebuilt only when
parameters change (state transition, Poll/Final, negotiation). On each TX
timer fire, the cached bytes are sent directly. For authenticated sessions,
the auth sequence number is updated in the cached packet on each transmission.

## Section 6.8.7: Jitter

**Implementation**: `bfd.ApplyJitter`

```go
func ApplyJitter(interval time.Duration, detectMult uint8) time.Duration {
    if detectMult == 1 {
        // 75-90% of interval (max 90% per RFC)
        jitterPercent = 10 + rand.IntN(16)
    } else {
        // 75-100% of interval
        jitterPercent = rand.IntN(26)
    }
    return interval - (interval * jitterPercent / 100)
}
```

Uses `math/rand/v2` for jitter because it is not security-sensitive and is
called on the hot path.

## Not Implemented

### Demand Mode (Section 6.6)

Demand Mode is not implemented in the current version. The Demand (D) bit is
always set to zero on transmit. The `bfd.RemoteDemandMode` variable is parsed
from received packets but has no effect on session behavior.

**Rationale**: Demand Mode is rarely used in production ISP/DC deployments.
The primary use case (reducing BFD traffic) is better served by tuning
TX/RX intervals. All major implementations (FRR, Junos, IOS-XR) default
to Asynchronous mode.

### Echo Mode (Section 6.4)

Echo Mode is not implemented. The `RequiredMinEchoRxInterval` field is always
set to zero on transmit, indicating that the local system does not support
the Echo function.

**Rationale**: Echo Mode requires kernel cooperation for reflecting echo
packets. It adds complexity with minimal benefit for the typical GoBFD
deployment scenario (BFD-assisted BGP failover).

### Point-to-Multipoint (Multipoint bit)

The Multipoint (M) bit is always zero. Received packets with M=1 are rejected
per RFC 5880 Section 6.8.6 step 5.
