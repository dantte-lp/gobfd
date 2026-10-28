# ADR-0009: Principal claims общего Echo wire

| Поле | Значение |
|:---|:---|
| Status | Accepted policy; local qualification separate |
| Date | 2026-10-25 |

## Контекст

Общий Echo transport требует principal-scoped claims. Удаление по discriminator не различает owners; withdrawal конфигурации не должен удалять приём surviving API owner.

## Решение

Используются `SessionOwner`, `apiOwnerFromContext` и comparable `SessionKey`. Echo key содержит unmapped peer/local addresses с zones и точный interface. TX interval и DetectMult должны совпадать до sharing. Один physical discriminator, sender lease и набор owner claims защищены serialization lifecycle→ownership→registry.

| Операция | Обязательный результат |
|:---|:---|
| Same-owner identical Add | Idempotent; без новой physical session, sender, discriminator либо protocol reset |
| Compatible other-owner Add | Общий physical wire |
| Parameter conflict | `FailedPrecondition` до sender acquisition либо claim mutation |
| Delete wire без caller claim | `PermissionDenied` |
| Delete отсутствующего discriminator | `NotFound` |
| Final claim release/shutdown | Join Echo owner, затем release lease вне Manager locks |

Principal задаётся verified transport, а не request fields. Unix UID, TLS URI, TLS DNS и unsafe-opt-in local namespaces различаются. Observation остаётся daemon-wide. Preview enablement и RX readiness проверяются перед каждым Add, включая retry; unready retry может отказать, сохранив прежний claim. List/Delete сохраняют management authorization.

Physical Echo lease держит RX независимо от creating source. Configuration withdrawal сохраняет TX/RX для API claims; final physical release закрывает этот RX ref, source refs сохраняют собственные lifetimes.

Reconciliation проверяет весь desired set против всех owners, готовит новые config claims до stale release и откатывает при preparation failure только claims своего pass. Уже canceled pass ничего не меняет. Cancellation до stale release сохраняет old claims; опубликованные new claims остаются, live retry использует их повторно.

## Последствия

Claim count отличается от physical-wire count. Claim-only changes не создают wire lifecycle event и сохраняют state, discriminator и source port. Compatibility methods имеют отдельного owner и не могут освободить config/API principals. Cleanup errors остаются наблюдаемыми.

Management authorization не аутентифицирует Echo returns и не квалифицирует RFC 9747, interhost forwarding, IPv6 link-local либо scale. Echo остаётся preview. Claim lifecycle, returned-packet security и final release qualification требуют отдельных evidence.

## Источники

- [Ownership и reconciliation](../../../internal/bfd/manager_echo.go).
- [Key и owner types](../../../internal/bfd/session_identity.go).
- [API admission](../../../internal/server/echo.go), [principal namespaces](../../../internal/server/mutation_guard.go).
- [Receive ownership](../../../cmd/gobfd/listeners.go), [reload](../../../cmd/gobfd/reload.go).
- [Security policy](../15-security.md), [граница RFC 9747](../08-rfc-compliance.md#rfc-9747-implementation-notes).
