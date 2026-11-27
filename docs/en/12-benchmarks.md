# Benchmarks

[Russian translation](../ru/12-benchmarks.md).

Micro-benchmarks measure one named operation. They do not establish packet throughput, supported session count, timer accuracy, loss or GC-pause immunity. [Runtime measurement](14-performance-analysis.md) defines the additional owner, UDP, clock and workload boundaries.

## Collect a snapshot

1. Prepare the [pinned development environment](09-development.md).
2. Select the baseline revision. Choose unused snapshot identifiers and preserve previous raw samples. Require identical benchmark bodies, compiler, architecture, resource limits and environment for both revisions. For cross-release API adaptation, source comparison must establish that the timed body still performs the same operations and identify untimed setup changes, as described below.
3. Collect a fresh baseline:

```sh
make up
make benchmark-save BENCH_VERSION=local-baseline
```

4. Select the candidate revision in the same checkout, retaining the baseline files and equivalent development environment. Collect and compare:

```sh
make benchmark-save BENCH_VERSION=local-candidate
make benchmark-compare OLD=local-baseline NEW=local-candidate
```

5. Require successful exit and complete samples before using metadata or reports. Keep partial failure output for diagnosis. Inspect uncertainty and contention before attributing a result to code; [Makefile](../../Makefile) does not enforce compiler or benchmark-body equivalence.

| Target | Collection/output |
|:---|:---|
| `benchmark` | BFD package, six non-race benchmem runs; stdout and benchmark.txt |
| `benchmark-all` | All packages, six non-race benchmem runs; stdout and benchmark.txt |
| `benchmark-save` | BFD/netio separately, six runs, 120-second package timeout |
| `benchmark-compare` | benchstat text and HTML under reports/benchmarks |
| `profile` | Separate BFD CPU/heap profiles: cpu.prof and mem.prof |
| `benchmark-cross` | Sequential local C models and Go samples under bench-results |
| `benchmark-report` | Legacy direct Go report generator; execute in the pinned container |
| `test-report`, `report-all` | Race JUnit/HTML; optionally snapshot and comparison |

Snapshot files are `benchmark-bfd.txt`, `benchmark-netio.txt`, combined `benchmark.txt` and `meta.json` with version/commit/date/Go/count. Make's collection/comparison pipelines use Bash errexit/pipefail; a failed Go, benchstat or tee command fails the target. A failed rerun can leave older metadata or HTML beside partial samples. A fresh identifier and successful complete run prevent accepting that mixture.

Repeating `benchmark-save` with the same identifier overwrites its named sample files; comparison also replaces its fixed report paths. Stop on failed collection or mismatched inputs. Preserve partial and prior evidence outside those output paths, then repeat with unused identifiers under equivalent conditions. No runtime rollback follows from a benchmark; resource cleanup follows the [owned development lifecycle](09-development.md#prepare-a-checkout). Source: [collection and comparison targets](../../Makefile).

### Paired CI comparison

CI `cictl benchmark-pair --old old.txt --new new.txt` uses ten alternating base/head and head/base pairs. Each Go invocation produces one 100 ms non-race sample; every package/name needs exactly ten valid records per side. Invalid/empty/incomplete output fails before reporting. A single detached base worktree has bounded cancellation-safe cleanup. BENCH_REGEX, GITHUB_BASE_REF and RUNNER_TEMP retain their source contracts in [automation](reference/automation.md).

Release comparison uses the same ten-pair collector for the previous published stable's verified frozen commit and current release source on one runner/compiler. Each invocation retains inclusive `-bench=.` selection and its 120-second timeout. Raw pairs, normalized copies and tag/commit/compiler/hash provenance remain under `reports/benchmarks`; six-run snapshots and their count-six metadata remain separate. Historical Manager1000 fixtures can create over 1000 owner threads: admit their actual resources separately; these microbenchmarks do not establish supported daemon scale.

Before release timing collection, [the producer](../../test/internal/cirunner/release_benchmark.go) runs the ManagerDemux1000Sessions and RecvDecodeLookupEnqueue fixtures with the race detector, 100000 iterations each, first at the baseline and then at the candidate. The historical FullRecvPath alias is accepted. A failed command, malformed record or missing fixture stops collection. Both bounded stdout/stderr logs remain in `reports/benchmarks/pair-{baseline,current}-race.txt`; successful comparison provenance includes their SHA256 hashes. Race timings never enter the non-race comparison. Passing this bounded preflight does not prove the absence of every race.

Release collection applies the explicit `decode-packet-per-iteration-v1` test-fixture contract to both sources through [Go's file overlay](https://github.com/golang/go/blob/go1.27.4/src/cmd/go/alldocs.go). Decode storage belongs to each loop iteration: the historical asynchronous queue retains its packet pointer. The producer verifies each original `bench_test.go` against its committed Git blob, moves only the packet declaration inside the recognized decode benchmark loop and passes the same overlay to race and timed commands. Unknown fixture shapes fail before execution. Product files, tags, ManagerDemux1000Sessions and comparison thresholds remain unchanged.

Retain `pair-{baseline,current}-fixture-{original,owned}.go.txt` and `pair-fixture-provenance.json` with all four SHA256 hashes. Recheck those hashes before comparison and include the fixture contract in `comparison-provenance.json`; drift fails without publishing a comparison. The temporary overlay map is removed on success or failure. This creates a new ownership-safe series; retain the original race failure separately. Do not reinterpret the original timings as valid. Per-iteration allocation in a pointer-retaining baseline is part of its safe cost. Queue-full attempt cost, outcome fractions and useful packet throughput are separate measurements.

Require equivalent timed operation boundaries across release sources. For v0.6.4 comparison, RecvDecodeFSM and TxMarshalJitter retain their timed bodies; RecvDecodeLookupEnqueue and ManagerDemux1000Sessions discard the additional Demux return value while retaining one production invocation. Sender-lease factory adaptation occurs before timing; ManagerLookup1000Sessions changes only that untimed setup. Netio benchmark bodies are unchanged. Product costs remain measured. New or unmatched rows and changed authentication setup do not acquire equivalence from matching names alone. This controlled microbenchmark comparison is separate from native workload A/B and stable acceptance.

Six-run Make/release snapshots and standalone benchmark-run/base remain non-interleaved diagnostics. Keep the fixed sampling budget; do not repeat until a preferred statistical result appears.

## Interpret one result

```text
BenchmarkControlPacketMarshal-8    195605820    5.869 ns/op    0 B/op    0 allocs/op
```

This retained historical format shows GOMAXPROCS 8, adaptive iteration count, time/op and heap allocation/op. The number is not a current performance promise. [Go testing](https://pkg.go.dev/testing#hdr-Benchmarks) defines the measurement interface. Construction B/op is not retained RSS; zero allocs/op applies only inside the benchmark boundary. Profiling and race tests are separate workloads.

## Operation boundaries

`ManagerDemux1000Sessions` and `RecvDecodeLookupEnqueue` report queued, queue-full, closed, committed and abandoned outcomes per attempted operation. Counters are sampled after timing stops and Manager shutdown joins the owners. The benchmark requires every attempt to reach admission and every queued item to reach an owner outcome. Shutdown abandonment is included; these fractions do not measure owner latency or sustained throughput. Compare the outcome mix alongside time/op: a faster owner can change the fraction of costly enqueues versus queue-full refusals. Subtracting lookup time from Demux time does not isolate channel-send cost.

| Family | Included work | Excluded work |
|:---|:---|:---|
| Marshal | Encode into supplied storage; auth section already prepared | Signing and send |
| Unmarshal | Decode/validate; value-owned bounded auth metadata | Hash verification and retained immutable wire |
| Authentication | Sign or VerifyKnownSequence, immutable key store, controlled prior sequence | Socket, retained-wire copies, owner commit |
| FSM | Indexed transition lookup | Owner loop, transport, timers, notification |
| Jitter/interval | Named calculation | Timer scheduling/wakeup |
| RecvDecodeLookupEnqueue | Decode, discriminator lookup, attempted buffered enqueue | Packet processing and owner commit |
| RecvDecodeFSM | Decode and stateless transition | Live owner and transport |
| ReceiveOwnerCollector | Commit by actual owner with real collector | UDP, decode, auth; synthetic timing |
| ReceiveOwnerCollectorAuthentication | Sign/marshal/decode/demux/verify/commit, real metrics, steady Up fixture | Socket/kernel RX, retained-wire allocation; synthetic clock |
| TxMarshalJitter | Marshal and jitter | Socket send and peer arrival |
| Create/reconcile | Lifecycle state | Retained process RSS |
| BuildInnerPacketInto | Production caller-buffer inner-packet assembly | Storage allocation |
| BuildInnerPacket | Allocating compatibility wrapper | Production caller-buffer ownership |

Read the actual bodies in [BFD benchmarks](../../internal/bfd/bench_test.go), [scaling fixtures](../../internal/bfd/bench_scaling_test.go), [Micro-BFD dispatch](../../internal/bfd/manager_micro_bench_test.go) [netio benchmarks](../../internal/netio/bench_test.go) and [real-collector owner benchmarks](../../internal/metrics/receive_timing_test.go). Do not preserve a fixed benchmark count when names evolve.

## Retained allocation measurements

The following dated results are retained observations, not fresh execution during a documentation update. Exact final-candidate qualification remains separate.

### Authentication methods — 2026-10-26

At `972946c59bbe8d9bc36fbe51edb9f6c82f3bfc33`, linux/amd64, pinned compiler, 16-byte key, three runs of 200000 iterations:

| Mode | Sign before → after B/op; allocs/op | Verify before → after B/op; allocs/op |
|:---|---:|---:|
| Simple Password | 16;1 → 16;1 | 0;0 → 0;0 |
| Keyed/Meticulous MD5 | 48;3 → 32;2 | 16;1 → 0;0 |
| Keyed/Meticulous SHA1 | 72;3 → 48;2 | 24;1 → 0;0 |

Fixed-array digest results remove one digest allocation. This revision still allocates signing payloads. These method results are not a statistical latency comparison.

### Inline payload — 2026-10-26

[ADR-0011](adr/0011-bfd-auth-inline-payload.md) uses 20-byte inline storage with independent logical length. The retained linux/amd64 matrix uses a 16-byte key and three runs of 200000 iterations. All five built-in Sign/Verify modes and ReceiveOwnerCollectorAuthentication report 0 B/op and 0 allocs/op.

The owner fixture reaches Up before timing, checks exact receives/commits and reuses wire only after commit. Socket receive, retained-wire allocation, kernel timestamps, loss, supported scale and latency distributions remain excluded. The allocation result does not complete the overall runtime comparison.

| Observation | Retained value |
|:---|:---|
| Packet/auth-section layout | 64/32 → 60/28 bytes |
| Receive queue element | 160 bytes; alignment preserves size |
| Validation daemon size | 31445503 → 31434427 bytes |
| Other three binaries | Unchanged |
| Plain in-memory RX fixture | 128 B/op; 1 alloc/op |

Binary sizes use like-for-like linux/amd64, CGO disabled, trimpath and no release ldflags. They are validation builds, not release assets or RSS.

### Micro-BFD dispatch logging — 2026-10-26

Before typed LogAttrs, no-actuator and noop-actuator samples report 11 and 15 allocs/op, respectively, both before and after inline auth. Three linux/amd64 runs of 200000 iterations with 64 retained groups and alternating Up/Down report:

| Path | Result | Limit |
|:---|:---|:---|
| No actuator, including enabled JSON after legacy queue fills | 0 B/op; 0 allocs/op | Log/dispatch fixture only |
| Noop actuator | 272 B/op; 4 allocs/op | Fresh bounded context/timer retained |

The five-second deadline, cancellation and ordered dispatch remain. The actuator path retains the measured four allocations. Neither sample qualifies backend I/O or scale.

The [accepted actuator allocation policy](adr/0003-linux-advanced-bfd-applicability.md#micro-bfd-actuator-allocation-boundary--accepted-2026-10-29) preserves zero allocations without an actuator and caps the unchanged `Background` plus noop fixture at 272 B/op and 4 allocs/op on the pinned Go 1.27.3 linux/amd64 toolchain. Collect three runs of at least 200000 iterations for each final candidate. The noop callback does not read `Done`; actual backends and different parent contexts need separate measurements. Retained contexts must remain immutable after cancellation. An allocation budget does not bound enforcement latency.

### Owner collector — 2026-10-22

The local receipt `reports/collector-owner-20261022/` records 16 B/op and 2 allocs/op before caching active peer/local packet-counter handles, followed by 0 B/op and 0 allocs/op in three runs. Reference counts bound cache lifetime to active registrations; historical CounterVec series retain their old lifetime. This fixture excludes UDP/decode/auth and uses synthetic timing. It does not qualify lifetime metric cardinality.

## Historical and comparison sources

[v0.4.0 samples](../../testdata/benchmarks/v0.4.0/benchmark.txt) and [metadata](../../testdata/benchmarks/v0.4.0/meta.json) retain Go 1.26.0, six runs, source identifier 48eb954, dated 2026-02-24. These results are not Go 1.27.3 qualification.

Use the retained samples only for a historical diagnostic, excluding claims of equivalent compiler/benchmark bodies or current-candidate qualification:

```sh
make benchmark-compare OLD=v0.4.0 NEW=local-candidate
```

[C models](../../bench/c/) are repository-owned replicas. Fixed C iterations and validation differ from Go's adaptive benchmark bodies; do not rank FRR/BIRD daemons or languages with them. See [comparison method](13-competitive-analysis.md).

## Regression policy

The [report classifier](../../test/internal/cirunner/benchmark_report.go) labels a positive time/op change of at least 10% critical only for its named critical benchmark set when either base/head is at least 100 ns/op. Other changes at that percentage are report-only when either side reaches 1 ns/op. A critical classification fails `benchmark-report` after publishing its reports and attempting the step summary and warnings. This also applies when the warning writer is absent or the release caller skips the summary. Acceptable, statistically inconclusive and report-only rows do not fail the command. Validate nonempty benchmark records on both sides; malformed records, zero iterations and failed benchstat commands fail the gate. Preserve six-run release inputs and ten-pair CI inputs. Local self-noise controls do not establish a universal noise bound or hosted-runner equivalence. Keep profiles and analysis UIs on loopback and protect operational data.

[Root benchmark index](../../BENCHMARKS.md), [runtime measurement](14-performance-analysis.md), [workload accounting](adr/0007-workload-phase-accounting.md).
