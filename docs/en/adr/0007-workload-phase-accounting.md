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

## Consequences

Commands without a control directory retain independent timers and explicit
limits. Fixture success does not qualify reference load, the complete daemon,
clock accuracy or stable delivery. Beads `gobfd-qj0.8.2.1.6.3.3` owns implementation
acceptance; reference distributions, regression policy, thread-pin A/B and final
wire qualification remain separate. Component results do not justify a new
scheduler or kernel fast path.

## References

- [Workload and clock boundaries](../14-performance-analysis.md)
- [RFC 5880 Section 6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4)
- [Linux time namespaces](https://man7.org/linux/man-pages/man7/time_namespaces.7.html)
- [Linux socket overflow counters](https://man7.org/linux/man-pages/man7/socket.7.html)
- [Go directory-scoped filesystem operations](https://github.com/golang/go/blob/master/src/os/root.go)
- [Linux cgroup v2 PID controller](https://docs.kernel.org/admin-guide/cgroup-v2.html#pid)
- [Go thread affinity](https://pkg.go.dev/runtime#LockOSThread)
