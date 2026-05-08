# ADR-0002: Нативный OVSDB-клиент для OVS LAG backend

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

Enforcement RFC 7130 Micro-BFD на bonded-портах Open vSwitch требует
добавления и удаления member-интерфейсов из bond. Первая реализация
GoBFD вызывала `ovs-vsctl` из демона. `ovs-vsctl` -- это schema-aware
CLI-клиент, который соединяется с OVSDB-сервером и меняет
конфигурационную базу `ovs-vswitchd`. Это не примитивный API.

Open vSwitch предоставляет OVSDB -- управляющий JSON-RPC протокол,
описанный в RFC 7047 и реализованный в `ovsdb-server`. Нативный
OVSDB-клиент устраняет порождение процессов, исключения review для
shell-injection, парсинг stdout/stderr и зависимость от PATH.

## Decision

GoBFD использует нативный OVSDB-клиент для дефолтного OVS LAG backend.
`ovs-vsctl` оставлен как явный fallback или путь диагностики.
Owner-policy для OVS-managed устройств остаётся явной до тех пор, пока
backend не сможет подтвердить ownership через OVSDB или через сетевой
менеджер более высокого уровня.

Production-адаптер использует `github.com/ovn-org/libovsdb`. Ядро GoBFD
зависит от узкого локального интерфейса, чтобы тесты не требовали
работающий OVSDB-сервер.

## Consequences

### Требуемые операции OVSDB

CLI-backend отображал переходы RFC 7130 в эти операции:

| Micro-BFD transition | CLI operation | OVSDB intent |
|---|---|---|
| `Up -> non-Up` | `ovs-vsctl --if-exists del-bond-iface <bond> <iface>` | Удаление UUID Interface из `Port.interfaces` |
| `non-Up -> Up` | `ovs-vsctl --may-exist add-bond-iface <bond> <iface>` | Вставка UUID Interface в `Port.interfaces`; создание Interface при необходимости |

Нативный backend работает со схемой `Open_vSwitch`:

- Таблица `Port`: поиск строки по `name == lagInterface`.
- Таблица `Interface`: поиск или создание строки по
  `name == memberInterface`.
- `Port.interfaces`: мутация множества UUID-ссылок Interface.
- Результат транзакции: проверка результатов всех операций до
  объявления успеха.

### Интерфейсы backend

```go
type OVSDBLAGBackend struct {
    client OVSDBClient
}

type OVSDBLAGClient interface {
    RemoveBondInterface(ctx context.Context, bond, iface string) error
    AddBondInterface(ctx context.Context, bond, iface string) error
}
```

Backend переиспользует валидацию и политику RFC 7130, уже применяемые
в kernel-bond и в CLI-backed OVS пути.

### Конфигурация и ownership

- Дефолтный endpoint OVSDB -- `unix:/var/run/openvswitch/db.sock`.
- Distro-специфичные пути задаются через
  `micro_bfd.actuator.ovsdb_endpoint`.
- Устройства, управляемые NetworkManager, используют отдельный D-Bus
  backend, потому что NetworkManager владеет connection-профилями и
  выставляет состояние устройств через D-Bus. Эти два backend нельзя
  смешивать.

### Риски

- Подключение `libovsdb` увеличивает поверхность зависимостей и должно
  пройти `make verify`, vulnerability audit и ревью модуля до релиза,
  где `libovsdb` становится дефолтом.
- `ovs-vsctl` остаётся в кодовой базе как fallback-тип, а не как
  дефолтный backend, выбираемый конфигурацией.

## References

- RFC 7047: <https://datatracker.ietf.org/doc/html/rfc7047>
- Open vSwitch `ovsdb(7)`:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- Open vSwitch `ovs-vsctl(8)`:
  <https://www.openvswitch.org/support/dist-docs/ovs-vsctl.8.html>
- `github.com/ovn-org/libovsdb`:
  <https://pkg.go.dev/github.com/ovn-org/libovsdb>
