# ADR-0010: Value ownership of BFD authentication metadata

| Field | Value |
|:---|:---|
| Status | Superseded by ADR-0011 |
| Date | 2026-10-26 |

## Context

Allocated authentication metadata adds receive allocation. A pointer into a reusable packet lets a later decode alter shallow copies. Hash verification that replaces a digest in received wire also destroys the input needed by a later owner verification.

## Decision

This historical decision uses value `ControlPacket.Auth` and one type-dependent slice `AuthSection.Data`. The complete zero value denotes absence; `IsZero` checks every field. Marshal validates A-bit agreement before writing; decode publishes authentication only after complete shape validation.

Read-only `DemuxWithWire` and `Verify` pass packet values; `Sign` retains a mutable pointer. Scalar metadata and slice descriptors belong to each copy; payload backing bytes remain borrowed and immutable. Default and overlay receivers copy the complete authenticated datagram and reparse before releasing pooled input.

Hash verification copies only declared BFD Length into a local 255-byte scratch PDU before key substitution. Digest must fit Length, Length must fit received wire. Transport padding remains outside the hash boundary. Constant-time comparison and replay-state update after success remain required; success and failure preserve received wire.

[ADR-0011](0011-bfd-auth-inline-payload.md) replaces borrowed slice storage with an owned inline array and removes the retained-wire reparse. It preserves value transfer, immutable verification and complete-wire retention. Current code follows ADR-0011; this record is not the current payload representation.

## Consequences

Historical local linux/amd64 samples report authenticated decode at 0 B/op and 0 allocs/op for 200000 iterations in three runs. The in-memory unauthenticated RX benchmark changes 176 B/2 allocs to 128 B/1 alloc; it excludes sockets and owner commit.

Historical layout changes ControlPacket 40→64 bytes and AuthSection 64→32 bytes. Sixteen RX slots add 384 bytes of queue elements per session. These are layout observations, not per-packet heap or RSS estimates. Native arm64, scale, latency distributions and release acceptance remain separate.

Allocation/lifetime qualification must include immutable verification and the unsolicited decoded-trigger regression. The historical measurements are not rerun by this documentation change.

## References

- [Current codec](../../../internal/bfd/packet.go), [authentication](../../../internal/bfd/auth.go).
- [Ownership regression tests](../../../internal/bfd/packet_auth_storage_test.go).
- [RFC 5880 §§4.1–4.4, 6.7](../../rfc/rfc5880.txt).
- [Performance scope](../14-performance-analysis.md).
