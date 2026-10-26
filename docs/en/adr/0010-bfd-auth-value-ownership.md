# ADR-0010: Value ownership of BFD authentication metadata

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-26 |

## Context

Authenticated decode allocated an `AuthSection` for every packet. Reusing that
pointer or pointing into the destination packet would let a later decode change
metadata retained by a shallow packet copy. Separate password and digest slices
also represented mutually exclusive payloads twice.

The receiver, overlay receiver and session queue retain authenticated wire.
Hash verification previously replaced its digest slot with key material. A
decoded payload aliases that slot, so admission verification could invalidate
the trigger before the session owner verified it again. A transmit-side fixture
with an independently copied digest did not model this receive lifetime.

## Decision

`ControlPacket.Auth` is an `AuthSection` value. Its zero value denotes absence;
`AuthSection.IsZero()` checks the complete metadata, and marshal validates its
agreement with the A bit before writing. `AuthSection.Data` is the single
type-dependent password or digest slice. Decode publishes the value only after
the entire authentication section validates.

Read-only `DemuxWithWire` and `Authenticator.Verify` boundaries pass packet
values. `Sign` retains a mutable packet pointer. These are internal Go contracts;
the protobuf API, configuration and BFD wire formats are unchanged.

The payload still borrows immutable wire. Default and overlay receivers copy
the complete authenticated datagram and reparse before releasing pooled input.
Packet copies own their scalar metadata and slice descriptors, while the backing
bytes remain shared and must stay alive until processing finishes.

Hash verification copies only the validated BFD PDU into a local 255-byte
scratch array before substituting the key. The one-octet Length bounds this
copy; RFC 9764 transport padding stays outside the authenticated PDU. Received
wire is unchanged on success and failure. Constant-time digest comparison and
replay-state updates after successful verification are retained.

## Consequences

Local linux/amd64 measurements with the pinned compiler report 0 B/op and
0 allocs/op for authenticated decode at 200000 iterations in three runs.
The existing in-memory unauthenticated RX benchmark changes from 176 B/op and
2 allocs/op to 128 B/op and 1 alloc/op. It excludes sockets and session commit.

Measured `ControlPacket` grows from 40 to 64 bytes while `AuthSection` shrinks
from 64 to 32 bytes. At 16 receive slots this adds 384 bytes of queue elements
per session; it is not a per-packet heap allocation or a retained RSS estimate.
These source measurements do not qualify native arm64, reference scale,
latency distributions or the final release candidate.

Beads `gobfd-qj0.8.2.1.18` owns the allocation and lifetime qualification;
`gobfd-qj0.8.2.1.18.1` owns immutable verification and the decoded unsolicited
trigger regression. Final runtime and release gates remain separate.

## References

- [Go value representation](https://go.dev/ref/spec#Representation_of_values).
- [RFC 5880](../../rfc/rfc5880.txt), Sections 4.1-4.4 and 6.7.
- [Packet codec](../../../internal/bfd/packet.go) and [authentication](../../../internal/bfd/auth.go).
- [Performance scope](../14-performance-analysis.md).
