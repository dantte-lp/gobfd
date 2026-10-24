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
| [0004](./0004-overlay-backend-vocabulary.md) | Единый словарь overlay backend | Accepted |
| [0005](./0005-smallstep-management-certificates.md) | Внешний Smallstep CA для management-сертификатов | Accepted |
| [0006](./0006-session-preserving-reload-policy.md) | Политика reload с сохранением сессии | Accepted policy; qualification отдельно |
| [0007](./0007-workload-phase-accounting.md) | Фазы нагрузки и конечный учёт пакетов | Accepted policy; qualification отдельно |
| [0008](./0008-experimental-prerelease-boundary.md) | Граница экспериментального prerelease | Proposed; публикация не разрешена |
