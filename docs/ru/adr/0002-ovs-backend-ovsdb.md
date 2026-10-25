# ADR-0002: Нативный OVSDB-клиент для OVS LAG backend

| Поле | Значение |
|---|---|
| Статус | Accepted |
| Дата | 2026-05-01 |

## Контекст

Применение состояния Micro-BFD по RFC 7130 к объединённым портам Open vSwitch
требует добавления и удаления интерфейсов-участников bond. Первая реализация
GoBFD вызывала `ovs-vsctl` из демона. `ovs-vsctl` — CLI-клиент с поддержкой
схемы, который соединяется с OVSDB-сервером и меняет
конфигурационную базу `ovs-vswitchd`. Это не примитивный API.

Open vSwitch предоставляет OVSDB -- управляющий JSON-RPC протокол,
описанный в RFC 7047 и реализованный в `ovsdb-server`. Нативный
OVSDB-клиент устраняет порождение процессов, исключения при проверке инъекций
в оболочку, разбор stdout/stderr и зависимость от PATH.

## Решение

GoBFD использует нативный OVSDB-клиент как OVS LAG backend по умолчанию.
`ovs-vsctl` оставлен как явно выбираемый резервный вариант или путь диагностики.
Политика владельца устройств под управлением OVS остаётся явной до тех пор, пока
backend не сможет подтвердить владение через OVSDB или через сетевой
менеджер более высокого уровня.

Рабочий адаптер использует `github.com/ovn-kubernetes/libovsdb` `v0.8.1`.
Прежний репозиторий `ovn-org` архивирован; активный владелец сохранил
используемый API транзакций и исправил взаимные блокировки при смене лидера
и проверке неактивности. Ядро GoBFD
зависит от узкого локального интерфейса, чтобы тесты не требовали
работающего OVSDB-сервера.

## Последствия

### Требуемые операции OVSDB

CLI-backend сопоставлял переходам RFC 7130 следующие операции:

| Переход Micro-BFD | Операция CLI | Изменение OVSDB |
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
    client BondLAGClient
}

type BondLAGClient interface {
    RemoveBondInterface(ctx context.Context, bond, iface string) error
    AddBondInterface(ctx context.Context, bond, iface string) error
}
```

Backend переиспользует валидацию и политику RFC 7130, уже применяемые
в kernel-bond и в CLI-backed OVS пути.

### Конфигурация и владение

- Endpoint OVSDB по умолчанию — `unix:/var/run/openvswitch/db.sock`.
- Пути для конкретного дистрибутива задаются через
  `micro_bfd.actuator.ovsdb_endpoint`.
- Устройства, управляемые NetworkManager, используют отдельный D-Bus
  backend, потому что NetworkManager владеет профилями соединений и
  выставляет состояние устройств через D-Bus. Эти два backend нельзя
  смешивать.

### Риски

- Подключение `libovsdb` увеличивает поверхность зависимостей и должно
  пройти `make verify`, аудит уязвимостей и ревью модуля до релиза,
  где `libovsdb` становится вариантом по умолчанию.
- `ovs-vsctl` остаётся в кодовой базе как резервный тип, а не как
  backend по умолчанию, выбираемый конфигурацией.

## Источники

- [Выбор backend и политики владельца](../../../internal/netio/lag_backend.go)
- [Нативный адаптер OVSDB](../../../internal/netio/lag_ovsdb.go)
- [Выбранная версия модуля](../../../go.mod)
- RFC 7047: <https://datatracker.ietf.org/doc/html/rfc7047>
- Open vSwitch `ovsdb(7)`:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- Open vSwitch `ovs-vsctl(8)`:
  <https://www.openvswitch.org/support/dist-docs/ovs-vsctl.8.html>
- `github.com/ovn-kubernetes/libovsdb`:
  <https://pkg.go.dev/github.com/ovn-kubernetes/libovsdb>
- Релиз v0.8.1:
  <https://github.com/ovn-kubernetes/libovsdb/releases/tag/v0.8.1>
