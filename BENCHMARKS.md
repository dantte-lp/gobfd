# GoBFD Benchmarks

## Overview

GoBFD maintains 34 micro-benchmarks covering individual BFD processing stages
and their observed allocation boundaries. A zero-allocation result applies
only to the measured benchmark body; it does not establish end-to-end UDP
latency, packet-loss behavior, GC immunity, or a supported session scale.

Benchmarks live in `internal/bfd/bench_test.go`,
`internal/bfd/bench_scaling_test.go`, and `internal/netio/bench_test.go`; CI
runs them through `benchstat` to catch performance regressions (>10% threshold).

## Running Benchmarks

```sh
make benchmark          # Run all benchmarks (6 iterations, benchstat)
make benchmark-all      # Run including race detector and memory profiling
make profile            # Generate pprof CPU profile
```

## Results

Measured on Go 1.26, 8 vCPU container (Podman). All results are median of 6 iterations.

### Packet Codec

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `ControlPacketMarshal` | ~5.8 | 0 | 0 | Serialize 24-byte BFD Control packet into pre-allocated buffer |
| `ControlPacketMarshalWithAuth` | ~11.4 | 0 | 0 | Serialize 52-byte packet with Keyed SHA1 auth section |
| `ControlPacketUnmarshal` | ~11.0 | 0 | 0 | Parse 24-byte wire-format BFD packet (no auth) |
| `ControlPacketUnmarshalWithAuth` | ~54 | 64 | 1 | Parse 52-byte packet with SHA1 auth (1 alloc for digest) |
| `ControlPacketRoundTrip` | ~17 | 0 | 0 | Marshal + unmarshal combined (full codec cost) |

The marshal path achieves ~170M ops/sec. The unmarshal-with-auth allocs 64 bytes for the SHA1 digest copy — this is intentional to avoid sharing memory with the receive buffer.

### FSM Transitions

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `FSMTransitionUpRecvUp` | ~20 | 0 | 0 | Steady-state keepalive self-loop (most frequent) |
| `FSMTransitionDownRecvDown` | ~20 | 0 | 0 | Three-way handshake step 1 (RFC 5880 §6.8.6) |
| `FSMTransitionUpTimerExpired` | ~20 | 0 | 0 | Detection timeout (RFC 5880 §6.8.4) |
| `FSMTransitionIgnored` | ~17 | 0 | 0 | Map miss for invalid state+event combinations |

All FSM transitions are O(1) map lookups. The transition table is pre-built at package init.

### Timer Operations

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `ApplyJitter` | ~9.2 | 0 | 0 | TX interval jitter (RFC 5880 §6.8.7, DetectMult>=2) |
| `ApplyJitterDetectMultOne` | ~9.1 | 0 | 0 | Stricter 75%-90% jitter range (DetectMult=1) |
| `DetectionTimeCalc` | ~0.75 | 0 | 0 | Detection time calculation (RFC 5880 §6.8.4) |
| `CalcTxInterval` | ~0.67 | 0 | 0 | TX interval negotiation (one atomic load) |

Sub-nanosecond detection time and TX interval calculations confirm pure arithmetic with no allocations.

### Session & Pool Operations

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `PacketPool` | ~12 | 0 | 0 | sync.Pool Get/Put cycle for receive buffers |
| `RecvStateToEvent` | ~3.7 | 0 | 0 | Wire State → FSM Event mapping (switch) |
| `SessionRecvPacket` | ~24 | 0 | 0 | Channel send to running session goroutine |

### Receive and Transmit Stage Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `RecvDecodeLookupEnqueue` | environment-dependent | measured | measured | Unmarshal + discriminator lookup + attempted buffered-channel enqueue |
| `RecvDecodeFSM` | environment-dependent | measured | measured | Unmarshal + state-to-event mapping + stateless FSM transition |
| `TxMarshalJitter` | environment-dependent | measured | measured | Marshal a pre-built packet + calculate jitter; no socket send |

`RecvDecodeLookupEnqueue` stops when `Session.RecvPacket` enqueues or drops the
item. Because `recvCh` is buffered, a successful send does not prove that the
session goroutine processed the packet. The benchmark excludes the kernel UDP
path, authentication, session-state mutation, FSM commit, timer reset,
diagnostic actions, notifications, and loss accounting.

### Overlay Codec

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `BuildInnerPacketInto` | ~— | 0 | 0 | Assemble inner packet into caller-owned TX buffer |
| `BuildInnerPacket` | ~— | 80 | 1 | Compatibility wrapper that allocates an owned packet |
| `StripInnerPacket` | ~25 | 0 | 0 | Parse inner packet headers and extract BFD payload |
| `VXLANHeaderMarshal` | ~8 | 0 | 0 | Serialize 8-byte VXLAN header (RFC 8971) |
| `VXLANHeaderUnmarshal` | ~6 | 0 | 0 | Parse 8-byte VXLAN header from wire format |
| `GeneveHeaderMarshal` | ~10 | 0 | 0 | Serialize 8-byte Geneve header (RFC 9521) |
| `GeneveHeaderUnmarshal` | ~7 | 0 | 0 | Parse 8-byte Geneve header from wire format |

The production VXLAN/Geneve TX paths use `BuildInnerPacketInto` with a connection-owned buffer and enforce zero allocations for inner assembly. `BuildInnerPacket` intentionally remains an allocating compatibility wrapper for standalone callers and tests.

### Session Scaling

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `ManagerCreate100Sessions` | ~— | — | — | Create + destroy lifecycle for 100 BFD sessions |
| `ManagerCreate1000Sessions` | ~— | — | — | Create + destroy lifecycle for 1000 BFD sessions |
| `ManagerDemux1000Sessions` | ~60 | 0 | 0 | Demux by discriminator across 1000 active sessions |
| `ManagerReconcile` | ~— | — | — | Reconcile diff: add 10, remove 5 on 100-session baseline |

`Demux1000Sessions` measures discriminator lookup and enqueue with 1,000
registered sessions. The map lookup is expected O(1); this benchmark is not a
supported-scale or end-to-end throughput qualification.

## Zero Allocation Policy

Selected packet codec, FSM, timer, lookup, and caller-buffer operations target
0 allocs/op. Current evidence consists of benchmark reports:

1. `b.ReportAllocs()` reports allocations made by each benchmark body
2. CI publishes `benchstat` comparisons; the >10% regression is a warning
3. Pre-allocated buffers via `sync.Pool` for receive paths
4. Value types (`ControlPacket` struct) instead of pointer indirection

There is no executable repository-wide assertion that every runtime hot path
allocates zero bytes. Release claims therefore remain limited to named
benchmark bodies and their recorded samples.

Known exceptions are `UnmarshalWithAuth` (1 alloc, 64 bytes), which copies the
SHA1 digest so it outlives the receive buffer, and `BuildInnerPacket` (1 alloc,
80 bytes), which owns its returned slice. Session create/reconcile benchmarks
measure lifecycle work and are not packet hot paths.

## Competitive Context

FRR bfdd (C implementation) processes BFD packets with similar latency characteristics but relies on `malloc`/`free` for packet buffers. GoBFD's sync.Pool approach achieves comparable throughput while providing memory safety and goroutine-per-session isolation.

## Go 1.26 Swiss Tables

Go 1.26 uses Swiss tables as the default `map` implementation. All benchmarks above reflect Swiss table performance — the FSM transition map, discriminator lookup, and session demuxing benefit from improved cache locality and group probing.

No code changes were required to adopt Swiss tables. Go 1.27 removed the
former `noswissmap` diagnostic experiment, so legacy-map A/B runs are no longer
supported; the Go 1.26 measurements above remain historical provenance.

---

# GoBFD: Бенчмарки

## Обзор

GoBFD содержит 34 микробенчмарка отдельных этапов обработки BFD и наблюдаемых
границ аллокаций. Результат с нулём аллокаций относится только к телу
конкретного бенчмарка и не доказывает сквозную задержку UDP, отсутствие потерь,
невосприимчивость к GC или поддерживаемый масштаб сессий.

Бенчмарки расположены в `internal/bfd/bench_test.go`,
`internal/bfd/bench_scaling_test.go` и `internal/netio/bench_test.go`; CI
запускает их через `benchstat` для обнаружения регрессий производительности
(порог >10%).

## Запуск бенчмарков

```sh
make benchmark          # Все бенчмарки (6 итераций, benchstat)
make benchmark-all      # С race detector и профилированием памяти
make profile            # Генерация CPU-профиля pprof
```

## Результаты

Измерено на Go 1.26, контейнер 8 vCPU (Podman). Все результаты — медиана 6 итераций.

### Кодек пакетов

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `ControlPacketMarshal` | ~5.8 | 0 | 0 | Сериализация 24-байтного BFD Control пакета |
| `ControlPacketMarshalWithAuth` | ~11.4 | 0 | 0 | Сериализация 52-байтного пакета с аутентификацией SHA1 |
| `ControlPacketUnmarshal` | ~11.0 | 0 | 0 | Разбор 24-байтного пакета (без аутентификации) |
| `ControlPacketUnmarshalWithAuth` | ~54 | 64 | 1 | Разбор 52-байтного пакета с SHA1 (1 аллокация для дайджеста) |
| `ControlPacketRoundTrip` | ~17 | 0 | 0 | Сериализация + разбор (полная стоимость кодека) |

### Переходы FSM

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `FSMTransitionUpRecvUp` | ~20 | 0 | 0 | Keepalive в устойчивом состоянии (самый частый) |
| `FSMTransitionDownRecvDown` | ~20 | 0 | 0 | Шаг 1 трёхстороннего рукопожатия (RFC 5880 §6.8.6) |
| `FSMTransitionUpTimerExpired` | ~20 | 0 | 0 | Таймаут обнаружения (RFC 5880 §6.8.4) |
| `FSMTransitionIgnored` | ~17 | 0 | 0 | Промах в таблице для невалидных комбинаций |

### Операции с таймерами

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `ApplyJitter` | ~9.2 | 0 | 0 | Джиттер TX-интервала (RFC 5880 §6.8.7) |
| `ApplyJitterDetectMultOne` | ~9.1 | 0 | 0 | Строгий диапазон 75%-90% (DetectMult=1) |
| `DetectionTimeCalc` | ~0.75 | 0 | 0 | Вычисление времени обнаружения (RFC 5880 §6.8.4) |
| `CalcTxInterval` | ~0.67 | 0 | 0 | Согласование TX-интервала (одна атомарная загрузка) |

### Сессии и пул

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `PacketPool` | ~12 | 0 | 0 | Цикл sync.Pool Get/Put для буферов приёма |
| `RecvStateToEvent` | ~3.7 | 0 | 0 | Маппинг State → Event FSM (switch) |
| `SessionRecvPacket` | ~24 | 0 | 0 | Отправка в канал работающей горутины сессии |

### Измеряемые этапы RX и TX

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `RecvDecodeLookupEnqueue` | зависит от окружения | измеряется | измеряется | Unmarshal + поиск дискриминатора + попытка записи в буферизованный канал |
| `RecvDecodeFSM` | зависит от окружения | измеряется | измеряется | Unmarshal + преобразование состояния + stateless-переход FSM |
| `TxMarshalJitter` | зависит от окружения | измеряется | измеряется | Marshal готового пакета + расчёт jitter; без socket send |

`RecvDecodeLookupEnqueue` заканчивается, когда `Session.RecvPacket` записывает
элемент в канал или отбрасывает его. Успешная запись в буферизованный `recvCh`
не подтверждает обработку пакета горутиной сессии. Не измеряются UDP-путь ядра,
аутентификация, изменение состояния, commit FSM, сброс таймера, диагностические
действия, уведомления и учёт потерь.

### Overlay-кодеки

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `BuildInnerPacketInto` | ~— | 0 | 0 | Сборка внутреннего пакета в caller-owned TX буфер |
| `BuildInnerPacket` | ~— | 80 | 1 | Совместимая обёртка с выделением собственного буфера |
| `StripInnerPacket` | ~25 | 0 | 0 | Разбор заголовков внутреннего пакета и извлечение BFD-полезной нагрузки |
| `VXLANHeaderMarshal` | ~8 | 0 | 0 | Сериализация 8-байтного заголовка VXLAN (RFC 8971) |
| `VXLANHeaderUnmarshal` | ~6 | 0 | 0 | Разбор 8-байтного заголовка VXLAN |
| `GeneveHeaderMarshal` | ~10 | 0 | 0 | Сериализация 8-байтного заголовка Geneve (RFC 9521) |
| `GeneveHeaderUnmarshal` | ~7 | 0 | 0 | Разбор 8-байтного заголовка Geneve |

Production TX-пути VXLAN/Geneve используют `BuildInnerPacketInto` с буфером, принадлежащим соединению, и не выделяют память при сборке inner-пакета. `BuildInnerPacket` остаётся allocating-обёрткой для автономных вызывающих сторон и тестов.

### Масштабирование сессий

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `ManagerCreate100Sessions` | ~— | — | — | Цикл создания + уничтожения 100 BFD-сессий |
| `ManagerCreate1000Sessions` | ~— | — | — | Цикл создания + уничтожения 1000 BFD-сессий |
| `ManagerDemux1000Sessions` | ~60 | 0 | 0 | Демультиплексирование по дискриминатору среди 1000 активных сессий |
| `ManagerReconcile` | ~— | — | — | Реконсиляция: добавить 10, удалить 5 на базе 100 сессий |

`Demux1000Sessions` измеряет поиск дискриминатора и enqueue при 1000
зарегистрированных сессиях. Ожидаемая сложность поиска — O(1), но этот
бенчмарк не квалифицирует поддерживаемый масштаб или сквозную пропускную
способность.

## Политика нулевых аллокаций

Для выбранных операций кодека, FSM, таймеров, поиска и записи в буфер целевое
значение равно 0 аллокаций/оп. Текущее доказательство ограничено отчётами
бенчмарков:

1. `b.ReportAllocs()` сообщает аллокации тела конкретного бенчмарка
2. CI публикует сравнение `benchstat`; регрессия >10% остаётся предупреждением
3. Предвыделенными буферами через `sync.Pool`
4. Value types (`ControlPacket` struct) вместо указателей

Исполняемого утверждения о нулевых аллокациях для всех runtime hot path в
репозитории нет. Публичное утверждение ограничивается именованными
бенчмарками и сохранёнными измерениями.

Известные исключения: `UnmarshalWithAuth` (1 аллокация, 64 байта), где дайджест
SHA1 копируется из буфера приёма, и `BuildInnerPacket` (1 аллокация, 80 байт),
который владеет возвращаемым slice. Бенчмарки создания и reconcile сессий
измеряют lifecycle, а не пакетный hot path.

## Swiss Tables в Go 1.26

Go 1.26 использует Swiss tables как реализацию `map` по умолчанию. Все бенчмарки выше отражают производительность Swiss tables — таблица переходов FSM, поиск дискриминаторов и демультиплексирование сессий выигрывают от улучшенной локальности кэша и группового зондирования.

Изменения кода для Swiss tables не потребовались. Go 1.27 удалил прежний
диагностический experiment `noswissmap`, поэтому A/B-прогоны со старой
реализацией map больше не поддерживаются; приведённые выше измерения Go 1.26
сохраняются как историческое подтверждение.
