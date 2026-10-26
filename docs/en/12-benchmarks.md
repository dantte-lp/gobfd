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

`benchmark`, `benchmark-all`, `benchmark-save` and `benchmark-compare` require
Bash on the host and use `errexit` with `pipefail`. A failed benchmark,
`benchstat` or `tee` makes the target fail; later metadata and HTML writes do
not run. `benchmark-save` also stops if reading the Go version fails. Raw output
already written remains available for diagnosis. A failed rerun can leave older
metadata or HTML alongside partial new samples: use a new snapshot identifier
and require a successful target exit and complete expected samples before
accepting results.
The Go-owned CI/release runners retain their separate failure handling.

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
| Control unmarshal | Parse and validate wire fields | Authentication metadata and its bounded payload are owned by value |
| `BenchmarkAuthentication` | Built-in `Sign` and `VerifyKnownSequence` with an immutable static key store | Verify uses decoded signed wire and resets the prior sequence; excludes sockets, retained-wire copies and owner commit |
| FSM | Indexed transition lookup | No owner loop, socket, timer or notification |
| Jitter and interval arithmetic | The named calculation | No timer wake-up or scheduling delay |
| `RecvDecodeLookupEnqueue` | Decode, discriminator lookup, attempted buffered enqueue | Enqueue is not packet processing or owner commit |
| `RecvDecodeFSM` | Decode and stateless FSM transition | No live owner state or transport |
| `ReceiveOwnerCollector` | Actual owner commit with the real collector | Synthetic timing; excludes UDP, wire decoding and authentication |
| `ReceiveOwnerCollectorAuthentication` | Sign, marshal, decode, demux, Verify and owner commit with real metrics | Reuses wire only after commit; steady Up handshake precedes timing; excludes sockets and kernel RX |
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
zero allocations, including authenticated decode with value-owned auth metadata.
The bounded payload is copied into the packet; hash verification separately
requires retained immutable wire. [ADR-0011](adr/0011-bfd-auth-inline-payload.md)
records packet and wire ownership.
The compatibility inner-packet builder allocates its returned slice. Session
construction and reconciliation allocate lifecycle state. A zero result for
one body neither proves all hot paths allocate zero nor bounds runtime GC pauses.

### Authentication methods — 2026-10-26

The digest-result change at `972946c59bbe8d9bc36fbe51edb9f6c82f3bfc33`
recorded these historical linux/amd64 samples: the pinned compiler, three runs of 200000
iterations, and a 16-byte key. Returning the shared digest as a fixed array
removes one digest allocation without changing wire or payload ownership.

| Modes | Sign before, B/op and allocs/op | Sign after, B/op and allocs/op | Verify before, B/op and allocs/op | Verify after, B/op and allocs/op |
|---|---|---|---|---|
| Simple Password | 16 / 1 | 16 / 1 | 0 / 0 | 0 / 0 |
| Keyed MD5, Meticulous Keyed MD5 | 48 / 3 | 32 / 2 | 16 / 1 | 0 / 0 |
| Keyed SHA1, Meticulous Keyed SHA1 | 72 / 3 | 48 / 2 | 24 / 1 | 0 / 0 |

These are successful method allocation samples, not a statistical latency
comparison. Signing still allocated caller-owned payloads in that revision.
The digest change is accepted under `gobfd-qj0.8.2.8.6.3.2.1`.

### Inline payload — 2026-10-26

The ADR-0011 candidate under `gobfd-qj0.8.2.8.6.3.2.2` uses a 20-byte inline
payload and an independent logical length. Local linux/amd64 samples report
0 B/op and 0 allocs/op for all five built-in Sign and Verify methods, three
runs of 200000 iterations with a 16-byte key.

`ReceiveOwnerCollectorAuthentication` also reports 0 B/op and 0 allocs/op in
the same sample matrix. It signs, marshals, decodes, queues and verifies each
packet, then waits for owner commit with the real collector. The fixture reaches
Up before timing and checks exact receive/commit counts; wire is reused only
after commit. Its clock input is synthetic. Socket receive, retained-wire
allocation, kernel timestamps, loss, supported scale and latency distributions
remain separate; the full comparison `gobfd-qj0.8.2.8.6.3.2` is not closed by
these allocation samples.

The local layout changes from 64/32 to 60/28 bytes for packet/auth section.
Receive queue elements remain 160 bytes due to alignment. Like-for-like
linux/amd64 validation builds with CGO disabled, trimpath and no release ldflags
change the daemon from 31445503 to 31434427 bytes; the other three binaries are
unchanged. These are validation binary sizes, not release asset or retained RSS
measurements. The plain in-memory RX fixture remains 128 B/op and 1 alloc/op.

### Micro-BFD dispatch logging — 2026-10-26

The pre-change matrix records 11 allocs/op with no actuator and 15 with a
noop actuator, both before and after inline authentication storage. Typed
`LogAttrs` calls remove boxed log attributes without changing levels, fields
or state delivery. Local linux/amd64 samples, three runs of 200000 iterations,
report 0 B/op and 0 allocs/op without an actuator, including enabled JSON
encoding after the legacy notification queue fills. The fixture retains 64
groups and alternates member Up/Down transitions.

The noop actuator path reports 272 B/op and 4 allocs/op. Allocation profiles
attribute the remaining four allocations to the fresh bounded context and
timer; the five-second deadline, cancellation and ordered dispatch remain
unchanged. The logging change is tracked by `gobfd-qj0.8.2.8.6.3.2.3.1`;
the full zero-allocation target remains open in `gobfd-qj0.8.2.8.6.3.2.3`.
These samples do not qualify actuator backend I/O, supported scale or latency
distributions and do not close the overall allocation gate.

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
