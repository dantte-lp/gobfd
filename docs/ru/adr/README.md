# Архитектурные решения

ADR фиксируют архитектурную политику и последствия. Принятие ADR не подтверждает реализацию, runtime qualification либо stable delivery.

## Контракт записи

Сохраняйте ID, filename и дату принятия. Установите Date при переходе в Accepted; оставьте superseded record и ссылку на замену. Используйте Status, Date, контекст, решение, последствия и источники. Добавьте qualification field, когда policy acceptance и runtime acceptance различаются.

Справочная часть содержит факты в настоящем времени со ссылками на исходники. Процедуры используют повелительное наклонение. Измерения оформляйте датированными receipts с их input; прежний receipt не подтверждает более поздний candidate.

## Указатель

| ID | Решение | Status |
|:---|:---|:---|
| [0001](0001-link-state-rtnetlink.md) | Мониторинг link/address через rtnetlink | Accepted |
| [0002](0002-ovs-backend-ovsdb.md) | Native OVSDB LAG backend | Accepted |
| [0003](0003-linux-advanced-bfd-applicability.md) | Linux ownership и applicability advanced modes | Accepted |
| [0004](0004-overlay-backend-vocabulary.md) | Общий словарь overlay backends | Accepted |
| [0005](0005-smallstep-management-certificates.md) | Внешнее Smallstep certificate renewal | Accepted |
| [0006](0006-session-preserving-reload-policy.md) | Reload с сохранением сессии | Принятая policy; qualification отдельно |
| [0007](0007-workload-phase-accounting.md) | Фазы workload и terminal accounting | Принятая policy; qualification отдельно |
| [0008](0008-experimental-prerelease-boundary.md) | Experimental prerelease channel | Proposed; публикация не разрешена |
| [0009](0009-echo-principal-claims.md) | Principal claims общего Echo wire | Принятая policy; local qualification отдельно |
| [0010](0010-bfd-auth-value-ownership.md) | Value auth metadata с borrowed payload | Superseded by ADR-0011 |
| [0011](0011-bfd-auth-inline-payload.md) | Inline authentication payload | Принятая policy; source/runtime qualification отдельно |
| [0012](0012-owner-down-transition-witness.md) | Owner-monotonic Down evidence | Принятая policy; classification/release qualification отдельно |

Границы реализации и peers заданы в [архитектуре](../01-architecture.md), [RFC coverage](../08-rfc-compliance.md), [vendor references](../reference/vendor-bfd.md).
