# ADR-0011: Inline BFD authentication payload ownership

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-26 |

## Context

[ADR-0010](0010-bfd-auth-value-ownership.md) removed the allocated authentication
section and preserved received wire during verification. Its slice payload
still shares backing storage when a packet is copied. Signing needs independent
password/digest storage: local samples show one allocation for Simple Password
and two for each hash mode after the digest-result optimization.

RFC 5880 bounds all supported authentication payloads: passwords occupy 1–16
bytes, MD5 digests 16 bytes, and SHA1 digests 20 bytes. There is no supported
authentication payload larger than 20 bytes.

## Decision

`AuthSection.Data` is a 20-byte array inside the value. `DataLen` records the
logical payload length independently of the wire `Len` field. This preserves
rejection of missing, short, long and inconsistent outbound payloads before any
wire write. Marshal and Verify validate the logical length before accessing
payload bytes. The complete zero value denotes an absent section.

Decode copies the bounded payload into a local authentication value and
publishes that value only after the section validates. Packet assignment owns
both metadata and payload bytes; input reuse or mutation of another packet copy
cannot change them. This is not a promise of atomic destination headers on a
failed decode; ingress still drops every failed decode.

Built-in Sign reads package-owned immutable keys through the existing private
accessor and copies their bytes into packet-owned storage. Public key-store
accessors retain their caller-owned copies. Hash signing replaces the inline
key with the fixed-value digest and writes only the RFC-defined digest length.
No packet exposes the store's backing bytes or another packet's payload.

Read-only Verify and DemuxWithWire continue to pass packet values. Hash
verification retains the bounded scratch PDU, exact digest widths, constant-time
comparison and replay-state update after success. Default and overlay receivers
still retain the complete authenticated datagram for verification after pooled
input release; owned decoded payloads do not remove this wire requirement.
They no longer need to decode that retained copy a second time.

The internal Go representation changes; protobuf, configuration and BFD wire
formats remain unchanged. Unsupported future authentication types fail closed;
supporting a larger payload requires a separate design and qualification.

## Consequences

The required target is zero allocations for all five built-in Sign methods and
the authenticated owner/event loop with real metrics. Qualification must cover
independent vectors, invalid lengths and keys, packet copies, repeated signing,
store isolation, race/fuzz checks and both supported compilation targets.
Local linux/amd64 layout is `ControlPacket` 60 bytes and `AuthSection` 28 bytes,
previously 64 and 32. The receive queue element remains 160 bytes because of
alignment; the smaller packet does not reduce that element. No retained RSS
estimate follows from these sizes. Allocation benchmarks do not qualify UDP, native arm64,
scale, timer distributions or the release candidate.

Beads `gobfd-qj0.8.2.8.6.3.2.2` owns implementation and source qualification;
the full comparison remains `gobfd-qj0.8.2.8.6.3.2`. This ADR supersedes ADR-0010
while retaining its value-transfer and immutable-verification decisions.

## References

- [RFC 5880](../../rfc/rfc5880.txt), Sections 4.2–4.4 and 6.7.
- [Go value representation](https://go.dev/ref/spec#Representation_of_values).
- [Packet codec](../../../internal/bfd/packet.go) and [authentication](../../../internal/bfd/auth.go).
- [Allocation measurements](../12-benchmarks.md).
