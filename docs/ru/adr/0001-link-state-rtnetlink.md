# ADR-0001: Мониторинг состояния интерфейсов через rtnetlink

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-05-01 |

## Контекст

Interface state служит control-plane input для BFD. Link-down notification запрашивает local PathDown до обычного detection expiry с учётом доставки события и scheduling owner. Packet tracing не задаёт владение lifecycle интерфейса.

## Решение

Используется Linux `NETLINK_ROUTE` в `internal/netio`. `RTM_NEWLINK`/`RTM_DELLINK` преобразуются в `InterfaceEvent{IfName, IfIndex, Up}`; operational Up равен `IFF_UP && IFF_RUNNING`. IPv4/IPv6 address changes запускают pending-resource retries. `cilium/ebpf` не входит в link-state monitoring.

| Input | Обязательное поведение | Source |
|:---|:---|:---|
| Link down/delete | Запросить Down/5 без ожидания Detection Time | [interface_events.go](../../../cmd/gobfd/interface_events.go) |
| Existing local AdminDown | Сохранить AdminDown/7 | [session_loop.go](../../../internal/bfd/session_loop.go) |
| Address change | Повторить pending resource classes без link side effects | [interface_events.go](../../../cmd/gobfd/interface_events.go) |
| Kernel/channel loss | Запросить full inventory resync | [ifmon_linux.go](../../../internal/netio/ifmon_linux.go) |
| Cancellation | Закрыть monitor socket и owner-owned event channel | [ifmon_linux.go](../../../internal/netio/ifmon_linux.go) |

## Последствия

Socket наблюдает свою network namespace; host-interface monitoring требует host namespace. Deployment policy должна разрешать этот доступ и netlink subscription. PathDown request может уступить timer expiry; более ранний commit не гарантируется.

Используйте rtnetlink для link/address/route notifications; TC/XDP/eBPF оставьте отдельно спроектированным packet-path features. Не-Linux stub не создаёт automatic retry events. Проверьте resync и interface recreation по [monitor tests](../../../internal/netio/ifmon_linux_test.go) и runtime reconciliation evidence.

## Источники

- [Реализация monitor](../../../internal/netio/ifmon_linux.go).
- [Linux rtnetlink API](https://man7.org/linux/man-pages/man7/rtnetlink.7.html).
- [Граница восстановления](../01-architecture.md#восстановление-и-readiness).
