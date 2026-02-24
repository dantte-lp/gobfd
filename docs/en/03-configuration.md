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
  align_intervals: false
  default_padded_pdu_size: 0       # RFC 9764: 0 = no padding

# Unsolicited BFD (RFC 9468)
unsolicited:
  enabled: false

# Echo (RFC 9747)
echo:
  enabled: false

# Micro-BFD (RFC 7130)
micro_bfd:
  groups: []

# VXLAN BFD (RFC 8971)
vxlan:
  enabled: false

# Geneve BFD (RFC 9521)
geneve:
  enabled: false

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
    padded_pdu_size: 128             # RFC 9764: per-session PDU padding
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
| `align_intervals` | bool | `false` | RFC 7419: align timers to nearest common interval |
| `default_padded_pdu_size` | uint16 | `0` | RFC 9764: pad BFD packets to this size with DF bit (0 = disabled, valid: 24-9000) |

These defaults apply to sessions created via gRPC that do not specify their own parameters. Sessions defined in the `sessions` section can override any of these values.

When `align_intervals` is `true`, `DesiredMinTxInterval` and `RequiredMinRxInterval` are rounded UP to the nearest RFC 7419 common interval (3.3ms, 10ms, 20ms, 50ms, 100ms, 1s). This prevents negotiation mismatches with hardware BFD implementations.

> **Note**: Per RFC 5880 Section 6.8.3, when the session is not in Up state, the TX interval is enforced to be at least 1 second regardless of the configured value.

#### `gobgp` -- GoBGP Integration

See [GoBGP Integration](#gobgp-integration) for details.

#### `unsolicited` -- RFC 9468 Unsolicited BFD

| Key | Type | Default | Description |
|---|---|---|---|
| `unsolicited.enabled` | bool | `false` | Enable unsolicited BFD (MUST be disabled by default per RFC 9468) |
| `unsolicited.max_sessions` | int | `0` | Max dynamically created sessions (0 = unlimited) |
| `unsolicited.cleanup_timeout` | duration | `"0s"` | Wait time before deleting Down passive sessions |
| `unsolicited.session_defaults.desired_min_tx` | duration | `"1s"` | Default TX interval for auto-created sessions |
| `unsolicited.session_defaults.required_min_rx` | duration | `"1s"` | Default RX interval for auto-created sessions |
| `unsolicited.session_defaults.detect_mult` | uint32 | `3` | Default detect multiplier for auto-created sessions |
| `unsolicited.interfaces.<name>.enabled` | bool | `false` | Enable unsolicited on this interface |
| `unsolicited.interfaces.<name>.allowed_prefixes` | []string | `[]` | Restrict source addresses (CIDR notation) |

Example:
```yaml
unsolicited:
  enabled: true
  max_sessions: 10
  cleanup_timeout: "30s"
  session_defaults:
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
  interfaces:
    "":
      enabled: true
      allowed_prefixes: ["10.0.0.0/8"]
```

> **Note**: The empty string key `""` matches the default listener (no interface binding). Use this for raw socket listeners that don't report an interface name.

#### `echo` -- RFC 9747 Unaffiliated BFD Echo

| Key | Type | Default | Description |
|---|---|---|---|
| `echo.enabled` | bool | `false` | Enable BFD echo sessions |
| `echo.default_tx_interval` | duration | -- | Default echo transmit interval |
| `echo.default_detect_multiplier` | uint32 | -- | Default echo detection multiplier |
| `echo.peers[].peer` | string | -- | Remote system IP (echo target) |
| `echo.peers[].local` | string | -- | Local system IP |
| `echo.peers[].interface` | string | -- | Network interface for `SO_BINDTODEVICE` (optional) |
| `echo.peers[].tx_interval` | duration | -- | Override `default_tx_interval` for this peer |
| `echo.peers[].detect_mult` | uint32 | -- | Override `default_detect_multiplier` for this peer |

Echo sessions detect forwarding-path failures without requiring the remote system to run BFD. The local system sends BFD Control packets to UDP port 3785 on the remote, which forwards them back via normal IP routing.

Echo peers are reconciled on SIGHUP reload. Session key: `(peer, local, interface)`.

Example:
```yaml
echo:
  enabled: true
  default_tx_interval: "100ms"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.1"
      local: "10.0.0.2"
      interface: "eth0"
      tx_interval: "50ms"
      detect_mult: 5
```

#### `micro_bfd` -- RFC 7130 Micro-BFD for LAG

| Key | Type | Default | Description |
|---|---|---|---|
| `micro_bfd.groups[].lag_interface` | string | -- | Logical LAG interface name (e.g., "bond0") |
| `micro_bfd.groups[].member_links` | []string | -- | Physical member link names |
| `micro_bfd.groups[].peer_addr` | string | -- | Remote system IP for all member sessions |
| `micro_bfd.groups[].local_addr` | string | -- | Local system IP |
| `micro_bfd.groups[].desired_min_tx` | duration | -- | BFD timer interval for member sessions |
| `micro_bfd.groups[].required_min_rx` | duration | -- | Minimum acceptable RX interval |
| `micro_bfd.groups[].detect_mult` | uint32 | -- | Detection time multiplier |
| `micro_bfd.groups[].min_active_links` | int | -- | Minimum Up members for LAG Up (>= 1) |

Micro-BFD runs independent BFD sessions on each LAG member link (UDP port 6784) with `SO_BINDTODEVICE` per member. The aggregate LAG state is Up when `upCount >= min_active_links`.

Groups are reconciled on SIGHUP reload. Group key: `lag_interface`.

Example:
```yaml
micro_bfd:
  groups:
    - lag_interface: "bond0"
      member_links: ["eth0", "eth1"]
      peer_addr: "10.0.0.2"
      local_addr: "10.0.0.1"
      desired_min_tx: "100ms"
      required_min_rx: "100ms"
      detect_mult: 3
      min_active_links: 1
```

#### `vxlan` -- RFC 8971 BFD for VXLAN

| Key | Type | Default | Description |
|---|---|---|---|
| `vxlan.enabled` | bool | `false` | Enable VXLAN BFD sessions |
| `vxlan.management_vni` | uint32 | -- | Management VNI for BFD control (24-bit, max 16777215) |
| `vxlan.default_desired_min_tx` | duration | -- | Default TX interval for VXLAN sessions |
| `vxlan.default_required_min_rx` | duration | -- | Default RX interval for VXLAN sessions |
| `vxlan.default_detect_multiplier` | uint32 | -- | Default detection multiplier |
| `vxlan.peers[].peer` | string | -- | Remote VTEP IP address |
| `vxlan.peers[].local` | string | -- | Local VTEP IP address |
| `vxlan.peers[].desired_min_tx` | duration | -- | Override default TX interval for this peer |
| `vxlan.peers[].required_min_rx` | duration | -- | Override default RX interval for this peer |
| `vxlan.peers[].detect_mult` | uint32 | -- | Override default detect multiplier for this peer |

BFD Control packets are encapsulated in VXLAN (outer UDP port 4789) with a dedicated Management VNI. The inner packet stack includes Ethernet (dst MAC `00:52:02:00:00:00`), IPv4 (TTL=255), and UDP (dst 3784) headers.

Peers are reconciled on SIGHUP reload. Session key: `(peer, local)`.

Example:
```yaml
vxlan:
  enabled: true
  management_vni: 16777215
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

#### `geneve` -- RFC 9521 BFD for Geneve

| Key | Type | Default | Description |
|---|---|---|---|
| `geneve.enabled` | bool | `false` | Enable Geneve BFD sessions |
| `geneve.default_vni` | uint32 | -- | Default Geneve VNI (24-bit, max 16777215) |
| `geneve.default_desired_min_tx` | duration | -- | Default TX interval for Geneve sessions |
| `geneve.default_required_min_rx` | duration | -- | Default RX interval for Geneve sessions |
| `geneve.default_detect_multiplier` | uint32 | -- | Default detection multiplier |
| `geneve.peers[].peer` | string | -- | Remote NVE IP address |
| `geneve.peers[].local` | string | -- | Local NVE IP address |
| `geneve.peers[].vni` | uint32 | -- | Override `default_vni` for this peer (0 = use default) |
| `geneve.peers[].desired_min_tx` | duration | -- | Override default TX interval for this peer |
| `geneve.peers[].required_min_rx` | duration | -- | Override default RX interval for this peer |
| `geneve.peers[].detect_mult` | uint32 | -- | Override default detect multiplier for this peer |

BFD Control packets are encapsulated in Geneve (outer UDP port 6081) with Format A (Ethernet payload, Protocol Type 0x6558). Per RFC 9521 Section 4: O bit (control) is set to 1, C bit (critical) is set to 0.

Peers are reconciled on SIGHUP reload. Session key: `(peer, local)`.

Example:
```yaml
geneve:
  enabled: true
  default_vni: 100
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      vni: 200
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

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
| `padded_pdu_size` | uint16 | No | RFC 9764: pad BFD packets to this size (overrides `bfd.default_padded_pdu_size`) |

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
4. Echo sessions are reconciled (added/removed)
5. Micro-BFD groups are reconciled (added/removed, member link changes)
6. VXLAN tunnel sessions are reconciled (added/removed)
7. Geneve tunnel sessions are reconciled (added/removed)
8. Errors during reload are logged -- the previous configuration remains in effect

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
| Echo `peer` must be a valid IP address | `ErrInvalidEchoPeer` |
| Echo `detect_mult` must be >= 1 | `ErrInvalidEchoDetectMult` |
| No duplicate echo session keys | `ErrDuplicateEchoSessionKey` |
| VXLAN `management_vni` must be <= 16777215 (24-bit) | `ErrInvalidVXLANVNI` |
| VXLAN `peer` must be a valid IP address | `ErrInvalidVXLANPeer` |
| No duplicate VXLAN session keys | `ErrDuplicateVXLANSessionKey` |
| Geneve `default_vni` and per-peer `vni` must be <= 16777215 | `ErrInvalidGeneveVNI` |
| Geneve `peer` must be a valid IP address | `ErrInvalidGenevePeer` |
| No duplicate Geneve session keys | `ErrDuplicateGeneveSessionKey` |

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

*Last updated: 2026-02-23*
