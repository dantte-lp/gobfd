# Сравнение реализаций BFD

Сравнивайте конкретную реализацию, релиз и профиль развёртывания. Поддержка RFC,
wire interoperability, эксплуатационная интеграция и производительность —
отдельные свойства. Текущие границы GoBFD задаёт [матрица RFC](08-rfc-compliance.md),
а не общий рейтинг функций.

## Что измеряет этот репозиторий

| Доказательство | Что подтверждает | Что не подтверждает |
|---|---|---|
| Go micro-benchmarks | Стоимость и allocations тела указанного benchmark | UDP throughput, точность таймеров и поддерживаемый масштаб |
| Локальные C-model benchmarks | Стоимость операций собственных моделей репозитория | Производительность upstream FRR/BIRD daemon или рейтинг языков |
| Базовая топология четырёх peers | Протокольное взаимодействие с настроенными FRR, BIRD3, Holo и Thoro | Все расширения, vendor NOS и окружения |
| Топология BGP coupling | Проверенные взаимодействия GoBGP, FRR, BIRD3 и ExaBGP | Непрерывность маршрутизации при непроверенных отказах и reload |
| Топология vendor NOS | Поведение выбранного образа и сценария при фактическом запуске | Все платформы, релизы, address families и hardware offload |
| Production-core workload fixture | Наблюдавшийся учёт пакетов owner/peer и очистку | Приёмку reference load или всего daemon |

Pins исходников peers и определения топологий хранятся в
[`test/interop/`](../../test/interop/),
[`test/interop-bgp/`](../../test/interop-bgp/) и
[`test/interop-clab/`](../../test/interop-clab/).
[Interoperability](05-interop.md) задаёт границы квалификации. При сравнении
сохраняйте точную identity образа, source SHA и результаты проверок.

## Метод сравнения программных реализаций

Используйте одинаковые число сессий, address families, аутентификацию, размер
пакета, согласованные интервалы, Detect Multiplier, потери и лимиты CPU/RAM.
Отделяйте протокольную нагрузку от обновления routing table и management traffic.
Фиксируйте фактическое настроенное поведение peer; не переносите настройки
по умолчанию из другой версии без проверки.

C-файлы FRR/BIRD в [`bench/c/`](../../bench/c/) — локальные модели отдельных
операций. Они не линкуют upstream daemon code. Границы validation, lookup,
scheduling и iterations отличаются от Go benchmarks. См.
[Анализ производительности](14-performance-analysis.md#граница-компонентного-сравнения).
Из этих таблиц нельзя выводить upstream throughput, RSS на сессию, поведение
при restart или относительную скорость языков.

Control и Echo owners GoBFD работают в отдельных goroutines; оба сейчас
закрепляют OS thread. Владение goroutine само по себе не резервирует CPU,
не изолирует scheduling и не задаёт поддерживаемый масштаб. Reference runtime
qualification пока не завершена.

## Hardware и cloud profiles

Hardware offload, минимальные таймеры, ёмкость по сессиям, address families,
Echo и control-plane fate sharing определяются точной моделью оборудования,
версией ПО и сервисом подключения. Наличие peer в списке или lab topology
не означает квалификацию hardware platform либо cloud provider.

Для целевого развёртывания получите первичную документацию, выберите общие
процедуры и интервалы, затем измерьте фактический путь. Храните политику
failure detection и graceful restart провайдера отдельно от реакции GoBFD.
Общие интервалы RFC 7419 не подтверждают поддержку провайдера, точность
таймеров или непрерывность forwarding.

## Интерпретация таймеров и нагрузки

В Asynchronous BFD локальное Detection Time равно принятому Detect Multiplier,
умноженному на `max(local RequiredMinRxInterval, remote DesiredMinTxInterval)`.
Номинальный интервал передачи до jitter равен
`max(local DesiredMinTxInterval, remote RequiredMinRxInterval)`.
Нулевой remote receive interval, remote Demand и Poll transactions влияют
на периодическую передачу; см. [Протокол](02-protocol.md).

Для 1 000 сессий с номинальным интервалом 100 ms значение 10 000 пакетов/с
в каждом направлении — расчёт до jitter, suppression и протокольных эффектов,
а не измеренная поддерживаемая скорость. Более короткие интервалы требуют
измеренного запаса scheduling и loss. [Границы runtime-измерений](14-performance-analysis.md)
описывают необходимые denominators, clock evidence и ограничения квалификации.

## Связанные документы

- [Руководство по benchmarks](12-benchmarks.md) — команды и статистическая интерпретация.
- [Снимки benchmarks](../../testdata/benchmarks/) — исходные исторические результаты.
- [Соответствие RFC](08-rfc-compliance.md) — реализованные и неполные процедуры.
- [Roadmap](roadmap.md) — датированный статус; исполнением управляет Beads.
