# Архитектурные решения (ADR)

Действующие записи решений GoBFD. Каждая запись фиксирует контекст,
решение и последствия. Поле даты (`Date` в английском оригинале) устанавливается при переходе в
статус `Accepted` и далее не меняется. Заменённые записи остаются на
месте с прямой ссылкой на замену.

## Формат

```markdown
# ADR-NNNN: Название

| Поле | Значение |
|---|---|
| Статус | Одно из: Proposed, Accepted, Superseded by ADR-NNNN, Deprecated |
| Дата | YYYY-MM-DD |

## Контекст
## Решение
## Последствия
## Источники
```

## Индекс

| ID | Заголовок | Статус |
|---|---|---|
| [0001](./0001-link-state-rtnetlink.md) | Мониторинг состояния линков через rtnetlink | Accepted |
| [0002](./0002-ovs-backend-ovsdb.md) | Нативный OVSDB-клиент для OVS LAG backend | Accepted |
| [0003](./0003-linux-advanced-bfd-applicability.md) | Применимость Micro-BFD, VXLAN BFD и Geneve BFD в Linux | Accepted |
| [0004](./0004-overlay-backend-vocabulary.md) | Единый словарь overlay backend | Accepted |
| [0005](./0005-smallstep-management-certificates.md) | Внешняя Smallstep CA для сертификатов управления | Accepted |
| [0006](./0006-session-preserving-reload-policy.md) | Политика перезагрузки с сохранением сессии | Принятая политика; квалификация отдельно |
| [0007](./0007-workload-phase-accounting.md) | Фазы нагрузки и конечный учёт пакетов | Принятая политика; квалификация отдельно |
| [0008](./0008-experimental-prerelease-boundary.md) | Граница экспериментального prerelease | Proposed; публикация не разрешена |
| [0009](./0009-echo-principal-claims.md) | Права субъектов на общую Echo-сессию | Принятая политика; локальная квалификация отдельно |
