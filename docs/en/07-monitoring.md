# Monitoring

![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C?style=for-the-badge&logo=prometheus)
![Grafana](https://img.shields.io/badge/Grafana-Dashboard-F46800?style=for-the-badge&logo=grafana)
![slog](https://img.shields.io/badge/log%2Fslog-Structured-00ADD8?style=for-the-badge)
![FlightRecorder](https://img.shields.io/badge/Flight_Recorder-Go_1.27-34a853?style=for-the-badge)

> Prometheus metrics, Grafana dashboard, structured logging, and the runtime flight recorder for production observability.

---

## Table of Contents

- [Prometheus Metrics](#prometheus-metrics)
- [Grafana Dashboard](#grafana-dashboard)
- [Alerting Rules](#alerting-rules)
- [Structured Logging](#structured-logging)
- [Flight Recorder](#flight-recorder)
- [Endpoints](#endpoints)

### Prometheus Metrics

GoBFD exposes Prometheus metrics at the configured endpoint (default `127.0.0.1:9100/metrics`). Metrics use the `gobfd_` namespace.

Control session `gobfd_bfd_packets_dropped_total` records queue full/closed,
owner zero-TX/auth refusals and stop abandonment. `gobfd_bfd_auth_failures_total`
is the auth subset of those drops. Parser, TTL, unknown-peer and unsolicited
pre-session refusals appear only in the process-lifetime
`gobfd_bfd_delivery_outcomes_total{source,outcome}` ledger; they create no peer
labels. All supported pairs are preinitialized to zero. Every stage has a
separate denominator: `queued` is not owner `committed`, and stage counts must
not be summed as packet totals. `read_error` does not prove packet loss.
ListSessions exposes the same fixed rows as sampled `delivery_counters`,
including after the last session is deleted; its revision does not cover
atomic counter sampling. Zero counts may be omitted by protobuf JSON encoding.
The `demux` source counts refusals after session selection: `scope_mismatch`
for a packet of the other transport class (multihop listener 4784 against a
single-hop session, or the reverse) or from another receive interface of a
single-hop session; `ttl_rejected` for a multihop packet with TTL below
`255 - multihop_max_hops`.
Echo has separate `echo_read`, `echo_parse`, `echo_ttl`, `echo_demux`,
`echo_admission` and `echo_owner` sources. Zero discriminators are parser
refusals, not demux attempts; ordinary closed/canceled reads are not read
errors. Echo queue refusals and owner abandonment also project to the existing
packet-drop metric for the admitted identity.

`control_notification`, `unsolicited_notification` and `echo_notification`
count each actual raw enqueue attempt. `notification_dispatch` distinguishes
accepted, stale and shutdown-abandoned raw events. `watch_recipient` counts
each recipient's original event as `queued` or `gapped`, including repeated
overflow while its single GAP marker remains queued. The GAP marker is not an
original successful event. `legacy_notification` counts queued/full attempts;
skipped Added events, closed channels and nil sinks are not attempts.
Notification loss never increments Control packet-drop counters. Raw shutdown
abandonment is counted after all producers and Manager workers join.
Kernel `SO_RXQ_OVFL` remains owned by `gobfd-qj0.8.2.1.6`; this ledger does not
qualify Echo transport/security. Echo Watch lifecycle is implemented separately
by `gobfd-qj0.8.2.1.3.10`: frozen rows sample actual Echo counters and timestamps
without counter-only observation-version churn. Its unowned drop/auth fields
are zero and do not replace the delivery ledger or prove absence of loss.
Public Control/Echo raw `queue_full` invalidates live revisioned subscribers
through a coalesced GAP and authoritative resnapshot. Recovery markers add no
`notification_dispatch`, `watch_recipient` or `legacy_notification` outcomes;
each originally queued raw event still has one accepted/stale/abandoned result.
Private unsolicited failures remain cleanup-only. Legacy delivery stays best
effort, and canceled dispatch has no restart/progress guarantee.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `gobfd_bfd_sessions` | Gauge | `peer_addr`, `local_addr`, `session_type` | Currently active BFD sessions |
| `gobfd_bfd_packets_sent_total` | Counter | `peer_addr`, `local_addr` | BFD Control packets transmitted |
| `gobfd_bfd_packets_received_total` | Counter | `peer_addr`, `local_addr` | BFD Control packets received |
| `gobfd_bfd_packets_dropped_total` | Counter | `peer_addr`, `local_addr` | Packets dropped (validation/buffer) |
| `gobfd_bfd_state_transitions_total` | Counter | `peer_addr`, `local_addr`, `from_state`, `to_state` | FSM state transitions |
| `gobfd_bfd_auth_failures_total` | Counter | `peer_addr`, `local_addr` | Authentication failures (RFC 5880 Section 6.7) |
| `gobfd_bfd_delivery_outcomes_total` | Counter | `source`, `outcome` | Fixed Control, Echo and notification stage decisions, retained after session deletion |
| `gobfd_gobgp_rpc_errors_total` | Counter | `rpc` | Failed GoBGP API calls: `DisablePeer`, `EnablePeer`, `CheckCompatibility` (preflight) |
| `gobfd_gobgp_endpoint_compatible` | Gauge | -- | 1 after the GoBGP v4 compatibility preflight passed, else 0 |
| `gobfd_gobgp_dampening_penalty` | Gauge | `peer_addr` | Effective decayed flap penalty at scrape time |
| `gobfd_gobgp_dampening_suppressed` | Gauge | `peer_addr` | 1 when recovery would still be held by dampening |
| `gobfd_gobgp_dampening_suppression_deadline_seconds` | Gauge | `peer_addr` | Unix timestamp of the current maximum suppression bound; 0 when unsuppressed |
| `gobfd_gobgp_pending_action` | Gauge | `peer_addr`, `action` | 1 for the unapplied desired action (`up` or `down`); 0 otherwise |
| `gobfd_gobgp_pending_action_deadline_seconds` | Gauge | `peer_addr`, `action` | Unix timestamp of its next retry or suppression recheck; 0 when none |

GoBGP per-peer metrics exist only with the integration enabled, for peers in
the latest validated `sessions` configuration. The allowlist changes on accepted
reloads; removed peers disappear on the next scrape, and invalid candidates
preserve the previous allowlist. IPv4-mapped addresses are unmapped and
duplicates are merged. API and unsolicited sessions do not create new metric
labels. Each configured peer has seven samples across the five gauges,
including zero-valued `up` and `down` action samples when idle.

Penalty and suppression are evaluated at scrape time using `half_life`,
`reuse_threshold` and `max_suppress_time`. Once recovery suppression expires,
the reported penalty is zero, as when the handler releases suppression.
Observation does not change routing state or record a flap. The suppression
deadline is `suppressed_since + max_suppress_time`; decay can release recovery
earlier. This bound describes eligibility for recovery, not RPC completion.

Pending action and deadline reflect the last completed handler event or
attempt. They cover transient RPC retries and a recovery held by dampening.
The action deadline is the next scheduled attempt or recheck, and may be in
the past while an RPC is in progress. It is neither the suppression bound nor
a guarantee of successful application. Successful application, cancellation,
session deletion or handler shutdown clears the pending action. AdminDown
cancels a pending Down; an Up that re-enables a peer GoBFD disabled remains.

#### Label Values

| Label | Example Values |
|---|---|
| `peer_addr` | `10.0.0.1`, `192.168.1.1`, `fd00::1` |
| `local_addr` | `10.0.0.2`, `192.168.1.2`, `fd00::2` |
| `session_type` | `single_hop`, `multi_hop` |
| `from_state` | `AdminDown`, `Down`, `Init`, `Up` |
| `to_state` | `AdminDown`, `Down`, `Init`, `Up` |

#### Example Queries

```promql
# Total active sessions
sum(gobfd_bfd_sessions)

# Sessions in Up state (requires combining with state transitions)
gobfd_bfd_state_transitions_total{to_state="Up"} - gobfd_bfd_state_transitions_total{from_state="Up"}

# Packet rate per peer (5 minute window)
rate(gobfd_bfd_packets_sent_total[5m])

# Session flaps (Up -> Down transitions in last hour)
increase(gobfd_bfd_state_transitions_total{from_state="Up", to_state="Down"}[1h])

# Auth failure rate
rate(gobfd_bfd_auth_failures_total[5m])

# Packet drop rate
rate(gobfd_bfd_packets_dropped_total[5m])
```

### Grafana Dashboard

A pre-built Grafana dashboard is included at:

```
deployments/compose/configs/grafana/dashboards/bfd.json
```

The dashboard provides:

| Panel | Description |
|---|---|
| **Session Overview** | Total sessions, sessions by state, sessions by type |
| **Packet Rates** | TX/RX packet rates per peer |
| **State Transitions** | Timeline of state changes with Up/Down annotations |
| **Error Rates** | Auth failures, dropped packets |

To import:
1. Start the production stack: `podman compose -f deployments/compose/compose.yml up -d`
2. Open Grafana at `http://localhost:3000` (default: admin/admin)
3. The dashboard auto-provisions from the JSON file

### Alerting Rules

Recommended Prometheus alerting rules for BFD:

```yaml
groups:
  - name: gobfd
    rules:
      # Alert when no sessions are configured or dynamically active.
      - alert: BFDNoActiveSessions
        expr: sum(gobfd_bfd_sessions) == 0
        for: 10s
        labels:
          severity: warning
        annotations:
          summary: "No active BFD sessions"

      # Alert on BFD session going Down.
      - alert: BFDSessionDownTransition
        expr: increase(gobfd_bfd_state_transitions_total{from_state="Up",to_state="Down"}[1m]) > 0
        for: 0s
        labels:
          severity: critical
        annotations:
          summary: "BFD session {{ $labels.local_addr }} -> {{ $labels.peer_addr }} went Down"

      # Alert on excessive session flapping.
      - alert: BFDSessionFlapping
        expr: sum by (peer_addr, local_addr) (increase(gobfd_bfd_state_transitions_total[5m])) > 3
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "BFD session {{ $labels.local_addr }} -> {{ $labels.peer_addr }} is flapping"

      # Alert on authentication failures
      - alert: BFDAuthFailures
        expr: rate(gobfd_bfd_auth_failures_total[5m]) > 0
        for: 30s
        labels:
          severity: warning
        annotations:
          summary: "BFD auth failures from {{ $labels.peer_addr }}"

      # Alert on high packet drop rate
      - alert: BFDPacketDrops
        expr: rate(gobfd_bfd_packets_dropped_total[5m]) > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "BFD packet drops for {{ $labels.peer_addr }}"
```

### Structured Logging

GoBFD uses `log/slog` (stdlib) exclusively. No `fmt.Println`, `log`, `zap`, or `zerolog`.

#### Log Levels

| Level | Usage |
|---|---|
| `debug` | Timer negotiation details, packet content, FSM event details |
| `info` | Session state changes, daemon start/stop, config reload |
| `warn` | Transition to Down, auth errors, invalid packets |
| `error` | Socket bind failures, critical FSM errors |

#### Structured Fields

Every log entry from a session includes:

```json
{
  "time": "2026-02-21T12:00:00.000Z",
  "level": "INFO",
  "msg": "session state changed",
  "peer": "10.0.0.1",
  "local_discr": 42,
  "old_state": "Down",
  "new_state": "Up",
  "event": "RecvInit",
  "diag": "None"
}
```

#### Runtime Log Level Change

The log level can be changed at runtime without restart via SIGHUP:

```bash
# Edit config to set log.level: debug
sudo vim /etc/gobfd/gobfd.yml

# Reload
sudo systemctl reload gobfd
```

For each valid startup or SIGHUP candidate, the coordinator emits one aggregate
structured event: the INFO event `configuration reconciliation converged` only
after all six sources converge, or the ERROR event `configuration
reconciliation incomplete` for a stale generation. The configured logging
threshold applies normally, so INFO convergence events are filtered at WARN or
ERROR. Fields include `desired_generation`, `applied_generation`, `stale`,
aggregate `pending`/`failed`, and per-source `created`, `released`, `pending`,
`failed`, and bounded error-code counts. Raw error text may appear only in that
immediate event; it is not retained in the runtime snapshot or used as a label.

Linux interface events update these same receipts without advancing the desired
generation. Unavailable dependencies make readiness stale and sources pending;
successful automatic retry can restore convergence. Duplicate receipts do not
emit another event. Netlink receive overflow logs a resync request; a full event
channel retains that request until it can be delivered. Queue size is fixed,
independent of event volume. This does not prove that an absent startup listener
or overlay backend can be activated. Non-Linux stubs do not auto-retry.

### Flight Recorder

`runtime/trace.FlightRecorder` captures a rolling window of execution traces for post-mortem debugging:

| Parameter | Value |
|---|---|
| Window age | 500ms |
| Max size | 2 MiB |

The flight recorder captures scheduling events, goroutine creation, GC pauses, and network I/O. On BFD session failure, the trace snapshot can be dumped for analysis with `go tool trace`.

The HTTP dump is off by default. With `grpc.flight_recorder: true` it is served at
`/debug/flightrecorder` on the management server, to callers with a principal only
(the Unix socket, or an mTLS identity allowed by `grpc.mutating_identities`); anonymous
loopback TCP callers get `403` even under `grpc.unsafe_loopback_mutations`. The
metrics listener serves no debug endpoint:

```bash
curl --unix-socket /run/gobfd/gobfd.sock -o trace.out http://localhost/debug/flightrecorder
go tool trace trace.out
```

### Endpoints

| Endpoint | Port | Path | Description |
|---|---|---|---|
| Prometheus metrics | `127.0.0.1:9100` | `/metrics` | Prometheus scrape target |
| gRPC API | `unix:///run/gobfd/gobfd.sock` | -- | ConnectRPC/gRPC API; optional TCP via `grpc.addr` |
| gRPC Health | `unix:///run/gobfd/gobfd.sock` | `/grpc.health.v1.Health/Check` | Empty service is reconciliation readiness; named services are process/service health |

The empty-service check is `NOT_SERVING` before the first converged generation
and while the latest desired generation is stale, then `SERVING` after full
convergence. Named checks for `grpc.health.v1.Health`, `bfd.v1.BfdService`,
`bfd.v1.EchoService`, and `bfd.v1.MicroBFDService` remain `SERVING`.
`gobfd.management` is `NOT_SERVING` while `grpc.unsafe_loopback_mutations`
lets anonymous loopback clients mutate, `SERVING` otherwise. With
`gobgp.enabled`, the named check `gobfd.gobgp` is `NOT_SERVING` until the
GoBGP v4 preflight passes, and after a failed preflight or a failed
`DisablePeer`/`EnablePeer` until the next success; a BFD Down for a
neighbor with more than one base session also sets it `NOT_SERVING` until
the next success
(`03-configuration.md`, Session Identity). systemd
`READY=1` continues to describe process/listener startup rather than
configuration convergence.

### Related Documents

- [03-configuration.md](./03-configuration.md) -- Metrics endpoint configuration
- [06-deployment.md](./06-deployment.md) -- Production stack with Prometheus + Grafana
- [04-cli.md](./04-cli.md) -- CLI monitor command for live events

---

*Last updated: 2026-02-21*
