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
- Authenticated unmarshal fills `AuthSection` by value without an auth-object
  allocation; its payload still borrows immutable input. The local three-run
  200000-iteration result is 0 B/op and 0 allocs/op. The in-memory plain RX
  benchmark changes from 176 B/op and 2 allocs/op to 128 B/op and 1 alloc/op;
  these measurements exclude sockets and owner commit. See
  [ADR-0010](docs/en/adr/0010-bfd-auth-value-ownership.md) for layout and lifetime.
- Built-in hash verification also reports 0 B/op and 0 allocs/op after returning
  digest results by value. Signing retains caller-owned payload allocations;
  [method samples](docs/en/12-benchmarks.md#authentication-methods--2026-10-26)
  exclude retained-wire copies and owner processing.
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

[Руководство по бенчмаркам](docs/ru/12-benchmarks.md) определяет команды,
измеряемые операции и политику отчётности.
[Анализ производительности](docs/ru/14-performance-analysis.md) задаёт отдельные
границы UDP, фиксации пакета владельцем, таймеров, нагрузки и часов.

### Сохранённые измерения

| Доказательство | Область | Ограничение |
|---|---|---|
| [Исходные измерения Go v0.4.0](testdata/benchmarks/v0.4.0/benchmark.txt) и [метаданные](testdata/benchmarks/v0.4.0/meta.json) | Исторические компонентные бенчмарки Go 1.26, шесть запусков | Не текущая квалификация Go 1.27.3 |
| `reports/collector-owner-20261022/` | Локальный бенчмарк фиксации владельцем с реальным сборщиком метрик, Go 1.27.3 | Синтетическое время, без UDP, кодека и аутентификации; точные результаты сохранены локально |
| [C-модели](bench/c/) | Собственные модели отдельных операций | Не измерения демонов из проектов FRR/BIRD |

Бенчмарк владельца от 2026-10-22 содержит исходные 16 B/op и 2 allocs/op и
последующие 0 B/op и 0 allocs/op после кеширования дескрипторов счётчиков пакетов
для активных пар соседнего и локального адресов. Кеш ограничен сроком регистрации;
исторические ряды CounterVec сохраняют прежний срок жизни. Это не квалификация
числа рядов за весь срок работы, поддерживаемого масштаба или распределения задержек.

### Интерпретация выделений памяти

- Нулевые выделения относятся только к указанному телу бенчмарка.
- Разбор аутентификации заполняет `AuthSection` по значению без выделения
  auth-объекта; payload по-прежнему заимствует неизменяемый вход. Локальные
  три серии по 200000 итераций дают 0 B/op и 0 allocs/op. In-memory benchmark
  обычного RX меняется со 176 B/op и 2 allocs/op на 128 B/op и 1 alloc/op;
  сокеты и фиксация владельцем в эти измерения не входят. Размеры и время жизни
  описаны в [ADR-0010](docs/ru/adr/0010-bfd-auth-value-ownership.md).
- Встроенная hash verification также даёт 0 B/op и 0 allocs/op после возврата
  digest по значению. Подпись сохраняет выделения payload во владение вызывающего
  кода; [измерения методов](docs/ru/12-benchmarks.md#методы-аутентификации--2026-10-26)
  не включают копирование сохраняемого wire и обработку владельцем.
- `BuildInnerPacketInto` пишет в память вызывающего кода и используется
  рабочим путём передачи overlay. `BuildInnerPacket` — обёртка совместимости
  с выделением памяти.
- Поиск FSM использует индексированный массив, созданный при инициализации
  из декларативной карты. Прежние измерения поиска по карте остаются историческими.
- B/op создания не является удерживаемым RSS на сессию. Запись в буферизованную
  очередь не является фиксацией пакета; ns/op вычислений не задаёт точность таймера.

```bash
make up
make benchmark       # пакет BFD, шесть запусков без race detector с benchmem
make benchmark-all   # все пакеты, шесть запусков без race detector с benchmem
make profile         # отдельные профили CPU и памяти
```

Текущий отчёт CI предупреждает о регрессии производительности; предупреждение
само по себе не завершает задание ошибкой. Калиброванная блокирующая политика
регрессий и полная приёмка работающей системы — отдельные задачи Beads.
Не превращайте компонентные измерения в поддерживаемую скорость обработки
пакетов и не заявляйте невосприимчивость к паузам GC.
