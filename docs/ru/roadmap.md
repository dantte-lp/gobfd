# Дорожная карта GoBFD

![Текущий релиз](https://img.shields.io/badge/Current-v0.6.1-1a73e8?style=for-the-badge)
![Следующий релиз](https://img.shields.io/badge/Next-v0.6.4-34a853?style=for-the-badge)
![Цель](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Проекция состояния Beads, сверенная 2026-08-27. Реестр задач находится в
> Beads; этот документ объясняет публичную последовательность релизов и не
> является независимым checklist.

Последний опубликованный релиз GitHub —
[`v0.6.1`](https://github.com/dantte-lp/gobfd/releases/tag/v0.6.1).
Неизменяемые теги `v0.6.2` и `v0.6.3` являются неопубликованными неудачными
попытками; следующий maintenance-релиз — `v0.6.4`, затем additive
production-контракт `v1.0.0`. Защищённая линия `release/v0.6` сохраняет GoBGP
v3.37.0; разработка v1 и переход на GoBGP v4 продолжаются в `dev`.

## Обозначения статусов

| Статус | Значение |
|---|---|
| Готово | Принято в текущей истории `dev` |
| В работе | Активная работа или независимая проверка не завершена |
| Открыто | Запланировано в Beads, но ещё не принято |

## Maintenance baseline v0.6

Milestone Beads: `gobfd-qj0.8.1` — **в работе**.

После создания защищённой ветки релиз будет готовиться в `release/v0.6`, где
сохраняются GoBGP v3.37.0 и существующие runtime-контракты `bfd.v1` и YAML. Он
обновляет зависимости, инструменты, CI, воспроизводимость, документацию и
тестовую инфраструктуру без добавления нового поведения протокола BFD.
Baseline остаётся **в работе**, пока не проверены тег и GitHub Release v0.6.4.

| Часть поставки | Статус |
|---|---|
| Инвентаризация версий зависимостей и инструментов | Готово |
| Toolchain Go 1.27 и обновление CI | Готово |
| Go-owned Podman testcontainers harness | Готово |
| Миграция interop, integration и E2E orchestration | Готово |
| Контракт Python 3.14.7 и Docker Compose v5 | Готово |
| Инвентарь лицензий, SBOM, OCI provenance и уязвимостей | Готово |
| Граница образов Debian trixie / Oracle Linux 10 | Готово |
| Исправление публичных RFC- и benchmark-заявлений | Готово |
| Roadmap, Quick Start, архитектура и EN/RU parity | Готово |
| Независимая проверка всех частей v0.6.2 и исправление P0/P1 | Готово |
| Регистрация изолированного `tools/go.mod` в Dependabot | Готово |

Релиз не получает тег, пока не приняты qualification milestone
`gobfd-qj0.8.1.7`, независимая проверка `gobfd-qj0.8.1.8` и все обязательные
локальные и удалённые release gates.

## Сверка legacy S12

Прежний waterfall-документ S12-S20 появился до утверждённого релизного плана
Beads. Typed CRUD из S12 был поставлен в `v0.6.0` только частично:

| Контракт S12 | Текущее подтверждение | Статус |
|---|---|---|
| CRUD `EchoService` и `gobfdctl echo` | Есть proto, server, CLI и tests | Готово |
| CRUD `MicroBFDService` и `gobfdctl micro` | Есть proto, server, CLI и tests | Готово |
| `OverlayService` для VXLAN/Geneve | Service и CLI-команды отсутствуют | Не поставлено |

Наличие конфигурационных и runtime-путей VXLAN/Geneve не означает наличия
typed Overlay API. Публичная граница поддержки остаётся указанной в
[матрице соответствия RFC](./08-rfc-compliance.md).

Старые checklists S13-S20 заменены текущим планом. Secure management, GoBGP v4,
S-BFD, kernel backends и AF_XDP нельзя считать запланированными или готовыми по
историческим целям; авторитетны только текущие issues Beads и принятый код.

## Production-контракт v1.0.0

Milestone Beads: `gobfd-qj0.8.2` — **открыт; заблокирован до приёмки и
публикации maintenance baseline v0.6 (`v0.6.4`)**.

Разработка продуктовой линии v1, включая переход на GoBGP v4, ведётся в `dev`
и не меняет границу GoBGP v3.37.0 ветки `release/v0.6`.

### Последовательность P0

| Часть поставки | Статус |
|---|---|
| Корректность RFC core и учёт потерь | Открыто |
| Reconciliation ownership и конфигурации | Открыто |
| Безопасные management defaults | Открыто |
| Безопасный переход на GoBGP v4 | Открыто |
| Независимая проверка реализации | Открыто |
| Interop, scale, security и release qualification | Открыто |

RFC core начинается с отслеживаемых пробелов Poll/Final и Demand procedures,
диагностик и сброса аутентификации, атомарной доставки BFD/AdminDown,
transport demultiplexing RFC 5881/5883, authenticated padding RFC 9764 и
fail-closed границ preview-возможностей.

### Последовательность P1

| Часть поставки | Статус |
|---|---|
| Настраиваемая BFD QoS socket policy с packet evidence | Открыто |
| Измерение committed latency и корректные performance gates | Открыто |
| Удаление постоянного OS-thread pinning сессий с A/B evidence | Открыто |
| Усиление companion binaries | Открыто |

Post-v1 R&D по scheduler, kernel, warm restart, S-BFD и аутентификации находится
вне этого релизного контракта и отслеживается отдельно в Beads.

## Релизные контракты

- [Maintenance-дизайн v0.6.2](../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md)
- [Production-дизайн v1](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
- [Матрица соответствия RFC](./08-rfc-compliance.md)
- [Разработка и quality gates](./09-development.md)
