# GoBFD documentation

| Language | Manual |
|:---|:---|
| English — canonical | [Operator and developer manual](en/README.md) |
| Русский — перевод | [Руководство оператора и разработчика](ru/README.md) |

Use documentation from the binary's commit. These pages describe `dev`;
the accepted stable line has its own configuration and compatibility
contract. Consult the [RFC matrix](en/08-rfc-compliance.md) before selecting
a profile and the [vendor reference guide](en/reference/vendor-bfd.md)
before configuring an equipment peer.

Использовать документацию commit развёрнутого бинарника. Эти страницы описывают
`dev`; принятая стабильная линия имеет собственный контракт конфигурации и
совместимости. Выбрать профиль по [матрице RFC](ru/08-rfc-compliance.md),
параметры оборудования — по [документам производителей](ru/reference/vendor-bfd.md).

## Sources and records

| Material | Purpose |
|:---|:---|
| [RFC texts](rfc/) | Verbatim normative inputs; file presence does not imply implementation |
| [ADRs EN](en/adr/README.md), [ADR RU](ru/adr/README.md) | Decisions, constraints and acceptance boundaries |
| [Status EN](en/roadmap.md), [Статус RU](ru/roadmap.md) | Dated qualification boundaries |
| [Contract EN](en/reference/production-contract.md), [контракт RU](ru/reference/production-contract.md) | Product requirements and explicit acceptance boundaries |
| [Design records EN](en/reference/design-records.md), [решения RU](ru/reference/design-records.md) | Historical technical decisions and current canonical owners |
| [Standard EN](en/reference/documentation-standard.md), [стандарт RU](ru/reference/documentation-standard.md) | Prose, Markdown, diagrams, sources and translation |
| `en/audits/`, `ru/audits/` | Dated audit evidence; not a live support matrix |

Historical design, audit and release records retain their factual content.
Current operational procedures belong in the manual; qualification claims
require evidence for the exact candidate and deployment profile.
