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
- [Workload and clock boundaries](../14-performance-analysis.md)
- [RFC 5880 Section 6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4)
- [Linux time namespaces](https://man7.org/linux/man-pages/man7/time_namespaces.7.html)
- [Linux socket overflow counters](https://man7.org/linux/man-pages/man7/socket.7.html)
- [Go directory-scoped filesystem operations](https://github.com/golang/go/blob/master/src/os/root.go)
- [Linux cgroup v2 PID controller](https://docs.kernel.org/admin-guide/cgroup-v2.html#pid)
- [Go thread affinity](https://pkg.go.dev/runtime#LockOSThread)
- [Linux thread CPU clocks](https://man7.org/linux/man-pages/man3/clock_gettime.3.html)
