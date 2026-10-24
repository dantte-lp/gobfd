# ADR-0001: Мониторинг состояния линков через rtnetlink

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

GoBFD должен отслеживать изменения состояния интерфейсов Linux, чтобы
переводить link-down события в BFD `Down` до истечения detection-таймера.
В Linux есть две кандидатные API:

- `NETLINK_ROUTE` / rtnetlink, выдающий сообщения `RTM_NEWLINK` и
  `RTM_DELLINK` через multicast-группу `RTMGRP_LINK`;
- `github.com/cilium/ebpf`, загружающий и подключающий eBPF-программы
  для XDP, TC, kprobes, tracepoints, perf events и ring buffers.

Уведомление о состоянии линка -- это control-plane задача об изменении
состояния, а не задача datapath обработки пакетов. eBPF применим к
наблюдению и фильтрации packet path, а не к нотификациям интерфейсов.

## Decision

GoBFD использует rtnetlink для нотификаций о состоянии линков.
`github.com/cilium/ebpf` не входит в путь мониторинга состояния линков.

## Consequences

| Item | Requirement |
|---|---|
| Kernel API | rtnetlink существует начиная с Linux 2.2 |
| Distribution support | Современные mainstream-дистрибутивы Linux содержат rtnetlink |
| Network namespace | Сокет netlink наблюдает namespace, в котором он открыт |
| Kubernetes/Podman deployment | Мониторинг хост-интерфейсов требует host network namespace |
| Message loss | `ENOBUFS` -- сигнал ресинхронизации; получатель должен перечитать таблицу линков |
| Capabilities | Профили контейнеров должны разрешать подписку на netlink-группы и доступ к host namespace |
| eBPF | Вне области link-state нотификаций; зарезервирован под будущие packet-path возможности |

### Выбор Linux networking API

| Task | Preferred Linux API |
|---|---|
| Создание/удаление/подъём/опускание интерфейса | rtnetlink `RTMGRP_LINK` |
| Изменение адресов интерфейса | rtnetlink address groups |
| Изменение маршрутов | rtnetlink route groups |
| NIC driver / offload settings | ethtool netlink |
| Packet fast path, фильтрация, телеметрия | TC, XDP, eBPF |
| Legacy device flags | ioctl, только когда нет netlink API |

### Контракт реализации

- Linux rtnetlink monitor живёт в `internal/netio`.
- Монитор парсит `RTM_NEWLINK` и `RTM_DELLINK` и эмитит
  `InterfaceEvent{IfName, IfIndex, Up}`.
- Operational-up rule: `IFF_UP && IFF_RUNNING`.
- Link-down переход запрашивает BFD `Down` с `DiagPathDown` без ожидания
  detection expiry. Доставка события и планирование owner не гарантируют
  опережение одновременно истекающего таймера; действующий локальный
  AdminDown сохраняет административное состояние и диагностику.

Текущий monitor также подписан на IPv4/IPv6 address notifications и передаёт
address/resync events для повторного получения pending resources. См.
[`ifmon_linux.go`](../../../internal/netio/ifmon_linux.go),
[`interface_events.go`](../../../cmd/gobfd/interface_events.go) и
[`Session.handlePathDown`](../../../internal/bfd/session_loop.go).

## References

- Linux `netlink(7)`: <https://man7.org/linux/man-pages/man7/netlink.7.html>
- Linux `rtnetlink(7)`: <https://man7.org/linux/man-pages/man7/rtnetlink.7.html>
- Спецификация rt-link ядра Linux:
  <https://docs.kernel.org/networking/netlink_spec/rt_link.html>
- Документация пакета `github.com/cilium/ebpf`:
  <https://pkg.go.dev/github.com/cilium/ebpf>
- Cilium system requirements:
  <https://docs.cilium.io/en/stable/operations/system_requirements/>
