# Configuration

![YAML](https://img.shields.io/badge/YAML-Config-cb171e?style=for-the-badge&logo=yaml)
![koanf](https://img.shields.io/badge/koanf-v2-00ADD8?style=for-the-badge)
![ENV](https://img.shields.io/badge/ENV-Override-ffc107?style=for-the-badge)
![SIGHUP](https://img.shields.io/badge/SIGHUP-Hot_Reload-34a853?style=for-the-badge)

> Complete configuration reference for the GoBFD daemon: YAML file format, environment variable overrides, declarative sessions, GoBGP integration, and hot reload via SIGHUP.

---

### Table of Contents

- [Configuration Sources](#configuration-sources)
- [Full Configuration Example](#full-configuration-example)
- [Configuration Sections](#configuration-sections)
- [Environment Variables](#environment-variables)
- [Declarative Sessions](#declarative-sessions)
- [GoBGP Integration](#gobgp-integration)
- [Hot Reload (SIGHUP)](#hot-reload-sighup)
- [Validation Rules](#validation-rules)
- [Defaults](#defaults)

### Configuration Sources

GoBFD loads configuration from three sources, applied in order (later sources override earlier ones):

1. **Defaults** -- sensible production defaults (see [Defaults](#defaults))
2. **YAML file** -- specified via `-config /path/to/gobfd.yml`
3. **Environment variables** -- prefixed with `GOBFD_`

### Full Configuration Example

```yaml
# GoBFD daemon configuration
# See: configs/gobfd.example.yml

grpc:
  addr: ":50051"

metrics:
  addr: ":9100"
  path: "/metrics"

log:
  level: "info"       # debug, info, warn, error
  format: "json"      # json, text

bfd:
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3

# GoBGP integration (RFC 5882 Section 4.3)
gobgp:
  enabled: true
  addr: "127.0.0.1:50052"
  strategy: "disable-peer"   # or "withdraw-routes"
  dampening:
    enabled: true
    suppress_threshold: 3
    reuse_threshold: 2
    max_suppress_time: "60s"
    half_life: "15s"

# Declarative sessions (reconciled on SIGHUP reload)
sessions:
  - peer: "10.0.0.1"
    local: "10.0.0.2"
    interface: "eth0"
    type: single_hop
    desired_min_tx: "100ms"
    required_min_rx: "100ms"
    detect_mult: 3
  - peer: "10.0.1.1"
    local: "10.0.1.2"
    type: multi_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 5
```

### Configuration Sections

#### `grpc` -- ConnectRPC Server

| Key | Type | Default | Description |
|---|---|---|---|
| `addr` | string | `":50051"` | gRPC (ConnectRPC) listen address |

#### `metrics` -- Prometheus Endpoint

| Key | Type | Default | Description |
|---|---|---|---|
| `addr` | string | `":9100"` | HTTP listen address for metrics |
| `path` | string | `"/metrics"` | URL path for Prometheus scraping |

#### `log` -- Logging

| Key | Type | Default | Description |
|---|---|---|---|
| `level` | string | `"info"` | Log level: `debug`, `info`, `warn`, `error` |
| `format` | string | `"json"` | Output format: `json` (production), `text` (development) |

Log level can be changed at runtime via SIGHUP reload without restarting the daemon.

#### `bfd` -- Default BFD Parameters

| Key | Type | Default | Description |
|---|---|---|---|
| `default_desired_min_tx` | duration | `"1s"` | Default Desired Min TX Interval |
| `default_required_min_rx` | duration | `"1s"` | Default Required Min RX Interval |
| `default_detect_multiplier` | uint32 | `3` | Default Detection Multiplier (MUST be >= 1) |

These defaults apply to sessions created via gRPC that do not specify their own parameters. Sessions defined in the `sessions` section can override any of these values.

> **Note**: Per RFC 5880 Section 6.8.3, when the session is not in Up state, the TX interval is enforced to be at least 1 second regardless of the configured value.

#### `gobgp` -- GoBGP Integration

See [GoBGP Integration](#gobgp-integration) for details.

#### `sessions` -- Declarative Sessions

See [Declarative Sessions](#declarative-sessions) for details.

### Environment Variables

All configuration keys can be overridden via environment variables with the `GOBFD_` prefix. The mapping rule is: strip prefix, lowercase, replace `_` with `.`.

| Environment Variable | Config Key | Example |
|---|---|---|
| `GOBFD_GRPC_ADDR` | `grpc.addr` | `":50051"` |
| `GOBFD_METRICS_ADDR` | `metrics.addr` | `":9100"` |
| `GOBFD_METRICS_PATH` | `metrics.path` | `"/metrics"` |
| `GOBFD_LOG_LEVEL` | `log.level` | `"debug"` |
| `GOBFD_LOG_FORMAT` | `log.format` | `"text"` |

### Declarative Sessions

Sessions defined under `sessions:` are created on daemon startup and reconciled on SIGHUP reload. Reconciliation semantics:

- **New sessions** (in config but not running) are created
- **Removed sessions** (running but not in config) are destroyed
- **Existing sessions** (matching key) are left untouched

Session key is the tuple: `(peer, local, interface)`.

| Key | Type | Required | Description |
|---|---|---|---|
| `peer` | string | Yes | Remote system's IP address |
| `local` | string | Yes | Local system's IP address |
| `interface` | string | No | Network interface for `SO_BINDTODEVICE` |
| `type` | string | No | `single_hop` (default) or `multi_hop` |
| `desired_min_tx` | duration | No | Override default TX interval |
| `required_min_rx` | duration | No | Override default RX interval |
| `detect_mult` | uint32 | No | Override default detect multiplier |

Session types determine the UDP port and TTL handling:

| Type | Port | Outgoing TTL | Incoming TTL Check |
|---|---|---|---|
| `single_hop` | 3784 | 255 (GTSM) | MUST be 255 (RFC 5881) |
| `multi_hop` | 4784 | 255 | MUST be >= 254 (RFC 5883) |

### GoBGP Integration

When enabled, BFD state changes are propagated to a GoBGP instance via its gRPC API (RFC 5882 Section 4.3):

- **BFD Down** --> Disable BGP peer (`DisablePeer()`) or withdraw routes (`DeletePath()`)
- **BFD Up** --> Enable BGP peer (`EnablePeer()`) or restore routes (`AddPath()`)

| Key | Type | Default | Description |
|---|---|---|---|
| `gobgp.enabled` | bool | `false` | Enable/disable GoBGP integration |
| `gobgp.addr` | string | `"127.0.0.1:50051"` | GoBGP gRPC API address |
| `gobgp.strategy` | string | `"disable-peer"` | Strategy: `disable-peer` or `withdraw-routes` |

#### Flap Dampening (RFC 5882 Section 3.2)

Prevents rapid BFD oscillation from causing excessive BGP route churn:

| Key | Type | Default | Description |
|---|---|---|---|
| `gobgp.dampening.enabled` | bool | `false` | Enable flap dampening |
| `gobgp.dampening.suppress_threshold` | float64 | `3` | Penalty above which events are suppressed |
| `gobgp.dampening.reuse_threshold` | float64 | `2` | Penalty below which suppression is lifted |
| `gobgp.dampening.max_suppress_time` | duration | `"60s"` | Maximum suppression duration |
| `gobgp.dampening.half_life` | duration | `"15s"` | Penalty decay half-life |

### Hot Reload (SIGHUP)

Sending `SIGHUP` to the gobfd process triggers configuration reload:

```bash
# Reload configuration
sudo systemctl reload gobfd
# or
kill -HUP $(pidof gobfd)
```

On reload:
1. The YAML file is re-read and validated
2. Log level is updated dynamically (no restart needed)
3. Declarative sessions are reconciled (added/removed)
4. Errors during reload are logged -- the previous configuration remains in effect

### Validation Rules

| Rule | Error |
|---|---|
| `grpc.addr` must not be empty | `ErrEmptyGRPCAddr` |
| `bfd.default_detect_multiplier` must be >= 1 | `ErrInvalidDetectMultiplier` |
| `bfd.default_desired_min_tx` must be > 0 | `ErrInvalidDesiredMinTx` |
| `bfd.default_required_min_rx` must be > 0 | `ErrInvalidRequiredMinRx` |
| `gobgp.addr` must not be empty when enabled | `ErrEmptyGoBGPAddr` |
| `gobgp.strategy` must be `disable-peer` or `withdraw-routes` | `ErrInvalidGoBGPStrategy` |
| `gobgp.dampening.suppress_threshold` must be > `reuse_threshold` | `ErrInvalidDampeningThreshold` |
| `gobgp.dampening.half_life` must be > 0 when dampening enabled | `ErrInvalidDampeningHalfLife` |
| Session `peer` must be a valid IP address | `ErrInvalidSessionPeer` |
| Session `type` must be `single_hop` or `multi_hop` | `ErrInvalidSessionType` |
| Session `detect_mult` must be >= 1 | `ErrInvalidSessionDetectMult` |
| No duplicate session keys (peer, local, interface) | `ErrDuplicateSessionKey` |

### Defaults

| Key | Default Value | Rationale |
|---|---|---|
| `grpc.addr` | `:50051` | Standard gRPC port |
| `metrics.addr` | `:9100` | Standard exporter port |
| `metrics.path` | `/metrics` | Prometheus convention |
| `log.level` | `info` | Production default |
| `log.format` | `json` | Machine-parseable |
| `bfd.default_desired_min_tx` | `1s` | RFC 5880 Section 6.8.3 slow rate |
| `bfd.default_required_min_rx` | `1s` | Conservative starting point |
| `bfd.default_detect_multiplier` | `3` | 3x detection time |
| `gobgp.enabled` | `false` | Opt-in integration |
| `gobgp.strategy` | `disable-peer` | Safest BGP action |

### Related Documents

- [04-cli.md](./04-cli.md) -- CLI commands for session management
- [06-deployment.md](./06-deployment.md) -- Production deployment with systemd
- [configs/gobfd.example.yml](../../configs/gobfd.example.yml) -- Annotated example configuration

---

*Last updated: 2026-02-21*
