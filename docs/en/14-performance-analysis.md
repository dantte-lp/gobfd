# Performance and Runtime Measurement Boundaries

![Go](https://img.shields.io/badge/Go-1.27-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![FRR](https://img.shields.io/badge/FRR-bfdd-dc3545?style=for-the-badge)
![BIRD3](https://img.shields.io/badge/BIRD3-BFD-28a745?style=for-the-badge)
![Podman](https://img.shields.io/badge/Podman-Reproducible-892CA0?style=for-the-badge&logo=podman)

> Receive telemetry, workload accounting and resource boundaries for the current
> implementation. Component benchmarks and historical receipts remain separate
> from final-candidate runtime qualification.

---

## Linux Receive Telemetry Boundary

Development tracks the committed-latency baseline in `gobfd-qj0.8.2.1.6`.
The receive metadata stage enables `SO_TIMESTAMPNS_NEW` and `SO_RXQ_OVFL`
on Linux listeners when supported. `ENOPROTOOPT` leaves that measurement
unavailable without stopping BFD; other socket-option failures include the
option name and stop listener creation. TTL and packet-info options remain
mandatory. Qualification requires actual samples; unavailable telemetry does
not mean zero latency or zero loss. The 128-byte ancillary buffer covers timestamp, drop,
TTL/Hop Limit and destination/interface metadata for IPv4 and IPv6.
`MSG_CTRUNC` returns `ErrAncillaryTruncated` before demultiplexing.

`PacketMeta.KernelRXUnixNano` is a raw software `CLOCK_REALTIME` sample;
zero means absent or invalid. Decode accepts native-endian time64 values
only when nanoseconds are normalized and the result fits positive int64
Unix nanoseconds. See the [kernel timestamp ABI](https://www.kernel.org/doc/html/latest/networking/timestamping.html).

`SocketDrops` is the cumulative uint32 counter carried by `SO_RXQ_OVFL`;
`SocketDropsKnown` records whether a valid counter message was present.
The kernel omits zero-valued drop messages. Samples describe drops before
that datagram was queued; aggregation must handle wrap and a final accepted
datagram after the load stops. See [socket(7)](https://man7.org/linux/man-pages/man7/socket.7.html)
and [`sock_recv_drops`](https://github.com/torvalds/linux/blob/master/net/socket.c).

Each Control listener now owns a paired realtime/monotonic calibration. A
sample maps to the calibration's monotonic time by its realtime offset;
samples before calibration, in the future, outside the numeric range or
without a monotonic reading are excluded. The observed realtime-versus-
monotonic elapsed difference must stay within 1 ms at receive and commit.
An observed larger step revokes the calibration for that listener's lifetime,
including already queued samples through shared atomic revocation. Recreating
the listener establishes a new calibration; restarting the receiver on the
same listener does not clear revocation. This avoids guessing the clock epoch
of queued timestamps after a backward step.

The bound is a measurement-validity policy, not a sub-millisecond accuracy
guarantee. A step that returns between observations cannot be detected by
these paired samples. See Go's [monotonic clock contract](https://pkg.go.dev/time#hdr-Monotonic_Clocks).
Clock outcomes never alter packet acceptance or detection deadlines, which
retain the existing monotonic accepted-packet time.

Timing follows discriminator, peer and unsolicited admissions by value.
`gobfd_bfd_control_rx_timing_total{outcome}` counts every normally completed
Control owner packet using six fixed outcomes: `valid`, `unavailable`,
`invalid`, `before_calibration`, `future` and `clock_step`. Only valid samples
enter `gobfd_bfd_control_rx_commit_seconds`. Validation refusal, enqueue,
queue refusal, abandonment and panic are not completed samples; their existing
delivery denominators remain separate. Neither metric has peer labels.
Overlay and direct legacy callers without kernel timing report `unavailable`.

Real IPv4/IPv6 UDP tests supply nonzero committed samples for both demux paths.
The real-collector owner benchmark waits for every commit, but excludes UDP,
wire parsing and authentication and uses synthetic clock input for cost only.
The initial result was 16 B/op and 2 allocs/op from peer-address formatting.
Active registered peer/local pairs now retain the four packet counter handles;
the unchanged benchmark reports 0 B/op and 0 allocs/op in three local runs.
Reference counting keeps shared labels cached until their last registration.
The cache contains only active pairs; historical CounterVec series remain
retained, and unregistered callers keep the existing vector lookup path.
Registration publishes zero packet/auth counters before their first update.
This does not qualify lifetime series retention or the complete UDP pipeline.
Before/after evidence is in `reports/collector-owner-20261022/`.
Bounded CPU/GC/loss distributions, calibrated regression policy, supported
scale and final qualification remain open under the parent.

## Timer observation boundary

Control and Echo owner loops retain the actual `time.Timer.C` timestamp.
Go's [sendTime](https://github.com/golang/go/blob/master/src/time/sleep.go)
adjusts a delayed channel delivery back to its scheduled firing time. Normal
runtime readings retain Go monotonic arithmetic; network realtime samples
are not used in these timer measurements.

`dispatch` measures scheduled firing to owner handling. A TX `effect` measures
successful `SendPacket` return, before later Poll bookkeeping; it does not
promise wire or peer arrival. A detection `effect` measures publication of a
timeout-caused transition to Down, before logs, notifications or announcement
sends. No deadline, jitter, suppression or FSM action is changed.

Four fixed timer kinds produce 20 counter series and eight histograms for
valid dispatch/effect stages. Zero-value or future timestamps remain explicit
outcomes and never enter valid latency histograms. `no_effect` covers normal
processing with no qualifying send/transition; it combines policy suppression,
send failure and idle detection, so it is not a packet-loss counter. A panic or
in-flight handler may leave dispatch without an effect/no-effect receipt.
Untimed actions do not enter timer observations. These measurements do not
establish false-Down rate, supported scale or the required load distributions;
those require the actual UDP workload and accounting in Beads.

## Socket-drop observation boundary

Linux [socket cmsgs](https://github.com/torvalds/linux/blob/a1c2b1da93f5f612aa7a33190c4900c98e51f6da/net/socket.c)
omit a zero dropcount; the [UDP enqueue path](https://github.com/torvalds/linux/blob/a1c2b1da93f5f612aa7a33190c4900c98e51f6da/net/ipv4/udp.c)
stores a cumulative snapshot on queued datagrams. Old queued packets therefore
do not reveal later drops. Drain the old queue and retain fresh post-load
markers, including a repeated marker that must not add the same losses twice.
Absence can also mean unsupported telemetry, so it never proves loss-zero.

Each base UDP listener owns a synchronized scalar high-water mark across
receiver reruns. Fresh daemon sockets start at zero; existing/custom sockets
need an initial known baseline. A new listener starts a new observation epoch.
Only a forward modular distance below `2^31` advances the mark and contributes
an increment. Equal known samples contribute zero; stale or half-range values
are `ambiguous` and preserve the previous mark. This uses bounded
[serial arithmetic](https://www.rfc-editor.org/rfc/rfc1982.html#section-3),
not an assumption that every numeric decrease is a wrap.

The counter epoch must stay unchanged and the observation gap must remain
below half the `uint32` space. Multiple unobserved wraps and a reset that
mimics forward progress cannot be detected. Initial samples on existing
transports, unavailable intervals and invalid samples are not complete-loss
evidence. The two fixed kinds and five outcomes produce ten sample counters
and two increment counters without peer labels. Handles are created once;
resetting or deleting their vector series is unsupported.

Sampling precedes read-size, TTL and BFD parser rejection. Payload truncation
keeps independently valid complete ancillary metadata while retaining its
typed read error; truncated, malformed or duplicate drop cmsgs are invalid.
Ordinary read errors, closure and timeouts are not consumed-datagram samples.
Overlay paths are not instrumented by this listener boundary. These observations
do not establish wire loss, supported scale or final runtime qualification.

## Workload environment observations

The Go-owned `test/internal/bfdload.CaptureEnvironment` observes its own
process. Run `go run ./test/cmd/bfdload environment` inside the pinned development
container to emit its JSON snapshot: [RSS and threads](https://www.kernel.org/doc/html/latest/filesystems/proc.html),
FDs, [process-wide context switches](https://man7.org/linux/man-pages/man2/getrusage.2.html),
effective `GOMAXPROCS`, GC settings and cumulative runtime histograms.
RSS uses `smaps_rollup`; FD counts include observation descriptors.
[CPU affinity](https://man7.org/linux/man-pages/man2/sched_setaffinity.2.html)
belongs to the observing thread. Start time and elapsed time bound a sequential,
non-atomic sample; observation overhead requires calibration.

[Histograms](https://github.com/golang/go/blob/2aae1000ea1d4b28923b96db28acf4e3b99d6aff/src/runtime/metrics/histogram.go)
retain copied counts and ordered half-open bounds, with infinite bounds as
JSON strings. Scheduler delay is separate from kernel RX-to-owner latency.

[Cgroup v2 data](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)
covers the current visible group. `cpu.stat` bandwidth counters exclude
inherited throttling; optional `cpu.stat.local` has separate validity.
Missing or malformed data remains unavailable. Limits distinguish `max` from
numeric zero. Hidden parents, reference admission, generator isolation and
supported load remain separate gates in Beads.

### Resource admission

Snapshots retain `pids.current` and `pids.max` as task counts. Before named
owner profiles create sessions, admission checks additional headroom of
`sessions + 64` at every visible ancestor: 1,064 tasks for reference and 5,064
for engineering. This conservative policy covers the currently pinned Control
baseline, not a measured upper bound. The report retains the budget even on
refusal; it does not reserve capacity or relax the separate CPU, memory,
thread-pin A/B and runtime qualification gates. See [ADR-0007](./adr/0007-workload-phase-accounting.md).

### CPU pressure

The owner workload generates requested CPU pressure with bounded helper workers
inside its own quota. `--cpu-load-percent` uses the shared profile validator;
named profiles retain the approved 80% input. Enabled CPU pressure starts one
thread-pinned worker per effective `GOMAXPROCS` and rejects values above eight.
Each 100 ms window requests that percentage of thread CPU time, bounded by
the window's wall-clock deadline. The JSON `cpu_pressure` receipt
separates requested duty, actual thread CPU time, summed worker wall time,
start/end lateness and missed 100 ms duty windows. It includes worker clock
polling and runtime overhead; elapsed wall time alone is not consumed CPU time.
Workers stop and join before the frozen owner snapshot and packet drain.
Completed generation does not establish a held 80% load or reference acceptance;
use the actual CPU/cgroup observations and clock-validity evidence separately.

### GC pressure

`--gc-pressure` adds a default-off, fixed allocation workload inside the owner
quota: up to 4 MiB per 100 ms, in 64 KiB objects, with at most 16 MiB of retained
references. Peers carry the same profile but do not allocate this workload.
The `gc_pressure` receipt separates actual bytes, objects, batches, skipped or
truncated batches, peak retained-reference count, released references and
phase lateness. A skipped window is not caught up later. Workers join and
clear the ring before measurement freeze and drain; unreachable old objects
still await normal GC, so this is not a heap/RSS limit. The helper does not
force GC or modify runtime settings. Existing GC-cycle and pause-window
observations cover the whole owner process and do not prove causality,
calibrated pressure, latency acceptance or reference support.
`released_objects` counts occupied ring entries dropped at worker exit,
not all prior overwrites or objects reclaimed by GC.

### Scheduler pressure

`--scheduler-pressure` adds four owner helpers per effective `GOMAXPROCS`;
it rejects values above eight rather than clamping the worker count. The
maximum is 32 helpers. In each common 100 ms window, a helper attempts up to 64 rounds
of 1024 compute iterations followed by `runtime.Gosched`, stopping at the
deadline or cancellation and skipping elapsed windows. The
`scheduler_pressure` receipt retains actual rounds/yield calls, joined
workers, skipped/incomplete worker windows, checksum and phase lateness.
Yield calls do not establish OS context-switch counts or runnable-queue depth.
The existing scheduler-window histogram and production timer/committed-RX
histograms remain distinct observations. Workers join before freeze/drain;
the default-off variant preserves named inputs and does not change runtime
settings or establish calibrated reference distributions.

### Session churn

The default-off `--session-churn` variant releases and recreates one actual
admitted Manager session at a time from the explicit peer manifest. It starts
250 ms after the common epoch, uses a five-second cadence and keeps a 200 ms
gap after the real final-claim drain. AdminDown's one-second advertised TX floor
requires at least three seconds of notification drain at multiplier 3. Enabled
fixtures therefore require `--duration=6s` or longer; a cycle with less than five
seconds remaining is skipped and counted separately. This is an admission
budget, not a drain or recovery latency guarantee. The mixed process fixture
uses an 11-second measurement window to exercise both IPv4 and IPv6 generations.
Up recovery waits at most five seconds after creation and ends no later than
measurement. An enabled window with no actual recovery fails instead of
reporting completed churn.

`session_churn` records actual release/create/Up recovery, phase times, source
ports and old/new generations for correlation with the independent peer report.
Release-call start and drain completion bound registry removal; the completion
timestamp is not its exact removal time. Down events outside `[start, end)`
are excluded; notification-gap loss remains conservative.
It skips slots outside Up and does not infer false-Down causes. Fixed slots and
a serialized inventory prevent a retired generation from appearing as the
current registration. `retired_received` and current-generation RX are separate;
`terminal_packets` uses the lifetime committed/rejected/kernel-drop ledger.
Active `registrations.known_peers` and `active_metric_registrations` are distinct
from retained session and packet series counts. Those series counts are real
collector DTO label sets, not expanded histogram buckets or active peer-Up
counts. Stopped snapshots retain the last session samples with `registered=false`.
The variant reuses prepared wire identities and does not qualify arbitrary
unique-peer label growth. Churn joins before measurement freeze; the bounded
event journal covers all five-second slots of the 24-hour profile and counts
later omissions explicitly.

### Owner and independent peers

`test/internal/bfdload.StartOwner` runs the production UDP listeners, receiver,
session manager and owning UDP senders with a fresh real collector. Its typed
snapshot includes configured endpoints, source ports, session observations,
delivery counters, collector metrics and self-process resources. Cancellation
or a failed receiver joins all owned workers and closes their sockets; `Close`
returns shutdown errors. Control receive ports exclude Echo port 3785 and the
49152–65535 TX source range. The IPv4/IPv6 component fixture uses isolated
alternate ports and validates actual packet commit, rejection and returned TX. It does not
qualify RFC ports, an independent generator, daemon-wide resource bounds or the
reference workload.

`test/internal/bfdload.RunPeers` drives actual UDP with its own unauthenticated
asynchronous Control state model, independent of the target session FSM.
Separate seeded PCG streams cover each peer and direction; protocol jitter
uses another stream. Random omissions and timed blackholes retain separate
counts from successful UDP writes, canceled writes and unprocessed input.
Partial startup and cancellation close owned sockets and join workers.
Named `reference` and `engineering` profiles reject reduced qualification
inputs; `fixture` is explicitly unqualified. Generator reports remain scoped
to the generator, with raw kernel-drop samples and no reference verdict.
`bfdload peers PROFILE` and `bfdload owner PROFILE` consume typed flags and
repeatable `--session=localAddrPort,peerAddrPort` bindings, plus `--interface`
for the named profiles. Scalar duplicates, malformed endpoints and reduced
named profiles fail before sockets open. SIGINT/SIGTERM cancel the workload,
join workers and emit a partial JSON report; signals retain normal termination
while stdout is blocked after cleanup.

### Measurement phases and historical fixtures

Commands without `--control-dir` retain independent duration timers and lifetime
totals that include setup and shutdown. Controlled owner/peer commands use the
existing Go-owned topology controller: bind both roles, reach Up, publish a common
future monotonic epoch, freeze the owner measurement snapshot, stop and join peers,
then drain terminal accounting before owner shutdown. The private bounded control
files refuse replacement and verify a common time namespace. Missing terminal
receipts fail at a bounded deadline instead of marking the run complete.
The conversion bracket does not establish independent RX timing uncertainty.
Lifetime totals must not be divided by the requested measurement duration to
claim window throughput; snapshots remain sampled observations.
See [ADR-0007](./adr/0007-workload-phase-accounting.md).

The 2026-10-24 independent-process IPv4/IPv6 fixture passes in 47.98 s with
79 actual writes and 79 owner commits, matching phase identities, idempotent
namespace setup and four verified container removals. Evidence:
`reports/udp-resume-20261024/`. Both reports retain `reference_qualified=false`
and unknown total clock uncertainty. This is a production-core fixture receipt,
not reference-load or complete-daemon qualification. Beads
`gobfd-qj0.8.2.1.6.3.3` owns implementation acceptance.
Local implementation acceptance is recorded at
`b2e68ad2a13824a05a9b92528fe55c9a8d4fd6d1`; the corrected process fixture
passes in 47.67 s with the same 79/79 ledger and explicit nonempty measurement
snapshot. Mutation and malformed-report checks prove snapshot and failure
boundaries. Reference and absolute-clock qualification remain open.

### Down-journal completeness

`RunOwner` records initial/final real collector snapshots, sampled resource
peaks, means for the first/final six-hour RSS windows, runtime histogram
deltas and a bounded Down journal. At the cutoff, `down_journal` captures the
actual retained FSM `Up`→`Down` counter without waiting for notification catchup.
The planned cutoff and actual counter-sample completion time are separate;
scheduling and metric collection can delay the sample. Neither the counter nor
the measurement snapshot is an atomic reading at the planned cutoff.
One consumer continuously reads the subscription from
setup through owner shutdown, retaining at most `maxDownEvents` compact Down
projections and counting gaps/omissions. Classify their monotonic timestamps
after the actual phase is known; setup readiness never leaves the channel
unread. After all owners join, join that consumer within five seconds and
require its total observed transitions to match the final retained source count.
Separate before/after-cutoff counts; only `[start,end)` rows enter the measured
journal. Normal post-window flaps are accounted without contaminating those
rows. Require `observed_before_cutoff <= source_transitions <=
final_source_transitions`. Gaps, omitted rows, a missing or invalid source counter
and unsettled delivery fail the run. `closed`, `settled` and `complete` confirm
the joined final check; an empty journal alone is not proof of no Down.
Unobserved startup flaps conservatively fail completeness; a sampled startup
baseline is not subtracted. Interval membership uses the existing notification
timestamp, not a newly calibrated FSM-commit timestamp. Beads
`gobfd-qj0.8.2.1.6.3.4.3.5` owns this boundary.

| Report field | Meaning |
|---|---|
| `down_journal.cutoff_at_utc` | Planned measurement end |
| `down_journal.source_sampled_at_utc` | Actual completion time of the cutoff counter read |
| `down_journal.source_transitions` | Retained lifetime `Up`→`Down` count at that read |
| `down_journal.final_source_transitions` | Retained lifetime count after owner shutdown |
| `down_journal.observed_before_cutoff` | Delivered transitions timestamped before cutoff, including setup |
| `down_journal.observed_after_cutoff` | Delivered transitions timestamped at or after cutoff |
| `down_events` | Only delivered transitions timestamped in `[start,end)` |

Completeness requires
`final_source_transitions = observed_before_cutoff + observed_after_cutoff`,
the ordering above, a stopped owner, a closed subscription and no recorded
gap, omission or error. Do not use `observed_before_cutoff` as the number of
measurement-window Down events. See the
[journal implementation](../../test/internal/bfdload/down_journal_linux.go)
and [timestamp classification](../../test/internal/bfdload/workload_linux.go).

### Owner commit witnesses

The optional `StateChange.TransitionTiming` carries a value copy of the last
validated owner receive, the actual detection deadline when dispatched,
negotiated Detection Time and a bracket around the local observation commit.
Its version must match the published `ObservationVersion`. Logging and
notification delays cannot move that bracket. `LastPacketReceived` remains
wall-clock telemetry; the validated receive is not a kernel arrival timestamp.

The bounded `down_witnesses` array retains all delivered source transitions,
including setup and drain. Unlike `down_events`, it is not filtered by the
notification timestamp. A notification after cutoff can therefore retain a
commit from inside the measurement window. Legacy cutoff counters keep their
existing notification-time semantics and source-completeness requirements.

Offsets are computed before UTC conversion or serialization. `commit_known`,
`validated_known` and `detection_known` distinguish measured phase-zero values
from unavailable data. Version mismatches, missing monotonic readings and
reversed commit intervals remain unknown, with an explicit diagnostic.
Virtual `synctest` timestamps do not establish native monotonic validity.

[ADR-0012](adr/0012-owner-down-transition-witness.md) defines this evidence.
Neither a commit witness nor `ObservationVersion` alone proves the session
incarnation or the cause of Down; those remain required classifier inputs.

### Actual peer blackhole omissions

Each peer retains `blackhole_windows` for actual generator omissions, separately
from the declared loss schedule. `start_ns` identifies the scheduled window;
`first_tx_ns`, `last_tx_ns`, `tx_omitted` and the corresponding RX fields record
actual omission decisions. A zero direction count means its timestamps have no
observation. These are decision times, not socket arrival or owner commit times.
RX decisions precede input validation; the journal does not establish source
identity or the validity of a discarded BFD packet.
Elapsed times use the adopted common phase when `phase` is present, otherwise
the peer worker epoch. Do not correlate uncontrolled worker-relative values
with owner events as though they shared an epoch.

One worker coalesces TX/RX decisions into at most 256 windows per peer. The next
distinct window increments `blackhole_events_omitted` and fails the run;
truncation cannot produce a completed report. Random omissions and cancelled
sends do not enter this journal. With no overflow, window TX/RX sums equal
`blackhole_omitted` and `receive_blackhole_omitted` respectively.

This journal supplies evidence for `gobfd-qj0.8.2.1.6.3.4.3.6`; it does not
classify a Down as intentional. Correlation still requires the session
incarnation, settled owner journal and valid phase/timing evidence. Schedule
overlap alone cannot establish causality or a passing false-Down gate.

### Completion and qualification

Both controlled and uncontrolled modes freeze `measurement_final` before
stop/drain and journal join; runtime histogram deltas use this snapshot.
The owner does not classify intentional Down without the independent peer
report. `completed` requires the observation duration and source-settled journal;
controlled mode additionally requires terminal-ledger settlement. It does not
mean all sessions remained Up. Reference resources require exact 8 CPU/
8 GiB child limits, GOMAXPROCS 8, the pinned compiler and no tighter visible
cgroup ancestors. Hidden ancestor roots fail closed. Admission does not reserve
capacity or prove host namespace visibility; the isolated runner must verify
these independently. Process isolation, settled 24-hour Up duration, background
CPU load, independent clock validity and full daemon qualification remain
separate gates in Beads. See the [cgroup v2 hierarchy contract](https://docs.kernel.org/admin-guide/cgroup-v2.html).

### Clock validity and conditional uncertainty

Listener `clock_calibrations` retain a three-read monotonic bracket around the
wall/monotonic pair and the nominal native clock resolutions. The conditional
conversion bound is `span_ns + realtime_resolution_ns + monotonic_resolution_ns`.
It assumes native clocks with a constant realtime-minus-monotonic offset. For
each clock, all read errors must stay in one fixed interval whose width is at
most that clock's nominal resolution; this is not an absolute `+/-resolution`
error model. Nominal resolution does not establish this assumption or physical
clock accuracy. The 1 ms consistency tolerance remains separate from this bound.

Let `B,A` be the outer monotonic readings, `M,R` the anchor's monotonic and
realtime readings, and `C,K` the owner-commit and kernel-RX readings. Measured
latency is `C-M-(K-R)`. Both anchor reads lie inside the bracket and observed
monotonic values preserve order. Relative to ideal latency, canceling the common
clock offsets bounds its error by `A-B` plus one within-clock error difference
for each clock. Those differences are bounded by their fixed interval widths,
giving `span + qMonotonic + qRealtime` for either anchor-read order. Fixed clock
biases cancel. Independent conversion fixtures exercise nonzero pairing skew
and opposite endpoints of both error intervals.

The workload's `ClockEpochGuard` arms a nonblocking
[realtime timerfd](https://man7.org/linux/man-pages/man2/timerfd_create.2.html)
with absolute `CANCEL_ON_SET` before calibration. `Finish` checks cancellation
after the last sample and closes its FD. A successful nonblocking read check
sets `no_cancellation_observed`; it does not prove that the clock never stepped.
[Timekeeping publishes before notification](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/kernel/time/timekeeping.c#L1468-L1474),
and [resume notification is deferred](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/fs/timerfd.c#L118-L131).
These upstream v6.12 sources do not qualify a vendor kernel. Cancellation, expiration, unavailable data and
aborted guards cannot provide a usable witness. Its seven-day horizon bounds
the witness lifetime. No per-packet syscall or guard goroutine is added.
`WithClockEpoch` requires process-local monotonic provenance covering the entire
calibration bracket and owner shutdown. UTC values remain reporting metadata;
replaying JSON cannot reconstruct that provenance or requalify a run. It accepts
only known calibrations inside the completed guard window and attaches the
largest conditional conversion bound to `conditional_timing_uncertainty_ns`.
The constant-offset clock model remains an assumption. Total
`timing_uncertainty_ns` stays null with an explicit unknown reason, and stale
reference qualification is cleared. Invalid or uncovered inputs clear both
bounds. Independent evidence of clock validity is still required before full
load acceptance; cancellation evidence alone cannot satisfy it. This does not
establish hardware timestamp accuracy, reference admission or supported load.

## Component comparison boundary

The C programs in [`bench/c/`](../../bench/c/) are repository-owned models,
not upstream FRR or BIRD binaries. `bench_frr.c` and `bench_bird.c` state that
scope in their headers. The FRR-style FSM uses a nested switch; the common
session map is a local linear-probing implementation. The C harness uses
`CLOCK_MONOTONIC`, fixed per-benchmark iteration counts (default 10 million;
creation uses 10,000) and six repetitions; Go uses adaptive iteration counts. The operations and validation work
are not equivalent enough for a vendor or language ranking.

`make benchmark-cross` runs these C models and the Go benchmarks sequentially
and writes `bench-results/bench-c-frr.txt`, `bench-c-bird.txt` and `bench-go.txt`.
`make benchmark-save` writes Go-only results under `testdata/benchmarks/`.
Neither command establishes daemon throughput or supported scale.
The legacy `benchmark-report` Make target invokes `go run` directly; run the
report generator inside the pinned development container rather than adding
a host Go toolchain. Raw historical Go samples remain in
[`testdata/benchmarks/v0.4.0/`](../../testdata/benchmarks/v0.4.0/).

Remove no workload stage from the denominator when comparing results. A
buffered enqueue, stateless FSM lookup and committed owner receive are three
different operations. Authentication parsing fills value-owned metadata and a
bounded inline payload without an auth-object allocation. Packet and retained-wire
ownership are recorded in [ADR-0011](adr/0011-bfd-auth-inline-payload.md).
Signing, retained wire and session processing keep their own measured boundaries.
Production VXLAN/Geneve TX uses caller-buffer
`BuildInnerPacketInto`; the allocating compatibility wrapper is a separate path.

## Runtime architecture and resource limits

Control and Echo each run an owner goroutine and currently call
`runtime.LockOSThread` for that loop's lifetime. This is a thread-resource
constraint, not CPU isolation or a scheduling guarantee. At least 1,000 pinned
Control owners cannot meet the approved 48-thread reference bound. The
thread-pin A/B task remains open; moving the candidate to another VM does not
resolve this constraint.

The daemon does not install a project-wide `GOMAXPROCS` override. The bounded
workload requires its declared value and cgroup limits; actual runtime defaults
must be observed. A container CPU quota does not reserve CPU time, and
`GOMAXPROCS` does not bound total OS threads. Keep the default `GOGC`; choose
`GOMEMLIMIT` only from deployment measurements. No numeric RSS-per-session,
startup-time or GC-pause promise follows from construction B/op or language choice.

Steady-state selected operations target zero allocations. That target does
not cover every receive, authentication, management, lifecycle or notification
path and does not exclude runtime pauses. Refer to the named benchmark body
and exact toolchain receipt when claiming an allocation result.

The historical numeric-zone sender recovery at
`706991db9433b13cec47e0bb8ea2549b01e948d9` raised the native IPv6 link-local
test's combined send/receive assertion cost from 3 to 51 allocations per
packet. The `gobfd-qj0.8.2.2.6.7` follow-up uses verified socket device scope
for the matching named zone. The same fixture records 3 allocations before
recreation and 3 after each of two recreation cycles, retaining source port
49152 and the new bound ifindex. This removes the forced numeric-zone
interface-table refresh; it does not establish a zero-allocation standalone
`SendPacket` benchmark, latency distribution or supported scale. See the
[sender recovery contract](03-configuration.md#hot-reload-sighup), historical
`reports/sender-rebind-20261025/red08-test.txt` and corrected
`reports/sender-rebind-20261025/green03-artifacts/sender-rebind-IPv6LinkLocal.json`.

## Deployment measurement

Use the actual enabled paths, peer procedure and load. Retain source and
binary identities, effective CPU/memory/PID limits, affinity, pressure,
throttling, clock validity, terminal packet accounting, false-Down observations
and cleanup results. Measure Linux receive, owner commit, timer dispatch/effect
and wire behavior separately; their timestamps and success denominators differ.

The approved reference is 1,000 mixed IPv4/IPv6 sessions, nominal 100 ms
intervals and 24 hours after setup. The engineering profile is 5,000 sessions
at 300 ms for 30 minutes. Named profiles preserve required CPU/loss/blackhole
inputs. Bounded CPU, GC, scheduler-pressure and fixed-manifest session-churn
drivers are implemented; GC, scheduler and churn variants are off by default.
Calibrated stress distributions and independent clock evidence remain incomplete.
Neither a short fixture nor a longer run without those
receipts is reference acceptance. Current inputs live in
[`profile_linux.go`](../../test/internal/bfdload/profile_linux.go); Beads owns
acceptance and execution.

There is no `network.dscp` configuration field. Qualify host queueing and QoS
externally rather than copying an unsupported daemon setting. Actual metric
names and label meanings are defined in [Monitoring](07-monitoring.md);
active-session gauges are not peer-Up gauges and delivery outcomes are not
complete wire-loss measurements.

## Related documents

- [Benchmark guide](12-benchmarks.md) — reproducible commands and boundaries.
- [Comparisons](13-competitive-analysis.md) — fair deployment comparison.
- [Workload accounting ADR](adr/0007-workload-phase-accounting.md) — phases and ledger.
- [RFC compliance](08-rfc-compliance.md) — procedure and qualification scope.
- [Roadmap](roadmap.md) — dated evidence; current work lives in Beads.
