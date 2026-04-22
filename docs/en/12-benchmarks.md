# Benchmark Guide

> How to run, read, and interpret GoBFD benchmark results.

---

### Table of Contents

- [Running Benchmarks](#running-benchmarks)
- [Understanding Go Benchmark Output](#understanding-go-benchmark-output)
- [Benchmark Categories](#benchmark-categories)
- [What "Good" Looks Like](#what-good-looks-like)
- [Zero Allocation Policy](#zero-allocation-policy)
- [Comparing Versions](#comparing-versions)
- [Generating Reports](#generating-reports)

---

### Running Benchmarks

GoBFD provides several Makefile targets for benchmarks. All commands run inside the Podman dev container — no local Go toolchain required.

```bash
# Quick run — stdout + benchmark.txt (BFD package only)
make benchmark

# All packages — stdout + benchmark.txt
make benchmark-all

# Save by version — testdata/benchmarks/<version>/
make benchmark-save                          # uses current git tag
make benchmark-save BENCH_VERSION=v0.5.0     # explicit version

# Statistical comparison between two versions
make benchmark-compare OLD=v0.4.0 NEW=v0.5.0
```

Each `benchmark-save` run produces:

```
testdata/benchmarks/v0.4.0/
    benchmark-bfd.txt       # internal/bfd benchmarks (22 benchmarks, 6 runs each)
    benchmark-netio.txt     # internal/netio benchmarks (6 benchmarks, 6 runs each)
    benchmark.txt           # combined output
    meta.json               # version, commit, date, Go version, run count
```

Parameters: `-bench=. -benchmem -count=6 -run='^$' -timeout=120s`. The `-count=6` flag provides enough samples for `benchstat` statistical analysis. The `-run='^$'` flag skips unit tests (only runs benchmarks).

---

### Understanding Go Benchmark Output

```
BenchmarkControlPacketMarshal-8    195605820    5.869 ns/op    0 B/op    0 allocs/op
                              |          |          |            |           |
                          GOMAXPROCS  iterations  time/op    bytes/op  allocs/op
```

| Column | Meaning |
|--------|---------|
| `-8` | GOMAXPROCS (number of CPUs available to the Go runtime) |
| `195605820` | Number of iterations the benchmark ran (higher = faster operation) |
| `5.869 ns/op` | Time per operation in nanoseconds (lower = better) |
| `0 B/op` | Heap bytes allocated per operation (lower = better) |
| `0 allocs/op` | Heap allocations per operation (0 = zero-allocation hot path) |

With `-count=6`, each benchmark line appears 6 times. The `benchstat` tool computes the **median** (not mean) and 95% confidence interval across these runs.

---

### Benchmark Categories

GoBFD has 28 benchmarks across 7 categories in two packages (`internal/bfd` and `internal/netio`).

#### 1. Packet Codec (`internal/bfd`)

| Benchmark | What It Measures | Why It Matters |
|-----------|-----------------|----------------|
| `ControlPacketMarshal` | Serialize BFD control packet (24 bytes) to wire format | Called on every TX interval for every session |
| `ControlPacketMarshalWithAuth` | Marshal with SHA1 authentication section | Auth adds HMAC computation overhead |
| `ControlPacketUnmarshal` | Parse wire bytes into control packet struct | Called on every received packet |
| `ControlPacketUnmarshalWithAuth` | Unmarshal with auth section validation | Auth parsing requires heap allocation for digest copy |
| `ControlPacketRoundTrip` | Marshal + Unmarshal combined | End-to-end codec cost |

#### 2. FSM Transitions (`internal/bfd`)

| Benchmark | What It Measures | Why It Matters |
|-----------|-----------------|----------------|
| `FSMTransitionUpRecvUp` | Steady-state keepalive (Up -> recv Up -> Up) | Most frequent transition in production |
| `FSMTransitionDownRecvDown` | Three-way handshake first step | Session establishment hot path |
| `FSMTransitionUpTimerExpired` | Detection timeout (Up -> timer -> Down) | Critical failure detection path |
| `FSMTransitionIgnored` | No-op transition (invalid event for current state) | Must be fast to not slow RX path |

#### 3. Timer Calculations (`internal/bfd`)

| Benchmark | What It Measures | Why It Matters |
|-----------|-----------------|----------------|
| `ApplyJitter` | RFC 5880 jitter calculation (up to 25% random) | Applied before every TX to prevent synchronization |
| `ApplyJitterDetectMultOne` | Jitter with DetectMult=1 (stricter, up to 90%) | Additional 10% jitter per RFC 5880 section 6.8.7 |
| `DetectionTimeCalc` | `DetectMult * max(RequiredMinRxInterval, remoteDesiredMinTxInterval)` | Evaluated on every parameter change |
| `CalcTxInterval` | TX interval negotiation between local and remote | Determines actual packet send rate |

#### 4. Session Infrastructure (`internal/bfd`)

| Benchmark | What It Measures | Why It Matters |
|-----------|-----------------|----------------|
| `PacketPool` | `sync.Pool` Get/Put cycle for packet buffers | Amortizes allocation cost across sessions |
| `RecvStateToEvent` | Map BFD state field to FSM event | Called on every received packet |
| `SessionRecvPacket` | Channel send cost (packet -> session goroutine) | Per-packet demux overhead |

#### 5. Full-Path Benchmarks (`internal/bfd`)

| Benchmark | What It Measures | Why It Matters |
|-----------|-----------------|----------------|
| `FullRecvPath` | Complete RX: unmarshal + state lookup + FSM transition | End-to-end receive latency budget |
| `FullTxPath` | Complete TX: build cached packet + marshal | End-to-end transmit latency budget |

#### 6. Session Scaling (`internal/bfd`)

| Benchmark | What It Measures | Why It Matters |
|-----------|-----------------|----------------|
| `ManagerCreate100Sessions` | Create + destroy 100 sessions throughput | Session churn during config reload |
| `ManagerCreate1000Sessions` | Create + destroy 1000 sessions throughput | Large-scale provisioning |
| `ManagerDemux1000Sessions` | Packet dispatch with 1000 active sessions | O(1) Swiss table map lookup verification |
| `ManagerReconcile` | Config reconciliation (add 10, remove 5 from 100 sessions) | Incremental config update cost |

#### 7. Overlay Codec (`internal/netio`)

| Benchmark | What It Measures | Why It Matters |
|-----------|-----------------|----------------|
| `BuildInnerPacket` | Assemble Ethernet + IPv4 + UDP + BFD inner packet | Required for VXLAN/Geneve encapsulation |
| `StripInnerPacket` | Extract BFD payload from inner packet | RX path for overlay sessions |
| `VXLANHeaderMarshal` | VXLAN header serialization (RFC 7348) | Per-packet TX overhead for VXLAN BFD |
| `VXLANHeaderUnmarshal` | VXLAN header parsing | Per-packet RX overhead for VXLAN BFD |
| `GeneveHeaderMarshal` | Geneve header serialization (RFC 8926) | Per-packet TX overhead for Geneve BFD |
| `GeneveHeaderUnmarshal` | Geneve header parsing | Per-packet RX overhead for Geneve BFD |

---

### What "Good" Looks Like

Target metrics for GoBFD based on BFD protocol requirements and production constraints:

| Category | Target | Rationale |
|----------|--------|-----------|
| Packet codec (marshal/unmarshal) | 0 allocs/op, <10 ns/op | Called every TX/RX interval (potentially thousands of times per second) |
| FSM transitions | 0 allocs/op, <25 ns/op | Most frequent operation in the hot path |
| Timer calculations | <1 ns/op | Pure arithmetic, no memory access required |
| Full receive path | 0 allocs/op, <100 ns/op | At 100ns/op, theoretical throughput is 10M+ packets/sec |
| Full transmit path | 0 allocs/op, <10 ns/op | TX uses cached pre-built packet (copy, not rebuild) |
| Session demux (any count) | O(1), ~50 ns/op | Swiss table map lookup, independent of session count |
| Overlay headers | 0 allocs/op, <5 ns/op | Thin wire format, fixed-size headers |

#### v0.4.0 Baseline Results

| Benchmark | ns/op | B/op | allocs/op | Status |
|-----------|------:|-----:|----------:|--------|
| ControlPacketMarshal | 5.9 | 0 | 0 | Target met |
| ControlPacketUnmarshal | 11.0 | 0 | 0 | Target met |
| ControlPacketRoundTrip | 17.9 | 0 | 0 | Target met |
| FSMTransitionUpRecvUp | 20.1 | 0 | 0 | Target met |
| ApplyJitter | 9.6 | 0 | 0 | Target met |
| DetectionTimeCalc | 0.7 | 0 | 0 | Target met |
| FullRecvPath | 64.2 | 0 | 0 | Target met |
| FullTxPath | 6.7 | 0 | 0 | Target met |
| ManagerDemux1000Sessions | 50.7 | 0 | 0 | Target met |
| VXLANHeaderMarshal | 2.8 | 0 | 0 | Target met |
| GeneveHeaderUnmarshal | 3.8 | 0 | 0 | Target met |

---

### Zero Allocation Policy

GoBFD enforces zero heap allocations in all hot-path operations. "Hot path" means any code executed per-packet or per-timer-tick during normal BFD operation.

#### Why Zero Allocation Matters for BFD

BFD sessions send and receive packets at intervals as low as 10ms. With 1,000 sessions at 100ms intervals, that is 10,000 packets per second in each direction. Any allocation in the hot path means:

1. **GC pressure** — more frequent garbage collection pauses
2. **Latency jitter** — GC stop-the-world pauses add unpredictable delay
3. **False positives** — if a GC pause exceeds the detection time, the remote peer declares the session Down

GoBFD uses `GOMEMLIMIT` + `GOGC=off` in production to minimize GC cycles, but zero-allocation design eliminates the root cause.

#### Verification Methods

1. **`-benchmem` flag** — every benchmark reports `B/op` and `allocs/op`
2. **`testing.AllocsPerRun`** — unit tests enforce `n == 0` for hot-path functions
3. **Escape analysis** — `go build -gcflags='-m'` verifies no heap escapes in hot paths

#### Known Exceptions

- `ControlPacketUnmarshalWithAuth`: 1 alloc/op (64 B) — copies the authentication digest for HMAC verification. This is intentional: the digest must outlive the input buffer.
- `BuildInnerPacket`: 1 alloc/op (80 B) — allocates the inner packet buffer. Overlay paths are less latency-sensitive than direct BFD.
- `ManagerCreate*` / `ManagerReconcile`: allocations expected — session lifecycle operations are not hot path.

---

### Comparing Versions

Use `benchstat` via the Makefile to compare benchmark results between versions:

```bash
# Save current benchmarks
make benchmark-save BENCH_VERSION=v0.5.0

# Compare with previous version
make benchmark-compare OLD=v0.4.0 NEW=v0.5.0
```

#### Reading benchstat Output

```
                           │  v0.4.0   │              v0.5.0              │
                           │  sec/op   │   sec/op     vs base             │
ControlPacketMarshal-8       5.87n ± 3%   5.52n ± 1%  -5.96% (p=0.002 n=6)
FSMTransitionUpRecvUp-8      20.1n ± 1%   20.3n ± 2%       ~ (p=0.485 n=6)
```

| Symbol | Meaning |
|--------|---------|
| `± 3%` | 95% confidence interval — lower means more stable measurements |
| `-5.96%` | Statistically significant improvement (negative = faster) |
| `~` | No statistically significant difference |
| `p=0.002` | p-value — below 0.05 means the difference is statistically significant |
| `n=6` | Number of samples used for comparison |

A regression shows as a positive percentage (e.g., `+12.3%`). Investigate any regression >5% in hot-path benchmarks before release.

#### HTML Report

`make benchmark-compare` also generates `reports/benchmarks/comparison.html` — a standalone HTML table with color-coded results. Open it in any browser.

---

### Generating Reports

#### Test Report (JUnit XML + HTML)

```bash
make test-report
```

Produces:
- `reports/tests/unit-report.xml` — JUnit XML (for CI integration)
- `reports/tests/unit-report.json` — JSON format (for programmatic analysis)
- `reports/tests/unit-report.html` — standalone HTML report (for humans)

#### Benchmark Comparison Report

```bash
make benchmark-compare OLD=v0.4.0 NEW=v0.5.0
```

Produces:
- `reports/benchmarks/comparison.txt` — text table
- `reports/benchmarks/comparison.html` — standalone HTML table

#### Full Pipeline

```bash
make report-all OLD=v0.4.0
```

Runs `test-report` + `benchmark-save` + `benchmark-compare` in sequence. All output goes to the `reports/` directory (gitignored — regenerate with `make report-all`).

#### Developer Workflow

```bash
# When releasing a new version:
make benchmark-save BENCH_VERSION=v0.5.0

# Compare with previous:
make benchmark-compare OLD=v0.4.0 NEW=v0.5.0
# -> open reports/benchmarks/comparison.html in browser

# Test report:
make test-report
# -> open reports/tests/unit-report.html in browser

# Everything at once:
make report-all OLD=v0.4.0
```
