# ADR-0011: Inline BFD authentication payload ownership

[Russian translation](../../ru/adr/0011-bfd-auth-inline-payload.md).

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-26 |

## Context

[ADR-0010](0010-bfd-auth-value-ownership.md) keeps metadata by value but shares slice backing storage. Supported passwords occupy 1–16 bytes, MD5 digests 16 and SHA1 digests 20. A bounded inline representation can own every supported payload without a separate allocation. [RFC 5880 §§4.2–4.4](../../rfc/rfc5880.txt) defines those bounds.

## Decision

`AuthSection.Data` is a 20-byte array in the value. Logical `DataLen` is separate from wire `Len`. Marshal and Verify reject missing, short, long and inconsistent payload lengths before byte access or wire writes. Complete zero value means absent authentication.

Decode validates into a local authentication value and publishes it only after complete section validation. Assignment copies metadata and payload; input-buffer reuse and mutation of another packet cannot change it. Failed header decode is not atomic and ingress drops every decode failure.

Built-in Sign reads package-owned immutable keys through its private accessor and copies into packet-owned storage. Public key-store accessors still return owned copies. Hash signing writes the fixed digest width and exposes no key-store or other-packet backing bytes.

Read-only Verify and DemuxWithWire pass values. Hash verification preserves bounded scratch PDU, digest-width checks, constant-time comparison and replay-state changes only on success. Receivers still retain complete authenticated wire before pool release; owned decoded payload does not replace it. They do not reparse the retained copy.

Protobuf, configuration and BFD wire formats remain unchanged. Unsupported future auth types fail closed; payloads above 20 bytes require a new design and qualification. This record supersedes ADR-0010 and preserves its immutable-verification and value-transfer decisions.

## Consequences

Required allocation targets cover all five built-in Sign methods and the authenticated owner/event loop with real metrics. Qualification covers independent vectors, invalid lengths/keys, packet copies, repeated signing, key-store isolation, races, fuzzing and both supported compilation targets.

Local linux/amd64 layout is ControlPacket 60 bytes and AuthSection 28, formerly 64 and 32. Alignment retains a 160-byte RX queue element; the smaller packet does not reduce it. No retained-RSS conclusion follows. UDP, native arm64, scale, timer distributions and release qualification remain outside an allocation benchmark.

Source qualification must retain storage/allocation regressions; the full runtime comparison remains separate.

## References

- [Codec and inline type](../../../internal/bfd/packet.go).
- [Key access, Sign and Verify](../../../internal/bfd/auth.go).
- [Storage and allocation regression tests](../../../internal/bfd/packet_auth_storage_test.go).
- [Padded independent vectors](../../../internal/bfd/auth_padding_test.go).
- [Allocation evidence](../12-benchmarks.md).
