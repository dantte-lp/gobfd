# GoBFD Benchmarks

## Overview

GoBFD maintains 18 micro-benchmarks covering every hot path in the BFD protocol implementation. All benchmarks enforce **zero heap allocations** on the critical packet processing paths, ensuring predictable sub-millisecond latency for production BFD deployments.

Benchmarks live in `internal/bfd/bench_test.go` and run automatically in CI via `benchstat` to catch performance regressions (>10% threshold).

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

### Full-Path Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| `FullRecvPath` | ~60 | 0 | 0 | Unmarshal + Manager.Demux (complete RX path) |
| `FullTxPath` | ~5.8 | 0 | 0 | Build packet + marshal (complete TX path) |

The full receive path processes ~16M packets/sec with zero allocations. This includes wire unmarshal, two-tier discriminator demultiplexing, and channel delivery to the session goroutine.

## Zero Allocation Policy

All hot paths (packet codec, FSM transitions, timer operations, session event loop) MUST maintain 0 allocs/op. This is enforced by:

1. `b.ReportAllocs()` in every benchmark
2. CI regression job (`benchstat`, >10% regression = warning)
3. Pre-allocated buffers via `sync.Pool` for receive paths
4. Value types (`ControlPacket` struct) instead of pointer indirection

The single exception is `UnmarshalWithAuth` (1 alloc, 64 bytes) — the SHA1 digest must be copied from the receive buffer to prevent use-after-free when the buffer is returned to the pool.

## Competitive Context

FRR bfdd (C implementation) processes BFD packets with similar latency characteristics but relies on `malloc`/`free` for packet buffers. GoBFD's sync.Pool approach achieves comparable throughput while providing memory safety and goroutine-per-session isolation.

## Go 1.26 Swiss Tables

Go 1.26 uses Swiss tables as the default `map` implementation. All benchmarks above reflect Swiss table performance — the FSM transition map, discriminator lookup, and session demuxing benefit from improved cache locality and group probing.

To compare against the legacy map implementation:

```sh
GOEXPERIMENT=noswissmap make benchmark
```

No code changes were required to adopt Swiss tables — the runtime switch is transparent. Existing benchmarks capture the performance delta automatically.

---

# GoBFD: Бенчмарки

## Обзор

GoBFD содержит 18 микробенчмарков, покрывающих все горячие пути реализации протокола BFD. Все бенчмарки обеспечивают **ноль аллокаций в куче** на критических путях обработки пакетов, гарантируя предсказуемую задержку менее миллисекунды для промышленных развёртываний BFD.

Бенчмарки расположены в `internal/bfd/bench_test.go` и автоматически запускаются в CI через `benchstat` для обнаружения регрессий производительности (порог >10%).

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

### Полные пути

| Бенчмарк | нс/оп | Б/оп | аллок/оп | Описание |
|----------|------:|-----:|----------:|----------|
| `FullRecvPath` | ~60 | 0 | 0 | Unmarshal + Manager.Demux (полный путь RX) |
| `FullTxPath` | ~5.8 | 0 | 0 | Сборка пакета + marshal (полный путь TX) |

Полный путь приёма обрабатывает ~16M пакетов/сек с нулём аллокаций.

## Политика нулевых аллокаций

Все горячие пути (кодек, FSM, таймеры, цикл событий сессии) ДОЛЖНЫ сохранять 0 аллокаций/оп. Это обеспечивается:

1. `b.ReportAllocs()` в каждом бенчмарке
2. CI-задачей регрессии (`benchstat`, >10% = предупреждение)
3. Предвыделенными буферами через `sync.Pool`
4. Value types (`ControlPacket` struct) вместо указателей

Единственное исключение — `UnmarshalWithAuth` (1 аллокация, 64 байта) — дайджест SHA1 копируется из буфера приёма для предотвращения use-after-free.

## Swiss Tables в Go 1.26

Go 1.26 использует Swiss tables как реализацию `map` по умолчанию. Все бенчмарки выше отражают производительность Swiss tables — таблица переходов FSM, поиск дискриминаторов и демультиплексирование сессий выигрывают от улучшенной локальности кэша и группового зондирования.

Для сравнения с устаревшей реализацией map:

```sh
GOEXPERIMENT=noswissmap make benchmark
```

Изменения кода не потребовались — переключение runtime прозрачно. Существующие бенчмарки автоматически фиксируют разницу производительности.
