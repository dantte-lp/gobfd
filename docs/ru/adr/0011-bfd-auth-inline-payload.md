# ADR-0011: Владение встроенным authentication payload

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-26 |

## Контекст

[ADR-0010](0010-bfd-auth-value-ownership.md) передаёт metadata по значению, но разделяет slice backing storage. Поддерживаемые passwords занимают 1–16 байт, MD5 digest — 16, SHA1 — 20. Bounded inline representation владеет любым supported payload без отдельной allocation. Размеры задаёт [RFC 5880 §§4.2–4.4](../../rfc/rfc5880.txt).

## Решение

`AuthSection.Data` — массив 20 байт внутри value. Logical `DataLen` отделён от wire `Len`. Marshal и Verify отклоняют missing, short, long и inconsistent lengths до byte access либо wire write. Полный zero value означает отсутствие auth.

Decode проверяет local authentication value и публикует после полной section validation. Assignment копирует metadata/payload; reuse input buffer и mutation другой копии их не меняют. Failed header decode не атомарен; ingress отбрасывает каждый decode failure.

Built-in Sign читает package-owned immutable keys через private accessor и копирует в packet-owned storage. Public key-store accessors возвращают owned copies. Hash signing записывает fixed digest width, не раскрывая backing bytes key store либо другого packet.

Read-only Verify и DemuxWithWire передают values. Hash verification сохраняет bounded scratch PDU, digest-width checks, constant-time comparison и изменение replay state только после успеха. Receivers сохраняют complete authenticated wire до pool release; owned decoded payload его не заменяет. Retained copy не декодируется повторно.

Protobuf, configuration и BFD wire formats сохраняются. Неподдерживаемые future auth types отклоняются; payload больше 20 байт требует нового design/qualification. ADR заменяет ADR-0010, сохраняя immutable verification и value transfer.

## Последствия

Allocation targets охватывают пять built-in Sign methods и authenticated owner/event loop с real metrics. Qualification проверяет independent vectors, invalid lengths/keys, packet copies, repeated signing, key-store isolation, races, fuzzing и обе supported compilation targets.

Local linux/amd64 layout: ControlPacket 60 байт, AuthSection 28 вместо 64 и 32. Alignment сохраняет RX queue element 160 байт; уменьшение packet его не уменьшает. Retained RSS из этого не следует. UDP, native arm64, scale, timer distributions и release qualification находятся за границами allocation benchmark.

Source qualification сохраняет storage/allocation regressions; full runtime comparison остаётся отдельным.

## Источники

- [Codec и inline type](../../../internal/bfd/packet.go).
- [Key access, Sign и Verify](../../../internal/bfd/auth.go).
- [Storage/allocation regression tests](../../../internal/bfd/packet_auth_storage_test.go).
- [Padded independent vectors](../../../internal/bfd/auth_padding_test.go).
- [Allocation evidence](../12-benchmarks.md).
