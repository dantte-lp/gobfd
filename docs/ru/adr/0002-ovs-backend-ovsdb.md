# ADR-0002: Нативный OVSDB-клиент для OVS LAG backend

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-05-01 |

## Контекст

Micro-BFD enforcement меняет набор members bonded port. Вызов configuration CLI добавляет daemon зависимости от процессов, PATH и parsing output. [RFC 7047](https://www.rfc-editor.org/rfc/rfc7047.html) задаёт OVSDB management protocol.

## Решение

Для configured OVS enforcement выбирается native `OVSDBLAGBackend`. `OVSLAGBackend` сохраняется как явно создаваемый CLI fallback/diagnostic type; daemon configuration его не выбирает. Owner policy остаётся явной до доказательства ownership со стороны actual manager.

Ядро использует `BondLAGClient` с context-bounded `RemoveBondInterface` и `AddBondInterface`. [Production adapter](../../../internal/netio/lag_ovsdb.go) владеет external client; [runtime manifest](../../../go.mod) задаёт pin.

| Операция | Database intent | Проверка |
|:---|:---|:---|
| Найти bond | Найти `Port` по имени | Existing unambiguous target |
| Member Down | Удалить Interface UUID из `Port.interfaces` | Transaction и affected rows |
| Member Up | Найти/создать `Interface`, добавить UUID | UUID shape и все operation results |

## Последствия

Default endpoint: `unix:/var/run/openvswitch/db.sock`; переопределение — `micro_bfd.actuator.ovsdb_endpoint`. Проверьте interface names до обеих операций. Backend construction использует общие LAG policy и input validation.

NetworkManager-owned profiles используют отдельный D-Bus backend. OVSDB access не подтверждает право менять externally managed bond. Dependency review, vulnerability checks, local transaction tests и actual OVS owner/member-wire qualification остаются отдельными release requirements.

## Источники

- [Выбор backend](../../../internal/netio/lag_backend.go).
- [Native adapter и transaction validation](../../../internal/netio/lag_ovsdb.go).
- [OVSDB tests](../../../internal/netio/lag_ovsdb_test.go).
- [Linux applicability](0003-linux-advanced-bfd-applicability.md).
