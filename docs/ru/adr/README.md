# Architecture Decision Records

Живые decision-записи для GoBFD. Каждая запись фиксирует контекст,
решение и последствия. Поле `Date` устанавливается при переходе в
статус `Accepted` и далее не меняется. Заменённые записи остаются на
месте с прямой ссылкой на замену.

## Формат

```markdown
# ADR-NNNN: Title

| Field | Value |
|---|---|
| Status | Proposed | Accepted | Superseded by ADR-NNNN | Deprecated |
| Date | YYYY-MM-DD |

## Context
## Decision
## Consequences
## References
```

## Индекс

| ID | Заголовок | Статус |
|---|---|---|
| [0001](./0001-link-state-rtnetlink.md) | Мониторинг состояния линков через rtnetlink | Accepted |
| [0002](./0002-ovs-backend-ovsdb.md) | Нативный OVSDB-клиент для OVS LAG backend | Accepted |
| [0003](./0003-linux-advanced-bfd-applicability.md) | Применимость Micro-BFD, VXLAN BFD и Geneve BFD в Linux | Accepted |
