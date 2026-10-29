# ADR-0010: Владение authentication metadata по значению

| Поле | Значение |
|:---|:---|
| Status | Superseded by ADR-0011 |
| Date | 2026-10-26 |

## Контекст

Allocated authentication metadata добавляет receive allocation. Pointer внутрь повторно используемого packet позволяет следующему decode менять shallow copies. Hash verification с заменой digest в received wire уничтожает input для последующей owner verification.

## Решение

Историческое решение использует value `ControlPacket.Auth` и один type-dependent slice `AuthSection.Data`. Полный zero value означает отсутствие; `IsZero` проверяет все поля. Marshal проверяет соответствие A-bit до записи; decode публикует auth после полной shape validation.

Read-only `DemuxWithWire` и `Verify` передают packet values; `Sign` сохраняет mutable pointer. Scalar metadata и slice descriptor принадлежат копии; backing bytes остаются borrowed и immutable. Default/overlay receivers копируют полную authenticated datagram и выполняют reparse до возврата pooled input.

Hash verification копирует объявленный BFD Length в local scratch PDU 255 байт перед подстановкой key. Digest должен помещаться в Length, Length — в received wire. Transport padding исключается из hash boundary. Constant-time comparison и изменение replay state только после успеха сохраняются; success/failure не меняют received wire.

[ADR-0011](0011-bfd-auth-inline-payload.md) заменяет borrowed slice на owned inline array и удаляет retained-wire reparse. Value transfer, immutable verification и complete-wire retention сохраняются. Current code следует ADR-0011; этот record не задаёт текущий payload representation.

## Последствия

Исторические local linux/amd64 samples дают authenticated decode 0 B/op и 0 allocs/op при 200000 iterations в трёх запусках. In-memory unauthenticated RX benchmark меняется с 176 B/2 allocs на 128 B/1 alloc; sockets и owner commit исключены.

Исторический layout: ControlPacket 40→64 байта, AuthSection 64→32 байта. Шестнадцать RX slots добавляют 384 байта queue elements на сессию. Это layout observations, а не per-packet heap либо RSS estimate. Native arm64, scale, latency distributions и release acceptance отдельны.

Allocation/lifetime qualification должна включать immutable verification и unsolicited decoded-trigger regression. Документационное изменение не повторяет исторические измерения.

## Источники

- [Текущий codec](../../../internal/bfd/packet.go), [аутентификация](../../../internal/bfd/auth.go).
- [Ownership regression tests](../../../internal/bfd/packet_auth_storage_test.go).
- [RFC 5880 §§4.1–4.4, 6.7](../../rfc/rfc5880.txt).
- [Performance scope](../14-performance-analysis.md).
