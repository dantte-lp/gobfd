# GoBFD Benchmarks / Бенчмарки GoBFD

## English

The [benchmark guide](docs/en/12-benchmarks.md) defines commands, measured
operations and reporting policy. [Performance analysis](docs/en/14-performance-analysis.md)
defines the separate UDP, owner-commit, timer, workload and clock boundaries.

### Retained samples

| Evidence | Scope | Limit |
|---|---|---|
| [v0.4.0 raw Go samples](testdata/benchmarks/v0.4.0/benchmark.txt) and [metadata](testdata/benchmarks/v0.4.0/meta.json) | Historical Go 1.26 component benchmarks, six runs | Not current Go 1.27.3 qualification |
| `reports/collector-owner-20261022/` | Local Go 1.27.3 owner-commit benchmark with the real collector | Synthetic timing, no UDP/codec/auth; exact receipts are local evidence |
| [C models](bench/c/) | Repository-owned replicas of selected operations | Not upstream FRR/BIRD daemon measurements |

The 2026-10-22 owner benchmark records a baseline of 16 B/op and 2 allocs/op
and subsequent samples of 0 B/op and 0 allocs/op after caching active
peer/local packet-counter handles. Registration lifetime bounds that cache;
historical CounterVec series retain their existing lifetime. This does not
qualify lifetime metric cardinality, supported scale or a latency distribution.

### Interpret allocation results

- A zero-allocation result applies only to the named benchmark body.
- Authenticated unmarshal allocates `AuthSection`; the parsed digest borrows
  the input buffer. The allocation is not a digest copy and provides no
  independent digest lifetime.
- `BuildInnerPacketInto` writes into caller-owned storage and is used by
  production overlay TX. `BuildInnerPacket` is the allocating compatibility wrapper.
- Runtime FSM lookup uses an indexed array built from the declarative map
  during initialization. Old map-based timing is historical provenance.
- Construction B/op is not retained per-session RSS. Buffered enqueue is
  not packet commit, and arithmetic ns/op is not timer precision.

```bash
make up
make benchmark       # BFD package, six non-race runs with benchmem
make benchmark-all   # all packages, six non-race runs with benchmem
make profile         # separate CPU and heap profiles
```

The current CI report warns on performance regression; a warning alone does
not fail the job. Calibrated blocking regression policy and full runtime
acceptance remain separate Beads tasks. Do not convert component timings to
supported packet rates or claim immunity to GC pauses.

## Русский

[Руководство по benchmarks](docs/ru/12-benchmarks.md) определяет команды,
измеряемые операции и политику отчётности.
[Анализ производительности](docs/ru/14-performance-analysis.md) задаёт отдельные
границы UDP, owner commit, timer, workload и clock.

### Сохранённые samples

| Доказательство | Область | Ограничение |
|---|---|---|
| [Исходные Go samples v0.4.0](testdata/benchmarks/v0.4.0/benchmark.txt) и [metadata](testdata/benchmarks/v0.4.0/meta.json) | Исторические компонентные benchmarks Go 1.26, шесть запусков | Не текущая квалификация Go 1.27.3 |
| `reports/collector-owner-20261022/` | Локальный Go 1.27.3 owner-commit benchmark с real collector | Synthetic timing, без UDP/codec/auth; точные результаты — локальные evidence |
| [C-модели](bench/c/) | Собственные модели отдельных операций | Не измерения upstream FRR/BIRD daemon |

Owner benchmark 2026-10-22 содержит baseline 16 B/op и 2 allocs/op и следующие
samples 0 B/op и 0 allocs/op после кеширования packet-counter handles активных
peer/local pairs. Кеш ограничен lifetime регистрации; исторические CounterVec
series сохраняют прежний lifetime. Это не квалификация lifetime cardinality
метрик, поддерживаемого масштаба или распределения задержек.

### Интерпретация allocations

- Zero-allocation относится только к указанному телу benchmark.
- Authenticated unmarshal создаёт `AuthSection`; parsed digest ссылается
  на входной буфер. Это не копия digest и не независимый lifetime digest.
- `BuildInnerPacketInto` пишет в caller-owned storage и используется
  production overlay TX. `BuildInnerPacket` — allocating compatibility wrapper.
- Runtime FSM lookup использует indexed array, созданный при initialization
  из declarative map. Старые map-based timings остаются историческими.
- Construction B/op не является retained RSS на сессию. Buffered enqueue —
  не packet commit; arithmetic ns/op не задаёт точность таймера.

```bash
make up
make benchmark       # BFD package, шесть non-race запусков с benchmem
make benchmark-all   # все пакеты, шесть non-race запусков с benchmem
make profile         # отдельные CPU и heap profiles
```

Текущий CI report предупреждает о performance regression; warning сам по себе
не завершает job ошибкой. Calibrated blocking regression policy и полная runtime
acceptance — отдельные задачи Beads. Не превращайте component timings в
поддерживаемую packet rate и не заявляйте невосприимчивость к GC pauses.
