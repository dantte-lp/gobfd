# Дорожная карта GoBFD

![Текущий релиз](https://img.shields.io/badge/Current-v0.6.4-1a73e8?style=for-the-badge)
![Следующий релиз](https://img.shields.io/badge/Next-TBD-34a853?style=for-the-badge)
![Цель](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Проекция состояния Beads, сверенная 2026-08-28. Реестр задач находится в
> Beads; этот документ объясняет публичную последовательность релизов и не
> является независимым checklist.

Последний опубликованный релиз GitHub —
[`v0.6.4`](https://github.com/dantte-lp/gobfd/releases/tag/v0.6.4).
Неизменяемые теги `v0.6.2` и `v0.6.3` остаются неопубликованными неудачными
попытками. Артефакты и cumulative notes v0.6.4 проверены, а принятая стабильная
история и оба changelog доставлены в `master`. Три ранее найденных P1 отдельно
отслеживаются в Beads. Защищённая линия `release/v0.6` сохраняет GoBGP v3.37.0;
разработка v1 и переход на GoBGP v4 продолжаются в `dev`.

## Обозначения статусов

| Статус | Значение |
|---|---|
| Готово | Принято в текущей истории `dev` |
| В работе | Активная работа или независимая проверка не завершена |
| Открыто | Запланировано в Beads, но ещё не принято |

## Maintenance baseline v0.6

Milestone Beads: `gobfd-qj0.8.1` — **в работе**.

Защищённая ветка `release/v0.6` сохраняет GoBGP v3.37.0 и существующие
runtime-контракты `bfd.v1` и YAML. Она обновляет зависимости, инструменты, CI,
воспроизводимость, документацию и тестовую инфраструктуру без добавления нового
поведения протокола BFD. Тег, assets и OCI-образы v0.6.4 проверены, но baseline
остаётся **в работе** до исправления и независимой проверки P1
`gobfd-qj0.8.1.8.8`, `.8.9` и `.8.10`.

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
| Независимая проверка всех частей v0.6 и исправление P0/P1 | В работе |
| Регистрация изолированного `tools/go.mod` в Dependabot | Готово |

Release-задача `gobfd-qj0.8.1.15` завершена после исправления: неизменяемый
`v0.6.4` по-прежнему указывает на
`b1c0bcd7d2e9abed00368b2082e34f521084c087`, все 12 assets и OCI indexes
проверены, а body теперь охватывает v0.6.2-v0.6.4. PR `#67` и `#68` доставили
принятое исправление в `release/v0.6` и `master`; эта история `dev` содержит
отдельный forward-port. Qualification `gobfd-qj0.8.1.7` и независимая проверка
`gobfd-qj0.8.1.8` остаются открыты на трёх P1 выше.

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

Milestone Beads: `gobfd-qj0.8.2` — **открыт; заблокирован до приёмки baseline
v0.6 после завершения closeout v0.6.4 и исправления P1**.

Разработка продуктовой линии v1, включая переход на GoBGP v4, ведётся в `dev`
и не меняет границу GoBGP v3.37.0 ветки `release/v0.6`.

### Последовательность P0

| Часть поставки | Статус |
|---|---|
| Корректность RFC core и учёт потерь | Открыто |
| Reconciliation ownership и конфигурации | В работе; реализованы C01.1, C01.2, C01.3a и C01.3b |
| Безопасные management defaults | Открыто |
| Безопасный переход на GoBGP v4 | Открыто |
| Независимая проверка реализации | Открыто |
| Interop, scale, security и release qualification | Открыто |

Принятый core C01.1 предоставляет канонический ключ сессии, отделённый от
packet demultiplexing, сериализованные типизированные claims конфигурации,
compatibility/API и unsolicited, а также неизменяемую static-auth identity.
C01.2 добавляет проверку полного candidate до создания sender, передачу пустого
desired set и отдельные типизированные owners для base BFD, Micro-BFD, VXLAN и
Geneve. C01.3a добавляет ленивые Manager-owned sender leases для принятых
physical sessions, точное освобождение при последнем claim и shutdown, возврат
API source port и явные non-owning leases для общих overlay и unsolicited
transports. C01.3b добавляет lifecycle Manager Open/Closing/Closed, fail-closed
gates для мутаций и подписок, ожидание зарегистрированных goroutines, точное
закрытие notification channels и sender callbacks после завершения session и
вне locks Manager. Он также охватывает echo reconciliation и CRUD/reconciliation
Micro-BFD groups как одну top-level lifecycle operation. Рекурсивный blocking
Close из synchronous release callback требует явного API design, который
отслеживается в `gobfd-qj0.8.2.2.5.1`; остальные Manager APIs callback может
безопасно вызывать повторно. Он не завершает C01 или SIGHUP reload. Замена
listeners/backends, стабильные owners отдельных groups/tunnels, generations,
согласование Poll/Final, transport-aware demultiplexing и аутентифицированные
API principals остаются открытыми.

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
