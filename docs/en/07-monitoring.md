# Monitoring

![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C?style=for-the-badge&logo=prometheus)
![Grafana](https://img.shields.io/badge/Grafana-Dashboard-F46800?style=for-the-badge&logo=grafana)
![slog](https://img.shields.io/badge/log%2Fslog-Structured-00ADD8?style=for-the-badge)
![FlightRecorder](https://img.shields.io/badge/Flight_Recorder-Go_1.26-34a853?style=for-the-badge)

> Prometheus metrics, Grafana dashboard, structured logging, and Go 1.26 flight recorder for production observability.

---

### Table of Contents

- [Prometheus Metrics](#prometheus-metrics)
- [Grafana Dashboard](#grafana-dashboard)
- [Alerting Rules](#alerting-rules)
- [Structured Logging](#structured-logging)
- [Flight Recorder](#flight-recorder)
- [Endpoints](#endpoints)

### Prometheus Metrics

GoBFD exposes Prometheus metrics at the configured endpoint (default `:9100/metrics`). All metrics use the `gobfd_bfd_` prefix.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `gobfd_bfd_sessions` | Gauge | `peer_addr`, `local_addr`, `session_type` | Currently active BFD sessions |
| `gobfd_bfd_packets_sent_total` | Counter | `peer_addr`, `local_addr` | BFD Control packets transmitted |
| `gobfd_bfd_packets_received_total` | Counter | `peer_addr`, `local_addr` | BFD Control packets received |
| `gobfd_bfd_packets_dropped_total` | Counter | `peer_addr`, `local_addr` | Packets dropped (validation/buffer) |
| `gobfd_bfd_state_transitions_total` | Counter | `peer_addr`, `local_addr`, `from_state`, `to_state` | FSM state transitions |
| `gobfd_bfd_auth_failures_total` | Counter | `peer_addr`, `local_addr` | Authentication failures (RFC 5880 Section 6.7) |

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
1. Start the production stack: `podman-compose -f deployments/compose/compose.yml up -d`
2. Open Grafana at `http://localhost:3000` (default: admin/admin)
3. The dashboard auto-provisions from the JSON file

### Alerting Rules

Recommended Prometheus alerting rules for BFD:

```yaml
groups:
  - name: gobfd
    rules:
      # Alert on BFD session going Down
      - alert: BFDSessionDown
        expr: increase(gobfd_bfd_state_transitions_total{from_state="Up", to_state="Down"}[5m]) > 0
        for: 0s
        labels:
          severity: critical
        annotations:
          summary: "BFD session {{ $labels.peer_addr }} went Down"

      # Alert on excessive session flapping
      - alert: BFDSessionFlapping
        expr: increase(gobfd_bfd_state_transitions_total{from_state="Up", to_state="Down"}[1h]) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "BFD session {{ $labels.peer_addr }} is flapping"

      # Alert on authentication failures
      - alert: BFDAuthFailure
        expr: rate(gobfd_bfd_auth_failures_total[5m]) > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "BFD auth failures from {{ $labels.peer_addr }}"

      # Alert on high packet drop rate
      - alert: BFDPacketDrops
        expr: rate(gobfd_bfd_packets_dropped_total[5m]) > 1
        for: 5m
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

### Flight Recorder

Go 1.26 `runtime/trace.FlightRecorder` captures a rolling window of execution traces for post-mortem debugging:

| Parameter | Value |
|---|---|
| Window age | 500ms |
| Max size | 2 MiB |

The flight recorder captures scheduling events, goroutine creation, GC pauses, and network I/O. On BFD session failure, the trace snapshot can be dumped for analysis with `go tool trace`.

### Endpoints

| Endpoint | Port | Path | Description |
|---|---|---|---|
| Prometheus metrics | `:9100` | `/metrics` | Prometheus scrape target |
| gRPC API | `:50051` | -- | ConnectRPC/gRPC API |
| gRPC Health | `:50051` | `/grpc.health.v1.Health/Check` | gRPC health check |

### Related Documents

- [03-configuration.md](./03-configuration.md) -- Metrics endpoint configuration
- [06-deployment.md](./06-deployment.md) -- Production stack with Prometheus + Grafana
- [04-cli.md](./04-cli.md) -- CLI monitor command for live events

---

*Last updated: 2026-02-21*
