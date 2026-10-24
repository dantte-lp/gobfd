# Руководство по benchmarks

Micro-benchmarks измеряют указанные операции. Они не подтверждают UDP throughput,
потери, точность таймеров, невосприимчивость к GC или поддерживаемый масштаб.
[Анализ производительности](14-performance-analysis.md) описывает отдельные
границы runtime-измерений и квалификации.

## Запуск benchmarks

Перечисленные ниже benchmark-команды выполняются в закреплённом Podman
development environment. Сначала выполните `make up`. Ограничения bootstrap
на хосте и отдельной цели `benchmark-report` описаны в
[Разработке](09-development.md) и разделе отчётов ниже.

| Команда | Вход и выход |
|---|---|
| `make benchmark` | `internal/bfd`, шесть запусков с `-benchmem`; stdout и `benchmark.txt` |
| `make benchmark-all` | Все пакеты, шесть запусков с `-benchmem`; stdout и `benchmark.txt` |
| `make benchmark-save BENCH_VERSION=local-doc-audit` | Samples BFD/netio и metadata в `testdata/benchmarks/local-doc-audit/` |
| `make benchmark-compare OLD=v0.4.0 NEW=local-doc-audit` | Сравнение `benchstat` и HTML в `reports/benchmarks/` |
| `make profile` | CPU/heap profiles BFD: `cpu.prof` и `mem.prof` |
| `make benchmark-cross` | Go-операции и локальные C-модели последовательно; `bench-results/` |

Используйте новый идентификатор snapshot, сохраняя предыдущие samples; он не
создаёт release tag. `benchmark-save` отдельно запускает BFD и netio с
`-bench=. -benchmem -count=6 -run='^$' -timeout=120s`.
`benchmark` и `benchmark-all` не включают race detector и не записывают CPU/
heap profiles. Race qualification относится к test gate; profiling выполняется отдельно.

| Файл snapshot | Содержание |
|---|---|
| `benchmark-bfd.txt` | Samples BFD benchmarks |
| `benchmark-netio.txt` | Samples network codec и metadata benchmarks |
| `benchmark.txt` | Объединённые samples |
| `meta.json` | Идентификатор snapshot, commit, дата, версия Go, число запусков |

## Чтение вывода

```text
BenchmarkControlPacketMarshal-8    195605820    5.869 ns/op    0 B/op    0 allocs/op
```

| Поле | Значение |
|---|---|
| `-8` | Фактический `GOMAXPROCS` benchmark |
| `195605820` | Адаптивное число iterations; сравнивайте time/op, а не только counts |
| `ns/op` | Elapsed time одной операции |
| `B/op`, `allocs/op` | Учтённые heap bytes и allocations одной операции |

Сохраняйте raw samples. Повторение само по себе не устраняет host contention,
CPU throttling, влияние cache или timer noise. Сравнивайте одинаковые тела
benchmarks, compiler, architecture, limits и environment; оцените статистическую
неопределённость до объяснения результата изменением исходников. Race и non-race
стоимости не являются одной нагрузкой.

## Границы операций

| Семейство benchmark | Измеряемая работа | Исключение или ограничение |
|---|---|---|
| Control marshal | Сериализация пакета в переданный буфер | With-auth marshal записывает готовую auth section, но не подписывает |
| Control unmarshal | Парсинг и validation wire fields | With-auth parse создаёт `AuthSection`; digest ссылается на входной буфер |
| FSM | Indexed transition lookup | Без owner loop, socket, timer и notification |
| Jitter и interval arithmetic | Указанное вычисление | Без timer wake-up и scheduling delay |
| `RecvDecodeLookupEnqueue` | Decode, discriminator lookup, попытка buffered enqueue | Enqueue не является обработкой пакета или owner commit |
| `RecvDecodeFSM` | Decode и stateless FSM transition | Без live owner state и transport |
| `ReceiveOwnerCollector` | Фактический owner commit с real collector | Synthetic timing; без UDP, wire decoding и authentication |
| `TxMarshalJitter` | Marshal и расчёт jitter | Без socket send и peer arrival |
| Create/reconcile | Жизненный цикл sessions | B/op не является полным retained RSS на сессию |
| `BuildInnerPacketInto` | Сборка inner packet в caller-owned storage | Production overlay TX path |
| `BuildInnerPacket` | Allocating compatibility wrapper | Отдельные стоимость и ownership |
| Metadata/timer/metrics | Указанный parser или observer | Без полного packet pipeline и scale guarantee |

Определения находятся в [`internal/bfd/`](../../internal/bfd/),
[`internal/netio/`](../../internal/netio/) и
[`internal/metrics/`](../../internal/metrics/). При изменении имени или границ
проверяйте тело benchmark. Не фиксируйте число benchmarks в документации.

## Политика allocations

Выбранные операции codec, FSM, timer, lookup и caller-buffer имеют цель
zero allocations. Authenticated parsing создаёт `AuthSection`; digest там
не копируется, поэтому retaining callers должны учитывать lifetime буфера.
Compatibility inner-packet builder выделяет возвращаемый slice. Construction
и reconciliation сессий выделяют lifecycle state. Нулевой результат одного
тела не доказывает zero allocations всех hot paths и не ограничивает GC pauses.

Исторические Go 1.26 samples сохранены в
[`testdata/benchmarks/v0.4.0/`](../../testdata/benchmarks/v0.4.0/).
Их ns/op не являются приёмкой Go 1.27.3; их нельзя отмечать как выполнение
постороннего threshold. [BENCHMARKS.md](../../BENCHMARKS.md) ссылается на
сохранённые samples и более новые ограниченные owner observations.

## Политика CI и отчёты

Текущий benchmark report выдаёт warning для critical regression выше своего
threshold; само предупреждение не завершает job ошибкой. Calibrated blocking
regression policy остаётся в Beads `gobfd-qj0.8.2.1.6.4`. Сохраняйте отказы
при malformed output и не называйте warning release performance gate.

`make test-report` запускает race suite и создаёт JUnit/HTML в
`reports/tests/`; `make report-all` также сохраняет и сравнивает Go benchmarks.
`benchmark-report` — старый Make target с прямым вызовом `go run`;
запускайте генератор в закреплённом контейнере без host Go.
Analysis UIs должны слушать loopback. Profiles могут содержать чувствительные данные.

## Связанные документы

- [Разработка](09-development.md) — закреплённые инструменты и quality workflow.
- [Сравнения](13-competitive-analysis.md) — ограничения C-моделей и deployment.
- [Производительность](14-performance-analysis.md) — telemetry, workload и resources.
- [ADR workload](adr/0007-workload-phase-accounting.md) — политика фаз и ledger.
