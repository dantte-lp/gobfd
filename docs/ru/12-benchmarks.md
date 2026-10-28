# Бенчмарки

Микробенчмарк измеряет одну именованную операцию. Он не определяет скорость обработки пакетов, поддерживаемое число сессий, точность таймеров, потери и отсутствие пауз GC. [Измерения runtime](14-performance-analysis.md) задают дополнительные границы owner, UDP, часов и нагрузки.

## Сбор снимка

1. Подготовьте [закреплённую среду разработки](09-development.md).
2. Выберите новый идентификатор снимка; сохраните прежние исходные измерения.
3. Выполните нужный сбор и сравнение:

```bash title="Снимок Go benchmarks"
make up
make benchmark-save BENCH_VERSION=local-doc-audit
make benchmark-compare OLD=v0.4.0 NEW=local-doc-audit
```

4. Требуйте успешного exit и полных samples до использования metadata и reports. Сохраните частичный вывод отказа для диагностики.
5. Сравнивайте одинаковые benchmark bodies, compiler, architecture, limits и environment. Проверьте uncertainty и конкуренцию нагрузки до отнесения результата к коду.

| Цель | Сбор и output |
|:---|:---|
| `benchmark` | Пакет BFD, шесть non-race benchmem runs; stdout и benchmark.txt |
| `benchmark-all` | Все пакеты, шесть non-race benchmem runs; stdout и benchmark.txt |
| `benchmark-save` | BFD/netio отдельно, шесть runs, package timeout 120 секунд |
| `benchmark-compare` | benchstat text и HTML в reports/benchmarks |
| `profile` | Отдельные BFD CPU/heap profiles: cpu.prof и mem.prof |
| `benchmark-cross` | Последовательные C-модели и Go samples в bench-results |
| `benchmark-report` | Legacy direct Go generator; запускается в закреплённый контейнер |
| `test-report`, `report-all` | Race JUnit/HTML; дополнительно снимок и comparison |

Снимок содержит `benchmark-bfd.txt`, `benchmark-netio.txt`, объединённый `benchmark.txt` и `meta.json` с version/commit/date/Go/count. Make pipelines используют Bash errexit/pipefail; ошибка Go, benchstat или tee завершает цель ошибкой. Неуспешный повтор может оставить прежние metadata/HTML рядом с частичными samples. Новый identifier и успешный полный запуск исключают принятие такой смеси.

### Парное сравнение в CI

CI `cictl benchmark-pair --old old.txt --new new.txt` использует десять чередующихся base/head и head/base-пар. Каждый Go invocation выдаёт один non-race sample длительностью 100 мс; каждая package/name требует ровно десяти valid records на сторону. Invalid/empty/incomplete output отклоняется до report. Один detached base worktree имеет bounded cancellation-safe cleanup. BENCH_REGEX, GITHUB_BASE_REF и RUNNER_TEMP определены в [automation](reference/automation.md).

Шесть Make/release runs и самостоятельные benchmark-run/base остаются последовательной диагностикой. Сохраняйте фиксированный бюджет; не повторяйте сбор ради желаемого статистического результата.

## Интерпретация результата

```text
BenchmarkControlPacketMarshal-8    195605820    5.869 ns/op    0 B/op    0 allocs/op
```

Исторический формат показывает GOMAXPROCS 8, adaptive iteration count, time/op и heap allocation/op. Значение не является текущим обещанием производительности. [Go testing](https://pkg.go.dev/testing#hdr-Benchmarks) определяет интерфейс измерения. Construction B/op не равно retained RSS; ноль allocs/op относится только к телу бенчмарка. Profiling и race-тесты — отдельные нагрузки.

## Границы операций

| Семейство | Включено | Исключено |
|:---|:---|:---|
| Marshal | Запись в supplied storage; auth section уже готова | Подпись и отправка |
| Unmarshal | Decode/validate; bounded auth metadata по значению | Hash verification и retained immutable wire |
| Authentication | Sign или VerifyKnownSequence, immutable key store, controlled prior sequence | Socket, retained-wire copies, фиксация пакета владельцем сессии |
| FSM | Indexed transition lookup | Owner loop, transport, timers, notification |
| Jitter/interval | Именованное вычисление | Планирование/пробуждение timer |
| RecvDecodeLookupEnqueue | Decode, discriminator lookup, попытка запись в буферизованную очередь | Обработка пакета и фиксация пакета владельцем сессии |
| RecvDecodeFSM | Decode и stateless transition | Живой owner и transport |
| ReceiveOwnerCollector | Commit реальным owner с real collector | UDP, decode, auth; синтетическое время |
| ReceiveOwnerCollectorAuthentication | Sign/marshal/decode/demux/verify/commit, real metrics, steady Up fixture | Socket/kernel RX, retained-wire allocation; синтетические часы |
| TxMarshalJitter | Marshal и jitter | Socket send и peer arrival |
| Create/reconcile | Lifecycle state | Retained process RSS |
| BuildInnerPacketInto | Production assembly во внешнем буфере | Storage allocation |
| BuildInnerPacket | Allocating compatibility wrapper | Production caller-буфер ownership |

Читайте тела в [BFD benchmarks](../../internal/bfd/bench_test.go), [scaling fixtures](../../internal/bfd/bench_scaling_test.go), [Micro-BFD dispatch](../../internal/bfd/manager_micro_bench_test.go) [netio benchmarks](../../internal/netio/bench_test.go) и [измерения owner с реальным collector](../../internal/metrics/receive_timing_test.go). Не фиксируйте число бенчмарков при изменении names.

## Сохранённые измерения выделений

Следующие датированные результаты являются сохранёнными наблюдениями, а не новым выполнением при обновлении документации. Квалификация финальный кандидат остаётся отдельной.

### Методы аутентификации — 2026-10-26

В `972946c59bbe8d9bc36fbe51edb9f6c82f3bfc33`: linux/amd64, закреплённый компилятор, ключ 16 bytes, три runs по 200000 iterations:

| Режим | Sign до → после B/op; allocs/op | Verify до → после B/op; allocs/op |
|:---|:---|:---|
| Simple Password | 16;1 → 16;1 | 0;0 → 0;0 |
| Keyed/Meticulous MD5 | 48;3 → 32;2 | 16;1 → 0;0 |
| Keyed/Meticulous SHA1 | 72;3 → 48;2 | 24;1 → 0;0 |

Fixed-array digest убирает одно выделение digest. Эта revision ещё выделяет signing payloads. Это результаты методов, а не статистическое сравнение задержки.

### Встроенный payload — 2026-10-26

[ADR-0011](adr/0011-bfd-auth-inline-payload.md) использует inline storage 20 bytes с отдельной logical length. Сохранённая linux/amd64 matrix использует ключ 16 bytes и три runs по 200000 iterations. Все пять built-in Sign/Verify modes и ReceiveOwnerCollectorAuthentication дают 0 B/op и 0 allocs/op.

Owner fixture достигает Up до timing, проверяет точные receives/commits и переиспользует wire только после commit. Socket receive, retained-wire allocation, kernel timestamps, loss, scale и распределения задержек исключены. Результат allocations не завершает общее runtime comparison.

| Наблюдение | Сохранённое значение |
|:---|:---|
| Packet/auth-section layout | 64/32 → 60/28 bytes |
| Receive queue element | 160 bytes; alignment сохраняет размер |
| Validation daemon size | 31445503 → 31434427 bytes |
| Остальные три binaries | Без изменений |
| Plain in-memory RX fixture | 128 B/op; 1 alloc/op |

Размеры binaries получены одинаковой linux/amd64 сборкой: CGO disabled, trimpath, без release ldflags. Это validation builds, не артефакты выпуска или RSS.

### Логирование Micro-BFD dispatch — 2026-10-26

До typed LogAttrs no-actuator/noop-actuator samples дают 11 и 15 allocs/op соответственно, до и после inline auth. Три linux/amd64 runs по 200000 iterations с 64 retained groups и чередованием Up/Down дают:

| Путь | Результат | Граница |
|:---|:---|:---|
| Без actuator, включая enabled JSON после заполнения legacy queue | 0 B/op; 0 allocs/op | Только log/dispatch fixture |
| Noop actuator | 272 B/op; 4 allocs/op | Сохраняется новый контекст с ограниченным временем/timer |

Пятисекундный deadline, cancellation и ordered dispatch сохраняются. Actuator path сохраняет четыре измеренных выделения. Samples не квалифицируют backend I/O и scale.

### Owner collector — 2026-10-22

Локальная запись `reports/collector-owner-20261022/` содержит 16 B/op и 2 allocs/op до кеша active peer/local packet-counter handles, затем 0 B/op и 0 allocs/op в трёх runs. Reference counts ограничивают кеш активными registrations; historical CounterVec series сохраняют прежний lifetime. Fixture исключает UDP/decode/auth и использует синтетическое время. Она не квалифицирует lifetime metric cardinality.

## Исторические и сравнительные источники

[v0.4.0 samples](../../testdata/benchmarks/v0.4.0/benchmark.txt) и [metadata](../../testdata/benchmarks/v0.4.0/meta.json) сохраняют Go 1.26.0, шесть runs, source identifier 48eb954, дату 2026-02-24. Это не квалификация Go 1.27.3.

[C-модели](../../bench/c/) принадлежат репозиторию. Fixed C iterations и validation отличаются от adaptive Go bodies; по ним нельзя ранжировать FRR/BIRD daemons и языки. См. [метод сравнения](13-competitive-analysis.md).

## Политика регрессии

[Классификатор отчёта](../../test/internal/cirunner/benchmark_report.go) считает увеличение time/op от 10% критическим только для указанного в коде набора benchmarks, когда base или head достигает 100 ns/op. Остальные изменения такого процента входят в отчёт при base или head от 1 ns/op. Критическая классификация создаёт CI warning. Само warning не завершает CI ошибкой; malformed collection завершает. Calibrated blocking policy остаётся неквалифицированной. Держите profiles и analysis UIs на loopback; защищайте operational data.

[Корневой индекс](../../BENCHMARKS.md), [измерения runtime](14-performance-analysis.md), [учёт нагрузки](adr/0007-workload-phase-accounting.md).
