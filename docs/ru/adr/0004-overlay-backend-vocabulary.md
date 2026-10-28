# ADR-0004: Общий словарь overlay backends

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-08-28 |

## Контекст

Startup validation и network-I/O construction должны одинаково принимать spellings и normalization backend. Зависимость configuration от Linux I/O либо socket-кода от configuration loaders нарушает границы пакетов.

## Решение

[`internal/overlay`](../../../internal/overlay/backend.go) владеет backend type, семью spellings, parser и implementation status. Он импортирует только standard library. Configuration и network I/O используют его, сохраняя compatibility aliases и package-specific sentinel errors.

| Input | Результат |
|:---|:---|
| Surrounding whitespace | Удаляется |
| Пустое значение | `userspace-udp` |
| Другая капитализация | Отказ; names case-sensitive |
| `userspace-udp` | Распознан и реализован |
| `kernel`, `ovs`, `ovn`, `cilium`, `calico`, `nsx` | Распознаны, не поддерживаются; fail closed |
| Другое имя | Invalid |

## Последствия

YAML/environment field names, defaults, values и public configuration types сохраняются. Startup validation и backend construction используют один parser. Invalid, unsupported и invalid-input errors сохраняют package context. Добавление spelling либо implementation status имеет одного owner; recognition не создаёт dataplane backend и не меняет protobuf.

## Источники

- [Общий parser](../../../internal/overlay/backend.go).
- [Constructor admission](../../../internal/netio/overlay_backend.go).
- [Configuration validation](../../../internal/config/config.go).
- [Linux applicability](0003-linux-advanced-bfd-applicability.md).
