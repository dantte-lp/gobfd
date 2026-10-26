# ADR-0007: Workload phases and terminal packet accounting

| Field | Value |
|---|---|
| Status | Accepted policy; runtime qualification separate |
| Date | 2026-10-24 |

## Context

Independent owner and peer timers can stop reception before the last successful
peer write reaches a terminal outcome. Queue admission is not FSM commit;
duration completion does not establish packet accounting or latency acceptance.

## Decision

Reuse the Go-owned production-core workload and independent peer model. After
both roles reach Up, publish one future native monotonic epoch. Each role checks
the time-namespace identity before conversion to a Go timer. Private,
owner-checked `os.Root` files are bounded at 64 bytes; complete publications
refuse replacement and reads reject links and special files. The conversion
bracket does not independently bound RX clock uncertainty.

Freeze the owner measurement snapshot, stop and join peers, retain their actual
successful write count, then drain the owner with a bounded deadline. Require
the disjoint terminal ledger:

```text
successful peer writes = owner committed + terminal rejections + observed kernel drops
```

Intermediate accepted/queued stages do not enter this sum. Excess terminal counts
fail immediately; missing counts fail at the drain deadline with an incomplete
report. Missing kernel overflow samples do not justify assumed loss counts.
Protocol success remains separate from ledger settlement.

Keep frozen measurement and final stopped snapshots distinct. Lifetime counters
include handshake and drain; dividing them by requested measurement duration
cannot establish window throughput. Snapshot collection is sampled; a common
epoch does not establish simultaneous observation. Missing clock evidence stays
unknown, not zero uncertainty.

Before starting a named owner profile, retain `pids.current` and `pids.max` for
every visible non-root cgroup ancestor. The pinned baseline requires additional
task headroom equal to the session count plus a policy margin of 64 tasks.
Reject missing or malformed observations and insufficient headroom before
creating sessions; subtract current usage from the limit without unsigned
overflow. Linux PID limits count threads, including descendant usage;
`GOMAXPROCS` does not bound thread creation. This sequential preflight neither
reserves capacity nor proves the runtime peak. Keep the conservative baseline
budget until the separately measured thread-pin A/B justifies a revision.

Generate requested CPU pressure inside the owner quota with at most eight
workers, using the effective `GOMAXPROCS` as the worker count. Prepare workers
before the common epoch. Each bounded helper locks its thread only to attribute
`CLOCK_THREAD_CPUTIME_ID` reads and unlocks it on exit. In fixed 100 ms duty
windows, request thread CPU time equal to the configured percentage of that
window; truncate the last window at the measurement end. Stop burning at each
wall deadline and count missed budgets instead of carrying debt into later
windows. Cancellation or a clock failure stops and joins workers before the
owner report can complete. The measurement snapshot and packet drain retain
their existing boundaries.

Retain requested duty, actual worker CPU and wall time, start/end lateness,
window and missed-window counts separately. Worker CPU time includes clock
polling and runtime work on those helper threads. These observations neither
prove an 80% held host load nor replace CPU/throttling calibration on admitted
reference resources. No scheduler setting or production session pin changes.

An explicit `--gc-pressure` variant adds one owner worker at the same epoch
without changing the named profile's CPU, loss, blackhole or session inputs.
It allocates up to 64 objects of 64 KiB in each 100 ms window and retains at
most 256 references (16 MiB). Touch each OS page, skip elapsed windows rather
than accumulating debt, and retain actual allocation, skipped/truncated-batch,
lateness and released-reference counts. Stop and join before measurement
freeze and drain. Clearing references does not immediately reclaim heap or
bound RSS. Do not force GC or change `GOGC`, `GOMEMLIMIT` or scheduler settings.
Process GC cycles and pause histograms remain process-wide observations;
they are not attributable solely to the allocation helper. Default-off and
completed fixture generation do not establish reference GC qualification.

The default-off `--scheduler-pressure` variant prepares four runnable helpers
per effective `GOMAXPROCS`, at most 32 helpers. In each common 100 ms window,
each helper attempts at most 64 rounds of the existing 1024-iteration CPU
mixing primitive followed by `runtime.Gosched`. Check cancellation and the
wall deadline between rounds; skip elapsed windows without carrying debt.
Do not pin these helpers or change runtime settings. Retain actual rounds,
yield calls, skipped/incomplete worker windows, checksum and phase lateness;
a yield call does not prove an OS context switch. Join CPU, GC and scheduler
helpers before measurement freeze and drain on success and failure. Peers
carry the request without generating owner pressure. Process scheduler,
timer and committed-RX observations remain separate from calibrated reference
distributions and preserve unknown clock uncertainty.

The default-off `--session-churn` variant cycles one admitted session from
the explicit endpoint manifest at a time. Start 250 ms after the measurement
epoch, then use a five-second cadence without replaying missed cycles. Release
the real final Manager claim and join its drain before a 200 ms registration
gap; recreate the same wire identity and require observed Up before the
measurement deadline. AdminDown advertises at least one second, so multiplier
3 requires at least a three-second notification drain. Enabled fixtures require
at least six seconds. Do not start a cycle with less than five seconds left;
count skipped trailing cycles separately. Wait for Up for at most five seconds
after creation and before measurement end; a window with no actual recovery
fails. Subscribe before churn and retain Down events only within the measurement
interval, excluding setup and drain. Keep notification-gap loss conservative. Skip
slots that are not Up and report the skip rather than attributing their state
to churn. Peer traffic continues independently throughout the gap and recovery.

Serialize inventory mutation, Close and membership/collector snapshots with
one owner lock. Reject creation after closing starts. Each fixed slot owns only
its current sender lease; accumulate retired packet counters after session
join instead of retaining every generation. Record old/new generations,
discriminators, source ports, phase times and actual Manager/collector
cardinality before release, during absence and after recovery. Release-call start
and drain-completion timestamps bound registry removal; completion is not the
removal instant. The Manager's
one-to-one session/demux registrations define the known-peer count; active
session gauge values and retained session/packet metric series remain separate.
Retain enough bounded event records for the 24-hour five-second profile and
count any omissions explicitly. Join churn before measurement freeze and use
the lifetime terminal ledger across all generations. Final-generation RX alone
cannot settle peer writes. This fixed-manifest variant does not introduce new
peer addresses or qualify arbitrary unique-label growth; it does not change
named baseline dimensions or establish reference qualification.

At measurement end, sample the Down-journal denominator from the actual retained
collector `Up`→`Down` counter, which increments before notification timestamp
and enqueue. Retain the planned cutoff and actual sample completion time
separately: scheduling and collection may delay the read, and it is not an
atomic snapshot at the cutoff. Start one bounded consumer with the subscription and keep it active
through setup, measurement and shutdown. Retain at most `maxDownEvents` compact
Down projections, preserving monotonic timestamps until the actual phase is
known. Count all gaps and omissions. After all owner goroutines join, join that
consumer within five seconds, cancelling and joining it on timeout. Freeze the
measurement snapshot before stop/drain and journal join in both controlled and
uncontrolled modes; use it for histogram deltas. Count every observed
notification, separating those before and after cutoff, but retain journal rows
only in `[start,end)`. The before-cutoff count includes setup and is not the
measurement-window event count. Require final source total to equal both observed counts
combined, and before-cutoff count <= cutoff source <= final source. Gaps,
overflow, invalid counters, an unclosed subscription or a count mismatch fail
completeness. Thus later flaps cannot compensate for a missing earlier event;
fully delivered later flaps remain outside the measured journal. Do not subtract
sampled setup history: missed startup flaps conservatively fail this contract.
Reuse production metrics; add no hot-path fence or counter. Journal completeness
does not classify Down as intentional or qualify commit timestamps or clocks.

### Measured blackholes (2026-10-26)

The first blackhole starts one period after the measurement epoch. A 30-minute
period therefore provides no blackhole in the engineering profile's measured
`[0,30 min)` interval. Activity at the cutoff belongs to drain and cannot
establish measured failure or recovery.

Require a 10-minute period and one-second width for engineering: full windows
start at minutes 10 and 20, with recovery time before the cutoff. Retain 5,000
sessions, 300 ms intervals, 30 minutes, random loss and CPU pressure. Reference
retains its 30-minute period and one-second width over 24 hours. Named-profile
validation and reports must identify these different inputs explicitly; older
engineering runs with a 30-minute period do not prove the corrected workload.
Actual omissions, Down/recovery and causal classification remain separate
acceptance requirements in Beads `gobfd-qj0.8.2.1.6.3.4.3.6.3` and its parent.

## Consequences

Commands without a control directory retain independent timers and explicit
limits. Fixture success does not qualify reference load, the complete daemon,
clock accuracy or stable delivery. Beads `gobfd-qj0.8.2.1.6.3.3` owns implementation
acceptance; reference distributions, regression policy, thread-pin A/B and final
wire qualification remain separate. Component results do not justify a new
scheduler or kernel fast path.

## References

- [Common workload epoch and stop/drain protocol](../../../test/internal/bfdload/control_phase_linux.go)
- [Terminal owner accounting](../../../test/internal/bfdload/controlled_owner_linux.go)
- [PID headroom admission](../../../test/internal/bfdload/admission_linux.go)
- [CPU pressure and measured thread CPU](../../../test/internal/bfdload/cpu_pressure_linux.go)
- [Bounded GC pressure](../../../test/internal/bfdload/gc_pressure_linux.go)
- [Bounded scheduler pressure](../../../test/internal/bfdload/scheduler_pressure_linux.go)
- [Real session churn and joined recovery](../../../test/internal/bfdload/session_churn_linux.go)
- [Serialized inventory and lease retirement](../../../test/internal/bfdload/owner_churn_linux.go)
- [Source-settled Down journal](../../../test/internal/bfdload/down_journal_linux.go)
- [Workload and clock boundaries](../14-performance-analysis.md)
- [RFC 5880 Section 6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4)
- [Linux time namespaces](https://man7.org/linux/man-pages/man7/time_namespaces.7.html)
- [Linux socket overflow counters](https://man7.org/linux/man-pages/man7/socket.7.html)
- [Go directory-scoped filesystem operations](https://github.com/golang/go/blob/master/src/os/root.go)
- [Linux cgroup v2 PID controller](https://docs.kernel.org/admin-guide/cgroup-v2.html#pid)
- [Go thread affinity](https://pkg.go.dev/runtime#LockOSThread)
- [Linux thread CPU clocks](https://man7.org/linux/man-pages/man3/clock_gettime.3.html)
