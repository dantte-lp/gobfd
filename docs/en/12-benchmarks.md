# Benchmark Guide

Micro-benchmarks measure named operations. They do not establish UDP throughput,
loss, timer accuracy, GC immunity or supported session scale.
[Performance analysis](14-performance-analysis.md) describes the separate
runtime measurement and qualification boundaries.

## Run benchmarks

The benchmark commands below run in the pinned Podman development environment.
Use `make up` first. Host bootstrap and the separate `benchmark-report` target
have the limitations described in [Development](09-development.md) and the
report section below.

| Command | Input and output |
|---|---|
| `make benchmark` | `internal/bfd`, six runs with `-benchmem`; stdout and `benchmark.txt` |
| `make benchmark-all` | All packages, six runs with `-benchmem`; stdout and `benchmark.txt` |
| `make benchmark-save BENCH_VERSION=local-doc-audit` | BFD/netio samples and metadata under `testdata/benchmarks/local-doc-audit/` |
| `make benchmark-compare OLD=v0.4.0 NEW=local-doc-audit` | `benchstat` comparison and HTML under `reports/benchmarks/` |
| `make profile` | BFD CPU and heap profiles, `cpu.prof` and `mem.prof` |
| `make benchmark-cross` | Go operations and local C replicas, sequentially; `bench-results/` |

Use a new snapshot identifier to preserve previous samples; it does not create
a release tag. `benchmark-save` runs BFD and netio separately with
`-bench=. -benchmem -count=6 -run='^$' -timeout=120s`.
`benchmark` and `benchmark-all` do not enable the race detector or write CPU/
heap profiles. Race qualification belongs to the test gate; profiling is separate.

The legacy `benchmark`, `benchmark-all`, `benchmark-save` and
`benchmark-compare` recipes pipe tool output through `tee` without preserving
the tool's failure status. A successful Make exit alone does not prove that
tests or `benchstat` succeeded; `benchmark-save` can still write metadata after
a failed benchmark. Inspect both streams and require complete expected samples
before accepting a snapshot or comparison. The Go-owned CI/release runners
preserve child-command failures separately. See the
[`Makefile`](../../Makefile); Beads `gobfd-qj0.8.2.8.7.2.5.1` tracks the legacy
failure-status defect.

| Saved file | Content |
|---|---|
| `benchmark-bfd.txt` | BFD benchmark samples |
| `benchmark-netio.txt` | Network codec and metadata benchmark samples |
| `benchmark.txt` | Combined samples |
| `meta.json` | Snapshot identifier, commit, date, Go version and run count |

## Read output

```text
BenchmarkControlPacketMarshal-8    195605820    5.869 ns/op    0 B/op    0 allocs/op
```

| Field | Meaning |
|---|---|
| `-8` | Effective benchmark `GOMAXPROCS` |
| `195605820` | Adaptive iteration count; compare time/op rather than counts alone |
| `ns/op` | Elapsed time per operation |
| `B/op`, `allocs/op` | Reported heap bytes and allocations per operation |

Keep raw samples. Repetition alone does not remove host contention, CPU
throttling, cache effects or timer noise. Compare equivalent benchmark bodies,
compiler, architecture, limits and environment; inspect statistical uncertainty
before attributing a change to source code. Do not compare race and non-race
costs as the same workload.

## Operation boundaries

| Benchmark family | Measured work | Important exclusion or limit |
|---|---|---|
| Control marshal | Serialize the packet into a supplied buffer | With-auth marshal serializes an already prepared auth section; it does not sign |
| Control unmarshal | Parse and validate wire fields | With-auth parse allocates `AuthSection`; its digest borrows the input buffer |
| FSM | Indexed transition lookup | No owner loop, socket, timer or notification |
| Jitter and interval arithmetic | The named calculation | No timer wake-up or scheduling delay |
| `RecvDecodeLookupEnqueue` | Decode, discriminator lookup, attempted buffered enqueue | Enqueue is not packet processing or owner commit |
| `RecvDecodeFSM` | Decode and stateless FSM transition | No live owner state or transport |
| `ReceiveOwnerCollector` | Actual owner commit with the real collector | Synthetic timing; excludes UDP, wire decoding and authentication |
| `TxMarshalJitter` | Marshal and jitter calculation | No socket send or peer arrival |
| Create/reconcile | Session lifecycle | B/op is not total retained RSS per session |
| `BuildInnerPacketInto` | Inner assembly into caller-owned storage | Production overlay TX path |
| `BuildInnerPacket` | Allocating compatibility wrapper | Separate cost and ownership |
| Metadata/timer/metrics | The named parser or observer | No complete packet pipeline or scale guarantee |

Definitions live in [`internal/bfd/`](../../internal/bfd/),
[`internal/netio/`](../../internal/netio/) and
[`internal/metrics/`](../../internal/metrics/). Read the benchmark body when its
name or boundary changes. Avoid fixed benchmark counts in documentation.

## Allocation policy

Selected codec, FSM, timer, lookup and caller-buffer operations target
zero allocations. Authenticated parsing allocates an `AuthSection`; its digest
is not copied there, so callers retaining it must respect buffer lifetime.
The compatibility inner-packet builder allocates its returned slice. Session
construction and reconciliation allocate lifecycle state. A zero result for
one body neither proves all hot paths allocate zero nor bounds runtime GC pauses.

Historical Go 1.26 samples remain in
[`testdata/benchmarks/v0.4.0/`](../../testdata/benchmarks/v0.4.0/).
Their ns/op are not Go 1.27.3 acceptance and must not be marked as meeting
an unrelated threshold. [BENCHMARKS.md](../../BENCHMARKS.md) points to the
retained samples and more recent scoped owner observations.

## CI policy and reports

The current benchmark report emits a warning for classified critical time/op
regressions of at least 10%; a warning does not itself fail the job. A calibrated blocking
regression policy remains Beads `gobfd-qj0.8.2.1.6.4`. Preserve malformed-output
failures and do not describe the warning as a release performance gate.

`make test-report` runs the race test suite and creates JUnit/HTML under
`reports/tests/`; `make report-all` also saves and compares Go benchmarks.
`benchmark-report` is a legacy Make target that directly invokes `go run`;
run its generator in the pinned container rather than using host Go.
Keep analysis UIs on loopback. Profiles can contain sensitive operational data.

## Related documents

- [Development](09-development.md) — pinned tools and quality workflow.
- [Comparisons](13-competitive-analysis.md) — C-model and deployment limitations.
- [Performance](14-performance-analysis.md) — telemetry, workload and resources.
- [Workload ADR](adr/0007-workload-phase-accounting.md) — phase and ledger policy.
