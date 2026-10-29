# GoBFD benchmark evidence

## English

[Benchmark procedures](docs/en/12-benchmarks.md) define collection, statistical interpretation and named operation boundaries. [Runtime measurement](docs/en/14-performance-analysis.md) defines separate UDP, owner-commit, timer, clock and workload evidence.

| Retained source | Scope | Limitation |
|:---|:---|:---|
| [v0.4.0 samples](testdata/benchmarks/v0.4.0/benchmark.txt) and [metadata](testdata/benchmarks/v0.4.0/meta.json) | Go 1.26.0, six component runs, 2026-02-24 | Historical; not Go 1.27.3 qualification |
| Local reports/collector-owner-20261022 | Real-collector owner: 16 B/op, 2 allocs/op → 0 B/op, 0 allocs/op | Synthetic timing; no UDP/decode/auth |
| [2026-10-26 allocation matrix](docs/en/12-benchmarks.md#retained-allocation-measurements) | Auth, owner and dispatch observations | Exact body only; no scale/latency verdict |
| [C models](bench/c/) | Repository-owned operation replicas | No upstream FRR/BIRD daemon or language ranking |

Run `make benchmark`, `make benchmark-all` or `make benchmark-save` after `make up`. Use `make profile` separately. Make/release snapshots retain six runs; CI collects ten alternating base/head pairs of 100 ms. `cictl benchmark-report` fails on classified critical regressions of at least 10% after report publication; the critical time/op floor is 100 ns/op on either side.

Zero allocations in a named body do not imply zero runtime allocations, GC immunity or supported packet rates. Production overlay TX uses caller-owned BuildInnerPacketInto; the compatibility wrapper allocates. See [ADR-0011](docs/en/adr/0011-bfd-auth-inline-payload.md) for auth packet/wire ownership.

## Русский

[Процедуры бенчмарков](docs/ru/12-benchmarks.md) задают сбор, статистическую интерпретацию и границы именованных операций. [Измерения runtime](docs/ru/14-performance-analysis.md) задают отдельные доказательства UDP, owner commit, timers, clocks и нагрузки.

| Сохранённый источник | Область | Ограничение |
|:---|:---|:---|
| [v0.4.0 samples](testdata/benchmarks/v0.4.0/benchmark.txt) и [metadata](testdata/benchmarks/v0.4.0/meta.json) | Go 1.26.0, шесть component runs, 2026-02-24 | История; не квалификация Go 1.27.3 |
| Локальные reports/collector-owner-20261022 | Real-collector owner: 16 B/op, 2 allocs/op → 0 B/op, 0 allocs/op | Synthetic timing; без UDP/decode/auth |
| [Матрица выделений 2026-10-26](docs/ru/12-benchmarks.md#сохранённые-измерения-выделений) | Наблюдения auth, owner и dispatch | Только точное тело; без scale/latency verdict |
| [C-модели](bench/c/) | Собственные replicas операций | Не upstream FRR/BIRD daemons и не рейтинг языков |

Выполните `make benchmark`, `make benchmark-all` или `make benchmark-save` после `make up`. Используйте `make profile` отдельно. Make/release snapshots содержат шесть runs; CI собирает десять чередующихся base/head-пар по 100 мс. `cictl benchmark-report` завершается ошибкой при классифицированных critical regressions от 10% после публикации отчётов; critical time/op floor — 100 ns/op с любой стороны.

Ноль выделений в одном теле не означает нулевых runtime allocations, отсутствия пауз GC и поддерживаемой скорости пакетов. Production overlay TX использует caller-owned BuildInnerPacketInto; compatibility wrapper выделяет память. [ADR-0011](docs/ru/adr/0011-bfd-auth-inline-payload.md) определяет владение auth packet/wire.
