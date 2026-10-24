# Comparing BFD Implementations

Compare a specific implementation, release and deployment profile. RFC support,
wire interoperability, operational integration and performance are separate
properties. GoBFD's current boundary is the [RFC matrix](08-rfc-compliance.md),
not a blanket feature ranking.

## What this repository measures

| Evidence | What it establishes | What it does not establish |
|---|---|---|
| Go micro-benchmarks | Cost and allocations of the named Go benchmark body | UDP throughput, timer accuracy or supported session scale |
| Local C-model benchmarks | Cost of repository-owned model operations | Performance of upstream FRR/BIRD daemons or a language ranking |
| Base four-peer topology | Protocol interactions with the configured FRR, BIRD3, Holo and Thoro peers | Every extension, vendor NOS or deployment environment |
| BGP coupling topology | The tested GoBGP, FRR, BIRD3 and ExaBGP interactions | Route continuity under untested outages or reloads |
| Vendor NOS topology | Behavior of the selected image and scenario when actually run | All platforms, releases, address families or hardware offload |
| Production-core workload fixture | Observed owner/peer packet accounting and cleanup | Reference-load or full-daemon acceptance |

Peer source pins and topology definitions live under
[`test/interop/`](../../test/interop/),
[`test/interop-bgp/`](../../test/interop-bgp/) and
[`test/interop-clab/`](../../test/interop-clab/).
[Interoperability](05-interop.md) states their qualification scope. Keep the
exact image identity, source SHA and test evidence when making a comparison.

## Software comparison method

Use the same session count, address families, authentication, packet size,
negotiated intervals, Detect Multiplier, loss input and CPU/memory limits.
Separate protocol load from routing-table updates and management traffic.
Record the peer implementation's actual configured behavior, rather than
assuming that a default in another release applies.

FRR/BIRD C files in [`bench/c/`](../../bench/c/) are local replicas of selected
operations. They are not linked upstream daemon code. Their validation,
lookup, scheduling and iteration boundaries differ from the Go benchmark
bodies. See [Performance analysis](14-performance-analysis.md#component-comparison-boundary).
Do not derive upstream throughput, per-session RSS, restart behavior or
relative language speed from those tables.

GoBFD runs Control and Echo owners in separate goroutines; both currently
pin an OS thread. Goroutine ownership alone supplies no CPU reservation,
scheduling isolation or supported-scale bound. Current reference runtime
qualification remains incomplete.

## Hardware and cloud profiles

Hardware offload, minimum timers, session capacity, address-family support,
Echo support and control-plane fate sharing belong to the exact vendor model,
software release and connection service. This repository does not qualify a
hardware platform or cloud provider merely by listing it as a peer or providing
a lab topology.

For a target deployment, obtain its primary documentation, select mutually
supported procedures and intervals, then measure the actual path. Retain the
provider's failure-detection and graceful-restart policies separately from
GoBFD's configured reaction. RFC 7419 common intervals do not establish
provider support, timer accuracy or forwarding continuity.

## Timer and load interpretation

For Asynchronous BFD, local Detection Time is the received Detect Multiplier
multiplied by `max(local RequiredMinRxInterval, remote DesiredMinTxInterval)`.
The nominal transmit interval is
`max(local DesiredMinTxInterval, remote RequiredMinRxInterval)` before jitter.
A zero remote receive interval, remote Demand and Poll transactions affect
periodic transmission; see [Protocol](02-protocol.md).

At 1,000 sessions and a nominal 100 ms interval, 10,000 packets/s per direction
is arithmetic before jitter, suppression and protocol effects, not a measured
supported rate. Tighter timers require measured scheduling and loss headroom.
[Runtime measurement boundaries](14-performance-analysis.md) explain the required
denominators, clock evidence and qualification limits.

## Related documents

- [Benchmark guide](12-benchmarks.md) — commands and statistical interpretation.
- [Benchmark snapshots](../../testdata/benchmarks/) — retained raw historical results.
- [RFC compliance](08-rfc-compliance.md) — implemented and incomplete procedures.
- [Roadmap](roadmap.md) — dated acceptance state; Beads owns execution.
